package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"stock-tui/internal/analysis"
	"stock-tui/internal/api"
	"stock-tui/internal/indicator"
	"stock-tui/internal/market"
	"stock-tui/internal/store"
)

var httpClient = &http.Client{Timeout: 15 * time.Second}

const defaultBars = 800

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("indicator-analyze", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	bars := fs.Int("n", defaultBars, "number of daily bars")
	save := fs.Bool("save", false, "persist the analysis snapshot to the SQLite store")
	useTDX := fs.Bool("tdx", false, "use TDX TCP protocol as primary data source")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: go run ./cmd/indicator-analyze [-n bars] [-save] [-tdx] <code>")
	}

	code, ok := market.NormalizeCode(fs.Arg(0))
	if !ok {
		return fmt.Errorf("invalid code: %s", fs.Arg(0))
	}
	if *bars <= 0 {
		return fmt.Errorf("-n must be positive")
	}

	// Data source dispatch — 前复权口径对齐 -save 落库口径。
	//
	// tdx (github.com/quantbeing/tdx) 的 GetSecurityBars 只回原始不复权价，库本身不提供
	// 复权。除权分红会在不复权序列里制造断崖（如 sh512480 2026-07-03 除权，不复权 2.70→1.33），
	// 污染 BOLL bandwidth/ATR/SAR/CYQ 获利盘比例/PRY1 与回测 PERF 样本，违反 CLAUDE.md
	// 「CYQ 须前复权」前置。
	//
	// 因此默认（含纯分析模式与 -save）统一走 HTTP 前复权（fetchDailyKline 请求腾讯 qfq），
	// 与 snapshot 落库口径一致。-tdx 仅作显式开启不复权口径的选项：保留 tdx 精确 Amount +
	// 本地换手率（cyq/cyc 的 VWAP 口径），但调用方须自负除权断崖污染指标的风险。
	var data api.KlineData
	var err error
	if *useTDX {
		data, err = fetchViaTDX(code, *bars)
		if err != nil {
			data, err = api.FetchDailyKline(httpClient, code, *bars)
			if err != nil {
				return fmt.Errorf("tdx 和 HTTP 都失败: %w", err)
			}
		}
	} else {
		data, err = api.FetchDailyKline(httpClient, code, *bars)
		if err != nil {
			return fmt.Errorf("HTTP 获取失败: %w", err)
		}
	}
	snap := printAnalysis(data)
	if *save {
		if stocks, err := api.FetchStocks([]string{code}); err == nil && len(stocks) > 0 {
			s := stocks[0]
			snap.TurnoverRate = s.TurnoverRate
			snap.MarketCap = s.MarketCap
			snap.PE = s.PE
			fmt.Printf("FUND 换手率=%.2f%% 市值=%.1f亿 PE=%.1f\n", s.TurnoverRate, s.MarketCap, s.PE)
		} else if err != nil {
			fmt.Fprintf(os.Stderr, "warn: fundamentals fetch failed: %v\n", err)
		}
		// 补拉东财基本面(行业/总股本/流通股本/上市日期),非致命
		if info, err := api.FetchStockInfo(code); err == nil {
			fmt.Printf("INFO 代码=%s 简称=%s 行业=%s 总股本=%.0f 流通=%.0f 总市值=%.2f亿 流通市值=%.2f亿 上市=%s\n",
				info.Code, info.Name, info.Industry,
				info.TotalShares, info.FloatShares,
				info.TotalMC/1e8, info.FloatMC/1e8,
				info.ListedDate)
		} else {
			fmt.Fprintf(os.Stderr, "warn: stock info fetch failed: %v\n", err)
		}
		if err := saveSnapshot(data, snap); err != nil {
			return fmt.Errorf("save snapshot: %w", err)
		}
		fmt.Printf("SAVED %s@%s -> %s\n", snap.Code, snap.TradeDate, store.DefaultPath())
	}
	return nil
}

// saveSnapshot upserts the instrument (name/market from the fetched series) and
// the analysis snapshot into the SQLite store at store.DefaultPath().
func saveSnapshot(data api.KlineData, snap store.Snapshot) error {
	st, err := store.Open(store.DefaultPath())
	if err != nil {
		return err
	}
	defer st.Close()

	if err := st.UpsertInstrument(data.Code, data.Name, market.Prefix(data.Code), ""); err != nil {
		return err
	}
	return st.SaveSnapshot(snap)
}

