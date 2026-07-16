package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

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

	data, err := fetchKline(client, ck, bars)
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

// ——— HTTP 拉取逻辑（与 indicator-analyze 保持一致） ———

type klineQuote struct {
	Qfqday [][]json.RawMessage          `json:"qfqday"`
	Day    [][]json.RawMessage          `json:"day"`
	Qt     map[string][]json.RawMessage `json:"qt"`
}

type klineResp struct {
	Data map[string]klineQuote `json:"data"`
}

type klineData struct {
	Code      string
	Name      string
	Dates     []string
	Candles   []indicator.Candle
	Turnovers []float64
}

func fetchKline(client *http.Client, code string, bars int) (klineData, error) {
	url := fmt.Sprintf("https://ifzq.gtimg.cn/appstock/app/fqkline/get?param=%s,day,,,%d,qfq", code, bars)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return klineData{}, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := client.Do(req)
	if err != nil {
		return klineData{}, fmt.Errorf("fetch %s: %w", code, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return klineData{}, fmt.Errorf("fetch %s: HTTP %s", code, resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return klineData{}, err
	}

	var parsed klineResp
	if err := json.Unmarshal(body, &parsed); err != nil {
		return klineData{}, err
	}

	raw, ok := parsed.Data[code]
	if !ok {
		return klineData{}, fmt.Errorf("no klines for %s", code)
	}

	rows := raw.Qfqday
	if len(rows) == 0 {
		rows = raw.Day
	}
	if len(rows) == 0 {
		return klineData{}, fmt.Errorf("no klines for %s", code)
	}

	name := code
	if q, ok := raw.Qt[code]; ok && len(q) > 1 {
		json.Unmarshal(q[1], &name)
	}

	dates := make([]string, len(rows))
	candles := make([]indicator.Candle, len(rows))
	for i, row := range rows {
		if len(row) < 6 {
			return klineData{}, fmt.Errorf("row %d short", i)
		}
		dates[i] = rawString(row[0])
		vol := rawFloat(row[5]) * 100 // 腾讯返回 手(÷100=股)
		candles[i] = indicator.Candle{
			Open:   rawFloat(row[1]),
			Close:  rawFloat(row[2]),
			High:   rawFloat(row[3]),
			Low:    rawFloat(row[4]),
			Volume: vol,
			Amount: rawFloat(row[2]) * vol, // Close × 股数 ≈ 成交额
		}
	}

	turnovers := fetchEMTurnover(client, code, len(candles), dates)

	return klineData{
		Code: code, Name: name, Dates: dates, Candles: candles,
		Turnovers: turnovers,
	}, nil
}

// fetchEMTurnover 从东财拉换手率。失败返回 nil。
func fetchEMTurnover(client *http.Client, code string, count int, tencentDates []string) []float64 {
	prefix := ""
	switch {
	case len(code) >= 2 && code[:2] == "sh":
		prefix = "1"
	case len(code) >= 2 && (code[:2] == "sz" || code[:2] == "bj"):
		prefix = "0"
	}
	if prefix == "" {
		return nil
	}
	secid := prefix + "." + code[2:]

	url := fmt.Sprintf(
		"https://push2his.eastmoney.com/api/qt/stock/kline/get?secid=%s&fields1=f1,f2,f3&fields2=f51,f61&klt=101&fqt=1&end=20500101&lmt=%d",
		secid, count)

	var resp *http.Response
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		req, _ := http.NewRequest(http.MethodGet, url, nil)
		req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")
		req.Header.Set("Referer", "https://data.eastmoney.com/")
		req.Header.Set("Accept", "application/json, text/plain, */*")
		resp, err = client.Do(req)
		if err == nil {
			defer resp.Body.Close()
			break
		}
		if resp != nil {
			resp.Body.Close()
			resp = nil
		}
		if attempt < 2 {
			time.Sleep(time.Duration(300*(attempt+1)) * time.Millisecond)
		}
	}
	if err != nil || resp == nil {
		return nil
	}

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	var emResp struct {
		Data struct {
			Klines []string `json:"klines"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &emResp); err != nil {
		return nil
	}

	turnMap := make(map[string]float64, len(emResp.Data.Klines))
	for _, ks := range emResp.Data.Klines {
		commaPos := -1
		for j := 0; j < len(ks); j++ {
			if ks[j] == ',' {
				commaPos = j
				break
			}
		}
		if commaPos < 0 || commaPos >= len(ks)-1 {
			continue
		}
		turnMap[ks[:commaPos]] = parseFloat(ks[commaPos+1:])
	}

	turns := make([]float64, len(tencentDates))
	hasData := false
	for i, d := range tencentDates {
		if v, ok := turnMap[d]; ok {
			turns[i] = v / 100
			hasData = true
		}
	}
	if !hasData {
		return nil
	}
	return turns
}

// ——— Snapshot 构建 ———

func buildSnapshot(data klineData) store.Snapshot {
	candles := data.Candles
	n := len(candles)
	if n == 0 {
		return store.Snapshot{Code: data.Code}
	}
	results := indicator.Calculate(candles)
	tds := indicator.TDSequential(candles)
	last := results[n-1]
	lastCandle := candles[n-1]
	closes := closeSeriesFn(candles)
	ma5, ma10, ma20, ma60 := meanTailFn(closes, 5), meanTailFn(closes, 10), meanTailFn(closes, 20), meanTailFn(closes, 60)
	volumes := volumeSeriesFn(candles)
	volMA20 := meanTailFn(volumes, 20)
	volRatio := ratioFn(lastCandle.Volume, volMA20)
	obv := obvSeriesFn(candles)
	_, upAvgVol, _, downAvgVol := recentVolumeHealthFn(candles, 5)
	score := scoreResultFn(candles, results, obv, upAvgVol, downAvgVol, volRatio)
	div := divergenceFn(candles, results, n-1)
	perfs := performanceFn(candles, data.Dates, results, tds, obv)
	scoreAdj, _ := applyPerfAdaptiveFn(score, perfs)
	latePen, _, _ := lateStagePenaltyFn(candles, results)
	scoreAdj = clampIntFn(scoreAdj+latePen, 0, 100)

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

	return store.Snapshot{
		Code:      data.Code,
		TradeDate: data.Dates[n-1],
		Close:     lastCandle.Close,
		ChangePct: changePct,
		Low:       lastCandle.Low,
		High:      lastCandle.High,
		MA5: ma5, MA10: ma10, MA20: ma20, MA60: ma60,
		KDJ_J:    last.KDJ.J,
		MACD_DIF: last.MACD.DIF, MACD_DEA: last.MACD.DEA, MACD_Hist: last.MACD.Histogram,
		RSI6: last.RSI.RSI6, WR14: last.WR.WR14,
		BIAS6: last.BIAS.BIAS6, BIAS24: last.BIAS.BIAS24,
		PDI: last.DMI.PDI, MDI: last.DMI.MDI, ADX: last.DMI.ADX, ADXR: last.DMI.ADXR,
		CMI: last.CMI, CHOP: last.CHOP,
		ATRPct:        last.ATR.Pct,
		BollPB:        last.BOLL.PercentB,
		BollBW:        last.BOLL.Bandwidth,
		MFI:           last.MFI,
		SARLong:       last.SAR.Long,
		SuperTrendLong: last.SuperTrend.Long,
		VolRatio:      volRatio,
		OBVUp:         len(obv) >= 6 && obv[n-1] > obv[n-6],
		ScoreTotal:    score.Total,
		ScoreDelta:    score.Delta,
		ScoreLabel:    score.Label,
		ScoreAdj:      scoreAdj,
		SigTrendBull:  score.Signals.TrendBull,
		SigOverbought: score.Signals.Overbought,
		SigOversold:   score.Signals.Oversold,
		DivBull:       div.Bull,
		DivBear:       div.Bear,
		DivBearToday:  div.BearToday,
		TDSetup:       fmt.Sprintf("%s/%d", tdSignalTextFn(lastTD.SetupSignal), lastTD.SetupCount),
		TDCountdown:   fmt.Sprintf("%s/%d", tdSignalTextFn(lastTD.CountdownSignal), lastTD.CountdownCount),
		Streak:        streakValueFn(candles),
		Ret20:         nDayReturnFn(candles, 20),
		Ret60:         nDayReturnFn(candles, 60),
		Ret120:        nDayReturnFn(candles, 120),
		PerfTrendFollowBullWin10: perfTFBWin10,
		PerfOverboughtBearWin10:  perfOBBWin10,
		PerfDivBearWin10:         perfDivBWin10,
		PerfTrendFollowBullN:     perfTFBN,
		PerfOverboughtBearN:      perfOBBN,
		PerfDivBearN:             perfDivBN,
		PerfTrendFollowBullAvg10: perfTFBAvg10,
		KeltnerSqueeze:           last.Keltner.Squeeze,
		DonchBreak20Bull:         donchianBreakFn(candles, results, 20, true),
		DonchBreak55Bull:         donchianBreakFn(candles, results, 55, true),
		SARValue:                 last.SAR.Value,
		SuperTrendValue:          last.SuperTrend.Value,
	}
}

// ===== 辅助函数 =====

func rawString(raw json.RawMessage) string {
	var s string
	json.Unmarshal(raw, &s)
	return s
}

func rawFloat(raw json.RawMessage) float64 {
	s := rawString(raw)
	var f float64
	fmt.Sscanf(s, "%f", &f)
	return f
}

func parseFloat(s string) float64 {
	var f float64
	fmt.Sscanf(s, "%f", &f)
	return f
}

func closeSeriesFn(candles []indicator.Candle) []float64 {
	v := make([]float64, len(candles))
	for i, c := range candles {
		v[i] = c.Close
	}
	return v
}

func volumeSeriesFn(candles []indicator.Candle) []float64 {
	v := make([]float64, len(candles))
	for i, c := range candles {
		v[i] = c.Volume
	}
	return v
}

func obvSeriesFn(candles []indicator.Candle) []float64 {
	if len(candles) == 0 {
		return nil
	}
	obv := make([]float64, len(candles))
	obv[0] = candles[0].Volume
	for i := 1; i < len(candles); i++ {
		switch {
		case candles[i].Close > candles[i-1].Close:
			obv[i] = obv[i-1] + candles[i].Volume
		case candles[i].Close < candles[i-1].Close:
			obv[i] = obv[i-1] - candles[i].Volume
		default:
			obv[i] = obv[i-1]
		}
	}
	return obv
}

func meanTailFn(values []float64, count int) float64 {
	if len(values) == 0 {
		return 0
	}
	start := maxIntFn(0, len(values)-count)
	total := 0.0
	for _, v := range values[start:] {
		total += v
	}
	return total / float64(len(values)-start)
}

func ratioFn(a, b float64) float64 {
	if b == 0 {
		return 0
	}
	return a / b
}

func maxIntFn(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func clampIntFn(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func countTrueFn(vv ...bool) int {
	c := 0
	for _, v := range vv {
		if v {
			c++
		}
	}
	return c
}

func absIntFn(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func closeMAFn(candles []indicator.Candle, end, period int) float64 {
	start := maxIntFn(0, end-period+1)
	total := 0.0
	for i := start; i <= end; i++ {
		total += candles[i].Close
	}
	return total / float64(end-start+1)
}

func volumeMAFn(candles []indicator.Candle, end, period int) float64 {
	start := maxIntFn(0, end-period+1)
	total := 0.0
	for i := start; i <= end; i++ {
		total += candles[i].Volume
	}
	return total / float64(end-start+1)
}

func nDayReturnFn(candles []indicator.Candle, n int) float64 {
	last := len(candles) - 1
	base := last - n
	if base < 0 || candles[base].Close == 0 {
		return 0
	}
	return (candles[last].Close - candles[base].Close) / candles[base].Close * 100
}

func recentVolumeHealthFn(candles []indicator.Candle, days int) (int, float64, int, float64) {
	upTotal, downTotal := 0.0, 0.0
	upCnt, downCnt := 0, 0
	start := maxIntFn(1, len(candles)-days)
	for i := start; i < len(candles); i++ {
		if candles[i].Close > candles[i-1].Close {
			upTotal += candles[i].Volume
			upCnt++
		} else if candles[i].Close < candles[i-1].Close {
			downTotal += candles[i].Volume
			downCnt++
		}
	}
	return upCnt, ratioFn(upTotal, float64(upCnt)), downCnt, ratioFn(downTotal, float64(downCnt))
}

func streakValueFn(candles []indicator.Candle) int {
	streak, direction := 0, 0
	for i := len(candles) - 1; i > 0; i-- {
		cur := 0
		if candles[i].Close > candles[i-1].Close {
			cur = 1
		} else if candles[i].Close < candles[i-1].Close {
			cur = -1
		}
		if cur == 0 {
			break
		}
		if streak == 0 {
			direction = cur
		}
		if cur != direction {
			break
		}
		streak++
	}
	return streak * direction
}

func tdSignalTextFn(signal indicator.TDSignal) string {
	switch signal {
	case indicator.TDBuy:
		return "见底"
	case indicator.TDSell:
		return "见顶"
	default:
		return "-"
	}
}

func donchianBreakFn(candles []indicator.Candle, results []indicator.Result, period int, bullish bool) bool {
	if len(candles) < 2 {
		return false
	}
	close := candles[len(candles)-1].Close
	prev := results[len(results)-2].Donchian
	if period == 55 {
		if bullish {
			return close > prev.Upper55
		}
		return close < prev.Lower55
	}
	if bullish {
		return close > prev.Upper20
	}
	return close < prev.Lower20
}

// ===== score / divergence / performance 逻辑 =====

type scoreState struct {
	Total, Delta                                   int
	Label                                          string
	Signals                                        signalState
	DMI, MA, MACD, KdjWr, RSI, BIAS, CHOPCMI, Volume, SAR, Divergence int
}

type signalState struct {
	TrendBullScore, TrendBearScore                int
	OversoldScore, OverboughtScore                int
	BreakBullScore, BreakBearScore                int
	RevertBullScore, RevertBearScore              int
	TrendBull, TrendBear                          bool
	Oversold, Overbought                          bool
	BreakBull, BreakBear                          bool
	RevertBull, RevertBear                        bool
}

type divergenceState struct {
	Ready                                      bool
	BullScore, BearScore                       int
	Bull, Bear, BullToday, BearToday           bool
	LowIdx, RefLowIdx, HighIdx, RefHighIdx     int
}

type perfStat struct {
	Name, Direction                    string
	Triggers                           int
	Win5, Win10                        int
	Sum5, Sum10                        float64
	Best10, Worst10, MaxAdverse        float64
	LastDate                           string
}

func scoreLabelFn(score int) string {
	switch {
	case score >= 85: return "技术极强"
	case score >= 70: return "技术偏强"
	case score >= 55: return "技术略偏强"
	case score >= 45: return "技术中性/方向不明"
	case score >= 31: return "技术略偏弱"
	case score >= 16: return "技术偏弱"
	default: return "技术极弱"
	}
}

func evalSignalsFn(candles []indicator.Candle, results []indicator.Result, obv []float64, i int) signalState {
	if i < 60 {
		return signalState{}
	}
	r, prev := results[i], results[i-1]
	ma5 := closeMAFn(candles, i, 5)
	ma20 := closeMAFn(candles, i, 20)
	ma60 := closeMAFn(candles, i, 60)
	vr := ratioFn(candles[i].Volume, volumeMAFn(candles, i, 20))
	fiveAgo := maxIntFn(0, i-5)
	priceUp5 := candles[i].Close > candles[fiveAgo].Close
	priceDown5 := candles[i].Close < candles[fiveAgo].Close
	obvUp := obv[i] > obv[fiveAgo]
	obvDown := obv[i] < obv[fiveAgo]
	crossUp20 := i > 0 && candles[i-1].Close <= closeMAFn(candles, i-1, 20) && candles[i].Close > ma20
	crossDown20 := i > 0 && candles[i-1].Close >= closeMAFn(candles, i-1, 20) && candles[i].Close < ma20
	crossUp60 := i > 0 && candles[i-1].Close <= closeMAFn(candles, i-1, 60) && candles[i].Close > ma60
	crossDown60 := i > 0 && candles[i-1].Close >= closeMAFn(candles, i-1, 60) && candles[i].Close < ma60

	s := signalState{
		TrendBullScore: countTrueFn(r.DMI.ADX > 25, r.MACD.DIF > 0 && r.DMI.PDI > r.DMI.MDI, candles[i].Close > ma5 && candles[i].Close > ma20 && ma5 > ma20),
		TrendBearScore: countTrueFn(r.DMI.ADX > 25, r.MACD.DIF < 0 && r.DMI.MDI > r.DMI.PDI, candles[i].Close < ma5 && candles[i].Close < ma20 && ma5 < ma20),
		OversoldScore:   countTrueFn(r.RSI.RSI6 < 30, r.WR.WR14 > 80 || (r.KDJ.K < 20 && (r.KDJ.K > r.KDJ.D || r.KDJ.J > prev.KDJ.J)), r.BIAS.BIAS24 < -10),
		OverboughtScore: countTrueFn(r.RSI.RSI6 > 70, r.WR.WR14 < 20 || (r.KDJ.K > 80 && (r.KDJ.K < r.KDJ.D || r.KDJ.J < prev.KDJ.J)), r.BIAS.BIAS24 > 10),
		BreakBullScore:  countTrueFn(crossUp20 || crossUp60, vr > 1.5, obvUp),
		BreakBearScore:  countTrueFn(crossDown20 || crossDown60, vr > 1.5, obvDown),
		RevertBullScore: countTrueFn(r.BIAS.BIAS24 < -10, r.CHOP > 45, priceDown5 && obvUp),
		RevertBearScore: countTrueFn(r.BIAS.BIAS24 > 10, r.CHOP > 45, priceUp5 && obvDown),
	}
	s.TrendBull = s.TrendBullScore >= 3
	s.TrendBear = s.TrendBearScore >= 3
	s.Oversold = s.OversoldScore >= 3
	s.Overbought = s.OverboughtScore >= 3
	s.BreakBull = s.BreakBullScore >= 2
	s.BreakBear = s.BreakBearScore >= 2
	s.RevertBull = s.RevertBullScore >= 2
	s.RevertBear = s.RevertBearScore >= 2
	return s
}

func scoreResultFn(candles []indicator.Candle, results []indicator.Result, obv []float64, avgUpVol, avgDownVol, volRatio float64) scoreState {
	n := len(candles)
	last := results[n-1]
	prev := last
	if n > 1 {
		prev = results[n-2]
	}
	sc := scoreState{Signals: evalSignalsFn(candles, results, obv, n-1)}

	dmiDiff := last.DMI.PDI - last.DMI.MDI
	switch {
	case dmiDiff > 15 && last.DMI.ADX > 25: sc.DMI = 12
	case dmiDiff > 8 && last.DMI.ADX > 20: sc.DMI = 8
	case dmiDiff > 0: sc.DMI = 3
	case dmiDiff < -15 && last.DMI.ADX > 25: sc.DMI = -12
	case dmiDiff < -8 && last.DMI.ADX > 20: sc.DMI = -8
	case dmiDiff < 0: sc.DMI = -3
	}

	ma5, ma10, ma20, ma60 := closeMAFn(candles, n-1, 5), closeMAFn(candles, n-1, 10), closeMAFn(candles, n-1, 20), closeMAFn(candles, n-1, 60)
	switch countTrueFn(candles[n-1].Close > ma5, candles[n-1].Close > ma10, candles[n-1].Close > ma20, candles[n-1].Close > ma60) {
	case 4: sc.MA = 10
	case 3: sc.MA = 6
	case 2: sc.MA = 2
	case 1: sc.MA = -4
	default: sc.MA = -10
	}
	if ma5 > ma10 && ma10 > ma20 && ma20 > ma60 { sc.MA += 2 } else if ma5 < ma10 && ma10 < ma20 && ma20 < ma60 { sc.MA -= 2 }

	macdGold := last.MACD.DIF >= last.MACD.DEA
	switch {
	case last.MACD.DIF > 0 && macdGold && last.MACD.Histogram > prev.MACD.Histogram: sc.MACD = 8
	case last.MACD.DIF > 0 && macdGold: sc.MACD = 5
	case last.MACD.DIF > 0: sc.MACD = 2
	case last.MACD.DIF < 0 && macdGold: sc.MACD = -2
	case last.MACD.DIF < 0 && last.MACD.Histogram < prev.MACD.Histogram: sc.MACD = -8
	case last.MACD.DIF < 0: sc.MACD = -5
	}

	kdjSig := 0
	switch {
	case last.KDJ.K < 20 && last.KDJ.K >= last.KDJ.D: kdjSig = 7
	case last.KDJ.K < 20: kdjSig = 1
	case last.KDJ.K <= 80 && last.KDJ.K >= last.KDJ.D: kdjSig = 3
	case last.KDJ.K <= 80: kdjSig = -3
	case last.KDJ.K >= last.KDJ.D: kdjSig = -2
	default: kdjSig = -7
	}
	wrSig := 0
	switch {
	case last.WR.WR14 > 90: wrSig = 4
	case last.WR.WR14 >= 80: wrSig = 2
	case last.WR.WR14 >= 60: wrSig = 1
	case last.WR.WR14 >= 40: wrSig = 0
	case last.WR.WR14 >= 20: wrSig = -1
	case last.WR.WR14 >= 10: wrSig = -2
	default: wrSig = -4
	}
	if absIntFn(kdjSig) >= absIntFn(wrSig) { sc.KdjWr = kdjSig } else { sc.KdjWr = wrSig }

	switch {
	case last.RSI.RSI6 < 20: sc.RSI = 5
	case last.RSI.RSI6 <= 30: sc.RSI = 3
	case last.RSI.RSI6 <= 45: sc.RSI = 1
	case last.RSI.RSI6 <= 55: sc.RSI = 0
	case last.RSI.RSI6 <= 70: sc.RSI = -1
	case last.RSI.RSI6 <= 80: sc.RSI = -3
	default: sc.RSI = -5
	}

	switch {
	case last.BIAS.BIAS24 < -15: sc.BIAS = 3
	case last.BIAS.BIAS24 <= -10: sc.BIAS = 2
	case last.BIAS.BIAS24 <= -5: sc.BIAS = 1
	case last.BIAS.BIAS24 <= 5: sc.BIAS = 0
	case last.BIAS.BIAS24 <= 10: sc.BIAS = -1
	case last.BIAS.BIAS24 <= 15: sc.BIAS = -2
	default: sc.BIAS = -3
	}

	switch {
	case last.CHOP < 30 && last.CMI > 70:
		sc.CHOPCMI = 3
		if dmiDiff < 0 { sc.CHOPCMI = -3 }
	case last.CHOP < 38.2 && last.CMI > 60:
		sc.CHOPCMI = 2
		if dmiDiff < 0 { sc.CHOPCMI = -2 }
	case last.CHOP > 70 && last.CMI < 30: sc.CHOPCMI = -3
	case last.CHOP > 61.8 && last.CMI < 40: sc.CHOPCMI = -2
	}

	priceUp, priceDown := false, false
	if n > 1 {
		priceUp = candles[n-1].Close > candles[n-2].Close
		priceDown = candles[n-1].Close < candles[n-2].Close
	}
	switch {
	case volRatio > 2.0 && priceUp: sc.Volume += 3
	case volRatio > 2.0 && priceDown: sc.Volume -= 3
	case volRatio >= 1.5 && priceUp: sc.Volume += 2
	case volRatio >= 1.5 && priceDown: sc.Volume -= 2
	case volRatio < 0.8 && priceUp: sc.Volume -= 2
	case volRatio < 0.8 && priceDown: sc.Volume++
	}
	if len(obv) >= 6 {
		if obv[n-1] > obv[n-6] { sc.Volume++ } else if obv[n-1] < obv[n-6] { sc.Volume-- }
	}
	if avgUpVol > avgDownVol { sc.Volume++ } else if avgUpVol < avgDownVol { sc.Volume-- }
	sc.Volume = clampIntFn(sc.Volume, -5, 5)

	switch {
	case last.SAR.Long && last.SuperTrend.Long: sc.SAR = 3
	case !last.SAR.Long && !last.SuperTrend.Long: sc.SAR = -3
	}

	if n >= 20 {
		d := divergenceFn(candles, results, n-1)
		if d.BearToday { sc.Divergence = -3 } else if d.Bear { sc.Divergence = -1 }
		if d.BullToday { sc.Divergence = 2 } else if d.Bull { sc.Divergence = 1 }
	}

	sc.Delta = sc.DMI + sc.MA + sc.MACD + sc.KdjWr + sc.RSI + sc.BIAS + sc.CHOPCMI + sc.Volume + sc.SAR + sc.Divergence
	sc.Total = clampIntFn(50+sc.Delta, 0, 100)
	sc.Label = scoreLabelFn(sc.Total)
	return sc
}

func divergenceFn(candles []indicator.Candle, results []indicator.Result, i int) divergenceState {
	const lookback = 20
	const minGap = 3
	if i < lookback { return divergenceState{} }
	refStart := i - lookback
	refEnd := i - minGap
	rsiPeakIdx, rsiTroughIdx := refStart, refStart
	for j := refStart + 1; j <= refEnd; j++ {
		if results[j].RSI.RSI6 > results[rsiPeakIdx].RSI.RSI6 { rsiPeakIdx = j }
		if results[j].RSI.RSI6 < results[rsiTroughIdx].RSI.RSI6 { rsiTroughIdx = j }
	}
	d := divergenceState{Ready: true, HighIdx: i, RefHighIdx: rsiPeakIdx, LowIdx: i, RefLowIdx: rsiTroughIdx}
	rsiNow := results[i].RSI.RSI6
	difNow := results[i].MACD.DIF
	if rsiNow > 60 && rsiNow < results[rsiPeakIdx].RSI.RSI6 && candles[i].Close >= candles[rsiPeakIdx].Close && difNow > 0 {
		d.BearScore, d.Bear, d.BearToday = 1, true, true
	}
	if rsiNow < 40 && rsiNow > results[rsiTroughIdx].RSI.RSI6 && candles[i].Close <= candles[rsiTroughIdx].Close && difNow < 0 {
		d.BullScore, d.Bull, d.BullToday = 1, true, true
	}
	return d
}

func perfWin10Fn(perfs []perfStat, name string) (float64, int) {
	for _, p := range perfs {
		if p.Name == name && p.Triggers > 0 {
			return float64(p.Win10) / float64(p.Triggers) * 100, p.Triggers
		}
	}
	return 0, 0
}

func applyPerfAdaptiveFn(sc scoreState, perfs []perfStat) (int, int) {
	obWin, obN := perfWin10Fn(perfs, "超买反转")
	divWin, divN := perfWin10Fn(perfs, "顶背离")
	adj := 0
	if sc.Signals.Overbought {
		for _, v := range []int{sc.KdjWr, sc.RSI, sc.BIAS} {
			adj += perfScaleFn(v, obWin, obN, 35, 55) - v
		}
	}
	if sc.Divergence < 0 {
		adj += perfScaleFn(sc.Divergence, divWin, divN, 40, 55) - sc.Divergence
	}
	return clampIntFn(50+sc.Delta+adj, 0, 100), adj
}

func perfScaleFn(v int, win float64, n int, weakBelow, strongAbove float64) int {
	if v >= 0 || n < 10 { return v }
	if win < weakBelow { return v / 2 }
	if win > strongAbove { return v * 3 / 2 }
	return v
}

func performanceFn(candles []indicator.Candle, dates []string, results []indicator.Result, tds []indicator.TD, obv []float64) []perfStat {
	ps := []perfStat{
		{Name: "趋势跟随多头", Direction: "多头", Best10: -1e100, Worst10: 1e100},
		{Name: "趋势跟随空头", Direction: "空头", Best10: -1e100, Worst10: 1e100},
		{Name: "超卖反转", Direction: "多头", Best10: -1e100, Worst10: 1e100},
		{Name: "超买反转", Direction: "空头", Best10: -1e100, Worst10: 1e100},
		{Name: "量价突破多头", Direction: "多头", Best10: -1e100, Worst10: 1e100},
		{Name: "量价突破空头", Direction: "空头", Best10: -1e100, Worst10: 1e100},
		{Name: "均值回归多头", Direction: "多头", Best10: -1e100, Worst10: 1e100},
		{Name: "均值回归空头", Direction: "空头", Best10: -1e100, Worst10: 1e100},
		{Name: "底背离", Direction: "多头", Best10: -1e100, Worst10: 1e100},
		{Name: "顶背离", Direction: "空头", Best10: -1e100, Worst10: 1e100},
		{Name: "TD见底Countdown", Direction: "多头", Best10: -1e100, Worst10: 1e100},
		{Name: "TD见顶Countdown", Direction: "空头", Best10: -1e100, Worst10: 1e100},
		{Name: "StochRSI钝化多头", Direction: "多头", Best10: -1e100, Worst10: 1e100},
		{Name: "StochRSI钝化空头", Direction: "空头", Best10: -1e100, Worst10: 1e100},
	}
	if len(candles) <= 90 { return ps }
	prev := evalSignalsFn(candles, results, obv, 79)
	prevDiv := divergenceFn(candles, results, 79)
	for i := 80; i+10 < len(candles); i++ {
		s := evalSignalsFn(candles, results, obv, i)
		d := divergenceFn(candles, results, i)
		if s.TrendBull && !prev.TrendBull { recordPerfFn(&ps[0], candles, dates, i) }
		if s.TrendBear && !prev.TrendBear { recordPerfFn(&ps[1], candles, dates, i) }
		if s.Oversold && !prev.Oversold { recordPerfFn(&ps[2], candles, dates, i) }
		if s.Overbought && !prev.Overbought { recordPerfFn(&ps[3], candles, dates, i) }
		if s.BreakBull && !prev.BreakBull { recordPerfFn(&ps[4], candles, dates, i) }
		if s.BreakBear && !prev.BreakBear { recordPerfFn(&ps[5], candles, dates, i) }
		if s.RevertBull && !prev.RevertBull { recordPerfFn(&ps[6], candles, dates, i) }
		if s.RevertBear && !prev.RevertBear { recordPerfFn(&ps[7], candles, dates, i) }
		if d.BullToday && !prevDiv.BullToday { recordPerfFn(&ps[8], candles, dates, i) }
		if d.BearToday && !prevDiv.BearToday { recordPerfFn(&ps[9], candles, dates, i) }
		if tds[i].CountdownCount == 13 {
			if tds[i].CountdownSignal == indicator.TDBuy { recordPerfFn(&ps[10], candles, dates, i) }
			if tds[i].CountdownSignal == indicator.TDSell { recordPerfFn(&ps[11], candles, dates, i) }
		}
		prev, prevDiv = s, d
	}
	return ps
}

func recordPerfFn(p *perfStat, candles []indicator.Candle, dates []string, i int) {
	entry := candles[i].Close
	ret5 := (candles[i+5].Close/entry - 1) * 100
	ret10 := (candles[i+10].Close/entry - 1) * 100
	adverse := 0.0
	if p.Direction == "空头" {
		ret5, ret10 = -ret5, -ret10
		for j := i + 1; j <= i+10; j++ {
			move := -(candles[j].High/entry - 1) * 100
			if move < adverse { adverse = move }
		}
	} else {
		for j := i + 1; j <= i+10; j++ {
			move := (candles[j].Low/entry - 1) * 100
			if move < adverse { adverse = move }
		}
	}
	p.Triggers++
	if ret5 > 0 { p.Win5++ }
	if ret10 > 0 { p.Win10++ }
	p.Sum5 += ret5
	p.Sum10 += ret10
	if ret10 > p.Best10 { p.Best10 = ret10 }
	if ret10 < p.Worst10 { p.Worst10 = ret10 }
	if adverse < p.MaxAdverse { p.MaxAdverse = adverse }
	p.LastDate = dates[i]
}

func lateStagePenaltyFn(candles []indicator.Candle, results []indicator.Result) (int, int, float64) {
	if len(results) == 0 { return 0, 0, 0 }
	last := results[len(results)-1]
	streak := streakValueFn(candles)
	biasAtr := 0.0
	if last.ATR.Pct > 0 { biasAtr = last.BIAS.BIAS24 / last.ATR.Pct }
	penalty := 0
	if biasAtr > 4 {
		penalty -= 2
		if biasAtr > 6 { penalty-- }
	}
	if streak >= 5 {
		penalty -= 2
		if streak >= 7 { penalty-- }
	}
	if penalty < -5 { penalty = -5 }
	return penalty, streak, biasAtr
}
