package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
	"time"

	"stock-tui/internal/api"
	"stock-tui/internal/indicator"
	"stock-tui/internal/market"
	"stock-tui/internal/store"
)

var httpClient = &http.Client{Timeout: 15 * time.Second}

const defaultBars = 800

type quoteData struct {
	Qfqday [][]json.RawMessage          `json:"qfqday"`
	Day    [][]json.RawMessage          `json:"day"`
	Qt     map[string][]json.RawMessage `json:"qt"`
}

type klineResponse struct {
	Data map[string]quoteData `json:"data"`
}

type seriesData struct {
	Code    string
	Name    string
	Dates   []string
	Candles []indicator.Candle
}

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
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: go run ./cmd/indicator-analyze [-n bars] [-save] <code>")
	}

	code, ok := market.NormalizeCode(fs.Arg(0))
	if !ok {
		return fmt.Errorf("invalid code: %s", fs.Arg(0))
	}
	if *bars <= 0 {
		return fmt.Errorf("-n must be positive")
	}

	data, err := fetchDailyKline(code, *bars)
	if err != nil {
		return err
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
		if err := saveSnapshot(data, snap); err != nil {
			return fmt.Errorf("save snapshot: %w", err)
		}
		fmt.Printf("SAVED %s@%s -> %s\n", snap.Code, snap.TradeDate, store.DefaultPath())
	}
	return nil
}

