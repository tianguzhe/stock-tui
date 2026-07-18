package analysis

import (
	"math"
	"stock-tui/internal/indicator"
)

// ScoreState 技术面综合评分状态。
type ScoreState struct {
	Total      int
	Delta      int
	DMI        int
	MA         int
	MACD       int
	KdjWr      int
	RSI        int
	BIAS       int
	CHOPCMI    int
	Volume     int
	SAR        int
	Divergence int
	Label      string
	Signals    SignalState
}

// SignalState 信号触发状态。
type SignalState struct {
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
	StochStagBull   bool
	StochStagBear   bool
}

// DivergenceState 背离检测状态。
type DivergenceState struct {
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

// PerfStat 单个信号的 PERF 回测统计。
type PerfStat struct {
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

// VolQuiet/VolSurge/VolStrong 是量比阈值，与 CLAUDE.md 量能口径一致。
const (
	VolQuiet  = 0.8
	VolSurge  = 1.5
	VolStrong = 2.0
)

// ScoreResult 计算技术面综合评分。
func ScoreResult(candles []indicator.Candle, results []indicator.Result, obv []float64, avgUpVol, avgDownVol, volRatio float64) ScoreState {
	n := len(candles)
	last := results[n-1]
	prev := last
	if n > 1 {
		prev = results[n-2]
	}

	score := ScoreState{Signals: EvalSignals(candles, results, obv, n-1)}
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

	ma5, ma10, ma20, ma60 := CloseMA(candles, n-1, 5), CloseMA(candles, n-1, 10), CloseMA(candles, n-1, 20), CloseMA(candles, n-1, 60)
	switch CountTrue(candles[n-1].Close > ma5, candles[n-1].Close > ma10, candles[n-1].Close > ma20, candles[n-1].Close > ma60) {
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
	if AbsInt(kdjSignal) >= AbsInt(wrSignal) {
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

	switch {
	case last.CHOP < 30 && last.CMI > 70:
		score.CHOPCMI = 3
		if dmiDiff < 0 {
			score.CHOPCMI = -3
		}
	case last.CHOP < 38.2 && last.CMI > 60:
		score.CHOPCMI = 2
		if dmiDiff < 0 {
			score.CHOPCMI = -2
		}
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
	case volRatio > VolStrong && priceUp:
		score.Volume += 3
	case volRatio > VolStrong && priceDown:
		score.Volume -= 3
	case volRatio >= VolSurge && priceUp:
		score.Volume += 2
	case volRatio >= VolSurge && priceDown:
		score.Volume -= 2
	case volRatio < VolQuiet && priceUp:
		score.Volume -= 2
	case volRatio < VolQuiet && priceDown:
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
	score.Volume = ClampInt(score.Volume, -5, 5)

	switch {
	case last.SAR.Long && last.SuperTrend.Long:
		score.SAR = 3
	case !last.SAR.Long && !last.SuperTrend.Long:
		score.SAR = -3
	}

	if n >= 20 {
		div := Divergence(candles, results, n-1)
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
	score.Total = ClampInt(50+score.Delta, 0, 100)
	score.Label = ScoreLabel(score.Total)
	return score
}

// EvalSignals 检测索引 i 处的各项信号触发状态。
func EvalSignals(candles []indicator.Candle, results []indicator.Result, obv []float64, i int) SignalState {
	if i < 60 {
		return SignalState{}
	}
	r, prev := results[i], results[i-1]
	ma5, ma20, ma60 := CloseMA(candles, i, 5), CloseMA(candles, i, 20), CloseMA(candles, i, 60)
	vr := Ratio(candles[i].Volume, VolumeMA(candles, i, 20))
	fiveAgo := MaxInt(0, i-5)
	priceUp5 := candles[i].Close > candles[fiveAgo].Close
	priceDown5 := candles[i].Close < candles[fiveAgo].Close
	obvUp := obv[i] > obv[fiveAgo]
	obvDown := obv[i] < obv[fiveAgo]
	crossUp20 := candles[i-1].Close <= CloseMA(candles, i-1, 20) && candles[i].Close > ma20
	crossDown20 := candles[i-1].Close >= CloseMA(candles, i-1, 20) && candles[i].Close < ma20
	crossUp60 := candles[i-1].Close <= CloseMA(candles, i-1, 60) && candles[i].Close > ma60
	crossDown60 := candles[i-1].Close >= CloseMA(candles, i-1, 60) && candles[i].Close < ma60

	s := SignalState{
		TrendBullScore:  CountTrue(r.DMI.ADX > 25, r.MACD.DIF > 0 && r.DMI.PDI > r.DMI.MDI, candles[i].Close > ma5 && candles[i].Close > ma20 && ma5 > ma20),
		TrendBearScore:  CountTrue(r.DMI.ADX > 25, r.MACD.DIF < 0 && r.DMI.MDI > r.DMI.PDI, candles[i].Close < ma5 && candles[i].Close < ma20 && ma5 < ma20),
		OversoldScore:   CountTrue(r.RSI.RSI6 < 30, r.WR.WR14 > 80 || (r.KDJ.K < 20 && (r.KDJ.K > r.KDJ.D || r.KDJ.J > prev.KDJ.J)), r.BIAS.BIAS24 < -10),
		OverboughtScore: CountTrue(r.RSI.RSI6 > 70, r.WR.WR14 < 20 || (r.KDJ.K > 80 && (r.KDJ.K < r.KDJ.D || r.KDJ.J < prev.KDJ.J)), r.BIAS.BIAS24 > 10),
		BreakBullScore:  CountTrue(crossUp20 || crossUp60, vr > VolSurge, obvUp),
		BreakBearScore:  CountTrue(crossDown20 || crossDown60, vr > VolSurge, obvDown),
		RevertBullScore: CountTrue(r.BIAS.BIAS24 < -10, r.CHOP > 45, priceDown5 && obvUp),
		RevertBearScore: CountTrue(r.BIAS.BIAS24 > 10, r.CHOP > 45, priceUp5 && obvDown),
	}
	s.TrendBull = s.TrendBullScore >= 3
	s.TrendBear = s.TrendBearScore >= 3
	s.Oversold = s.OversoldScore >= 3
	s.Overbought = s.OverboughtScore >= 3
	s.BreakBull = s.BreakBullScore >= 2
	s.BreakBear = s.BreakBearScore >= 2
	s.RevertBull = s.RevertBullScore >= 2
	s.RevertBear = s.RevertBearScore >= 2
	s.StochStagBull, s.StochStagBear = StochStagnation(r.RSI.RSI6, r.StochRSI.K, r.StochRSI.D, prev.StochRSI.K, prev.StochRSI.D)
	return s
}

// Divergence 检测索引 i 处的 RSI 动量背离。
func Divergence(candles []indicator.Candle, results []indicator.Result, i int) DivergenceState {
	const lookback = 20
	const minGap = 3
	if i < lookback {
		return DivergenceState{}
	}

	refStart := i - lookback
	refEnd := i - minGap

	rsiPeakIdx, rsiTroughIdx := refStart, refStart
	for j := refStart + 1; j <= refEnd; j++ {
		if results[j].RSI.RSI6 > results[rsiPeakIdx].RSI.RSI6 {
			rsiPeakIdx = j
		}
		if results[j].RSI.RSI6 < results[rsiTroughIdx].RSI.RSI6 {
			rsiTroughIdx = j
		}
	}

	d := DivergenceState{
		Ready:   true,
		HighIdx: i, RefHighIdx: rsiPeakIdx,
		LowIdx: i, RefLowIdx: rsiTroughIdx,
	}

	rsiNow := results[i].RSI.RSI6
	difNow := results[i].MACD.DIF

	if rsiNow > 60 &&
		rsiNow < results[rsiPeakIdx].RSI.RSI6 &&
		candles[i].Close >= candles[rsiPeakIdx].Close &&
		difNow > 0 {
		d.BearScore = 1
		d.Bear = true
		d.BearToday = true
	}

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

// Performance 回测各信号的前瞻 5/10 日收益(仅计信号 rising edge)。
func Performance(candles []indicator.Candle, dates []string, results []indicator.Result, tds []indicator.TD, obv []float64) []PerfStat {
	perfs := []PerfStat{
		NewPerf("趋势跟随多头", "多头"), NewPerf("趋势跟随空头", "空头"),
		NewPerf("超卖反转", "多头"), NewPerf("超买反转", "空头"),
		NewPerf("量价突破多头", "多头"), NewPerf("量价突破空头", "空头"),
		NewPerf("均值回归多头", "多头"), NewPerf("均值回归空头", "空头"),
		NewPerf("底背离", "多头"), NewPerf("顶背离", "空头"),
		NewPerf("TD见底Countdown", "多头"), NewPerf("TD见顶Countdown", "空头"),
		NewPerf("StochRSI钝化多头", "多头"), NewPerf("StochRSI钝化空头", "空头"),
	}
	if len(candles) <= 90 {
		return perfs
	}
	prev := EvalSignals(candles, results, obv, 79)
	prevDiv := Divergence(candles, results, 79)
	for i := 80; i+10 < len(candles); i++ {
		s := EvalSignals(candles, results, obv, i)
		d := Divergence(candles, results, i)
		if s.TrendBull && !prev.TrendBull {
			RecordPerf(&perfs[0], candles, dates, i)
		}
		if s.TrendBear && !prev.TrendBear {
			RecordPerf(&perfs[1], candles, dates, i)
		}
		if s.Oversold && !prev.Oversold {
			RecordPerf(&perfs[2], candles, dates, i)
		}
		if s.Overbought && !prev.Overbought {
			RecordPerf(&perfs[3], candles, dates, i)
		}
		if s.BreakBull && !prev.BreakBull {
			RecordPerf(&perfs[4], candles, dates, i)
		}
		if s.BreakBear && !prev.BreakBear {
			RecordPerf(&perfs[5], candles, dates, i)
		}
		if s.RevertBull && !prev.RevertBull {
			RecordPerf(&perfs[6], candles, dates, i)
		}
		if s.RevertBear && !prev.RevertBear {
			RecordPerf(&perfs[7], candles, dates, i)
		}
		if d.BullToday && !prevDiv.BullToday {
			RecordPerf(&perfs[8], candles, dates, i)
		}
		if d.BearToday && !prevDiv.BearToday {
			RecordPerf(&perfs[9], candles, dates, i)
		}
		if tds[i].CountdownCount == 13 {
			if tds[i].CountdownSignal == indicator.TDBuy {
				RecordPerf(&perfs[10], candles, dates, i)
			} else if tds[i].CountdownSignal == indicator.TDSell {
				RecordPerf(&perfs[11], candles, dates, i)
			}
		}
		if s.StochStagBull && !prev.StochStagBull {
			RecordPerf(&perfs[12], candles, dates, i)
		}
		if s.StochStagBear && !prev.StochStagBear {
			RecordPerf(&perfs[13], candles, dates, i)
		}
		prev, prevDiv = s, d
	}
	return perfs
}

// NewPerf 创建初始 PERF 统计。
func NewPerf(name, direction string) PerfStat {
	return PerfStat{Name: name, Direction: direction, Best10: math.Inf(-1), Worst10: math.Inf(1)}
}

// RecordPerf 记录信号触发日的前瞻收益。
func RecordPerf(p *PerfStat, candles []indicator.Candle, dates []string, i int) {
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

// ApplyPerfAdaptive 按本股 PERF 历史重算调整分。
func ApplyPerfAdaptive(score ScoreState, perfs []PerfStat) (adjTotal, perfAdj int) {
	obWin, obN := PerfWin10(perfs, "超买反转")
	divWin, divN := PerfWin10(perfs, "顶背离")

	adj := 0
	if score.Signals.Overbought {
		for _, v := range []int{score.KdjWr, score.RSI, score.BIAS} {
			adj += PerfScale(v, obWin, obN, 35, 55) - v
		}
	}
	if score.Divergence < 0 {
		adj += PerfScale(score.Divergence, divWin, divN, 40, 55) - score.Divergence
	}
	return ClampInt(50+score.Delta+adj, 0, 100), adj
}

// PerfWin10 返回指定 PERF 统计的前瞻 10 日胜率(%)和触发次数。
func PerfWin10(perfs []PerfStat, name string) (float64, int) {
	for _, p := range perfs {
		if p.Name == name && p.Triggers > 0 {
			return float64(p.Win10) / float64(p.Triggers) * 100, p.Triggers
		}
	}
	return 0, 0
}

// PerfScale 按本股历史胜率重算惩罚值。
func PerfScale(v int, win float64, n int, weakBelow, strongAbove float64) int {
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

// LateStagePenalty 末端拥挤惩罚。
func LateStagePenalty(candles []indicator.Candle, results []indicator.Result) (penalty, streak int, biasAtr float64) {
	last := results[len(results)-1]
	streak = StreakValue(candles)
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
