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
	if len(perfs) < 12 {
		t.Fatalf("performance() returned %d rows, want at least 12", len(perfs))
	}

	if perfs[10].Name != "TD见底Countdown" {
		t.Fatalf("TD bottom signal name = %q, want TD见底Countdown", perfs[10].Name)
	}
	if perfs[11].Name != "TD见顶Countdown" {
		t.Fatalf("TD top signal name = %q, want TD见顶Countdown", perfs[11].Name)
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