// saveSnapshot upserts the instrument (name/market from the fetched series) and
// the analysis snapshot into the SQLite store at store.DefaultPath().
func saveSnapshot(data seriesData, snap store.Snapshot) error {
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

func fetchDailyKline(code string, bars int) (seriesData, error) {
	url := fmt.Sprintf("https://ifzq.gtimg.cn/appstock/app/fqkline/get?param=%s,day,,,%d,qfq", code, bars)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return seriesData{}, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := httpClient.Do(req)
	if err != nil {
		return seriesData{}, fmt.Errorf("fetch %s: %w", code, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return seriesData{}, fmt.Errorf("fetch %s: HTTP %s", code, resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return seriesData{}, err
	}

	var parsed klineResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return seriesData{}, err
	}

	raw, ok := parsed.Data[code]
	if !ok {
		return seriesData{}, fmt.Errorf("no klines for %s", code)
	}

	rows := raw.Qfqday
	if len(rows) == 0 {
		rows = raw.Day
	}
	if len(rows) == 0 {
		return seriesData{}, fmt.Errorf("no klines for %s", code)
	}

	name := code
	if q, ok := raw.Qt[code]; ok && len(q) > 1 {
		name = rawString(q[1])
	}

	dates := make([]string, len(rows))
	candles := make([]indicator.Candle, len(rows))
	for i, row := range rows {
		if len(row) < 6 {
			return seriesData{}, fmt.Errorf("row %d short", i)
		}
		dates[i] = rawString(row[0])
		candles[i] = indicator.Candle{
			Close:  rawFloat(row[2]),
			High:   rawFloat(row[3]),
			Low:    rawFloat(row[4]),
			Volume: rawFloat(row[5]),
		}
	}

	return seriesData{Code: code, Name: name, Dates: dates, Candles: candles}, nil
}

func printAnalysis(data seriesData) store.Snapshot {
	candles := data.Candles
	dates := data.Dates
	results := indicator.Calculate(candles)
	tds := indicator.TDSequential(candles)
	n := len(candles)
	last := results[n-1]
	lastCandle := candles[n-1]
	closes := closeSeries(candles)
	ma5, ma10, ma20, ma60 := meanTail(closes, 5), meanTail(closes, 10), meanTail(closes, 20), meanTail(closes, 60)
	volumes := volumeSeries(candles)
	lowAll, highAll := rangeLowHigh(candles, 0, n)
	low20, high20 := rangeLowHigh(candles, n-20, n)
	low60, high60 := rangeLowHigh(candles, n-60, n)
	low120, high120 := rangeLowHigh(candles, n-120, n)
	volMA20 := meanTail(volumes, 20)
	volRatio := ratio(lastCandle.Volume, volMA20)
	obv := obvSeries(candles)
	upCnt, upAvgVol, downCnt, downAvgVol := recentVolumeHealth(candles, 5)
	score := scoreResult(candles, results, obv, upAvgVol, downAvgVol, volRatio)
	div := divergence(candles, results, n-1)
	// PERF stats must exist before scoring: applyPerfAdaptive reweighs the
	// overbought/divergence penalties by this stock's own signal history.
	perfs := performance(candles, dates, results, tds, obv)
	scoreAdj, perfAdj := applyPerfAdaptive(score, perfs)
	// Late-stage crowding penalty folds into score_adj (the adaptive sidecar),
	// not score_total — keeping the fixed scale historically comparable while the
	// single-stock CLI view still reflects end-of-move overheating.
	latePen, _, _ := lateStagePenalty(candles, results)
	scoreAdj = clampInt(scoreAdj+latePen, 0, 100)

	change, changePct := 0.0, 0.0
	if n > 1 {
		change = lastCandle.Close - candles[n-2].Close
		changePct = ratio(change, candles[n-2].Close) * 100
	}

	fmt.Printf("%s %s  %s..%s (%d根)  close=%.3f change=%+.3f pct=%+.2f%% high=%.3f low=%.3f volume=%.0f\n",
		data.Code, data.Name, dates[0], dates[n-1], n, lastCandle.Close, change, changePct, lastCandle.High, lastCandle.Low, lastCandle.Volume)
	if n < 120 {
		fmt.Printf("SAMPLE_WARN 日K根数=%d (<120), 均线预热、背离检测和历史PERF样本都偏弱\n", n)
	}
	fmt.Printf("MA5=%.3f MA10=%.3f MA20=%.3f MA60=%.3f | allRange %.3f..%.3f pos=%.0f%% | range20 %.3f..%.3f pos=%.0f%% | range60 %.3f..%.3f pos=%.0f%% | range120 %.3f..%.3f pos=%.0f%%\n",
		ma5, ma10, ma20, ma60,
		lowAll, highAll, position(lastCandle.Close, lowAll, highAll),
		low20, high20, position(lastCandle.Close, low20, high20),
		low60, high60, position(lastCandle.Close, low60, high60),
		low120, high120, position(lastCandle.Close, low120, high120))
	fmt.Printf("KDJ K=%.2f D=%.2f J=%.2f | MACD DIF=%.4f DEA=%.4f H=%.4f\n",
		last.KDJ.K, last.KDJ.D, last.KDJ.J, last.MACD.DIF, last.MACD.DEA, last.MACD.Histogram)
	fmt.Printf("RSI %.2f/%.2f/%.2f | WR %.2f/%.2f | BIAS %.2f/%.2f/%.2f\n",
		last.RSI.RSI6, last.RSI.RSI12, last.RSI.RSI24, last.WR.WR10, last.WR.WR14,
		last.BIAS.BIAS6, last.BIAS.BIAS12, last.BIAS.BIAS24)
	stochBull, stochBear := false, false
	if n >= 2 {
		prevStoch := results[n-2].StochRSI
		stochBull, stochBear = stochStagnation(last.RSI.RSI6, last.StochRSI.K, last.StochRSI.D, prevStoch.K, prevStoch.D)
	}
	fmt.Printf("STOCHRSI K=%.1f D=%.1f | RSI6=%.1f 钝化=%s 择时=%s\n",
		last.StochRSI.K, last.StochRSI.D, last.RSI.RSI6, stagnationZone(last.RSI.RSI6), stochTimingText(stochBull, stochBear))
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
		donchianBreak(candles, results, 20, true), donchianBreak(candles, results, 20, false),
		donchianBreak(candles, results, 55, true), donchianBreak(candles, results, 55, false))
	fmt.Printf("VolMA5=%.0f VolMA10=%.0f VolMA20=%.0f median20=%.0f | 今日量=%.0f 量比=%.2f | OBV=%s | 近5日量价: upDays=%d avgUpVol=%.0f downDays=%d avgDownVol=%.0f\n",
		meanTail(volumes, 5), meanTail(volumes, 10), volMA20, medianTail(volumes, 20),
		lastCandle.Volume, volRatio, obvTrend(obv), upCnt, upAvgVol, downCnt, downAvgVol)
	fmt.Printf("SCORE total=%d delta=%+d dmi=%+d ma=%+d macd=%+d kdjwr=%+d rsi=%+d bias=%+d chopcmi=%+d volume=%+d sar=%+d div=%+d adj=%d perfadj=%+d late=%+d label=%s\n",
		score.Total, score.Delta, score.DMI, score.MA, score.MACD, score.KdjWr, score.RSI,
		score.BIAS, score.CHOPCMI, score.Volume, score.SAR, score.Divergence, scoreAdj, perfAdj, latePen, score.Label)
	fmt.Printf("当前策略触发: trendBull=%t(%d/4) trendBear=%t(%d/4) oversold=%t(%d/4) overbought=%t(%d/4) breakBull=%t(%d/3) breakBear=%t(%d/3) revertBull=%t(%d/3) revertBear=%t(%d/3) divBull=%t(%d/1,today=%t) divBear=%t(%d/1,today=%t)\n",
		score.Signals.TrendBull, score.Signals.TrendBullScore, score.Signals.TrendBear, score.Signals.TrendBearScore,
		score.Signals.Oversold, score.Signals.OversoldScore, score.Signals.Overbought, score.Signals.OverboughtScore,
		score.Signals.BreakBull, score.Signals.BreakBullScore, score.Signals.BreakBear, score.Signals.BreakBearScore,
		score.Signals.RevertBull, score.Signals.RevertBullScore, score.Signals.RevertBear, score.Signals.RevertBearScore,
		div.Bull, div.BullScore, div.BullToday, div.Bear, div.BearScore, div.BearToday) // score /1
	printDivergence(div, dates, candles, results)
	printTD(tds[n-1])
	printFib(candles, dates, 60)
	printFib(candles, dates, 120)
	printRecentExtremes(candles, dates, results)
	printStreak(candles)
	verdict := evalBullBear(candles, results, tds, obv, div, perfs, volRatio)
	printBullBear(verdict, score)
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
		OBVUp:    len(obv) >= 6 && obv[len(obv)-1] > obv[len(obv)-6],

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

		TDSetup:     fmt.Sprintf("%s/%d", tdSignalText(lastTD.SetupSignal), lastTD.SetupCount),
		TDCountdown: fmt.Sprintf("%s/%d", tdSignalText(lastTD.CountdownSignal), lastTD.CountdownCount),

		Streak: streakValue(candles),

		Ret20:  nDayReturn(candles, 20),
		Ret60:  nDayReturn(candles, 60),
		Ret120: nDayReturn(candles, 120),

		PerfTrendFollowBullWin10: perfTrendFollowBullWin10,
		PerfOverboughtBearWin10:  perfOverboughtBearWin10,
		PerfDivBearWin10:         perfDivBearWin10,
		PerfTrendFollowBullN:     perfTrendFollowBullN,
		PerfOverboughtBearN:      perfOverboughtBearN,
		PerfDivBearN:             perfDivBearN,
		PerfTrendFollowBullAvg10: perfTrendFollowBullAvg10,

		KeltnerSqueeze:   last.Keltner.Squeeze,
		DonchBreak20Bull: donchianBreak(candles, results, 20, true),
		DonchBreak55Bull: donchianBreak(candles, results, 55, true),

		SARValue:        last.SAR.Value,
		SuperTrendValue: last.SuperTrend.Value,
	}
	return snap
}

// nDayReturn computes (close[last] - close[last-n]) / close[last-n] * 100.
// Returns 0 if there are fewer than n+1 bars or the base price is zero.
func nDayReturn(candles []indicator.Candle, n int) float64 {
	last := len(candles) - 1
	base := last - n
	if base < 0 || candles[base].Close == 0 {
		return 0
	}
	return (candles[last].Close - candles[base].Close) / candles[base].Close * 100
}

type scoreState struct {
	Total      int
	Delta      int
	DMI        int
	MA         int
	MACD       int
	KdjWr      int // merged KDJ+WR (same source, pick the more extreme signal)
	RSI        int
	BIAS       int
	CHOPCMI    int
	Volume     int
	SAR        int // SAR/SuperTrend dual confirmation
	Divergence int // bull/bear divergence signal
	Label      string
	Signals    signalState
}

type signalState struct {
	TrendBullScore  int
	TrendBearScore  int
	OversoldScore   int
	OverboughtScore int
	BreakBullScore  int
	BreakBearScore  int
	RevertBullScore int
	RevertBearScore int
	TrendBull       bool
	TrendBear       bool
	Oversold        bool
	Overbought      bool
	BreakBull       bool
	BreakBear       bool
	RevertBull      bool
	RevertBear      bool
	StochStagBull   bool // StochRSI %K up-cross inside an oversold-pinned RSI zone
	StochStagBear   bool // StochRSI %K down-cross inside an overbought-pinned RSI zone
}