func printAnalysis(data api.KlineData) store.Snapshot {
	candles := data.Candles
	dates := data.Dates
	n := len(candles)
	if n == 0 {
		fmt.Fprintf(os.Stderr, "warn: %s 返回空K线,跳过分析\n", data.Code)
		return store.Snapshot{Code: data.Code}
	}
	results := indicator.Calculate(candles)
	tds := indicator.TDSequential(candles)
	last := results[n-1]
	lastCandle := candles[n-1]
	closes := analysis.CloseSeries(candles)
	ma5, ma10, ma20, ma60 := analysis.MeanTail(closes, 5), analysis.MeanTail(closes, 10), analysis.MeanTail(closes, 20), analysis.MeanTail(closes, 60)
	volumes := analysis.VolumeSeries(candles)
	lowAll, highAll := analysis.RangeLowHigh(candles, 0, n)
	low20, high20 := analysis.RangeLowHigh(candles, n-20, n)
	low60, high60 := analysis.RangeLowHigh(candles, n-60, n)
	low120, high120 := analysis.RangeLowHigh(candles, n-120, n)
	volMA20 := analysis.MeanTail(volumes, 20)
	// 量比: 优先腾讯 qt 实时值,缺失时本地重算(同一口径,见 analysis.VolRatio)
	volRatio := data.VolRatioRT
	if volRatio <= 0 {
		volRatio = analysis.VolRatio(candles, n-1)
	}
	obv := analysis.OBVSeries(candles)
	upCnt, upAvgVol, downCnt, downAvgVol := analysis.RecentVolumeHealth(candles, 5)
	score := analysis.ScoreResult(candles, results, obv, upAvgVol, downAvgVol, volRatio)
	div := analysis.Divergence(candles, results, n-1)
	// PERF stats must exist before scoring: applyPerfAdaptive reweighs the
	// overbought/divergence penalties by this stock's own signal history.
	perfs := analysis.Performance(candles, dates, results, tds, obv)
	scoreAdj, perfAdj := analysis.ApplyPerfAdaptive(score, perfs)
	// Late-stage crowding penalty folds into score_adj (the adaptive sidecar),
	// not score_total — keeping the fixed scale historically comparable while the
	// single-stock CLI view still reflects end-of-move overheating.
	latePen, _, _ := analysis.LateStagePenalty(candles, results)
	scoreAdj = analysis.ClampInt(scoreAdj+latePen, 0, 100)

	change, changePct := 0.0, 0.0
	if n > 1 {
		change = lastCandle.Close - candles[n-2].Close
		changePct = analysis.Ratio(change, candles[n-2].Close) * 100
	}

	fmt.Printf("%s %s  %s..%s (%d根)  close=%.3f change=%+.3f pct=%+.2f%% high=%.3f low=%.3f volume=%.0f\n",
		data.Code, data.Name, dates[0], dates[n-1], n, lastCandle.Close, change, changePct, lastCandle.High, lastCandle.Low, lastCandle.Volume)
	if n < 120 {
		fmt.Printf("SAMPLE_WARN 日K根数=%d (<120), 均线预热、背离检测和历史PERF样本都偏弱\n", n)
	}
	fmt.Printf("MA5=%.3f MA10=%.3f MA20=%.3f MA60=%.3f | allRange %.3f..%.3f pos=%.0f%% | range20 %.3f..%.3f pos=%.0f%% | range60 %.3f..%.3f pos=%.0f%% | range120 %.3f..%.3f pos=%.0f%%\n",
		ma5, ma10, ma20, ma60,
		lowAll, highAll, analysis.Position(lastCandle.Close, lowAll, highAll),
		low20, high20, analysis.Position(lastCandle.Close, low20, high20),
		low60, high60, analysis.Position(lastCandle.Close, low60, high60),
		low120, high120, analysis.Position(lastCandle.Close, low120, high120))
	fmt.Printf("KDJ K=%.2f D=%.2f J=%.2f | MACD DIF=%.4f DEA=%.4f H=%.4f\n",
		last.KDJ.K, last.KDJ.D, last.KDJ.J, last.MACD.DIF, last.MACD.DEA, last.MACD.Histogram)
	fmt.Printf("RSI %.2f/%.2f/%.2f | WR %.2f/%.2f | BIAS %.2f/%.2f/%.2f\n",
		last.RSI.RSI6, last.RSI.RSI12, last.RSI.RSI24, last.WR.WR10, last.WR.WR14,
		last.BIAS.BIAS6, last.BIAS.BIAS12, last.BIAS.BIAS24)
	stochBull, stochBear := false, false
	if n >= 2 {
		prevStoch := results[n-2].StochRSI
		stochBull, stochBear = analysis.StochStagnation(last.RSI.RSI6, last.StochRSI.K, last.StochRSI.D, prevStoch.K, prevStoch.D)
	}
	fmt.Printf("STOCHRSI K=%.1f D=%.1f | RSI6=%.1f 钝化=%s 择时=%s\n",
		last.StochRSI.K, last.StochRSI.D, last.RSI.RSI6, analysis.StagnationZone(last.RSI.RSI6), analysis.StochTimingText(stochBull, stochBear))
	fmt.Printf("DMI PDI=%.2f MDI=%.2f ADX=%.2f ADXR=%.2f | CMI=%.2f | CHOP=%.2f\n",
		last.DMI.PDI, last.DMI.MDI, last.DMI.ADX, last.DMI.ADXR, last.CMI, last.CHOP)
	fmt.Printf("RISK ATR14=%.3f ATR%%=%.2f | BOLL mid=%.3f upper=%.3f lower=%.3f %%B=%.1f bandwidth=%.2f%% | Donchian20 %.3f..%.3f Donchian55 %.3f..%.3f | MFI14=%.1f\n",
		last.ATR.ATR14, last.ATR.Pct, last.BOLL.Mid, last.BOLL.Upper, last.BOLL.Lower,
		last.BOLL.PercentB, last.BOLL.Bandwidth, last.Donchian.Lower20, last.Donchian.Upper20,
		last.Donchian.Lower55, last.Donchian.Upper55, last.MFI)
	fmt.Printf("SAR_KELT SAR=%.3f stance=%s reversed=%t | Keltner mid=%.3f upper=%.3f lower=%.3f squeeze=%t\n",
		last.SAR.Value, longShort(last.SAR.Long), last.SAR.Reversed, last.Keltner.Mid,
		last.Keltner.Upper, last.Keltner.Lower, last.Keltner.Squeeze)
	fmt.Printf("SUPERTREND value=%.3f trend=%s reversed=%t\n",
		last.SuperTrend.Value, longShort(last.SuperTrend.Long), last.SuperTrend.Reversed)
	fmt.Printf("DONCHIAN_BREAK bull20=%t bear20=%t bull55=%t bear55=%t (今日Close vs 前一根Donchian上下沿)\n",
		analysis.DonchianBreak(candles, results, 20, true), analysis.DonchianBreak(candles, results, 20, false),
		analysis.DonchianBreak(candles, results, 55, true), analysis.DonchianBreak(candles, results, 55, false))
	fmt.Printf("VolMA5=%.0f VolMA10=%.0f VolMA20=%.0f median20=%.0f | 今日量=%.0f 量比=%.2f | OBV=%s | 近5日量价: upDays=%d avgUpVol=%.0f downDays=%d avgDownVol=%.0f\n",
		analysis.MeanTail(volumes, 5), analysis.MeanTail(volumes, 10), volMA20, analysis.MedianTail(volumes, 20),
		lastCandle.Volume, volRatio, analysis.OBVTrend(obv)+"/3日持续="+ynMark(analysis.OBVUp3Day(obv)), upCnt, upAvgVol, downCnt, downAvgVol)
	fmt.Printf("SCORE total=%d delta=%+d dmi=%+d ma=%+d macd=%+d kdjwr=%+d rsi=%+d bias=%+d chopcmi=%+d volume=%+d div=%+d adj=%d perfadj=%+d late=%+d label=%s\n",
		score.Total, score.Delta, score.DMI, score.MA, score.MACD, score.KdjWr, score.RSI,
		score.BIAS, score.CHOPCMI, score.Volume, score.Divergence, scoreAdj, perfAdj, latePen, score.Label)
	// CYQ 筹码指标(换手率数据不足时跳过)
	if n := len(data.Turnovers); n == len(candles) && n > 0 {
		cyq := indicator.CalcCYQ(candles, data.Turnovers)
		if len(cyq) > 0 {
			if len(candles) < indicator.MinCYQBars {
				fmt.Printf("SAMPLE_WARN CYQ 日K根数=%d (<%d), 筹码分布权重偏近期, WINNER/ASR/PRY1 参考价值降低\n",
					len(candles), indicator.MinCYQBars)
			}
			lastC := cyq[len(cyq)-1]
			cyqLabel := "中性"
			switch {
			case lastC.WinnerClose*100 < 10:
				cyqLabel = "深度套牢"
			case lastC.WinnerClose*100 > 90:
				cyqLabel = "全民获利"
			}
			if lastC.ASR > 50 {
				cyqLabel += "·筹码密集"
			} else if lastC.ASR < 25 {
				cyqLabel += "·筹码稀疏"
			}
			if lastC.PRY1 < 40 {
				cyqLabel += "·近年底位"
			}
			fmt.Printf("CYQ WINNER=%.1f%%  ASR=%.1f%%  PRY1=%.1f%%  | 博弈K线 开=%.1f%% 收=%.1f%% 高=%.1f%% 低=%.1f%% 长=%+.1f%%\n",
				lastC.WinnerClose*100, lastC.ASR, lastC.PRY1,
				lastC.CYQK_Open, lastC.CYQK_Close, lastC.CYQK_High, lastC.CYQK_Low, lastC.CYQK_Length)
			fmt.Printf("CYQ 控盘: 无量长阳=%s  90比3=%s  低位=%s  | %s\n",
				ynMark(lastC.VolumeLessBigKline), ynMark(lastC.Ratio90v3), ynMark(lastC.IsLowPosition), cyqLabel)
		}
	}
	// CYC 成本均线(筹码面): 成交量加权均价,无需换手率
	cyc := indicator.CalcCYC(candles)
	if len(cyc) > 0 {
		lastC := cyc[n-1]
		note := ""
		if candles[n-1].Amount <= 0 {
			note = " (Amount=0·退化为收盘价)"
		}
		fmt.Printf("CYC CYC5=%.3f  CYC13=%.3f  CYC34=%.3f  CYC∞=%.3f%s\n",
			lastC.CYC5, lastC.CYC13, lastC.CYC34, lastC.CYCInf, note)
	}
	fmt.Printf("当前策略触发: trendBull=%t(%d/4) trendBear=%t(%d/4) oversold=%t(%d/4) overbought=%t(%d/4) breakBull=%t(%d/3) breakBear=%t(%d/3) revertBull=%t(%d/3) revertBear=%t(%d/3) divBull=%t(%d/1,today=%t) divBear=%t(%d/1,today=%t)\n",
		score.Signals.TrendBull, score.Signals.TrendBullScore, score.Signals.TrendBear, score.Signals.TrendBearScore,
		score.Signals.Oversold, score.Signals.OversoldScore, score.Signals.Overbought, score.Signals.OverboughtScore,
		score.Signals.BreakBull, score.Signals.BreakBullScore, score.Signals.BreakBear, score.Signals.BreakBearScore,
		score.Signals.RevertBull, score.Signals.RevertBullScore, score.Signals.RevertBear, score.Signals.RevertBearScore,
		div.Bull, div.BullScore, div.BullToday, div.Bear, div.BearScore, div.BearToday) // score /1
	printDivergence(div, dates, candles, results)
	printTD(tds[n-1])
	printRecentExtremes(candles, dates, results)
	printStreak(candles)
	verdict := evalBullBear(candles, results, tds, obv, div, perfs, volRatio, score.Signals)
	printBullBear(verdict, score, last)
	printReading(candles, results, tds, obv, score, div, perfs, volRatio, verdict)
	printPerf(perfs)
	printRecentRows(candles, dates, results, tds)

	// 提取 PERF 胜率数据（含样本数）
	var perfTrendFollowBullWin10, perfOverboughtBearWin10, perfDivBearWin10 *float64
	var perfTrendFollowBullN, perfOverboughtBearN, perfDivBearN *int
	var perfTrendFollowBullAvg10 *float64
	for _, p := range perfs {
		if p.Name == "趋势跟随多头" && p.Triggers > 0 {
			val := float64(p.Win10) / float64(p.Triggers) * 100
			perfTrendFollowBullWin10 = &val
			perfTrendFollowBullN = &p.Triggers
			avg := p.Sum10 / float64(p.Triggers)
			perfTrendFollowBullAvg10 = &avg
		}
		if p.Name == "超买反转" && p.Triggers > 0 {
			val := float64(p.Win10) / float64(p.Triggers) * 100
			perfOverboughtBearWin10 = &val
			perfOverboughtBearN = &p.Triggers
		}
		if p.Name == "顶背离" && p.Triggers > 0 {
			val := float64(p.Win10) / float64(p.Triggers) * 100
			perfDivBearWin10 = &val
			perfDivBearN = &p.Triggers
		}
	}

	// Reuse the values already computed above; printing behavior is unchanged.
	lastTD := tds[n-1]
	snap := store.Snapshot{
		Code:      data.Code,
		TradeDate: dates[n-1],

		Close:     lastCandle.Close,
		ChangePct: changePct,

		Low:  lastCandle.Low,
		High: lastCandle.High,

		MA5:  ma5,
		MA10: ma10,
		MA20: ma20,
		MA60: ma60,

		KDJ_J:     last.KDJ.J,
		MACD_DIF:  last.MACD.DIF,
		MACD_DEA:  last.MACD.DEA,
		MACD_Hist: last.MACD.Histogram,
		RSI6:      last.RSI.RSI6,
		WR14:      last.WR.WR14,
		BIAS6:     last.BIAS.BIAS6,
		BIAS24:    last.BIAS.BIAS24,

		PDI:  last.DMI.PDI,
		MDI:  last.DMI.MDI,
		ADX:  last.DMI.ADX,
		ADXR: last.DMI.ADXR,
		CMI:  last.CMI,
		CHOP: last.CHOP,

		ATRPct: last.ATR.Pct,
		BollPB: last.BOLL.PercentB,
		BollBW: last.BOLL.Bandwidth,
		MFI:    last.MFI,

		SARLong:        last.SAR.Long,
		SuperTrendLong: last.SuperTrend.Long,

		VolRatio: volRatio,
		OBVUp:    analysis.OBVUpLast(obv),

		ScoreTotal: score.Total,
		ScoreDelta: score.Delta,
		ScoreLabel: score.Label,
		ScoreAdj:   scoreAdj,

		SigTrendBull:  score.Signals.TrendBull,
		SigOverbought: score.Signals.Overbought,
		SigOversold:   score.Signals.Oversold,

		DivBull:      div.Bull,
		DivBear:      div.Bear,
		DivBearToday: div.BearToday,

		TDSetup:     fmt.Sprintf("%s/%d", analysis.TDSignalText(lastTD.SetupSignal), lastTD.SetupCount),
		TDCountdown: fmt.Sprintf("%s/%d", analysis.TDSignalText(lastTD.CountdownSignal), lastTD.CountdownCount),

		Streak: analysis.StreakValue(candles),

		Ret20:  analysis.NDayReturn(candles, 20),
		Ret60:  analysis.NDayReturn(candles, 60),
		Ret120: analysis.NDayReturn(candles, 120),

		PerfTrendFollowBullWin10: perfTrendFollowBullWin10,
		PerfOverboughtBearWin10:  perfOverboughtBearWin10,
		PerfDivBearWin10:         perfDivBearWin10,
		PerfTrendFollowBullN:     perfTrendFollowBullN,
		PerfOverboughtBearN:      perfOverboughtBearN,
		PerfDivBearN:             perfDivBearN,
		PerfTrendFollowBullAvg10: perfTrendFollowBullAvg10,

		KeltnerSqueeze:   last.Keltner.Squeeze,
		DonchBreak20Bull: analysis.DonchianBreak(candles, results, 20, true),
		DonchBreak55Bull: analysis.DonchianBreak(candles, results, 55, true),

		SARValue:        last.SAR.Value,
		SuperTrendValue: last.SuperTrend.Value,

		// Computed here from the full K-line series: the screener must not
		// re-derive these from sparse snapshot history (see store.Snapshot.Low20).
		Low20:  low20,
		OBVUp3: analysis.OBVUp3Day(obv),
	}
	// 填充振幅和内外盘(如果 proxy.qq.com 提供了)
	if len(data.Amplitudes) == n && n > 0 {
		snap.Amplitude = data.Amplitudes[n-1]
	}
	snap.InsideVol = data.InsideVol
	snap.OutsideVol = data.OutsideVol
	return snap
}

