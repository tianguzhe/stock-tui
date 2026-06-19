package main

import (
	"strings"
	"testing"

	"stock-tui/internal/indicator"
)

func TestRunRequiresCode(t *testing.T) {
	err := run(nil)
	if err == nil {
		t.Fatal("run(nil) error = nil, want usage error")
	}
}

func TestRunRejectsInvalidBarsBeforeNetwork(t *testing.T) {
	err := run([]string{"-n", "0", "600900"})
	if err == nil {
		t.Fatal("run(-n 0) error = nil, want validation error")
	}
}

func TestRunRejectsMalformedCodeBeforeNetwork(t *testing.T) {
	err := run([]string{"abc"})
	if err == nil {
		t.Fatal("run(malformed) error = nil, want validation error")
	}
	if !strings.Contains(err.Error(), "invalid code") {
		t.Fatalf("run(malformed) error = %v, want invalid code", err)
	}
}

func TestScoreLabelUsesTechnicalState(t *testing.T) {
	cases := []struct {
		score int
		want  string
	}{
		{100, "技术极强"},
		{85, "技术极强"},
		{84, "技术偏强"},
		{70, "技术偏强"},
		{69, "技术略偏强"},
		{55, "技术略偏强"},
		{54, "技术中性/方向不明"},
		{45, "技术中性/方向不明"},
		{44, "技术略偏弱"},
		{31, "技术略偏弱"},
		{30, "技术偏弱"},
		{16, "技术偏弱"},
		{15, "技术极弱"},
		{0, "技术极弱"},
	}

	for _, tc := range cases {
		if got := scoreLabel(tc.score); got != tc.want {
			t.Fatalf("scoreLabel(%d) = %q, want %q", tc.score, got, tc.want)
		}
	}
}

// perfStatOf builds a perfStat whose win10 rate is winPct over n triggers.
func perfStatOf(name string, n int, winPct float64) perfStat {
	return perfStat{Name: name, Triggers: n, Win10: int(winPct / 100 * float64(n))}
}

func TestApplyPerfAdaptive(t *testing.T) {
	// Base: overbought-family penalties KdjWr=-7 RSI=-5 BIAS=-3, Delta/Total consistent.
	base := func(overbought bool, divergence int) scoreState {
		s := scoreState{
			KdjWr: -7, RSI: -5, BIAS: -3, Divergence: divergence,
			Signals: signalState{Overbought: overbought},
		}
		s.Delta = s.KdjWr + s.RSI + s.BIAS + s.Divergence
		s.Total = clampInt(50+s.Delta, 0, 100)
		return s
	}

	cases := []struct {
		name      string
		score     scoreState
		perfs     []perfStat
		wantAdj   int
		wantTotal int
	}{
		{
			name:  "未触发复合超买不调整",
			score: base(false, 0),
			perfs: []perfStat{perfStatOf("超买反转", 50, 20)},
			// total = 50-15 = 35
			wantAdj: 0, wantTotal: 35,
		},
		{
			name:  "超买历史无效(win<35)惩罚减半向零截断",
			score: base(true, 0),
			perfs: []perfStat{perfStatOf("超买反转", 20, 30)},
			// -7→-3(+4) -5→-2(+3) -3→-1(+2) → adj=+9, total=35+9=44
			wantAdj: 9, wantTotal: 44,
		},
		{
			name:  "超买历史有效(win>55)惩罚x1.5",
			score: base(true, 0),
			perfs: []perfStat{perfStatOf("超买反转", 20, 60)},
			// -7→-10(-3) -5→-7(-2) -3→-4(-1) → adj=-6, total=35-6=29
			wantAdj: -6, wantTotal: 29,
		},
		{
			name:    "样本不足(n<10)不调整",
			score:   base(true, 0),
			perfs:   []perfStat{perfStatOf("超买反转", 9, 0)},
			wantAdj: 0, wantTotal: 35,
		},
		{
			name:    "中间胜率(35-55)不调整",
			score:   base(true, 0),
			perfs:   []perfStat{perfStatOf("超买反转", 20, 45)},
			wantAdj: 0, wantTotal: 35,
		},
		{
			name:  "顶背离历史无效(win<40)惩罚减半",
			score: base(false, -3),
			perfs: []perfStat{perfStatOf("顶背离", 100, 25)},
			// -3→-1(+2), total = 50-18+2 = 34
			wantAdj: 2, wantTotal: 34,
		},
		{
			name:  "顶背离历史有效(win>55)惩罚x1.5",
			score: base(false, -3),
			perfs: []perfStat{perfStatOf("顶背离", 100, 60)},
			// -3→-4(-1), total = 50-18-1 = 31
			wantAdj: -1, wantTotal: 31,
		},
		{
			name: "底背离奖励不动",
			score: func() scoreState {
				s := scoreState{Divergence: 2}
				s.Delta = 2
				s.Total = 52
				return s
			}(),
			perfs:   []perfStat{perfStatOf("顶背离", 100, 25)},
			wantAdj: 0, wantTotal: 52,
		},
		{
			name: "小惩罚减半归零(-1/2=0)",
			score: func() scoreState {
				s := scoreState{RSI: -1, Signals: signalState{Overbought: true}}
				s.Delta = -1
				s.Total = 49
				return s
			}(),
			perfs:   []perfStat{perfStatOf("超买反转", 20, 30)},
			wantAdj: 1, wantTotal: 50,
		},
		{
			name:  "无PERF样本不调整",
			score: base(true, -3),
			perfs: nil,
			// total = 50-18 = 32
			wantAdj: 0, wantTotal: 32,
		},
	}

	for _, tc := range cases {
		gotTotal, gotAdj := applyPerfAdaptive(tc.score, tc.perfs)
		if gotAdj != tc.wantAdj || gotTotal != tc.wantTotal {
			t.Errorf("%s: applyPerfAdaptive() = (total=%d, adj=%d), want (total=%d, adj=%d)",
				tc.name, gotTotal, gotAdj, tc.wantTotal, tc.wantAdj)
		}
	}
}