// Volume-ratio (量比) thresholds, unified across scoring, signals, and display
// so one volRatio reads the same everywhere (CLAUDE.md 量能口径): 缩量 < volQuiet,
// 放量 ≥ volSurge, 强放量 ≥ volStrong.
const (
	volQuiet  = 0.8
	volSurge  = 1.5
	volStrong = 2.0
)

func scoreResult(candles []indicator.Candle, results []indicator.Result, obv []float64, avgUpVol, avgDownVol, volRatio float64) scoreState {
	n := len(candles)
	last := results[n-1]
	prev := last
	if n > 1 {
		prev = results[n-2]
	}

	score := scoreState{Signals: evalSignals(candles, results, obv, n-1)}
	dmiDiff := last.DMI.PDI - last.DMI.MDI
	switch {
	case dmiDiff > 15 && last.DMI.ADX > 25:
		score.DMI = 12
	case dmiDiff > 8 && last.DMI.ADX > 20:
		score.DMI = 8
	case dmiDiff > 0:
		score.DMI = 3
	case dmiDiff < -15 && last.DMI.ADX > 25:
		score.DMI = -12
	case dmiDiff < -8 && last.DMI.ADX > 20:
		score.DMI = -8
	case dmiDiff < 0:
		score.DMI = -3
	}

	ma5, ma10, ma20, ma60 := closeMA(candles, n-1, 5), closeMA(candles, n-1, 10), closeMA(candles, n-1, 20), closeMA(candles, n-1, 60)
	switch countTrue(candles[n-1].Close > ma5, candles[n-1].Close > ma10, candles[n-1].Close > ma20, candles[n-1].Close > ma60) {
	case 4:
		score.MA = 10
	case 3:
		score.MA = 6
	case 2:
		score.MA = 2
	case 1:
		score.MA = -4
	default:
		score.MA = -10
	}
	if ma5 > ma10 && ma10 > ma20 && ma20 > ma60 {
		score.MA += 2
	} else if ma5 < ma10 && ma10 < ma20 && ma20 < ma60 {
		score.MA -= 2
	}

	macdGold := last.MACD.DIF >= last.MACD.DEA
	switch {
	case last.MACD.DIF > 0 && macdGold && last.MACD.Histogram > prev.MACD.Histogram:
		score.MACD = 8
	case last.MACD.DIF > 0 && macdGold:
		score.MACD = 5
	case last.MACD.DIF > 0:
		score.MACD = 2
	case last.MACD.DIF < 0 && macdGold:
		score.MACD = -2
	case last.MACD.DIF < 0 && last.MACD.Histogram < prev.MACD.Histogram:
		score.MACD = -8
	case last.MACD.DIF < 0:
		score.MACD = -5
	}

	// KDJ + WR merged: both measure close position within N-day high-low range.
	// Take the more extreme signal to avoid double-counting same-source indicators.
	kdjGold := last.KDJ.K >= last.KDJ.D
	kdjSignal := 0
	switch {
	case last.KDJ.K < 20 && kdjGold:
		kdjSignal = 7
	case last.KDJ.K < 20:
		kdjSignal = 1
	case last.KDJ.K <= 80 && kdjGold:
		kdjSignal = 3
	case last.KDJ.K <= 80:
		kdjSignal = -3
	case kdjGold:
		kdjSignal = -2
	default:
		kdjSignal = -7
	}
	wrSignal := 0
	switch {
	case last.WR.WR14 > 90:
		wrSignal = 4
	case last.WR.WR14 >= 80:
		wrSignal = 2
	case last.WR.WR14 >= 60:
		wrSignal = 1
	case last.WR.WR14 >= 40:
		wrSignal = 0
	case last.WR.WR14 >= 20:
		wrSignal = -1
	case last.WR.WR14 >= 10:
		wrSignal = -2
	default:
		wrSignal = -4
	}
	if abs(kdjSignal) >= abs(wrSignal) {
		score.KdjWr = kdjSignal
	} else {
		score.KdjWr = wrSignal
	}

	switch {
	case last.RSI.RSI6 < 20:
		score.RSI = 5
	case last.RSI.RSI6 <= 30:
		score.RSI = 3
	case last.RSI.RSI6 <= 45:
		score.RSI = 1
	case last.RSI.RSI6 <= 55:
		score.RSI = 0
	case last.RSI.RSI6 <= 70:
		score.RSI = -1
	case last.RSI.RSI6 <= 80:
		score.RSI = -3
	default:
		score.RSI = -5
	}

	switch {
	case last.BIAS.BIAS24 < -15:
		score.BIAS = 3
	case last.BIAS.BIAS24 <= -10:
		score.BIAS = 2
	case last.BIAS.BIAS24 <= -5:
		score.BIAS = 1
	case last.BIAS.BIAS24 <= 5:
		score.BIAS = 0
	case last.BIAS.BIAS24 <= 10:
		score.BIAS = -1
	case last.BIAS.BIAS24 <= 15:
		score.BIAS = -2
	default:
		score.BIAS = -3
	}

	// CHOPCMI: pure trend efficiency score, decoupled from DMI direction.
	switch {
	case last.CHOP < 30 && last.CMI > 70:
		score.CHOPCMI = 3
	case last.CHOP < 38.2 && last.CMI > 60:
		score.CHOPCMI = 2
	case last.CHOP > 70 && last.CMI < 30:
		score.CHOPCMI = -3
	case last.CHOP > 61.8 && last.CMI < 40:
		score.CHOPCMI = -2
	}

	priceUp, priceDown := false, false
	if n > 1 {
		priceUp = candles[n-1].Close > candles[n-2].Close
		priceDown = candles[n-1].Close < candles[n-2].Close
	}
	switch {
	case volRatio > volStrong && priceUp:
		score.Volume += 3
	case volRatio > volStrong && priceDown:
		score.Volume -= 3
	case volRatio >= volSurge && priceUp:
		score.Volume += 2
	case volRatio >= volSurge && priceDown:
		score.Volume -= 2
	case volRatio < volQuiet && priceUp:
		score.Volume -= 2
	case volRatio < volQuiet && priceDown:
		score.Volume++
	}
	if len(obv) >= 6 {
		if obv[n-1] > obv[n-6] {
			score.Volume++
		} else if obv[n-1] < obv[n-6] {
			score.Volume--
		}
	}
	if avgUpVol > avgDownVol {
		score.Volume++
	} else if avgUpVol < avgDownVol {
		score.Volume--
	}
	score.Volume = clampInt(score.Volume, -5, 5)

	// SAR/SuperTrend dual confirmation: trend-following stance.
	switch {
	case last.SAR.Long && last.SuperTrend.Long:
		score.SAR = 3
	case !last.SAR.Long && !last.SuperTrend.Long:
		score.SAR = -3
	}

	// Divergence signal: bear divergence penalizes, bull divergence rewards.
	if n >= 20 {
		div := divergence(candles, results, n-1)
		if div.BearToday {
			score.Divergence = -3
		} else if div.Bear {
			score.Divergence = -1
		}
		if div.BullToday {
			score.Divergence = 2
		} else if div.Bull {
			score.Divergence = 1
		}
	}

	score.Delta = score.DMI + score.MA + score.MACD + score.KdjWr + score.RSI + score.BIAS + score.CHOPCMI + score.Volume + score.SAR + score.Divergence
	score.Total = clampInt(50+score.Delta, 0, 100)
	score.Label = scoreLabel(score.Total)
	return score
}