func printDivergence(d analysis.DivergenceState, dates []string, candles []indicator.Candle, results []indicator.Result) {
	if !d.Ready {
		fmt.Println("DIVERGENCE N/A (样本不足: 需要至少20根日K)")
		return
	}
	// Bear: today vs RSI peak reference; Bull: today vs RSI trough reference.
	fmt.Printf("DIVERGENCE bull=%t bear=%t | rsiTrough=%s close=%.3f RSI6=%.1f -> today close=%.3f RSI6=%.1f DIF=%.4f | rsiPeak=%s close=%.3f RSI6=%.1f -> today close=%.3f RSI6=%.1f DIF=%.4f\n",
		d.Bull, d.Bear,
		dates[d.RefLowIdx], candles[d.RefLowIdx].Close, results[d.RefLowIdx].RSI.RSI6,
		candles[d.LowIdx].Close, results[d.LowIdx].RSI.RSI6, results[d.LowIdx].MACD.DIF,
		dates[d.RefHighIdx], candles[d.RefHighIdx].Close, results[d.RefHighIdx].RSI.RSI6,
		candles[d.HighIdx].Close, results[d.HighIdx].RSI.RSI6, results[d.HighIdx].MACD.DIF)
}

func printTD(td indicator.TD) {
	tdPerf := ""
	if td.SetupCount == 9 && td.SetupPerfected {
		tdPerf = "(perfected)"
	}
	fmt.Printf("TD_NOW setup=%s/%d%s countdown=%s/%d\n",
		analysis.TDSignalText(td.SetupSignal), td.SetupCount, tdPerf, analysis.TDSignalText(td.CountdownSignal), td.CountdownCount)
}

