// Package snapshot builds a store.Snapshot from a day's K-line series.
//
// cmd/indicator-analyze (single-stock CLI, prints a full report) and
// cmd/stockdb batch-save (nightly full-pool job, persists only) both need
// the exact same technical-face computation — they used to duplicate ~170
// lines of it field-by-field, which meant a new snapshot column had to be
// added in two places or the two paths would silently disagree. Build is
// the single place that computation now lives; the CLI additionally keeps
// the intermediate values (MA/range/volume series etc.) it prints but
// batch-save never persisted.
package snapshot

import (
	"fmt"

	"stock-tui/internal/analysis"
	"stock-tui/internal/api"
	"stock-tui/internal/indicator"
	"stock-tui/internal/store"
)

// Built holds the snapshot plus the intermediate values computed along the
// way. batch-save only needs Snap; the CLI report also prints the rest.
type Built struct {
	N       int
	Candles []indicator.Candle
	Dates   []string
	Results []indicator.Result
	TDs     []indicator.TD
	Last    indicator.Result
	LastTD  indicator.TD

	Closes                     []float64
	MA5, MA10, MA20, MA60      float64
	Volumes                    []float64
	LowAll, HighAll            float64
	Low20, High20              float64
	Low60, High60              float64
	Low120, High120            float64
	VolMA20                    float64
	VolRatio                   float64
	OBV                        []float64
	UpCnt, DownCnt             int
	UpAvgVol, DownAvgVol       float64
	Score                      analysis.ScoreState
	Div                        analysis.DivergenceState
	Perfs                      []analysis.PerfStat
	ScoreAdj, PerfAdj, LatePen int
	Change, ChangePct          float64

	Snap store.Snapshot
}

// Build computes the full technical-face snapshot for data. n==0 (empty
// K-line, e.g. bad/delisted code) yields a zero Built with Snap holding only
// Code — callers decide whether/how to warn, Build stays silent so it's
// equally usable from a batch job as from an interactive CLI.
func Build(data api.KlineData) Built {
	candles := data.Candles
	dates := data.Dates
	n := len(candles)
	if n == 0 {
		return Built{Snap: store.Snapshot{Code: data.Code}}
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
	// PERF stats must exist before scoring: ApplyPerfAdaptive reweighs the
	// overbought/divergence penalties by this stock's own signal history.
	perfs := analysis.Performance(candles, dates, results, tds, obv)
	scoreAdj, perfAdj := analysis.ApplyPerfAdaptive(score, perfs, div.BearToday)
	// Late-stage crowding penalty folds into score_adj (the adaptive sidecar),
	// not score_total — keeping the fixed scale historically comparable while
	// still reflecting end-of-move overheating.
	latePen, _, _ := analysis.LateStagePenalty(candles, results)
	scoreAdj = analysis.ClampInt(scoreAdj+latePen, 0, 100)

	change, changePct := 0.0, 0.0
	if n > 1 {
		change = lastCandle.Close - candles[n-2].Close
		changePct = analysis.Ratio(change, candles[n-2].Close) * 100
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
		TradeDate: dates[n-1],
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

	return Built{
		N: n, Candles: candles, Dates: dates,
		Results: results, TDs: tds, Last: last, LastTD: lastTD,
		Closes: closes,
		MA5:    ma5, MA10: ma10, MA20: ma20, MA60: ma60,
		Volumes: volumes,
		LowAll:  lowAll, HighAll: highAll,
		Low20: low20, High20: high20,
		Low60: low60, High60: high60,
		Low120: low120, High120: high120,
		VolMA20:  volMA20,
		VolRatio: volRatio,
		OBV:      obv,
		UpCnt:    upCnt, DownCnt: downCnt,
		UpAvgVol: upAvgVol, DownAvgVol: downAvgVol,
		Score:    score,
		Div:      div,
		Perfs:    perfs,
		ScoreAdj: scoreAdj, PerfAdj: perfAdj, LatePen: latePen,
		Change: change, ChangePct: changePct,
		Snap: snap,
	}
}
