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

	"stock-tui/internal/indicator"
	"stock-tui/internal/market"
)

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
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: go run ./cmd/indicator-analyze [-n bars] <code>")
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
	printAnalysis(data)
	return nil
}

func fetchDailyKline(code string, bars int) (seriesData, error) {
	url := fmt.Sprintf("https://ifzq.gtimg.cn/appstock/app/fqkline/get?param=%s,day,,,%d,qfq", code, bars)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return seriesData{}, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return seriesData{}, fmt.Errorf("fetch %s: %w", code, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return seriesData{}, fmt.Errorf("fetch %s: HTTP %s", code, resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
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

func printAnalysis(data seriesData) {
	candles := data.Candles
	dates := data.Dates
	results := indicator.Calculate(candles)
	tds := indicator.TDSequential(candles)
	n := len(candles)
	last := results[n-1]
	lastCandle := candles[n-1]
	closes := closeSeries(candles)
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
		meanTail(closes, 5), meanTail(closes, 10), meanTail(closes, 20), meanTail(closes, 60),
		lowAll, highAll, position(lastCandle.Close, lowAll, highAll),
		low20, high20, position(lastCandle.Close, low20, high20),
		low60, high60, position(lastCandle.Close, low60, high60),
		low120, high120, position(lastCandle.Close, low120, high120))
	fmt.Printf("KDJ K=%.2f D=%.2f J=%.2f | MACD DIF=%.4f DEA=%.4f H=%.4f\n",
		last.KDJ.K, last.KDJ.D, last.KDJ.J, last.MACD.DIF, last.MACD.DEA, last.MACD.Histogram)
	fmt.Printf("RSI %.2f/%.2f/%.2f | WR %.2f/%.2f | BIAS %.2f/%.2f/%.2f\n",
		last.RSI.RSI6, last.RSI.RSI12, last.RSI.RSI24, last.WR.WR10, last.WR.WR14,
		last.BIAS.BIAS6, last.BIAS.BIAS12, last.BIAS.BIAS24)
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
	fmt.Printf("SCORE total=%d delta=%+d dmi=%+d ma=%+d macd=%+d kdj=%+d rsi=%+d wr=%+d bias=%+d chopcmi=%+d volume=%+d label=%s\n",
		score.Total, score.Delta, score.DMI, score.MA, score.MACD, score.KDJ, score.RSI, score.WR,
		score.BIAS, score.CHOPCMI, score.Volume, score.Label)
	fmt.Printf("当前策略触发: trendBull=%t(%d/4) trendBear=%t(%d/4) oversold=%t(%d/4) overbought=%t(%d/4) breakBull=%t(%d/3) breakBear=%t(%d/3) revertBull=%t(%d/3) revertBear=%t(%d/3) divBull=%t(%d/2,today=%t) divBear=%t(%d/2,today=%t)\n",
		score.Signals.TrendBull, score.Signals.TrendBullScore, score.Signals.TrendBear, score.Signals.TrendBearScore,
		score.Signals.Oversold, score.Signals.OversoldScore, score.Signals.Overbought, score.Signals.OverboughtScore,
		score.Signals.BreakBull, score.Signals.BreakBullScore, score.Signals.BreakBear, score.Signals.BreakBearScore,
		score.Signals.RevertBull, score.Signals.RevertBullScore, score.Signals.RevertBear, score.Signals.RevertBearScore,
		div.Bull, div.BullScore, div.BullToday, div.Bear, div.BearScore, div.BearToday)
	printDivergence(div, dates, candles, results)
	printTD(tds[n-1])
	printFib(candles, dates, 60)
	printFib(candles, dates, 120)
	printRecentExtremes(candles, dates, results)
	printStreak(candles)
	printPerf(performance(candles, dates, results, tds, obv))
	printRecentRows(candles, dates, results, tds)
}

type scoreState struct {
	Total   int
	Delta   int
	DMI     int
	MA      int
	MACD    int
	KDJ     int
	RSI     int
	WR      int
	BIAS    int
	CHOPCMI int
	Volume  int
	Label   string
	Signals signalState
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
}

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

	kdjGold := last.KDJ.K >= last.KDJ.D
	switch {
	case last.KDJ.K < 20 && kdjGold:
		score.KDJ = 7
	case last.KDJ.K < 20:
		score.KDJ = 1
	case last.KDJ.K <= 80 && kdjGold:
		score.KDJ = 3
	case last.KDJ.K <= 80:
		score.KDJ = -3
	case kdjGold:
		score.KDJ = -2
	default:
		score.KDJ = -7
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
	case last.WR.WR14 > 90:
		score.WR = 4
	case last.WR.WR14 >= 80:
		score.WR = 2
	case last.WR.WR14 >= 60:
		score.WR = 1
	case last.WR.WR14 >= 40:
		score.WR = 0
	case last.WR.WR14 >= 20:
		score.WR = -1
	case last.WR.WR14 >= 10:
		score.WR = -2
	default:
		score.WR = -4
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

	if last.CHOP < 38.2 {
		if last.DMI.PDI > last.DMI.MDI {
			score.CHOPCMI += 2
		} else if last.DMI.MDI > last.DMI.PDI {
			score.CHOPCMI -= 2
		}
	}
	if last.CMI > 60 {
		if last.DMI.PDI > last.DMI.MDI {
			score.CHOPCMI++
		} else if last.DMI.MDI > last.DMI.PDI {
			score.CHOPCMI--
		}
	}

	priceUp, priceDown := false, false
	if n > 1 {
		priceUp = candles[n-1].Close > candles[n-2].Close
		priceDown = candles[n-1].Close < candles[n-2].Close
	}
	switch {
	case volRatio > 2.0 && priceUp:
		score.Volume += 3
	case volRatio > 2.0 && priceDown:
		score.Volume -= 3
	case volRatio >= 1.3 && priceUp:
		score.Volume += 2
	case volRatio >= 1.3 && priceDown:
		score.Volume -= 2
	case volRatio < 0.7 && priceUp:
		score.Volume -= 2
	case volRatio < 0.7 && priceDown:
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
	score.Delta = score.DMI + score.MA + score.MACD + score.KDJ + score.RSI + score.WR + score.BIAS + score.CHOPCMI + score.Volume
	score.Total = clampInt(50+score.Delta, 0, 100)
	score.Label = scoreLabel(score.Total)
	return score
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
		BreakBullScore:  countTrue(crossUp20 || crossUp60, vr > 1.5, obvUp),
		BreakBearScore:  countTrue(crossDown20 || crossDown60, vr > 1.5, obvDown),
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
	return s
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

func divergence(candles []indicator.Candle, results []indicator.Result, i int) divergenceState {
	if i < 34 {
		return divergenceState{}
	}
	hiIdx, loIdx := windowExtremes(candles, i, 20)
	refHiIdx, refLoIdx := windowExtremes(candles, i-15, 20)
	d := divergenceState{
		Ready: true, HighIdx: hiIdx, LowIdx: loIdx,
		RefHighIdx: refHiIdx, RefLowIdx: refLoIdx,
	}
	lowNew := candles[loIdx].Low < candles[refLoIdx].Low
	highNew := candles[hiIdx].High > candles[refHiIdx].High
	if lowNew {
		d.BullScore = countTrue(results[loIdx].MACD.DIF > results[refLoIdx].MACD.DIF, results[loIdx].RSI.RSI6 > results[refLoIdx].RSI.RSI6)
		d.Bull = d.BullScore > 0
		d.BullToday = d.Bull && loIdx == i
	}
	if highNew {
		d.BearScore = countTrue(results[hiIdx].MACD.DIF < results[refHiIdx].MACD.DIF, results[hiIdx].RSI.RSI6 < results[refHiIdx].RSI.RSI6)
		d.Bear = d.BearScore > 0
		d.BearToday = d.Bear && hiIdx == i
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

func performance(candles []indicator.Candle, dates []string, results []indicator.Result, tds []indicator.TD, obv []float64) []perfStat {
	perfs := []perfStat{
		newPerf("趋势跟随多头", "多头"), newPerf("趋势跟随空头", "空头"),
		newPerf("超卖反转", "多头"), newPerf("超买反转", "空头"),
		newPerf("量价突破多头", "多头"), newPerf("量价突破空头", "空头"),
		newPerf("均值回归多头", "多头"), newPerf("均值回归空头", "空头"),
		newPerf("底背离", "多头"), newPerf("顶背离", "空头"),
		newPerf("TD见底Countdown", "多头"), newPerf("TD见顶Countdown", "空头"),
	}
	for i := 80; i+10 < len(candles); i++ {
		s := evalSignals(candles, results, obv, i)
		d := divergence(candles, results, i)
		if s.TrendBull {
			recordPerf(&perfs[0], candles, dates, i)
		}
		if s.TrendBear {
			recordPerf(&perfs[1], candles, dates, i)
		}
		if s.Oversold {
			recordPerf(&perfs[2], candles, dates, i)
		}
		if s.Overbought {
			recordPerf(&perfs[3], candles, dates, i)
		}
		if s.BreakBull {
			recordPerf(&perfs[4], candles, dates, i)
		}
		if s.BreakBear {
			recordPerf(&perfs[5], candles, dates, i)
		}
		if s.RevertBull {
			recordPerf(&perfs[6], candles, dates, i)
		}
		if s.RevertBear {
			recordPerf(&perfs[7], candles, dates, i)
		}
		if d.BullToday {
			recordPerf(&perfs[8], candles, dates, i)
		}
		if d.BearToday {
			recordPerf(&perfs[9], candles, dates, i)
		}
		if tds[i].CountdownCount == 13 {
			if tds[i].CountdownSignal == indicator.TDBuy {
				recordPerf(&perfs[10], candles, dates, i)
			} else if tds[i].CountdownSignal == indicator.TDSell {
				recordPerf(&perfs[11], candles, dates, i)
			}
		}
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
		fmt.Println("DIVERGENCE N/A (样本不足: 需要至少35根日K)")
		return
	}
	fmt.Printf("DIVERGENCE bull=%t(%d/2,today=%t) bear=%t(%d/2,today=%t) | low cur=%s %.3f(DIF=%.4f RSI6=%.1f) ref=%s %.3f(DIF=%.4f RSI6=%.1f) | high cur=%s %.3f(DIF=%.4f RSI6=%.1f) ref=%s %.3f(DIF=%.4f RSI6=%.1f)\n",
		d.Bull, d.BullScore, d.BullToday, d.Bear, d.BearScore, d.BearToday,
		dates[d.LowIdx], candles[d.LowIdx].Low, results[d.LowIdx].MACD.DIF, results[d.LowIdx].RSI.RSI6,
		dates[d.RefLowIdx], candles[d.RefLowIdx].Low, results[d.RefLowIdx].MACD.DIF, results[d.RefLowIdx].RSI.RSI6,
		dates[d.HighIdx], candles[d.HighIdx].High, results[d.HighIdx].MACD.DIF, results[d.HighIdx].RSI.RSI6,
		dates[d.RefHighIdx], candles[d.RefHighIdx].High, results[d.RefHighIdx].MACD.DIF, results[d.RefHighIdx].RSI.RSI6)
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

func printStreak(candles []indicator.Candle) {
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
	if direction > 0 {
		fmt.Printf("连续上涨 %d 日\n", streak)
	} else if direction < 0 {
		fmt.Printf("连续下跌 %d 日\n", streak)
	}
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
			if ratio > 1.5 {
				volumeTag = "放量"
			} else if ratio < 0.7 {
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
	value, _ := strconv.ParseFloat(rawString(raw), 64)
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