func printRecentExtremes(candles []indicator.Candle, dates []string, results []indicator.Result) {
	hiIdx, loIdx := analysis.WindowExtremes(candles, len(candles)-1, 20)
	fmt.Printf("近20日 高点=%s %.3f(DIF=%.4f RSI6=%.1f) 低点=%s %.3f(DIF=%.4f RSI6=%.1f)\n",
		dates[hiIdx], candles[hiIdx].High, results[hiIdx].MACD.DIF, results[hiIdx].RSI.RSI6,
		dates[loIdx], candles[loIdx].Low, results[loIdx].MACD.DIF, results[loIdx].RSI.RSI6)
}

func printStreak(candles []indicator.Candle) {
	sv := analysis.StreakValue(candles)
	if sv > 0 {
		fmt.Printf("连续上涨 %d 日\n", sv)
	} else if sv < 0 {
		fmt.Printf("连续下跌 %d 日\n", -sv)
	}
}

// printBullBear emits a structured BULLBEAR line summarising bull/bear evidence
// from existing indicators. Pure deterministic rules, no LLM.
type bbItem struct {
	Label  string
	Weight int
}

type bullBearVerdict struct {
	Bulls     []bbItem
	Bears     []bbItem
	BullScore int
	BearScore int
	Verdict   string // 偏多 / 偏空 / 中性
	// SwingConflict: 超买超卖轴内部方向矛盾(如 RSI 超卖同时 WR 超买)。**仍按
	// 最极端项正常计票**(分值口径不变、历史可比),仅作提示: 该票此时可信度低,
	// 应以趋势维度(SAR/ST/DMI)复核后再采信。
	SwingConflict bool
}