// TestPerformanceCountsRisingEdgesOnly verifies that consecutive trigger days
// of the same signal are counted once (off→on edge), not once per day —
// overlapping forward windows would otherwise inflate N.
func TestPerformanceCountsRisingEdgesOnly(t *testing.T) {
	// Synthetic regime-alternating series: 25-bar rallies separated by 15-bar
	// choppy pullbacks, so TrendBull fires in runs and extinguishes between
	// them (edges < trigger days, both > 0).
	n := 280
	candles := make([]indicator.Candle, n)
	dates := make([]string, n)
	price := 10.0
	for i := range candles {
		if i%40 < 25 {
			price *= 1.012 // rally leg
		} else if i%2 == 0 {
			price *= 1.004 // choppy leg
		} else {
			price *= 0.992
		}
		candles[i] = indicator.Candle{
			Close: price, High: price * 1.01, Low: price * 0.99, Volume: 1000,
		}
		dates[i] = "2026-01-01"
	}
	results := indicator.Calculate(candles)
	tds := indicator.TDSequential(candles)
	obv := obvSeries(candles)

	// Count trigger days and rising edges for TrendBull over the same window.
	triggerDays, edges := 0, 0
	prev := evalSignals(candles, results, obv, 79)
	for i := 80; i+10 < n; i++ {
		s := evalSignals(candles, results, obv, i)
		if s.TrendBull {
			triggerDays++
			if !prev.TrendBull {
				edges++
			}
		}
		prev = s
	}
	if triggerDays < 2 || edges == 0 {
		t.Fatalf("synthetic series too weak: triggerDays=%d edges=%d (need consecutive triggers)", triggerDays, edges)
	}
	if triggerDays == edges {
		t.Fatalf("synthetic series has no consecutive trigger runs (triggerDays=%d == edges=%d), test is vacuous", triggerDays, edges)
	}

	perfs := performance(candles, dates, results, tds, obv)
	if perfs[0].Triggers != edges {
		t.Fatalf("趋势跟随多头 N = %d, want rising-edge count %d (per-day count would be %d)",
			perfs[0].Triggers, edges, triggerDays)
	}
}

