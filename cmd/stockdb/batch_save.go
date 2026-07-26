package main

import (
	"database/sql"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"stock-tui/internal/analysis"
	"stock-tui/internal/api"
	"stock-tui/internal/indicator"
	"stock-tui/internal/market"
	"stock-tui/internal/store"
)

// batchSaveCmd 从 instrument 表读取所有代码，并行拉取 K 线、计算指标，
// 串行写入 SQLite（避免 SQLITE_BUSY）。复用 store 包和 indicator 包，
// 内联 HTTP 拉取逻辑（省略 verbose 输出，仅打印进度）。
func batchSaveCmd(args []string) error {
	fs := flag.NewFlagSet("batch-save", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	bars := fs.Int("n", 800, "number of daily bars")
	workers := fs.Int("P", 4, "parallel workers")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *workers < 1 {
		*workers = 1
	}
	if *bars <= 0 {
		return fmt.Errorf("-n must be positive")
	}

	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	// 从 instrument 表读取所有代码
	codes, err := allCodes(st.DB())
	if err != nil {
		return fmt.Errorf("read instrument codes: %w", err)
	}
	if len(codes) == 0 {
		fmt.Println("(instrument 表为空)")
		return nil
	}
	fmt.Fprintf(os.Stderr, "batch-save: %d stocks, %d workers\n", len(codes), *workers)

	// 任务管道 + 结果管道
	type result struct {
		code string
		err  error
	}
	tasks := make(chan string, len(codes))
	results := make(chan result, len(codes))

	// storeLock 串行化所有 SQLite 写操作（避免 SQLITE_BUSY）
	var storeLock sync.Mutex

	// 启动 worker 池
	var wg sync.WaitGroup
	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client := &http.Client{
				Timeout: 30 * time.Second,
				Transport: &http.Transport{
					DisableKeepAlives: true,
				},
			}
			for code := range tasks {
				err := saveOneStock(st, client, &storeLock, code, *bars)
				results <- result{code: code, err: err}
			}
		}()
	}

	// 发送任务
	go func() {
		for _, code := range codes {
			tasks <- code
		}
		close(tasks)
	}()

	// 等待 worker 完成
	go func() {
		wg.Wait()
		close(results)
	}()

	// 收集结果，打印进度
	var okCount, errCount int
	var lastReport time.Time
	for r := range results {
		if r.err != nil {
			errCount++
			fmt.Fprintf(os.Stderr, "ERR %s: %v\n", r.code, r.err)
		} else {
			okCount++
		}
		if time.Since(lastReport) > 5*time.Second {
			fmt.Fprintf(os.Stderr, "progress: %d/%d ok, %d/%d err\n", okCount, len(codes), errCount, len(codes))
			lastReport = time.Now()
		}
	}
	fmt.Printf("batch-save: %d success, %d failed out of %d\n", okCount, errCount, len(codes))
	return nil
}

func allCodes(db *sql.DB) ([]string, error) {
	rows, err := db.Query("SELECT code FROM instrument ORDER BY code")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var codes []string
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return nil, err
		}
		codes = append(codes, code)
	}
	return codes, rows.Err()
}

// saveOneStock 抓取单只股票日 K + 换手率，计算指标，保存快照。
// storeLock 串行化 SQLite 写操作（避免 SQLITE_BUSY）。
func saveOneStock(st *store.Store, client *http.Client, storeLock *sync.Mutex, code string, bars int) error {
	ck, ok := market.NormalizeCode(code)
	if !ok {
		return fmt.Errorf("invalid code: %s", code)
	}

	data, err := api.FetchDailyKline(client, ck, bars)
	if err != nil {
		return fmt.Errorf("fetch: %w", err)
	}

	snap := buildSnapshot(data)

	// 补市盈率等基本面
	if stocks, err := api.FetchStocks([]string{ck}); err == nil && len(stocks) > 0 {
		s := stocks[0]
		snap.TurnoverRate = s.TurnoverRate
		snap.MarketCap = s.MarketCap
		snap.PE = s.PE
	}

	storeLock.Lock()
	if err := st.UpsertInstrument(data.Code, data.Name, market.Prefix(data.Code), ""); err != nil {
		storeLock.Unlock()
		return fmt.Errorf("upsert: %w", err)
	}
	if err := st.SaveSnapshot(snap); err != nil {
		storeLock.Unlock()
		return fmt.Errorf("save: %w", err)
	}
	storeLock.Unlock()
	return nil
}

// ——— Snapshot 构建 ———