func (v *bullBearVerdict) addBull(label string, weight int) {
	if weight <= 0 {
		return
	}
	v.Bulls = append(v.Bulls, bbItem{Label: label, Weight: weight})
	v.BullScore += weight
}

func (v *bullBearVerdict) addBear(label string, weight int) {
	if weight <= 0 {
		return
	}
	v.Bears = append(v.Bears, bbItem{Label: label, Weight: weight})
	v.BearScore += weight
}

// perfNote labels how PERF-history reweighting changed a penalty, for display.
func perfNote(orig, adj int) string {
	switch {
	case adj == orig:
		return ""
	case analysis.AbsInt(adj) < analysis.AbsInt(orig):
		return "(本股历史无效·降权)"
	default:
		return "(本股历史有效·加权)"
	}
}

// strongestSwingVote returns the single most extreme overbought/oversold
// reading across RSI6 / WR14 / KDJ-J / BIAS24. These share one axis (CLAUDE.md
// 「指标分工」: close position within the recent high-low range), so only the
// most extreme of them votes — counting all four would triple-count one signal.
// Positive weight = oversold (bullish), negative = overbought (bearish); zero
// means no member is in an extreme zone. Ties keep the first (RSI) for
// determinism.
//
// Taking the most extreme member is deliberately the aggressive choice, and it
// hides disagreement: when one member reads oversold while another reads
// overbought (different lookback windows — KDJ 9 日 vs WR 14 日 vs BIAS 24 日 —
// or a gap distorting one of them), the losing side vanishes silently. conflict
// reports that case so callers can flag the axis as unreliable rather than
// presenting a confident one-sided vote.
func strongestSwingVote(last indicator.Result) (label string, weight int, conflict bool) {
	type cand struct {
		label string
		w     int
	}
	var cands []cand
	switch {
	case last.RSI.RSI6 > 80:
		cands = append(cands, cand{fmt.Sprintf("RSI超买(%.1f)", last.RSI.RSI6), -3})
	case last.RSI.RSI6 > 70:
		cands = append(cands, cand{fmt.Sprintf("RSI偏高(%.1f)", last.RSI.RSI6), -2})
	case last.RSI.RSI6 < 20:
		cands = append(cands, cand{fmt.Sprintf("RSI超卖(%.1f)", last.RSI.RSI6), 3})
	case last.RSI.RSI6 < 30:
		cands = append(cands, cand{fmt.Sprintf("RSI偏低(%.1f)", last.RSI.RSI6), 2})
	}
	switch { // WR 正值口径:高=超卖(看多),低=超买(看空)
	case last.WR.WR14 > 90:
		cands = append(cands, cand{fmt.Sprintf("WR超卖(%.1f)", last.WR.WR14), 3})
	case last.WR.WR14 >= 80:
		cands = append(cands, cand{fmt.Sprintf("WR偏超卖(%.1f)", last.WR.WR14), 2})
	case last.WR.WR14 < 10:
		cands = append(cands, cand{fmt.Sprintf("WR超买(%.1f)", last.WR.WR14), -3})
	case last.WR.WR14 <= 20:
		cands = append(cands, cand{fmt.Sprintf("WR偏超买(%.1f)", last.WR.WR14), -2})
	}
	switch {
	case last.BIAS.BIAS24 > 15:
		cands = append(cands, cand{fmt.Sprintf("乖离过大(%.1f)", last.BIAS.BIAS24), -3})
	case last.BIAS.BIAS24 > 10:
		cands = append(cands, cand{fmt.Sprintf("乖离偏大(%.1f)", last.BIAS.BIAS24), -2})
	case last.BIAS.BIAS24 < -15:
		cands = append(cands, cand{fmt.Sprintf("负乖离过大(%.1f)", last.BIAS.BIAS24), 3})
	case last.BIAS.BIAS24 < -10:
		cands = append(cands, cand{fmt.Sprintf("负乖离偏大(%.1f)", last.BIAS.BIAS24), 2})
	}
	switch {
	case last.KDJ.J > 100:
		cands = append(cands, cand{fmt.Sprintf("KDJ-J超买(%.1f)", last.KDJ.J), -2})
	case last.KDJ.J < 0:
		cands = append(cands, cand{fmt.Sprintf("KDJ-J超卖(%.1f)", last.KDJ.J), 2})
	}
	best, bestLabel := 0, ""
	sawBull, sawBear := false, false
	for _, c := range cands {
		if c.w > 0 {
			sawBull = true
		} else if c.w < 0 {
			sawBear = true
		}
		if analysis.AbsInt(c.w) > analysis.AbsInt(best) {
			best, bestLabel = c.w, c.label
		}
	}
	return bestLabel, best, sawBull && sawBear
}

// swingConflictText lists every extreme reading on the overbought/oversold axis,
// used to explain a SWING_CONFLICT. Order matches strongestSwingVote's scan.
func swingMembers(last indicator.Result) []string {
	var out []string
	add := func(format string, v float64) { out = append(out, fmt.Sprintf(format, v)) }
	switch {
	case last.RSI.RSI6 > 70:
		add("RSI6=%.1f(偏高)", last.RSI.RSI6)
	case last.RSI.RSI6 < 30:
		add("RSI6=%.1f(偏低)", last.RSI.RSI6)
	}
	switch {
	case last.WR.WR14 >= 80:
		add("WR14=%.1f(超卖)", last.WR.WR14)
	case last.WR.WR14 <= 20:
		add("WR14=%.1f(超买)", last.WR.WR14)
	}
	switch {
	case last.BIAS.BIAS24 > 10:
		add("BIAS24=%.1f(正乖离)", last.BIAS.BIAS24)
	case last.BIAS.BIAS24 < -10:
		add("BIAS24=%.1f(负乖离)", last.BIAS.BIAS24)
	}
	switch {
	case last.KDJ.J > 100:
		add("KDJ-J=%.1f(超买)", last.KDJ.J)
	case last.KDJ.J < 0:
		add("KDJ-J=%.1f(超卖)", last.KDJ.J)
	}
	return out
}