// applyPerfAdaptive recomputes the total score with per-stock PERF-history
// weighting (CLAUDE.md 方法论: 不用同一把尺子量所有股). Overbought-family
// penalties (negative KdjWr/RSI/BIAS) are only adjusted when the composite
// Overbought signal fired — that is exactly the signal the "超买反转" PERF
// stat backtests, and gating there avoids mis-weighting e.g. a mid-range KDJ
// dead cross. Penalties are halved when the signal is historically
// ineffective (win10 < 35%) and amplified ×1.5 when effective (win10 > 55%);
// the bear-divergence penalty is adjusted the same way against the "顶背离"
// stat (<40% / >55%). Bull rewards are never touched. Requires n ≥ 10
// samples. Integer division truncates toward zero (-7/2 = -3, -1/2 = 0).
//
// The adjusted total goes to the score_adj sidecar column; score_total keeps
// the original scale so history stays comparable.
func applyPerfAdaptive(score scoreState, perfs []perfStat) (adjTotal, perfAdj int) {
	obWin, obN := perfWin10(perfs, "超买反转")
	divWin, divN := perfWin10(perfs, "顶背离")

	adj := 0
	if score.Signals.Overbought {
		for _, v := range []int{score.KdjWr, score.RSI, score.BIAS} {
			adj += perfScale(v, obWin, obN, 35, 55) - v
		}
	}
	if score.Divergence < 0 {
		adj += perfScale(score.Divergence, divWin, divN, 40, 55) - score.Divergence
	}
	return clampInt(50+score.Delta+adj, 0, 100), adj
}

// perfWin10 returns the forward-10-day win rate (%) and trigger count for the
// named PERF stat, or (0, 0) when it is absent or never triggered.
func perfWin10(perfs []perfStat, name string) (float64, int) {
	for _, p := range perfs {
		if p.Name == name && p.Triggers > 0 {
			return float64(p.Win10) / float64(p.Triggers) * 100, p.Triggers
		}
	}
	return 0, 0
}

