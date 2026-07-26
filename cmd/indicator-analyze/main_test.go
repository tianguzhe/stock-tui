package main

import (
	"strings"
	"testing"

	"stock-tui/internal/analysis"
	"stock-tui/internal/api"
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

func TestPrintAnalysisEmptyCandlesNoPanic(t *testing.T) {
	snap := printAnalysis(api.KlineData{Code: "sh600000"})
	if snap.Code != "sh600000" {
		t.Fatalf("printAnalysis(empty).Code = %q, want sh600000", snap.Code)
	}
	if snap.TradeDate != "" {
		t.Fatalf("printAnalysis(empty).TradeDate = %q, want empty", snap.TradeDate)
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
		if got := analysis.ScoreLabel(tc.score); got != tc.want {
			t.Fatalf("analysis.ScoreLabel(%d) = %q, want %q", tc.score, got, tc.want)
		}
	}
}

// perfStatOf builds a analysis.PerfStat whose win10 rate is winPct over n triggers.
func perfStatOf(name string, n int, winPct float64) analysis.PerfStat {
	return analysis.PerfStat{Name: name, Triggers: n, Win10: int(winPct / 100 * float64(n))}
}

func TestApplyPerfAdaptive(t *testing.T) {
	// Base: overbought-family penalties KdjWr=-7 RSI=-5 BIAS=-3, Delta/Total consistent.
	base := func(overbought bool, divergence int) analysis.ScoreState {
		s := analysis.ScoreState{
			KdjWr: -7, RSI: -5, BIAS: -3, Divergence: divergence,
			Signals: analysis.SignalState{Overbought: overbought},
		}
		s.Delta = s.KdjWr + s.RSI + s.BIAS + s.Divergence
		s.Total = analysis.ClampInt(50+s.Delta, 0, 100)
		return s
	}

	cases := []struct {
		name      string
		score     analysis.ScoreState
		perfs     []analysis.PerfStat
		wantAdj   int
		wantTotal int
	}{
		{
			name:  "未触发复合超买不调整",
			score: base(false, 0),
			perfs: []analysis.PerfStat{perfStatOf("超买反转", 50, 20)},
			// total = 50-15 = 35
			wantAdj: 0, wantTotal: 35,
		},
		{
			// Wilson 上界 34.8% < 35% → 有把握说历史差,惩罚减半
			name:  "超买历史显著差(上界<35)惩罚减半向零截断",
			score: base(true, 0),
			perfs: []analysis.PerfStat{perfStatOf("超买反转", 40, 20)},
			// -7→-3(+4) -5→-2(+3) -3→-1(+2) → adj=+9, total=35+9=44
			wantAdj: 9, wantTotal: 44,
		},
		{
			// Wilson 下界 70.9% > 55% → 有把握说历史有效,惩罚加权
			name:  "超买历史显著有效(下界>55)惩罚x1.5",
			score: base(true, 0),
			perfs: []analysis.PerfStat{perfStatOf("超买反转", 40, 85)},
			// -7→-10(-3) -5→-7(-2) -3→-4(-1) → adj=-6, total=35-6=29
			wantAdj: -6, wantTotal: 29,
		},
		{
			// 关键回归: 旧实现(点估计 win=30 < 35 且 n>=10)会把惩罚砍半。
			// Wilson 区间 [14.6, 51.9] 跨越 50%,统计上说明不了任何问题。
			name:    "点估计偏低但区间过宽_不调整",
			score:   base(true, 0),
			perfs:   []analysis.PerfStat{perfStatOf("超买反转", 20, 30)},
			wantAdj: 0, wantTotal: 35,
		},
		{
			name:    "样本不足不调整",
			score:   base(true, 0),
			perfs:   []analysis.PerfStat{perfStatOf("超买反转", 5, 50)},
			wantAdj: 0, wantTotal: 35,
		},
		{
			name:    "中间胜率不调整",
			score:   base(true, 0),
			perfs:   []analysis.PerfStat{perfStatOf("超买反转", 20, 45)},
			wantAdj: 0, wantTotal: 35,
		},
		{
			// Wilson 上界 34.3% < 40%
			name:  "顶背离历史显著差(上界<40)惩罚减半",
			score: base(false, -3),
			perfs: []analysis.PerfStat{perfStatOf("顶背离", 100, 25)},
			// -3→-1(+2), total = 50-18+2 = 34
			wantAdj: 2, wantTotal: 34,
		},
		{
			// Wilson 下界 60.4% > 55%
			name:  "顶背离历史显著有效(下界>55)惩罚x1.5",
			score: base(false, -3),
			perfs: []analysis.PerfStat{perfStatOf("顶背离", 100, 70)},
			// -3→-4(-1), total = 50-18-1 = 31
			wantAdj: -1, wantTotal: 31,
		},
		{
			name: "底背离奖励不动",
			score: func() analysis.ScoreState {
				s := analysis.ScoreState{Divergence: 2}
				s.Delta = 2
				s.Total = 52
				return s
			}(),
			perfs:   []analysis.PerfStat{perfStatOf("顶背离", 100, 25)},
			wantAdj: 0, wantTotal: 52,
		},
		{
			name: "小惩罚减半归零(-1/2=0)",
			score: func() analysis.ScoreState {
				s := analysis.ScoreState{RSI: -1, Signals: analysis.SignalState{Overbought: true}}
				s.Delta = -1
				s.Total = 49
				return s
			}(),
			perfs:   []analysis.PerfStat{perfStatOf("超买反转", 40, 20)},
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
		gotTotal, gotAdj := analysis.ApplyPerfAdaptive(tc.score, tc.perfs)
		if gotAdj != tc.wantAdj || gotTotal != tc.wantTotal {
			t.Errorf("%s: analysis.ApplyPerfAdaptive() = (total=%d, adj=%d), want (total=%d, adj=%d)",
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
	obv := analysis.OBVSeries(candles)

	// Count trigger days and rising edges for TrendBull over the same window.
	triggerDays, edges := 0, 0
	prev := analysis.EvalSignals(candles, results, obv, 79)
	for i := 80; i+10 < n; i++ {
		s := analysis.EvalSignals(candles, results, obv, i)
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

	perfs := analysis.Performance(candles, dates, results, tds, obv)
	if perfs[0].Triggers != edges {
		t.Fatalf("趋势跟随多头 N = %d, want rising-edge count %d (per-day count would be %d)",
			perfs[0].Triggers, edges, triggerDays)
	}
}

func TestPerformanceUsesSignalNames(t *testing.T) {
	perfs := analysis.Performance(nil, nil, nil, nil, nil)
	if len(perfs) < 14 {
		t.Fatalf("analysis.Performance() returned %d rows, want at least 14", len(perfs))
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
		gotBull, gotBear := analysis.StochStagnation(tc.rsi6, tc.kNow, tc.dNow, tc.kPrev, tc.dPrev)
		if gotBull != tc.wantBull || gotBear != tc.wantBear {
			t.Errorf("%s: analysis.StochStagnation() = (bull=%t, bear=%t), want (bull=%t, bear=%t)",
				tc.name, gotBull, gotBear, tc.wantBull, tc.wantBear)
		}
	}
}

func TestTDSignalTextUsesTechnicalState(t *testing.T) {
	if got := analysis.TDSignalText(indicator.TDBuy); got != "见底" {
		t.Fatalf("analysis.TDSignalText(TDBuy) = %q, want 见底", got)
	}
	if got := analysis.TDSignalText(indicator.TDSell); got != "见顶" {
		t.Fatalf("analysis.TDSignalText(TDSell) = %q, want 见顶", got)
	}
}

func TestTDShortUsesTechnicalDirection(t *testing.T) {
	bottom := analysis.TDShort(indicator.TD{SetupSignal: indicator.TDBuy, SetupCount: 9, SetupPerfected: true})
	if bottom != "S底9*" {
		t.Fatalf("analysis.TDShort(bottom setup) = %q, want S底9*", bottom)
	}

	top := analysis.TDShort(indicator.TD{CountdownSignal: indicator.TDSell, CountdownCount: 13})
	if top != "C顶13" {
		t.Fatalf("analysis.TDShort(top countdown) = %q, want C顶13", top)
	}
}

// flat* build the minimal fixtures evalBullBear reads: it only touches the last
// Result/TD and the candle/obv slices (for MA, price direction, OBV). Flat
// candles make every MA equal (no MA-arrangement vote) and price direction
// neutral, isolating whichever signal the case sets on the last bar.
func flatCandles(n int, close float64) []indicator.Candle {
	cs := make([]indicator.Candle, n)
	for i := range cs {
		cs[i] = indicator.Candle{Open: close, High: close, Low: close, Close: close, Volume: 1}
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

// TestSwingMembersMatchesConflictDetection 锁住 swingMembers 与
// strongestSwingVote 的耦合: 前者渲染 SWING_CONFLICT 行的明细,后者判定是否冲突。
// 两处各写了一套阈值,一旦漂移就会出现"报告了冲突却列不出矛盾项"(或反之)的
// 自相矛盾输出。断言: 判定冲突 ⟺ 明细里同时存在看多项与看空项。
func TestSwingMembersMatchesConflictDetection(t *testing.T) {
	bullish := func(s string) bool {
		return strings.Contains(s, "偏低") || strings.Contains(s, "超卖") || strings.Contains(s, "负乖离")
	}
	bearish := func(s string) bool {
		return strings.Contains(s, "偏高") || strings.Contains(s, "超买") ||
			(strings.Contains(s, "乖离") && !strings.Contains(s, "负乖离"))
	}

	cases := []struct {
		name string
		last indicator.Result
	}{
		{"RSI超卖 vs WR超买", func() indicator.Result {
			var r indicator.Result
			r.RSI.RSI6, r.WR.WR14 = 15, 5
			return r
		}()},
		{"RSI超买 vs 负乖离", func() indicator.Result {
			var r indicator.Result
			r.RSI.RSI6, r.WR.WR14, r.BIAS.BIAS24 = 85, 50, -20
			return r
		}()},
		{"KDJ超卖 vs 正乖离", func() indicator.Result {
			var r indicator.Result
			r.RSI.RSI6, r.WR.WR14, r.BIAS.BIAS24, r.KDJ.J = 50, 50, 20, -10
			return r
		}()},
		{"三项同向看空_无冲突", func() indicator.Result {
			var r indicator.Result
			r.RSI.RSI6, r.WR.WR14, r.BIAS.BIAS24 = 85, 5, 20
			return r
		}()},
		{"全中性_无冲突", func() indicator.Result {
			var r indicator.Result
			r.RSI.RSI6, r.WR.WR14 = 50, 50
			return r
		}()},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, conflict := strongestSwingVote(tc.last)
			members := swingMembers(tc.last)
			var sawBull, sawBear bool
			for _, m := range members {
				if bullish(m) {
					sawBull = true
				}
				if bearish(m) {
					sawBear = true
				}
			}
			if got := sawBull && sawBear; got != conflict {
				t.Errorf("conflict=%v 但明细方向混合=%v; members=%v", conflict, got, members)
			}
			if conflict && len(members) < 2 {
				t.Errorf("判定冲突却只列出 %d 项: %v", len(members), members)
			}
		})
	}
}

func TestEvalBullBear(t *testing.T) {
	const n = 61 // ≥ ma60 window + the look-back for OBV/price direction
	mk := func(last indicator.Result, td indicator.TD, div analysis.DivergenceState, perfs []analysis.PerfStat, volRatio float64, sig analysis.SignalState) bullBearVerdict {
		return evalBullBear(flatCandles(n, 10), flatResults(n, last), flatTDs(n, td), make([]float64, n), div, perfs, volRatio, sig, 10)
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
		// RSI>70 + WR<20 + BIAS>10 三项齐备,复合超买信号成立 → PERF 调权生效。
		v := mk(last, indicator.TD{}, analysis.DivergenceState{}, []analysis.PerfStat{perfStatOf("超买反转", 40, 20)}, 1.0,
			analysis.SignalState{Overbought: true})
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
		v := mk(last, indicator.TD{}, analysis.DivergenceState{}, []analysis.PerfStat{perfStatOf("超买反转", 5, 0)}, 1.0,
			analysis.SignalState{Overbought: true})
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

	// PERF「超买反转」是 3/3 复合信号的历史,只能调同一复合信号的权重。
	// 单指标(这里只有 BIAS24 过线)投出的看空票不在该样本口径内,不得按它降权,
	// 否则与落库 score_adj(ApplyPerfAdaptive 有同名 gate)得出两套结论。
	t.Run("复合超买未触发_不按PERF调权", func(t *testing.T) {
		last := noTrend(indicator.Result{})
		last.RSI.RSI6 = 50    // 未超买
		last.WR.WR14 = 50     // 中性
		last.BIAS.BIAS24 = 20 // 仅乖离过大 → -3
		perfs := []analysis.PerfStat{perfStatOf("超买反转", 20, 20)}
		v := mk(last, indicator.TD{}, analysis.DivergenceState{}, perfs, 1.0, analysis.SignalState{Overbought: false})
		if v.BearScore != 3 {
			t.Errorf("BearScore=%d, want 3 (复合信号未触发,不降权)", v.BearScore)
		}
		if len(v.Bears) != 1 || strings.Contains(v.Bears[0].Label, "降权") {
			t.Errorf("bear label %+v should carry no PERF note", v.Bears)
		}
	})

	// 同轴矛盾只标记、不改计票: 分值必须与"无矛盾时取最极端项"完全一致,
	// 否则 score 口径变动会让历史不可比。
	t.Run("同轴方向矛盾_仍照常计票只标记", func(t *testing.T) {
		last := noTrend(indicator.Result{})
		last.RSI.RSI6 = 15    // 超卖 → +3
		last.WR.WR14 = 5      // 超买 → -3(与 RSI 矛盾)
		last.BIAS.BIAS24 = 20 // 乖离过大 → -3
		last.KDJ.J = 50       // 中性
		v := mk(last, indicator.TD{}, analysis.DivergenceState{}, nil, 1.0, analysis.SignalState{})
		if !v.SwingConflict {
			t.Error("SwingConflict = false, want true (RSI 超卖 vs WR/BIAS 超买)")
		}
		// 取绝对值最大者,平手保留先扫描到的 RSI(+3) → 记为多头票
		if v.BullScore != 3 || v.BearScore != 0 {
			t.Errorf("bullW=%d bearW=%d, want 3/0 (矛盾不改计票,仍取最极端项)", v.BullScore, v.BearScore)
		}
	})

	t.Run("无矛盾时不置 SwingConflict", func(t *testing.T) {
		last := noTrend(indicator.Result{})
		last.RSI.RSI6 = 85
		last.WR.WR14 = 5
		last.BIAS.BIAS24 = 20
		v := mk(last, indicator.TD{}, analysis.DivergenceState{}, nil, 1.0, analysis.SignalState{})
		if v.SwingConflict {
			t.Error("SwingConflict = true, want false (三项同向看空)")
		}
	})

	t.Run("加权研判方向取决于权重和", func(t *testing.T) {
		var last indicator.Result
		last.RSI.RSI6, last.WR.WR14, last.KDJ.J, last.BIAS.BIAS24 = 50, 50, 50, 0 // 中性摆动,无超买超卖票
		last.SAR.Long, last.SuperTrend.Long = true, true                          // SAR/ST 双多 → 趋势确认 +1
		last.MACD.DIF, last.MACD.DEA, last.MACD.Histogram = 1, 0, 1               // MACD 金叉 +2
		td := indicator.TD{CountdownCount: 13, CountdownSignal: indicator.TDBuy}  // TD 见底 +1（学术证据不支持高权重）
		v := mk(last, td, analysis.DivergenceState{}, nil, 1.0, analysis.SignalState{})
		if v.BearScore != 0 || v.BullScore != 4 {
			t.Fatalf("bullW=%d bearW=%d, want 4/0 (趋势1+MACD2+TD1); bulls=%+v", v.BullScore, v.BearScore, v.Bulls)
		}
		if v.Verdict != "偏多" {
			t.Errorf("Verdict=%q, want 偏多", v.Verdict)
		}
	})
}

// TestCHOPCMISignFollowsDMIDirection guards the #7 口径 correction: the CHOPCMI
// score component confirms trend *efficiency* (CHOP/CMI) but takes its *sign*
// from DMI direction (dmiDiff = PDI - MDI), so a strong downtrend confirms
// bearish, not bullish.  CMI uses abs() so a mirrored up/downtrend yields the
// same CMI — only dmiDiff breaks the symmetry.
func TestCHOPCMISignFollowsDMIDirection(t *testing.T) {
	const n = 61
	candles := flatCandles(n, 10)
	obv := make([]float64, n)

	mkCHOPCMI := func(last indicator.Result) int {
		results := flatResults(n, last)
		return analysis.ScoreResult(candles, results, obv, 1, 1, 1.0).CHOPCMI
	}

	strong := indicator.Result{CHOP: 15, CMI: 95} // CHOP<30 && CMI>70 → strong trend efficiency

	// Bull direction: PDI>MDI → CHOPCMI positive.
	if got := mkCHOPCMI(withDMI(strong, 40, 10, 30)); got != 3 {
		t.Fatalf("bull dmiDiff: CHOPCMI=%d, want +3", got)
	}
	// Bear direction: PDI<MDI → CHOPCMI negative (the #7 fix).
	if got := mkCHOPCMI(withDMI(strong, 10, 40, 30)); got != -3 {
		t.Fatalf("bear dmiDiff: CHOPCMI=%d, want -3 (sign must follow DMI, not be +3)", got)
	}

	weak := indicator.Result{CHOP: 15, CMI: 65} // CHOP<38.2 && CMI>60 → 2 magnitude
	if got := mkCHOPCMI(withDMI(weak, 40, 10, 25)); got != 2 {
		t.Fatalf("bull weak dmiDiff: CHOPCMI=%d, want +2", got)
	}
	if got := mkCHOPCMI(withDMI(weak, 10, 40, 25)); got != -2 {
		t.Fatalf("bear weak dmiDiff: CHOPCMI=%d, want -2", got)
	}

	// Choppy market (CHOP high / CMI low) stays negative regardless of stance —
	// chop drags any directional strategy, no direction symmetry to enforce.
	choppy := indicator.Result{CHOP: 80, CMI: 20}
	if got := mkCHOPCMI(withDMI(choppy, 40, 10, 30)); got != -3 {
		t.Fatalf("choppy bull: CHOPCMI=%d, want -3", got)
	}
}

// withDMI returns a copy of r with its DMI fields set (PDI, MDI, ADX).
func withDMI(r indicator.Result, pdi, mdi, adx float64) indicator.Result {
	r.DMI.PDI, r.DMI.MDI, r.DMI.ADX = pdi, mdi, adx
	return r
}

// evalSignals: CHOP must not double-count with ADX as a second trend-strength
// vote, and WR/KDJ must share one same-source momentum vote.  Flat candles pin
// close==ma5==ma20 so the MA 排列 branch never trips — only the score-axis votes
// remain, letting us assert exact counts.
func TestEvalSignalsSameSourceNoDoubleVote(t *testing.T) {
	const n = 61
	i := n - 1
	candles := flatCandles(n, 10)
	obv := make([]float64, n)
	results := flatResults(n, indicator.Result{})

	mk := func(mutate func(*indicator.Result)) analysis.SignalState {
		last := results[i]
		mutate(&last)
		results[i] = last
		return analysis.EvalSignals(candles, results, obv, i)
	}

	// #5: a downtrend with ADX>25, MACD dead cross, CHOP very low (trend strong).
	// Old code counted CHOP<38.2 + ADX>25 as TWO votes → 3 with MACD → fired
	// TrendBear WITHOUT MA confirmation.  New code drops CHOP → only ADX + MACD =
	// 2 votes → TrendBear must NOT fire (needs MA 排列 to reach 3).
	t.Run("趋势同轴_CHOP不与ADX双计_无MA不触发", func(t *testing.T) {
		s := mk(func(r *indicator.Result) {
			r.DMI.ADX, r.DMI.PDI, r.DMI.MDI = 30, 10, 40 // ADX>25 + MDI>PDI
			r.MACD.DIF = -0.5                            // dead cross direction
			r.CHOP = 15                                  // very low (trend efficient) — must NOT add a vote
			r.RSI.RSI6 = 50                              // neutral, no oversold interference
		})
		if s.TrendBearScore != 2 {
			t.Fatalf("TrendBearScore=%d, want 2 (ADX + MACD; CHOP no longer double-counts)", s.TrendBearScore)
		}
		if s.TrendBear {
			t.Fatalf("TrendBear fired at score=2 — must require 3 (MA 排列 guarantees independence)")
		}
	})

	// #6: oversold with RSI<30, WR>80, KDJ<20, BIAS not extreme.  Old code counted
	// WR and KDJ as TWO same-source votes → 3 with RSI → fired Oversold.  New code
	// merges WR||KDJ into ONE → RSI + same-source = 2 → must NOT fire.
	t.Run("动量同源_WR与KDJ合一票_不足3不触发", func(t *testing.T) {
		s := mk(func(r *indicator.Result) {
			r.RSI.RSI6 = 25                        // RSI oversold (1 vote)
			r.WR.WR14 = 85                         // WR oversold (same-source as KDJ)
			r.KDJ.K, r.KDJ.D, r.KDJ.J = 15, 18, 10 // KDJ oversold + J turning up (same-source)
			r.BIAS.BIAS24 = -8                     // not extreme, so BIAS adds no vote
		})
		if s.OversoldScore != 2 {
			t.Fatalf("OversoldScore=%d, want 2 (RSI + WR||KDJ merged; WR and KDJ no longer double-count)", s.OversoldScore)
		}
		if s.Oversold {
			t.Fatalf("Oversold fired at score=2 — WR/KDJ must share one vote, threshold stays 3")
		}
	})

	// Confirm the symmetric overbought path merges WR||KDJ too (catch a regression
	// that fixes only one side).
	t.Run("超买同源_WR与KDJ合一票_不足3不触发", func(t *testing.T) {
		s := mk(func(r *indicator.Result) {
			r.RSI.RSI6 = 75
			r.WR.WR14 = 15
			r.KDJ.K, r.KDJ.D, r.KDJ.J = 85, 88, 90
			r.BIAS.BIAS24 = 8
		})
		if s.OverboughtScore != 2 {
			t.Fatalf("OverboughtScore=%d, want 2 (RSI + WR||KDJ merged)", s.OverboughtScore)
		}
		if s.Overbought {
			t.Fatalf("Overbought fired at score=2 — same-source merge must apply symmetrically")
		}
	})
}