// evalBullBear synthesizes a weighted, de-duplicated bull/bear verdict from the
// CLI indicators. Each axis (CLAUDE.md「指标分工」) votes at most once so
// same-source indicators can't manufacture false consensus: trend folds
// DMI + SAR/ST + MA into a single vote, the overbought/oversold axis takes only
// its most extreme member (RSI/WR/KDJ-J/BIAS), and the composite score is NOT
// fed back as a vote. Overbought and top-divergence bear votes are reweighted by
// this stock's own PERF history (perfScale), matching score_adj's methodology.
func evalBullBear(candles []indicator.Candle, results []indicator.Result, tds []indicator.TD, obv []float64, div analysis.DivergenceState, perfs []analysis.PerfStat, volRatio float64, sig analysis.SignalState) bullBearVerdict {
	n := len(candles)
	last := results[n-1]
	lastTD := tds[n-1]
	var v bullBearVerdict

	obWin, obN := analysis.PerfWin10(perfs, "超买反转")
	divWin, divN := analysis.PerfWin10(perfs, "顶背离")

	// 趋势维度:DMI 方向强度 + SAR/ST 双确认 + MA 排列,合并一票(三者一致才满权)。
	ma5, ma10, ma20, ma60 := analysis.CloseMA(candles, n-1, 5), analysis.CloseMA(candles, n-1, 10), analysis.CloseMA(candles, n-1, 20), analysis.CloseMA(candles, n-1, 60)
	adx := last.DMI.ADX
	maBull := ma5 > ma10 && ma10 > ma20 && ma20 > ma60
	maBear := ma5 < ma10 && ma10 < ma20 && ma20 < ma60
	sarStBull := last.SAR.Long && last.SuperTrend.Long
	sarStBear := !last.SAR.Long && !last.SuperTrend.Long
	bullConfirm := analysis.CountTrue(adx > 25 && last.DMI.PDI > last.DMI.MDI, sarStBull, maBull)
	bearConfirm := analysis.CountTrue(adx > 25 && last.DMI.MDI > last.DMI.PDI, sarStBear, maBear)
	switch {
	case bullConfirm > bearConfirm:
		v.addBull(fmt.Sprintf("趋势多头(ADX%.1f,确认%d/3)", adx, bullConfirm), bullConfirm)
	case bearConfirm > bullConfirm:
		v.addBear(fmt.Sprintf("趋势空头(ADX%.1f,确认%d/3)", adx, bearConfirm), bearConfirm)
	}

	// 动量维度:MACD 趋势性动量,独立一票。
	switch {
	case last.MACD.Histogram > 0 && last.MACD.DIF > last.MACD.DEA:
		v.addBull(fmt.Sprintf("MACD金叉(H%+.4f)", last.MACD.Histogram), 2)
	case last.MACD.Histogram < 0 && last.MACD.DIF < last.MACD.DEA:
		v.addBear(fmt.Sprintf("MACD死叉(H%+.4f)", last.MACD.Histogram), 2)
	}

	// 超买超卖维度:RSI/WR/KDJ-J/BIAS 同源,只取最极端的一项计一票;看空侧按 PERF 调权。
	//
	// PERF「超买反转」是 RSI6>70 + WR/KDJ + BIAS24>10 的 3/3 复合信号,它的历史
	// 胜率只能用来调**同一个复合信号**的权重。单指标(可能只是 BIAS24 略过 10)
	// 投出的看空票不在该样本口径内,拿复合信号胜率去调它是分母错配,还会与落库的
	// score_adj 得出两套结论。gate 与 ApplyPerfAdaptive 保持一致:仅当复合超买
	// 信号真的触发时才按 PERF 调权。
	// 同轴内方向矛盾只做标记,不改计票口径(仍取最极端项)——分值保持历史可比,
	// 冲突由 SWING_CONFLICT 行提示人工判断。见 CLAUDE.md「指标分工」。
	label, swingW, swingConflict := strongestSwingVote(last)
	v.SwingConflict = swingConflict
	if swingW < 0 {
		adj := swingW
		note := ""
		if sig.Overbought {
			adj = analysis.PerfScale(swingW, obWin, obN, 35, 55)
			note = perfNote(swingW, adj)
		}
		v.addBear(label+note, -adj)
	} else if swingW > 0 {
		v.addBull(label, swingW)
	}

	// 资金维度:OBV 净流向 + 量比异动,合并一票。
	moneyW := 0
	var moneyParts []string
	switch d := analysis.OBVDelta(obv); {
	case d > 0:
		moneyW++
		moneyParts = append(moneyParts, "OBV净流入")
	case d < 0:
		moneyW--
		moneyParts = append(moneyParts, "OBV净流出")
	}
	priceUp := n > 1 && candles[n-1].Close > candles[n-2].Close
	priceDown := n > 1 && candles[n-1].Close < candles[n-2].Close
	switch {
	case volRatio > analysis.VolSurge && priceUp:
		moneyW += 2
		moneyParts = append(moneyParts, fmt.Sprintf("放量上涨(量比%.2f)", volRatio))
	case volRatio > analysis.VolSurge && priceDown:
		moneyW -= 2
		moneyParts = append(moneyParts, fmt.Sprintf("放量下跌(量比%.2f)", volRatio))
	}
	switch {
	case moneyW > 0:
		v.addBull("资金:"+joinComma(moneyParts), moneyW)
	case moneyW < 0:
		v.addBear("资金:"+joinComma(moneyParts), -moneyW)
	}

	// 择时维度:TD countdown 反转倒计时(统一 w=1——学术证据显示
	// TD Sequential 计数体系无统计显著预测力,完成 13 也不改变此结论)。
	if lastTD.CountdownCount > 0 {
		w := 1
		switch lastTD.CountdownSignal {
		case indicator.TDSell:
			v.addBear(fmt.Sprintf("TD见顶countdown/%d", lastTD.CountdownCount), w)
		case indicator.TDBuy:
			v.addBull(fmt.Sprintf("TD见底countdown/%d", lastTD.CountdownCount), w)
		}
	}

	// 背离维度:顶背离看空(按本股「顶背离」历史调权),底背离看多;非当日各降一档。
	if div.Bear {
		w := -2
		label := "顶背离"
		if !div.BearToday {
			w = -1
			label = "顶背离(非当日)"
		}
		adj := analysis.PerfScale(w, divWin, divN, 40, 55)
		v.addBear(label+perfNote(w, adj), -adj)
	}
	if div.Bull {
		w := 2
		label := "底背离"
		if !div.BullToday {
			w = 1
			label = "底背离(非当日)"
		}
		v.addBull(label, w)
	}

	switch {
	case v.BullScore-v.BearScore >= 2:
		v.Verdict = "偏多"
	case v.BearScore-v.BullScore >= 2:
		v.Verdict = "偏空"
	default:
		v.Verdict = "中性"
	}
	return v
}