// perfScale reweighs a negative penalty by this stock's own historical win
// rate (CLAUDE.md PERF 自适应): halved when the signal was historically
// ineffective (win < weakBelow), amplified ×1.5 when effective (win >
// strongAbove). Non-negative values and thin samples (n < 10) pass through
// unchanged. Integer division truncates toward zero (-7/2 = -3, -1/2 = 0).
func perfScale(v int, win float64, n int, weakBelow, strongAbove float64) int {
	if v >= 0 || n < 10 {
		return v
	}
	if win < weakBelow {
		return v / 2
	}
	if win > strongAbove {
		return v * 3 / 2
	}
	return v
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// lateStagePenalty scores end-of-move overheating from candle-only inputs, so it
// is identical on -save and plain runs (turnover, which the CLI lacks here, is
// left to screen-stocks). 口径对齐 screen-stocks `_late_stage_risk`: a positive
// BIAS24 stretched > 4 daily ATRs (volatility-normalized 乖离) and/or an up-run
// of ≥5 closes mark a crowded, reversal-prone tail. Returns a small non-positive
// penalty (≥ -5) plus the raw streak/biasAtr for display. Only the UP side is
// penalized — oversold (negative bias / down-run) returns 0.
func lateStagePenalty(candles []indicator.Candle, results []indicator.Result) (penalty, streak int, biasAtr float64) {
	last := results[len(results)-1]
	streak = streakValue(candles)
	if last.ATR.Pct > 0 {
		biasAtr = last.BIAS.BIAS24 / last.ATR.Pct
	}
	if biasAtr > 4 {
		penalty -= 2
		if biasAtr > 6 {
			penalty--
		}
	}
	if streak >= 5 {
		penalty -= 2
		if streak >= 7 {
			penalty--
		}
	}
	if penalty < -5 {
		penalty = -5
	}
	return penalty, streak, biasAtr
}

func evalSignals(candles []indicator.Candle, results []indicator.Result, obv []float64, i int) signalState {
	if i < 60 {
		return signalState{}
	}
	r, prev := results[i], results[i-1]
	ma5, ma20, ma60 := closeMA(candles, i, 5), closeMA(candles, i, 20), closeMA(candles, i, 60)
	vr := ratio(candles[i].Volume, volumeMA(candles, i, 20))
	fiveAgo := maxInt(0, i-5)
	priceUp5 := candles[i].Close > candles[fiveAgo].Close
	priceDown5 := candles[i].Close < candles[fiveAgo].Close
	obvUp := obv[i] > obv[fiveAgo]
	obvDown := obv[i] < obv[fiveAgo]
	crossUp20 := candles[i-1].Close <= closeMA(candles, i-1, 20) && candles[i].Close > ma20
	crossDown20 := candles[i-1].Close >= closeMA(candles, i-1, 20) && candles[i].Close < ma20
	crossUp60 := candles[i-1].Close <= closeMA(candles, i-1, 60) && candles[i].Close > ma60
	crossDown60 := candles[i-1].Close >= closeMA(candles, i-1, 60) && candles[i].Close < ma60

	s := signalState{
		TrendBullScore:  countTrue(r.CHOP < 38.2, r.DMI.ADX > 25, r.MACD.DIF > 0 && r.DMI.PDI > r.DMI.MDI, candles[i].Close > ma5 && candles[i].Close > ma20 && ma5 > ma20),
		TrendBearScore:  countTrue(r.CHOP < 38.2, r.DMI.ADX > 25, r.MACD.DIF < 0 && r.DMI.MDI > r.DMI.PDI, candles[i].Close < ma5 && candles[i].Close < ma20 && ma5 < ma20),
		OversoldScore:   countTrue(r.RSI.RSI6 < 30, r.WR.WR14 > 80, r.KDJ.K < 20 && (r.KDJ.K > r.KDJ.D || r.KDJ.J > prev.KDJ.J), r.BIAS.BIAS24 < -10),
		OverboughtScore: countTrue(r.RSI.RSI6 > 70, r.WR.WR14 < 20, r.KDJ.K > 80 && (r.KDJ.K < r.KDJ.D || r.KDJ.J < prev.KDJ.J), r.BIAS.BIAS24 > 10),
		BreakBullScore:  countTrue(crossUp20 || crossUp60, vr > volSurge, obvUp),
		BreakBearScore:  countTrue(crossDown20 || crossDown60, vr > volSurge, obvDown),
		RevertBullScore: countTrue(r.BIAS.BIAS24 < -10, r.CHOP > 45, priceDown5 && obvUp),
		RevertBearScore: countTrue(r.BIAS.BIAS24 > 10, r.CHOP > 45, priceUp5 && obvDown),
	}
	s.TrendBull = s.TrendBullScore >= 3
	s.TrendBear = s.TrendBearScore >= 3
	s.Oversold = s.OversoldScore >= 3
	s.Overbought = s.OverboughtScore >= 3
	s.BreakBull = s.BreakBullScore >= 2
	s.BreakBear = s.BreakBearScore >= 2
	s.RevertBull = s.RevertBullScore >= 2
	s.RevertBear = s.RevertBearScore >= 2
	s.StochStagBull, s.StochStagBear = stochStagnation(r.RSI.RSI6, r.StochRSI.K, r.StochRSI.D, prev.StochRSI.K, prev.StochRSI.D)
	return s
}

// stochStagnation flags a StochRSI %K/%D crossover that occurs while RSI6 is
// pinned in an extreme zone ("钝化") — the timing signal RSI alone can't give
// because it has lost resolution at the top/bottom. It is a sidecar timing cue,
// deliberately kept OUT of the score (same momentum dimension as RSI/KDJ — see
// CLAUDE.md「指标分工」: avoid double-counting one axis as independent votes).
func stochStagnation(rsi6, kNow, dNow, kPrev, dPrev float64) (bull, bear bool) {
	crossDown := kPrev >= dPrev && kNow < dNow
	crossUp := kPrev <= dPrev && kNow > dNow
	bear = rsi6 > 75 && crossDown && kPrev > 80 // high-zone 钝化 + %K rolling over from overbought
	bull = rsi6 < 25 && crossUp && kPrev < 20   // low-zone 钝化 + %K turning up from oversold
	return
}

// stagnationZone labels RSI6's 钝化 state for display.
func stagnationZone(rsi6 float64) string {
	switch {
	case rsi6 > 75:
		return "高位"
	case rsi6 < 25:
		return "低位"
	default:
		return "正常"
	}
}

// stochTimingText renders the stagnation-timing cue for the current bar.
func stochTimingText(bull, bear bool) string {
	switch {
	case bear:
		return "今日空头转向"
	case bull:
		return "今日多头转向"
	default:
		return "-"
	}
}

type divergenceState struct {
	Ready      bool
	BullScore  int
	BearScore  int
	Bull       bool
	Bear       bool
	BullToday  bool
	BearToday  bool
	LowIdx     int
	RefLowIdx  int
	HighIdx    int
	RefHighIdx int
}

// divergence detects momentum divergence by comparing today's RSI6 against the
// RSI6 peak/trough within the recent lookback window rather than comparing two
// price extremes. This avoids cross-trend comparisons: a lower RSI at a higher
// price only means divergence when momentum genuinely peaked inside the same move.
//
// Bear: RSI already peaked inside the window while price is still up → weakening.
// Bull: RSI already troughed inside the window while price is still down → strengthening.
func divergence(candles []indicator.Candle, results []indicator.Result, i int) divergenceState {
	const lookback = 20
	const minGap = 3 // ignore the last few bars to reduce single-day noise
	if i < lookback {
		return divergenceState{}
	}

	refStart := i - lookback
	refEnd := i - minGap

	// Find the RSI6 peak and trough in [refStart, refEnd].
	rsiPeakIdx, rsiTroughIdx := refStart, refStart
	for j := refStart + 1; j <= refEnd; j++ {
		if results[j].RSI.RSI6 > results[rsiPeakIdx].RSI.RSI6 {
			rsiPeakIdx = j
		}
		if results[j].RSI.RSI6 < results[rsiTroughIdx].RSI.RSI6 {
			rsiTroughIdx = j
		}
	}

	d := divergenceState{
		Ready:   true,
		HighIdx: i, RefHighIdx: rsiPeakIdx,
		LowIdx: i, RefLowIdx: rsiTroughIdx,
	}

	rsiNow := results[i].RSI.RSI6
	difNow := results[i].MACD.DIF

	// Bear divergence: RSI already peaked, but price is still at or above the peak
	// day's close. Confirm we are in high territory (RSI > 60) and MACD positive.
	if rsiNow > 60 &&
		rsiNow < results[rsiPeakIdx].RSI.RSI6 &&
		candles[i].Close >= candles[rsiPeakIdx].Close &&
		difNow > 0 {
		d.BearScore = 1
		d.Bear = true
		d.BearToday = true
	}

	// Bull divergence: RSI already troughed, but price is still at or below the
	// trough day's close. Confirm low territory (RSI < 40) and MACD negative.
	if rsiNow < 40 &&
		rsiNow > results[rsiTroughIdx].RSI.RSI6 &&
		candles[i].Close <= candles[rsiTroughIdx].Close &&
		difNow < 0 {
		d.BullScore = 1
		d.Bull = true
		d.BullToday = true
	}

	return d
}

type perfStat struct {
	Name       string
	Direction  string
	Triggers   int
	Win5       int
	Win10      int
	Sum5       float64
	Sum10      float64
	Best10     float64
	Worst10    float64
	MaxAdverse float64
	LastDate   string
}

// performance backtests each signal's forward 5/10-day returns. Signals are
// counted on the RISING EDGE only (off→on transition): consecutive trigger
// days re-sample the same overlapping forward window and would inflate N
// without adding independent information (overlapping-returns problem), which
// previously made win-rate samples look 3-5x larger than they really were.
// The TD countdown==13 trigger is inherently a one-shot event and needs no
// edge detection.
func performance(candles []indicator.Candle, dates []string, results []indicator.Result, tds []indicator.TD, obv []float64) []perfStat {
	perfs := []perfStat{
		newPerf("趋势跟随多头", "多头"), newPerf("趋势跟随空头", "空头"),
		newPerf("超卖反转", "多头"), newPerf("超买反转", "空头"),
		newPerf("量价突破多头", "多头"), newPerf("量价突破空头", "空头"),
		newPerf("均值回归多头", "多头"), newPerf("均值回归空头", "空头"),
		newPerf("底背离", "多头"), newPerf("顶背离", "空头"),
		newPerf("TD见底Countdown", "多头"), newPerf("TD见顶Countdown", "空头"),
		newPerf("StochRSI钝化多头", "多头"), newPerf("StochRSI钝化空头", "空头"),
	}
	if len(candles) <= 90 {
		return perfs
	}
	prev := evalSignals(candles, results, obv, 79)
	prevDiv := divergence(candles, results, 79)
	for i := 80; i+10 < len(candles); i++ {
		s := evalSignals(candles, results, obv, i)
		d := divergence(candles, results, i)
		if s.TrendBull && !prev.TrendBull {
			recordPerf(&perfs[0], candles, dates, i)
		}
		if s.TrendBear && !prev.TrendBear {
			recordPerf(&perfs[1], candles, dates, i)
		}
		if s.Oversold && !prev.Oversold {
			recordPerf(&perfs[2], candles, dates, i)
		}
		if s.Overbought && !prev.Overbought {
			recordPerf(&perfs[3], candles, dates, i)
		}
		if s.BreakBull && !prev.BreakBull {
			recordPerf(&perfs[4], candles, dates, i)
		}
		if s.BreakBear && !prev.BreakBear {
			recordPerf(&perfs[5], candles, dates, i)
		}
		if s.RevertBull && !prev.RevertBull {
			recordPerf(&perfs[6], candles, dates, i)
		}
		if s.RevertBear && !prev.RevertBear {
			recordPerf(&perfs[7], candles, dates, i)
		}
		if d.BullToday && !prevDiv.BullToday {
			recordPerf(&perfs[8], candles, dates, i)
		}
		if d.BearToday && !prevDiv.BearToday {
			recordPerf(&perfs[9], candles, dates, i)
		}
		if tds[i].CountdownCount == 13 {
			if tds[i].CountdownSignal == indicator.TDBuy {
				recordPerf(&perfs[10], candles, dates, i)
			} else if tds[i].CountdownSignal == indicator.TDSell {
				recordPerf(&perfs[11], candles, dates, i)
			}
		}
		if s.StochStagBull && !prev.StochStagBull {
			recordPerf(&perfs[12], candles, dates, i)
		}
		if s.StochStagBear && !prev.StochStagBear {
			recordPerf(&perfs[13], candles, dates, i)
		}
		prev, prevDiv = s, d
	}
	return perfs
}

func newPerf(name, direction string) perfStat {
	return perfStat{Name: name, Direction: direction, Best10: math.Inf(-1), Worst10: math.Inf(1)}
}

func recordPerf(p *perfStat, candles []indicator.Candle, dates []string, i int) {
	entry := candles[i].Close
	ret5 := (candles[i+5].Close/entry - 1) * 100
	ret10 := (candles[i+10].Close/entry - 1) * 100
	adverse := 0.0
	if p.Direction == "空头" {
		ret5, ret10 = -ret5, -ret10
		for j := i + 1; j <= i+10; j++ {
			move := -(candles[j].High/entry - 1) * 100
			if move < adverse {
				adverse = move
			}
		}
	} else {
		for j := i + 1; j <= i+10; j++ {
			move := (candles[j].Low/entry - 1) * 100
			if move < adverse {
				adverse = move
			}
		}
	}
	p.Triggers++
	if ret5 > 0 {
		p.Win5++
	}
	if ret10 > 0 {
		p.Win10++
	}
	p.Sum5 += ret5
	p.Sum10 += ret10
	if ret10 > p.Best10 {
		p.Best10 = ret10
	}
	if ret10 < p.Worst10 {
		p.Worst10 = ret10
	}
	if adverse < p.MaxAdverse {
		p.MaxAdverse = adverse
	}
	p.LastDate = dates[i]
}

func printDivergence(d divergenceState, dates []string, candles []indicator.Candle, results []indicator.Result) {
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
		tdSignalText(td.SetupSignal), td.SetupCount, tdPerf, tdSignalText(td.CountdownSignal), td.CountdownCount)
}