func TestPerformanceUsesSignalNames(t *testing.T) {
	perfs := performance(nil, nil, nil, nil, nil)
	if len(perfs) < 14 {
		t.Fatalf("performance() returned %d rows, want at least 14", len(perfs))
	}

	if perfs[10].Name != "TD见底Countdown" {
		t.Fatalf("TD bottom signal name = %q, want TD见底Countdown", perfs[10].Name)
	}
	if perfs[11].Name != "TD见顶Countdown" {
		t.Fatalf("TD top signal name = %q, want TD见顶Countdown", perfs[11].Name)
	}
	if perfs[12].Name != "StochRSI钝化多头" {
		t.Fatalf("StochRSI bull signal name = %q, want StochRSI钝化多头", perfs[12].Name)
	}
	if perfs[13].Name != "StochRSI钝化空头" {
		t.Fatalf("StochRSI bear signal name = %q, want StochRSI钝化空头", perfs[13].Name)
	}
}

// TestStochStagnation verifies the AND-gate: a %K/%D crossover only fires when
// RSI6 is pinned in the matching extreme zone AND %K came from the
// overbought/oversold band — each guard independently suppresses the signal.
func TestStochStagnation(t *testing.T) {
	cases := []struct {
		name                           string
		rsi6, kNow, dNow, kPrev, dPrev float64
		wantBull, wantBear             bool
	}{
		{"空头转向: 高位钝化+%K从超买下穿", 82, 70, 75, 85, 80, false, true},
		{"空头不触发: RSI未钝化(60≤75)", 60, 70, 75, 85, 80, false, false},
		{"空头不触发: %K前值未超买(78≤80)", 82, 70, 75, 78, 76, false, false},
		{"空头不触发: 无下穿(%K仍在%D上)", 82, 88, 82, 85, 80, false, false},
		{"多头转向: 低位钝化+%K从超卖上穿", 18, 25, 20, 15, 18, true, false},
		{"多头不触发: RSI未钝化(40≥25)", 40, 25, 20, 15, 18, false, false},
		{"多头不触发: %K前值未超卖(25≥20)", 18, 30, 25, 25, 28, false, false},
		{"多头不触发: 无上穿(%K仍在%D下)", 18, 12, 16, 15, 18, false, false},
		{"正常区双侧不触发", 50, 60, 40, 30, 45, false, false},
	}
	for _, tc := range cases {
		gotBull, gotBear := stochStagnation(tc.rsi6, tc.kNow, tc.dNow, tc.kPrev, tc.dPrev)
		if gotBull != tc.wantBull || gotBear != tc.wantBear {
			t.Errorf("%s: stochStagnation() = (bull=%t, bear=%t), want (bull=%t, bear=%t)",
				tc.name, gotBull, gotBear, tc.wantBull, tc.wantBear)
		}
	}
}

func TestTDSignalTextUsesTechnicalState(t *testing.T) {
	if got := tdSignalText(indicator.TDBuy); got != "见底" {
		t.Fatalf("tdSignalText(TDBuy) = %q, want 见底", got)
	}
	if got := tdSignalText(indicator.TDSell); got != "见顶" {
		t.Fatalf("tdSignalText(TDSell) = %q, want 见顶", got)
	}
}

func TestTDShortUsesTechnicalDirection(t *testing.T) {
	bottom := tdShort(indicator.TD{SetupSignal: indicator.TDBuy, SetupCount: 9, SetupPerfected: true})
	if bottom != "S底9*" {
		t.Fatalf("tdShort(bottom setup) = %q, want S底9*", bottom)
	}

	top := tdShort(indicator.TD{CountdownSignal: indicator.TDSell, CountdownCount: 13})
	if top != "C顶13" {
		t.Fatalf("tdShort(top countdown) = %q, want C顶13", top)
	}
}

// flat* build the minimal fixtures evalBullBear reads: it only touches the last
// Result/TD and the candle/obv slices (for MA, price direction, OBV). Flat
// candles make every MA equal (no MA-arrangement vote) and price direction
// neutral, isolating whichever signal the case sets on the last bar.
func flatCandles(n int, close float64) []indicator.Candle {
	cs := make([]indicator.Candle, n)
	for i := range cs {
		cs[i] = indicator.Candle{High: close, Low: close, Close: close, Volume: 1}
	}
	return cs
}