func buildSnapshot(data api.KlineData) store.Snapshot {
	candles := data.Candles
	n := len(candles)
	if n == 0 {
		return store.Snapshot{Code: data.Code}
	}
	results := indicator.Calculate(candles)
	tds := indicator.TDSequential(candles)
	last := results[n-1]
	lastCandle := candles[n-1]
	closes := analysis.CloseSeries(candles)
	ma5, ma10, ma20, ma60 := analysis.MeanTail(closes, 5), analysis.MeanTail(closes, 10), analysis.MeanTail(closes, 20), analysis.MeanTail(closes, 60)
	// 量比: 优先腾讯 qt 实时值,缺失时本地重算(同一口径,见 analysis.VolRatio)
	volRatio := data.VolRatioRT
	if volRatio <= 0 {
		volRatio = analysis.VolRatio(candles, n-1)
	}
	obv := analysis.OBVSeries(candles)
	// 20 日最低价:止损回退线,必须来自完整日K(见 store.Snapshot.Low20 注释)。
	low20, _ := analysis.RangeLowHigh(candles, n-20, n)
	_, upAvgVol, _, downAvgVol := analysis.RecentVolumeHealth(candles, 5)
	score := analysis.ScoreResult(candles, results, obv, upAvgVol, downAvgVol, volRatio)
	div := analysis.Divergence(candles, results, n-1)
	perfs := analysis.Performance(candles, data.Dates, results, tds, obv)
	scoreAdj, _ := analysis.ApplyPerfAdaptive(score, perfs)
	latePen, _, _ := analysis.LateStagePenalty(candles, results)
	scoreAdj = analysis.ClampInt(scoreAdj+latePen, 0, 100)

	changePct := 0.0
	if n > 1 {
		changePct = (candles[n-1].Close - candles[n-2].Close) / candles[n-2].Close * 100
	}

	lastTD := tds[n-1]

	var perfTFBWin10, perfOBBWin10, perfDivBWin10 *float64
	var perfTFBN, perfOBBN, perfDivBN *int
	var perfTFBAvg10 *float64
	for _, p := range perfs {
		if p.Name == "趋势跟随多头" && p.Triggers > 0 {
			val := float64(p.Win10) / float64(p.Triggers) * 100
			perfTFBWin10 = &val
			perfTFBN = &p.Triggers
			avg := p.Sum10 / float64(p.Triggers)
			perfTFBAvg10 = &avg
		}
		if p.Name == "超买反转" && p.Triggers > 0 {
			val := float64(p.Win10) / float64(p.Triggers) * 100
			perfOBBWin10 = &val
			perfOBBN = &p.Triggers
		}
		if p.Name == "顶背离" && p.Triggers > 0 {
			val := float64(p.Win10) / float64(p.Triggers) * 100
			perfDivBWin10 = &val
			perfDivBN = &p.Triggers
		}
	}

	snap := store.Snapshot{
		Code:      data.Code,
		TradeDate: data.Dates[n-1],
		Close:     lastCandle.Close,
		ChangePct: changePct,
		Low:       lastCandle.Low,
		High:      lastCandle.High,
		MA5:       ma5, MA10: ma10, MA20: ma20, MA60: ma60,
		KDJ_J:    last.KDJ.J,
		MACD_DIF: last.MACD.DIF, MACD_DEA: last.MACD.DEA, MACD_Hist: last.MACD.Histogram,
		RSI6: last.RSI.RSI6, WR14: last.WR.WR14,
		BIAS6: last.BIAS.BIAS6, BIAS24: last.BIAS.BIAS24,
		PDI: last.DMI.PDI, MDI: last.DMI.MDI, ADX: last.DMI.ADX, ADXR: last.DMI.ADXR,
		CMI: last.CMI, CHOP: last.CHOP,
		ATRPct:                   last.ATR.Pct,
		BollPB:                   last.BOLL.PercentB,
		BollBW:                   last.BOLL.Bandwidth,
		MFI:                      last.MFI,
		SARLong:                  last.SAR.Long,
		SuperTrendLong:           last.SuperTrend.Long,
		VolRatio:                 volRatio,
		OBVUp:                    analysis.OBVUpLast(obv),
		ScoreTotal:               score.Total,
		ScoreDelta:               score.Delta,
		ScoreLabel:               score.Label,
		ScoreAdj:                 scoreAdj,
		SigTrendBull:             score.Signals.TrendBull,
		SigOverbought:            score.Signals.Overbought,
		SigOversold:              score.Signals.Oversold,
		DivBull:                  div.Bull,
		DivBear:                  div.Bear,
		DivBearToday:             div.BearToday,
		TDSetup:                  fmt.Sprintf("%s/%d", analysis.TDSignalText(lastTD.SetupSignal), lastTD.SetupCount),
		TDCountdown:              fmt.Sprintf("%s/%d", analysis.TDSignalText(lastTD.CountdownSignal), lastTD.CountdownCount),
		Streak:                   analysis.StreakValue(candles),
		Ret20:                    analysis.NDayReturn(candles, 20),
		Ret60:                    analysis.NDayReturn(candles, 60),
		Ret120:                   analysis.NDayReturn(candles, 120),
		PerfTrendFollowBullWin10: perfTFBWin10,
		PerfOverboughtBearWin10:  perfOBBWin10,
		PerfDivBearWin10:         perfDivBWin10,
		PerfTrendFollowBullN:     perfTFBN,
		PerfOverboughtBearN:      perfOBBN,
		PerfDivBearN:             perfDivBN,
		PerfTrendFollowBullAvg10: perfTFBAvg10,
		KeltnerSqueeze:           last.Keltner.Squeeze,
		DonchBreak20Bull:         analysis.DonchianBreak(candles, results, 20, true),
		DonchBreak55Bull:         analysis.DonchianBreak(candles, results, 55, true),
		SARValue:                 last.SAR.Value,
		SuperTrendValue:          last.SuperTrend.Value,
		Low20:                    low20,
		OBVUp3:                   analysis.OBVUp3Day(obv),
	}
	// 填充振幅和内外盘(如果 proxy.qq.com 提供了)
	if len(data.Amplitudes) == n && n > 0 {
		snap.Amplitude = data.Amplitudes[n-1]
	}
	snap.InsideVol = data.InsideVol
	snap.OutsideVol = data.OutsideVol
	return snap
}