func printFib(candles []indicator.Candle, dates []string, lookback int) {
	fib := indicator.FibRetracementOf(candles, lookback)
	dir := "上升(回撤=支撑)"
	if !fib.Uptrend {
		dir = "下降(反弹=阻力)"
	}
	fmt.Printf("FIB lookback=%d dir=%s high=%.3f(%s) low=%.3f(%s)",
		lookback, dir, fib.High, dates[fib.HighIndex], fib.Low, dates[fib.LowIndex])
	for _, level := range fib.Levels {
		fmt.Printf(" %.1f%%=%.3f", level.Ratio*100, level.Price)
	}
	fmt.Println()
}

func printRecentExtremes(candles []indicator.Candle, dates []string, results []indicator.Result) {
	hiIdx, loIdx := windowExtremes(candles, len(candles)-1, 20)
	fmt.Printf("近20日 高点=%s %.3f(DIF=%.4f RSI6=%.1f) 低点=%s %.3f(DIF=%.4f RSI6=%.1f)\n",
		dates[hiIdx], candles[hiIdx].High, results[hiIdx].MACD.DIF, results[hiIdx].RSI.RSI6,
		dates[loIdx], candles[loIdx].Low, results[loIdx].MACD.DIF, results[loIdx].RSI.RSI6)
}

// streakValue returns the current consecutive-close run length, signed:
// positive for an up run, negative for a down run, 0 when flat or none.
func streakValue(candles []indicator.Candle) int {
	streak, direction := 0, 0
	for i := len(candles) - 1; i > 0; i-- {
		current := 0
		if candles[i].Close > candles[i-1].Close {
			current = 1
		} else if candles[i].Close < candles[i-1].Close {
			current = -1
		}
		if current == 0 {
			break
		}
		if streak == 0 {
			direction = current
		}
		if current != direction {
			break
		}
		streak++
	}
	return streak * direction
}