func flatResults(n int, last indicator.Result) []indicator.Result {
	rs := make([]indicator.Result, n)
	rs[n-1] = last
	return rs
}

func flatTDs(n int, last indicator.TD) []indicator.TD {
	ts := make([]indicator.TD, n)
	ts[n-1] = last
	return ts
}

func TestEvalBullBear(t *testing.T) {
	const n = 61 // ≥ ma60 window + the look-back for OBV/price direction
	mk := func(last indicator.Result, td indicator.TD, div divergenceState, perfs []perfStat, volRatio float64) bullBearVerdict {
		return evalBullBear(flatCandles(n, 10), flatResults(n, last), flatTDs(n, td), make([]float64, n), div, perfs, volRatio)
	}
	// noTrend cancels the trend vote: SAR long + ST short is neither dual-long
	// nor dual-short, and ADX=0/flat-MA add nothing.
	noTrend := func(r indicator.Result) indicator.Result {
		r.SAR.Long, r.SuperTrend.Long = true, false
		return r
	}

	t.Run("超买同源只计一票_无score回灌_PERF无效降权", func(t *testing.T) {
		last := noTrend(indicator.Result{})
		last.RSI.RSI6 = 85    // -3
		last.WR.WR14 = 5      // -3 (high=超卖反口径,低=超买)
		last.BIAS.BIAS24 = 20 // -3
		last.KDJ.J = 110      // -2
		// 旧实现会把 RSI/BIAS 各投一票再叠加"评分偏弱",至少 3 个 bear;
		// 新实现同轴只取最极端一项,且不回灌 score → 恰好 1 个 bear。
		v := mk(last, indicator.TD{}, divergenceState{}, []perfStat{perfStatOf("超买反转", 20, 20)}, 1.0)
		if len(v.Bears) != 1 || len(v.Bulls) != 0 {
			t.Fatalf("bulls=%d bears=%d, want 0/1; bears=%+v", len(v.Bulls), len(v.Bears), v.Bears)
		}
		if v.BearScore != 1 { // -3 经 win10=20%(<35) 减半向零截断为 -1
			t.Errorf("BearScore=%d, want 1", v.BearScore)
		}
		if !strings.Contains(v.Bears[0].Label, "降权") {
			t.Errorf("bear label %q missing 降权 note", v.Bears[0].Label)
		}
	})

	t.Run("超买样本不足_不降权", func(t *testing.T) {
		last := noTrend(indicator.Result{})
		last.RSI.RSI6 = 85
		v := mk(last, indicator.TD{}, divergenceState{}, []perfStat{perfStatOf("超买反转", 5, 0)}, 1.0)
		if v.BearScore != 3 {
			t.Errorf("BearScore=%d, want 3 (n<10 不调权)", v.BearScore)
		}
		if v.Verdict != "偏空" {
			t.Errorf("Verdict=%q, want 偏空", v.Verdict)
		}
		if len(v.Bears) == 1 && strings.Contains(v.Bears[0].Label, "降权") {
			t.Errorf("bear label %q should not be downweighted", v.Bears[0].Label)
		}
	})

	t.Run("加权研判方向取决于权重和", func(t *testing.T) {
		var last indicator.Result
		last.RSI.RSI6, last.WR.WR14, last.KDJ.J, last.BIAS.BIAS24 = 50, 50, 50, 0 // 中性摆动,无超买超卖票
		last.SAR.Long, last.SuperTrend.Long = true, true                          // SAR/ST 双多 → 趋势确认 +1
		last.MACD.DIF, last.MACD.DEA, last.MACD.Histogram = 1, 0, 1               // MACD 金叉 +2
		td := indicator.TD{CountdownCount: 13, CountdownSignal: indicator.TDBuy}  // TD 见底 +3
		v := mk(last, td, divergenceState{}, nil, 1.0)
		if v.BearScore != 0 || v.BullScore != 6 {
			t.Fatalf("bullW=%d bearW=%d, want 6/0 (趋势1+MACD2+TD3); bulls=%+v", v.BullScore, v.BearScore, v.Bulls)
		}
		if v.Verdict != "偏多" {
			t.Errorf("Verdict=%q, want 偏多", v.Verdict)
		}
	})
}