// printBullBear renders the weighted verdict. The composite score is shown as
// context only (score=), never as a vote — it is the sum of the very dimensions
// already counted above, so feeding it back would double-count everything.
func printBullBear(v bullBearVerdict, score analysis.ScoreState, last indicator.Result) {
	fmt.Printf("BULLBEAR bull=[%s] bear=[%s] bullW=%d bearW=%d verdict=%s score=%d\n",
		bbItemsText(v.Bulls), bbItemsText(v.Bears), v.BullScore, v.BearScore, v.Verdict, score.Total)
	if v.SwingConflict {
		fmt.Printf("SWING_CONFLICT 超买超卖轴方向矛盾(%s) → 该票仍按最极端项计入但可信度低, 请以趋势维度(SAR/ST/DMI)复核\n",
			joinComma(swingMembers(last)))
	}
}

// bbItemsText renders weighted items as "label·wN,...", or "-" when empty.
func bbItemsText(items []bbItem) string {
	if len(items) == 0 {
		return "-"
	}
	parts := make([]string, len(items))
	for i, it := range items {
		parts[i] = fmt.Sprintf("%s·w%d", it.Label, it.Weight)
	}
	return joinComma(parts)
}

// printReading emits a per-dimension natural-language reading of the indicators
// (CLAUDE.md 分析输出口径: 量能一律用量比数值). It only synthesizes values already
// computed upstream — nothing here is persisted or recomputed independently.
func printReading(candles []indicator.Candle, results []indicator.Result, tds []indicator.TD, obv []float64, score analysis.ScoreState, div analysis.DivergenceState, perfs []analysis.PerfStat, volRatio float64, v bullBearVerdict) {
	n := len(candles)
	last := results[n-1]
	lastTD := tds[n-1]

	trend := "震荡/方向不明"
	switch {
	case last.DMI.ADX > 25 && last.DMI.PDI > last.DMI.MDI:
		trend = "上升趋势延续"
	case last.DMI.ADX > 25 && last.DMI.MDI > last.DMI.PDI:
		trend = "下降趋势延续"
	case last.DMI.ADX < 20:
		trend = "无趋势震荡(ADX<20)"
	}
	fmt.Printf("READ 趋势: ADX=%.1f PDI/MDI=%.1f/%.1f SAR/ST=%s/%s CHOP=%.1f → %s\n",
		last.DMI.ADX, last.DMI.PDI, last.DMI.MDI, longShort(last.SAR.Long), longShort(last.SuperTrend.Long), last.CHOP, trend)

	momentum := "动量中性"
	switch {
	case last.RSI.RSI6 > 70:
		momentum = "短线超买,警惕回落"
	case last.RSI.RSI6 < 30:
		momentum = "短线超卖,关注企稳"
	case last.MACD.DIF > last.MACD.DEA && last.MACD.Histogram > 0:
		momentum = "MACD金叉,动量向上"
	case last.MACD.DIF < last.MACD.DEA && last.MACD.Histogram < 0:
		momentum = "MACD死叉,动量转弱"
	}
	fmt.Printf("READ 动量: MACD DIF/DEA/H=%.4f/%.4f/%+.4f RSI6=%.1f KDJ-J=%.1f → %s\n",
		last.MACD.DIF, last.MACD.DEA, last.MACD.Histogram, last.RSI.RSI6, last.KDJ.J, momentum)

	vol := "通道内正常波动"
	switch {
	case last.Keltner.Squeeze:
		vol = "波动压缩(squeeze),临近突破·方向未定"
	case last.BOLL.PercentB > 100:
		vol = "突破BOLL上轨,强势但偏热"
	case last.BOLL.PercentB < 0:
		vol = "跌破BOLL下轨,弱势但偏冷"
	}
	fmt.Printf("READ 波动: ATR%%=%.2f BOLL %%B=%.1f bandwidth=%.2f%% squeeze=%t → %s\n",
		last.ATR.Pct, last.BOLL.PercentB, last.BOLL.Bandwidth, last.Keltner.Squeeze, vol)

	money := "量价中性"
	switch {
	case volRatio > analysis.VolSurge:
		money = fmt.Sprintf("量比%.2f(>%.1f)放量,需看价配合", volRatio, analysis.VolSurge)
	case volRatio < analysis.VolQuiet:
		money = fmt.Sprintf("量比%.2f(<%.1f)清淡", volRatio, analysis.VolQuiet)
	}
	mfiTag := ""
	switch {
	case last.MFI > 80:
		mfiTag = ",MFI>80资金偏热"
	case last.MFI < 20:
		mfiTag = ",MFI<20资金偏冷"
	}
	fmt.Printf("READ 资金: 量比=%.2f OBV=%s MFI=%.1f → %s%s\n",
		volRatio, analysis.OBVTrend(obv), last.MFI, money, mfiTag)

	var timing []string
	if lastTD.SetupCount > 0 {
		timing = append(timing, fmt.Sprintf("TD-setup %s/%d", analysis.TDSignalText(lastTD.SetupSignal), lastTD.SetupCount))
	}
	if lastTD.CountdownCount > 0 {
		timing = append(timing, fmt.Sprintf("TD-countdown %s/%d", analysis.TDSignalText(lastTD.CountdownSignal), lastTD.CountdownCount))
	}
	if div.Bear {
		t := "顶背离"
		if !div.BearToday {
			t = "顶背离(非当日)"
		}
		timing = append(timing, t)
	}
	if div.Bull {
		t := "底背离"
		if !div.BullToday {
			t = "底背离(非当日)"
		}
		timing = append(timing, t)
	}
	timingText := "无明确反转择时信号"
	if len(timing) > 0 {
		timingText = joinComma(timing)
	}
	fmt.Printf("READ 择时: %s\n", timingText)

	latePen, streak, biasAtr := analysis.LateStagePenalty(candles, results)
	lateText := "无明显末端拥挤"
	if latePen < 0 {
		lateText = fmt.Sprintf("末端追高风险(score_adj %+d)", latePen)
	}
	streakText := "无连涨"
	if streak >= 2 {
		streakText = fmt.Sprintf("连涨%d日", streak)
	} else if streak <= -2 {
		streakText = fmt.Sprintf("连跌%d日", -streak)
	}
	fmt.Printf("READ 末端: %s bias24/atr=%.1f(>4偏热) → %s\n", streakText, biasAtr, lateText)

	var caveats []string
	if score.Signals.Overbought {
		if w, nn := analysis.PerfWin10(perfs, "超买反转"); nn >= 10 && w < 35 {
			caveats = append(caveats, fmt.Sprintf("超买本股win10=%.0f%%(<35%%),已降权", w))
		}
	}
	if div.Bear {
		if w, nn := analysis.PerfWin10(perfs, "顶背离"); nn >= 10 && w < 40 {
			caveats = append(caveats, fmt.Sprintf("顶背离本股win10=%.0f%%(<40%%),已降权", w))
		}
	}
	caveatText := "无"
	if len(caveats) > 0 {
		caveatText = joinComma(caveats)
	}
	fmt.Printf("READ 综述: 评分%d(%s) 加权研判=%s(多%d/空%d) | PERF修正:%s\n",
		score.Total, score.Label, v.Verdict, v.BullScore, v.BearScore, caveatText)
}