func printStreak(candles []indicator.Candle) {
	sv := streakValue(candles)
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
	case abs(adj) < abs(orig):
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
func strongestSwingVote(last indicator.Result) (string, int) {
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
	for _, c := range cands {
		if abs(c.w) > abs(best) {
			best, bestLabel = c.w, c.label
		}
	}
	return bestLabel, best
}

// evalBullBear synthesizes a weighted, de-duplicated bull/bear verdict from the
// CLI indicators. Each axis (CLAUDE.md「指标分工」) votes at most once so
// same-source indicators can't manufacture false consensus: trend folds
// DMI + SAR/ST + MA into a single vote, the overbought/oversold axis takes only
// its most extreme member (RSI/WR/KDJ-J/BIAS), and the composite score is NOT
// fed back as a vote. Overbought and top-divergence bear votes are reweighted by
// this stock's own PERF history (perfScale), matching score_adj's methodology.
func evalBullBear(candles []indicator.Candle, results []indicator.Result, tds []indicator.TD, obv []float64, div divergenceState, perfs []perfStat, volRatio float64) bullBearVerdict {
	n := len(candles)
	last := results[n-1]
	lastTD := tds[n-1]
	var v bullBearVerdict

	obWin, obN := perfWin10(perfs, "超买反转")
	divWin, divN := perfWin10(perfs, "顶背离")

	// 趋势维度:DMI 方向强度 + SAR/ST 双确认 + MA 排列,合并一票(三者一致才满权)。
	ma5, ma10, ma20, ma60 := closeMA(candles, n-1, 5), closeMA(candles, n-1, 10), closeMA(candles, n-1, 20), closeMA(candles, n-1, 60)
	adx := last.DMI.ADX
	maBull := ma5 > ma10 && ma10 > ma20 && ma20 > ma60
	maBear := ma5 < ma10 && ma10 < ma20 && ma20 < ma60
	sarStBull := last.SAR.Long && last.SuperTrend.Long
	sarStBear := !last.SAR.Long && !last.SuperTrend.Long
	bullConfirm := countTrue(adx > 25 && last.DMI.PDI > last.DMI.MDI, sarStBull, maBull)
	bearConfirm := countTrue(adx > 25 && last.DMI.MDI > last.DMI.PDI, sarStBear, maBear)
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
	if label, w := strongestSwingVote(last); w < 0 {
		adj := perfScale(w, obWin, obN, 35, 55)
		v.addBear(label+perfNote(w, adj), -adj)
	} else if w > 0 {
		v.addBull(label, w)
	}

	// 资金维度:OBV 净流向 + 量比异动,合并一票。
	moneyW := 0
	var moneyParts []string
	if n >= 6 {
		switch {
		case obv[n-1] > obv[n-6]:
			moneyW++
			moneyParts = append(moneyParts, "OBV净流入")
		case obv[n-1] < obv[n-6]:
			moneyW--
			moneyParts = append(moneyParts, "OBV净流出")
		}
	}
	priceUp := n > 1 && candles[n-1].Close > candles[n-2].Close
	priceDown := n > 1 && candles[n-1].Close < candles[n-2].Close
	switch {
	case volRatio > volSurge && priceUp:
		moneyW += 2
		moneyParts = append(moneyParts, fmt.Sprintf("放量上涨(量比%.2f)", volRatio))
	case volRatio > volSurge && priceDown:
		moneyW -= 2
		moneyParts = append(moneyParts, fmt.Sprintf("放量下跌(量比%.2f)", volRatio))
	}
	switch {
	case moneyW > 0:
		v.addBull("资金:"+joinComma(moneyParts), moneyW)
	case moneyW < 0:
		v.addBear("资金:"+joinComma(moneyParts), -moneyW)
	}

	// 择时维度:TD countdown 反转倒计时。
	if lastTD.CountdownCount > 0 {
		w := 1
		if lastTD.CountdownCount >= 13 {
			w = 3
		} else if lastTD.CountdownCount >= 8 {
			w = 2
		}
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
		adj := perfScale(w, divWin, divN, 40, 55)
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
func printBullBear(v bullBearVerdict, score scoreState) {
	fmt.Printf("BULLBEAR bull=[%s] bear=[%s] bullW=%d bearW=%d verdict=%s score=%d\n",
		bbItemsText(v.Bulls), bbItemsText(v.Bears), v.BullScore, v.BearScore, v.Verdict, score.Total)
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
func printReading(candles []indicator.Candle, results []indicator.Result, tds []indicator.TD, obv []float64, score scoreState, div divergenceState, perfs []perfStat, volRatio float64, v bullBearVerdict) {
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
	case volRatio > volSurge:
		money = fmt.Sprintf("量比%.2f(>%.1f)放量,需看价配合", volRatio, volSurge)
	case volRatio < volQuiet:
		money = fmt.Sprintf("量比%.2f(<%.1f)清淡", volRatio, volQuiet)
	}
	mfiTag := ""
	switch {
	case last.MFI > 80:
		mfiTag = ",MFI>80资金偏热"
	case last.MFI < 20:
		mfiTag = ",MFI<20资金偏冷"
	}
	fmt.Printf("READ 资金: 量比=%.2f OBV=%s MFI=%.1f → %s%s\n",
		volRatio, obvTrend(obv), last.MFI, money, mfiTag)

	var timing []string
	if lastTD.SetupCount > 0 {
		timing = append(timing, fmt.Sprintf("TD-setup %s/%d", tdSignalText(lastTD.SetupSignal), lastTD.SetupCount))
	}
	if lastTD.CountdownCount > 0 {
		timing = append(timing, fmt.Sprintf("TD-countdown %s/%d", tdSignalText(lastTD.CountdownSignal), lastTD.CountdownCount))
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

	latePen, streak, biasAtr := lateStagePenalty(candles, results)
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
		if w, nn := perfWin10(perfs, "超买反转"); nn >= 10 && w < 35 {
			caveats = append(caveats, fmt.Sprintf("超买本股win10=%.0f%%(<35%%),已降权", w))
		}
	}
	if div.Bear {
		if w, nn := perfWin10(perfs, "顶背离"); nn >= 10 && w < 40 {
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

func printPerf(perfs []perfStat) {
	fmt.Println("历史信号性能(仅用信号当日及以前判断, 统计未来5/10日; N<5统计意义弱):")
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
	start := maxInt(0, len(candles)-15)
	for i := start; i < len(candles); i++ {
		row := results[i]
		volumeTag := "平"
		if vm := volumeMA(candles, i, 20); vm > 0 {
			ratio := candles[i].Volume / vm
			if ratio > volSurge {
				volumeTag = "放量"
			} else if ratio < volQuiet {
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
			row.DMI.MDI, row.DMI.ADX, row.CHOP, tdShort(tds[i]), sarTag)
	}
}

func rawString(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

func rawFloat(raw json.RawMessage) float64 {
	s := rawString(raw)
	value, err := strconv.ParseFloat(s, 64)
	if err != nil && s != "" && s != "0" && s != "0.00" {
		fmt.Fprintf(os.Stderr, "warn: rawFloat: %q -> 0\n", s)
	}
	return value
}

func closeSeries(candles []indicator.Candle) []float64 {
	values := make([]float64, len(candles))
	for i, candle := range candles {
		values[i] = candle.Close
	}
	return values
}

func volumeSeries(candles []indicator.Candle) []float64 {
	values := make([]float64, len(candles))
	for i, candle := range candles {
		values[i] = candle.Volume
	}
	return values
}

func obvSeries(candles []indicator.Candle) []float64 {
	obv := make([]float64, len(candles))
	if len(candles) == 0 {
		return obv
	}
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

func rangeLowHigh(candles []indicator.Candle, start, end int) (float64, float64) {
	if start < 0 {
		start = 0
	}
	if end > len(candles) {
		end = len(candles)
	}
	if start >= end {
		// Empty range — return last candle's values to avoid Inf→NaN.
		if len(candles) > 0 {
			last := candles[len(candles)-1]
			return last.Low, last.High
		}
		return 0, 0
	}
	low, high := math.Inf(1), math.Inf(-1)
	for i := start; i < end; i++ {
		if candles[i].Low < low {
			low = candles[i].Low
		}
		if candles[i].High > high {
			high = candles[i].High
		}
	}
	return low, high
}

func windowExtremes(candles []indicator.Candle, end, period int) (int, int) {
	start := maxInt(0, end-period+1)
	hiIdx, loIdx := start, start
	for i := start + 1; i <= end; i++ {
		if candles[i].High > candles[hiIdx].High {
			hiIdx = i
		}
		if candles[i].Low < candles[loIdx].Low {
			loIdx = i
		}
	}
	return hiIdx, loIdx
}

func meanTail(values []float64, count int) float64 {
	if len(values) == 0 {
		return 0
	}
	start := maxInt(0, len(values)-count)
	total := 0.0
	for _, value := range values[start:] {
		total += value
	}
	return total / float64(len(values)-start)
}

func medianTail(values []float64, count int) float64 {
	start := maxInt(0, len(values)-count)
	cp := append([]float64(nil), values[start:]...)
	sort.Float64s(cp)
	if len(cp) == 0 {
		return 0
	}
	mid := len(cp) / 2
	if len(cp)%2 == 1 {
		return cp[mid]
	}
	return (cp[mid-1] + cp[mid]) / 2
}

func closeMA(candles []indicator.Candle, end, period int) float64 {
	start := maxInt(0, end-period+1)
	total := 0.0
	for i := start; i <= end; i++ {
		total += candles[i].Close
	}
	return total / float64(end-start+1)
}

func volumeMA(candles []indicator.Candle, end, period int) float64 {
	start := maxInt(0, end-period+1)
	total := 0.0
	for i := start; i <= end; i++ {
		total += candles[i].Volume
	}
	return total / float64(end-start+1)
}

func recentVolumeHealth(candles []indicator.Candle, days int) (int, float64, int, float64) {
	upTotal, downTotal := 0.0, 0.0
	upCnt, downCnt := 0, 0
	start := maxInt(1, len(candles)-days)
	for i := start; i < len(candles); i++ {
		if candles[i].Close > candles[i-1].Close {
			upTotal += candles[i].Volume
			upCnt++
		} else if candles[i].Close < candles[i-1].Close {
			downTotal += candles[i].Volume
			downCnt++
		}
	}
	return upCnt, ratio(upTotal, float64(upCnt)), downCnt, ratio(downTotal, float64(downCnt))
}

func donchianBreak(candles []indicator.Candle, results []indicator.Result, period int, bullish bool) bool {
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

func obvTrend(obv []float64) string {
	if len(obv) < 6 {
		return "样本不足"
	}
	if obv[len(obv)-1] > obv[len(obv)-6] {
		return "上升(净流入)"
	}
	if obv[len(obv)-1] < obv[len(obv)-6] {
		return "下降(净流出)"
	}
	return "持平"
}

func tdSignalText(signal indicator.TDSignal) string {
	switch signal {
	case indicator.TDBuy:
		return "见底"
	case indicator.TDSell:
		return "见顶"
	default:
		return "-"
	}
}

func tdShort(td indicator.TD) string {
	dir := func(signal indicator.TDSignal) string {
		if signal == indicator.TDSell {
			return "顶"
		}
		return "底"
	}
	switch {
	case td.CountdownCount > 0:
		return fmt.Sprintf("C%s%d", dir(td.CountdownSignal), td.CountdownCount)
	case td.SetupCount > 0:
		text := fmt.Sprintf("S%s%d", dir(td.SetupSignal), td.SetupCount)
		if td.SetupCount == 9 && td.SetupPerfected {
			text += "*"
		}
		return text
	default:
		return "-"
	}
}

func longShort(long bool) string {
	if long {
		return "多"
	}
	return "空"
}

func scoreLabel(score int) string {
	switch {
	case score >= 85:
		return "技术极强"
	case score >= 70:
		return "技术偏强"
	case score >= 55:
		return "技术略偏强"
	case score >= 45:
		return "技术中性/方向不明"
	case score >= 31:
		return "技术略偏弱"
	case score >= 16:
		return "技术偏弱"
	default:
		return "技术极弱"
	}
}

func countTrue(values ...bool) int {
	count := 0
	for _, value := range values {
		if value {
			count++
		}
	}
	return count
}

func position(value, low, high float64) float64 {
	if high <= low {
		return 50
	}
	return (value - low) / (high - low) * 100
}

func ratio(numerator, denominator float64) float64 {
	if denominator == 0 {
		return 0
	}
	return numerator / denominator
}

func clampInt(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