func joinComma(ss []string) string {
	s := ""
	for i, v := range ss {
		if i > 0 {
			s += ","
		}
		s += v
	}
	return s
}

func printPerf(perfs []analysis.PerfStat) {
	// N 是信号 0→1 的**边沿数**,不是独立样本数: 相隔不足 10 日的两次边沿共享
	// 前向窗口,胜率的有效样本量小于 N。因此 N 不能直接当独立试验数解读,
	// 显著性一律走 analysis.WilsonBounds(score_adj / screener 门槛已统一用它)。
	fmt.Println("历史信号性能(仅用信号当日及以前判断, 统计未来5/10日; N=信号边沿数, 窗口重叠使有效样本小于N, 显著性看Wilson区间):")
	for _, p := range perfs {
		if p.Triggers == 0 {
			fmt.Printf("PERF %-14s dir=%s N=0\n", p.Name, p.Direction)
			continue
		}
		fmt.Printf("PERF %-14s dir=%s N=%d win5=%.0f%% avg5=%.2f%% win10=%.0f%% avg10=%.2f%% best10=%.2f%% worst10=%.2f%% maxAdverse=%.2f%% last=%s\n",
			p.Name, p.Direction, p.Triggers,
			float64(p.Win5)/float64(p.Triggers)*100, p.Sum5/float64(p.Triggers),
			float64(p.Win10)/float64(p.Triggers)*100, p.Sum10/float64(p.Triggers),
			p.Best10, p.Worst10, p.MaxAdverse, p.LastDate)
	}
}

func printRecentRows(candles []indicator.Candle, dates []string, results []indicator.Result, tds []indicator.TD) {
	start := analysis.MaxInt(0, len(candles)-15)
	for i := start; i < len(candles); i++ {
		row := results[i]
		volumeTag := "平"
		// 与 SCORE/BULLBEAR 行同口径(analysis.VolRatio),避免同一屏出现
		// "量比 0.69" 与"放量"并存的自相矛盾。
		if ratio := analysis.VolRatio(candles, i); ratio > 0 {
			if ratio > analysis.VolSurge {
				volumeTag = "放量"
			} else if ratio < analysis.VolQuiet {
				volumeTag = "缩量"
			}
		}
		priceDir := "↑"
		if i > 0 && candles[i].Close < candles[i-1].Close {
			priceDir = "↓"
		} else if i > 0 && candles[i].Close == candles[i-1].Close {
			priceDir = "→"
		}
		sarTag := "多"
		if !row.SAR.Long {
			sarTag = "空"
		}
		if row.SAR.Reversed {
			sarTag += "*"
		}
		fmt.Printf("%s c=%.3f %s Vol=%.0f(%s) J=%.1f MH=%.4f RSI6=%.1f MFI=%.1f ATR%%=%.2f PDI=%.1f MDI=%.1f ADX=%.1f CHOP=%.1f TD=%s SAR=%s\n",
			dates[i], candles[i].Close, priceDir, candles[i].Volume, volumeTag, row.KDJ.J,
			row.MACD.Histogram, row.RSI.RSI6, row.MFI, row.ATR.Pct, row.DMI.PDI,
			row.DMI.MDI, row.DMI.ADX, row.CHOP, analysis.TDShort(tds[i]), sarTag)
	}
}

func longShort(long bool) string {
	if long {
		return "多"
	}
	return "空"
}

func ynMark(v bool) string {
	if v {
		return "是"
	}
	return "否"
}
