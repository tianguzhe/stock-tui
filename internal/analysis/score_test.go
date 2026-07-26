package analysis

import (
	"math"
	"testing"

	"stock-tui/internal/indicator"
)

func TestScoreLabel(t *testing.T) {
	cases := []struct {
		score int
		want  string
	}{
		{90, "技术极强"},
		{75, "技术偏强"},
		{60, "技术略偏强"},
		{50, "技术中性/方向不明"},
		{35, "技术略偏弱"},
		{20, "技术偏弱"},
		{5, "技术极弱"},
	}
	for _, tc := range cases {
		if got := ScoreLabel(tc.score); got != tc.want {
			t.Errorf("ScoreLabel(%d) = %q, want %q", tc.score, got, tc.want)
		}
	}
}

func TestCountTrue(t *testing.T) {
	if got := CountTrue(true, false, true); got != 2 {
		t.Errorf("CountTrue(true,false,true) = %d, want 2", got)
	}
	if got := CountTrue(false, false); got != 0 {
		t.Errorf("CountTrue(false,false) = %d, want 0", got)
	}
	if got := CountTrue(); got != 0 {
		t.Errorf("CountTrue() = %d, want 0", got)
	}
}

func TestClampInt(t *testing.T) {
	if got := ClampInt(5, 0, 10); got != 5 {
		t.Errorf("ClampInt(5,0,10) = %d, want 5", got)
	}
	if got := ClampInt(-3, 0, 10); got != 0 {
		t.Errorf("ClampInt(-3,0,10) = %d, want 0", got)
	}
	if got := ClampInt(15, 0, 10); got != 10 {
		t.Errorf("ClampInt(15,0,10) = %d, want 10", got)
	}
}

func TestRatio(t *testing.T) {
	if got := Ratio(10, 2); got != 5 {
		t.Errorf("Ratio(10,2) = %v, want 5", got)
	}
	if got := Ratio(10, 0); got != 0 {
		t.Errorf("Ratio(10,0) = %v, want 0", got)
	}
}

func TestMaxInt(t *testing.T) {
	if got := MaxInt(3, 7); got != 7 {
		t.Errorf("MaxInt(3,7) = %d, want 7", got)
	}
	if got := MaxInt(7, 3); got != 7 {
		t.Errorf("MaxInt(7,3) = %d, want 7", got)
	}
}

func TestAbsInt(t *testing.T) {
	if got := AbsInt(-5); got != 5 {
		t.Errorf("AbsInt(-5) = %d, want 5", got)
	}
	if got := AbsInt(5); got != 5 {
		t.Errorf("AbsInt(5) = %d, want 5", got)
	}
}

func TestMeanTail(t *testing.T) {
	values := []float64{1, 2, 3, 4, 5}
	if got := MeanTail(values, 3); got != 4 { // (3+4+5)/3
		t.Errorf("MeanTail([1..5], 3) = %v, want 4", got)
	}
	if got := MeanTail(nil, 3); got != 0 {
		t.Errorf("MeanTail(nil, 3) = %v, want 0", got)
	}
}

func TestOBVSeries(t *testing.T) {
	candles := []indicator.Candle{
		{Close: 10, Volume: 100},
		{Close: 11, Volume: 200}, // up
		{Close: 9, Volume: 150},  // down
		{Close: 9, Volume: 100},  // flat
	}
	obv := OBVSeries(candles)
	if len(obv) != 4 {
		t.Fatalf("len = %d, want 4", len(obv))
	}
	if obv[0] != 100 {
		t.Errorf("obv[0] = %v, want 100", obv[0])
	}
	if obv[1] != 300 { // 100 + 200
		t.Errorf("obv[1] = %v, want 300", obv[1])
	}
	if obv[2] != 150 { // 300 - 150
		t.Errorf("obv[2] = %v, want 150", obv[2])
	}
	if obv[3] != 150 { // flat, unchanged
		t.Errorf("obv[3] = %v, want 150", obv[3])
	}
}

func TestOBVTrend(t *testing.T) {
	if got := OBVTrend([]float64{1}); got != "样本不足" {
		t.Errorf("short = %q, want 样本不足", got)
	}
	obv := []float64{100, 200, 300, 400, 500, 600}
	if got := OBVTrend(obv); got != "上升(净流入)" {
		t.Errorf("rising = %q, want 上升(净流入)", got)
	}
	obv2 := []float64{600, 500, 400, 300, 200, 100}
	if got := OBVTrend(obv2); got != "下降(净流出)" {
		t.Errorf("falling = %q, want 下降(净流出)", got)
	}
	obv3 := []float64{100, 100, 100, 100, 100, 100}
	if got := OBVTrend(obv3); got != "持平" {
		t.Errorf("flat = %q, want 持平", got)
	}
}

// TestOBVDeltaAndUpLast 覆盖落库 snapshot.obv_up 的判据本身。obv_up 进
// screener 的 coreTech 硬门槛,判错会直接改变选股结果,必须锁死窗口口径。
func TestOBVDeltaAndUpLast(t *testing.T) {
	// 样本不足: 需要能回看 obvLookback(5) 根,即至少 6 根
	short := []float64{1, 2, 3, 4, 5}
	if got := OBVDelta(short); got != 0 {
		t.Errorf("OBVDelta(5 bars) = %v, want 0 (样本不足)", got)
	}
	if OBVUpLast(short) {
		t.Error("OBVUpLast(样本不足) = true, want false (不臆断方向)")
	}
	if got := OBVDelta(nil); got != 0 {
		t.Errorf("OBVDelta(nil) = %v, want 0", got)
	}
	if OBVUpLast(nil) {
		t.Error("OBVUpLast(nil) = true, want false")
	}

	// 恰好 6 根: 末根与首根比较
	six := []float64{10, 0, 0, 0, 0, 12}
	if got := OBVDelta(six); got != 2 {
		t.Errorf("OBVDelta(6 bars) = %v, want 2 (12-10)", got)
	}
	if !OBVUpLast(six) {
		t.Error("OBVUpLast(净流入) = false, want true")
	}

	// 净流出
	out := []float64{20, 0, 0, 0, 0, 15}
	if got := OBVDelta(out); got != -5 {
		t.Errorf("OBVDelta(净流出) = %v, want -5", got)
	}
	if OBVUpLast(out) {
		t.Error("OBVUpLast(净流出) = true, want false")
	}

	// 持平不算净流入(严格大于)
	flat := []float64{7, 1, 2, 3, 4, 7}
	if got := OBVDelta(flat); got != 0 {
		t.Errorf("OBVDelta(持平) = %v, want 0", got)
	}
	if OBVUpLast(flat) {
		t.Error("OBVUpLast(持平) = true, want false")
	}

	// 三者必须共用同一窗口: OBVUpLast / OBVTrend / OBVUp3Day 末根判据一致
	rising := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	if !OBVUpLast(rising) || OBVTrend(rising) != "上升(净流入)" || !OBVUp3Day(rising) {
		t.Errorf("同一上升序列三者不一致: upLast=%v trend=%q up3=%v",
			OBVUpLast(rising), OBVTrend(rising), OBVUp3Day(rising))
	}
	falling := []float64{10, 9, 8, 7, 6, 5, 4, 3, 2, 1}
	if OBVUpLast(falling) || OBVTrend(falling) != "下降(净流出)" || OBVUp3Day(falling) {
		t.Errorf("同一下降序列三者不一致: upLast=%v trend=%q up3=%v",
			OBVUpLast(falling), OBVTrend(falling), OBVUp3Day(falling))
	}
}

func TestOBVUp3Day(t *testing.T) {
	// 样本不足: 最后 3 根各自要能回看 obvLookback(5) 根 → 至少 8 根
	if got := OBVUp3Day([]float64{1, 2, 3, 4, 5, 6, 7}); got {
		t.Error("OBVUp3Day(7 bars) = true, want false (样本不足)")
	}
	// 单调上升: 最后 3 根都高于各自 5 日前 → true
	rising := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	if got := OBVUp3Day(rising); !got {
		t.Error("OBVUp3Day(rising) = false, want true")
	}
	// 最后一根回落到 5 日前之下 → false(要求连续 3 根成立)
	broken := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 3}
	if got := OBVUp3Day(broken); got {
		t.Error("OBVUp3Day(broken last bar) = true, want false")
	}
	// 只有最后一根成立(单日净流入),前两根不成立 → false,应降为 watch
	onlyLast := []float64{10, 10, 10, 10, 10, 10, 9, 20}
	if got := OBVUp3Day(onlyLast); got {
		t.Error("OBVUp3Day(only last day up) = true, want false")
	}
	// 持平不算净流入(严格大于)
	flat := []float64{5, 5, 5, 5, 5, 5, 5, 5, 5}
	if got := OBVUp3Day(flat); got {
		t.Error("OBVUp3Day(flat) = true, want false")
	}
	// 与单日 obv_up 口径一致: 末根的判据必须与 obv[n-1] > obv[n-6] 相同
	series := []float64{100, 100, 100, 100, 100, 100, 101, 102, 103}
	n := len(series)
	singleDay := series[n-1] > series[n-6]
	if !singleDay {
		t.Fatal("fixture broken: 单日 obv_up 应为 true")
	}
	if !OBVUp3Day(series) {
		t.Error("三日全满足时 OBVUp3Day 应为 true(与单日同窗口)")
	}
}

func TestStreakValue(t *testing.T) {
	candles := []indicator.Candle{
		{Close: 10},
		{Close: 11},
		{Close: 12},
		{Close: 11},
	}
	if got := StreakValue(candles); got != -1 {
		t.Errorf("StreakValue = %d, want -1", got)
	}

	candles2 := []indicator.Candle{
		{Close: 10},
		{Close: 9},
		{Close: 10},
		{Close: 11},
		{Close: 12},
	}
	if got := StreakValue(candles2); got != 3 {
		t.Errorf("StreakValue = %d, want 3", got)
	}
}

func TestPerfScale(t *testing.T) {
	// Non-negative passes through
	if got := PerfScale(3, 50, 20, 35, 55); got != 3 {
		t.Errorf("PerfScale(3) = %d, want 3", got)
	}
	// 小样本一律原样保留: n=5 的区间几乎覆盖整个 [0,100]
	if got := PerfScale(-7, 50, 5, 35, 55); got != -7 {
		t.Errorf("PerfScale(-7,n=5) = %d, want -7", got)
	}
	// 关键回归: n=10/win=30% 的 Wilson 区间是 [10.8, 60.3],跨越 50% 两侧,
	// 统计上无法断定「历史差」。旧实现(点估计+n>=10)会把惩罚砍半。
	if got := PerfScale(-7, 30, 10, 35, 55); got != -7 {
		t.Errorf("PerfScale(-7,win=30,n=10) = %d, want -7 (区间过宽,不调权)", got)
	}
	// 同理 n=20/win=20%: 上界 41.6% > 35%,仍不足以断定历史差
	if got := PerfScale(-7, 20, 20, 35, 55); got != -7 {
		t.Errorf("PerfScale(-7,win=20,n=20) = %d, want -7 (上界41.6%%>35%%)", got)
	}
	// 上界显著低于 35%(win=20,n=40 → hi=34.8) → 惩罚减半(向零截断)
	if got := PerfScale(-7, 20, 40, 35, 55); got != -3 {
		t.Errorf("PerfScale(-7,win=20,n=40) = %d, want -3", got)
	}
	// 下界显著高于 55%(win=85,n=40 → lo=70.9) → 惩罚 x1.5
	if got := PerfScale(-4, 85, 40, 35, 55); got != -6 {
		t.Errorf("PerfScale(-4,win=85,n=40) = %d, want -6", got)
	}
}

func TestStochStagnation(t *testing.T) {
	// Bear: RSI > 75, cross down, kPrev > 80
	bull, bear := StochStagnation(80, 70, 75, 85, 80)
	if !bear {
		t.Error("expected bear stagnation")
	}
	if bull {
		t.Error("unexpected bull stagnation")
	}
	// Bull: RSI < 25, cross up, kPrev < 20
	bull, bear = StochStagnation(20, 25, 20, 15, 20)
	if !bull {
		t.Error("expected bull stagnation")
	}
	if bear {
		t.Error("unexpected bear stagnation")
	}
}

func TestNDayReturn(t *testing.T) {
	candles := []indicator.Candle{
		{Close: 100},
		{Close: 105},
		{Close: 110},
		{Close: 120},
	}
	if got := NDayReturn(candles, 3); got != 20 { // (120-100)/100*100
		t.Errorf("NDayReturn(3) = %v, want 20", got)
	}
	if got := NDayReturn(candles, 10); got != 0 { // not enough bars
		t.Errorf("NDayReturn(10) = %v, want 0", got)
	}
}

func TestPosition(t *testing.T) {
	if got := Position(50, 0, 100); got != 50 {
		t.Errorf("Position(50,0,100) = %v, want 50", got)
	}
	if got := Position(0, 0, 100); got != 0 {
		t.Errorf("Position(0,0,100) = %v, want 0", got)
	}
	if got := Position(50, 50, 50); got != 50 { // high == low
		t.Errorf("Position(50,50,50) = %v, want 50", got)
	}
}

func TestTDSignalText(t *testing.T) {
	if got := TDSignalText(indicator.TDBuy); got != "见底" {
		t.Errorf("TDBuy = %q, want 见底", got)
	}
	if got := TDSignalText(indicator.TDSell); got != "见顶" {
		t.Errorf("TDSell = %q, want 见顶", got)
	}
}

func TestTDShort(t *testing.T) {
	td := indicator.TD{CountdownCount: 8, CountdownSignal: indicator.TDSell}
	if got := TDShort(td); got != "C顶8" {
		t.Errorf("TDShort countdown = %q, want C顶8", got)
	}
	td2 := indicator.TD{SetupCount: 9, SetupSignal: indicator.TDBuy, SetupPerfected: true}
	if got := TDShort(td2); got != "S底9*" {
		t.Errorf("TDShort setup perfected = %q, want S底9*", got)
	}
}

func TestDivergenceDetection(t *testing.T) {
	// Create a minimal scenario: 25 candles with RSI data
	candles := make([]indicator.Candle, 25)
	results := make([]indicator.Result, 25)
	for i := range candles {
		candles[i] = indicator.Candle{Close: float64(100 + i), High: float64(101 + i), Low: float64(99 + i)}
		results[i] = indicator.Result{
			RSI:  indicator.RSI{RSI6: 50},
			MACD: indicator.MACD{DIF: 0.1},
		}
	}
	// Make RSI peak in the middle
	results[10].RSI.RSI6 = 80
	// Current bar: RSI declining but price still high
	results[24].RSI.RSI6 = 65
	candles[24].Close = 130 // higher than peak day

	div := Divergence(candles, results, 24)
	if !div.Ready {
		t.Error("Divergence should be ready with 25 bars")
	}
	// This should trigger bear divergence: RSI declining from peak, price still up
	if !div.Bear {
		t.Error("expected bear divergence")
	}
	if !div.BearToday {
		t.Error("condition holds at index 24 itself, expected BearToday true")
	}
}

// TestDivergenceScore 锁定顶/底背离分别计分后相加,不互相覆盖。
func TestDivergenceScore(t *testing.T) {
	tests := []struct {
		name string
		d    DivergenceState
		want int
	}{
		{"无背离", DivergenceState{}, 0},
		{"当日顶背离", DivergenceState{Bear: true, BearToday: true}, -3},
		{"非当日顶背离", DivergenceState{Bear: true}, -1},
		{"当日底背离", DivergenceState{Bull: true, BullToday: true}, 2},
		{"非当日底背离", DivergenceState{Bull: true}, 1},
		// 回归用例: 旧实现在这里返回 +1(顶背离被覆盖),方向反了。
		{
			name: "当日顶背离 + 窗口内底背离",
			d:    DivergenceState{Bear: true, BearToday: true, Bull: true},
			want: -2,
		},
		// 反向: 当日底背离 + 窗口内顶背离,旧实现返回 +2,丢掉顶背离。
		{
			name: "当日底背离 + 窗口内顶背离",
			d:    DivergenceState{Bull: true, BullToday: true, Bear: true},
			want: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DivergenceScore(tt.d); got != tt.want {
				t.Errorf("DivergenceScore() = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestDivergenceBearTodayDistinctFromBear covers the case where the bear
// divergence condition held a couple of days ago but not on the current bar —
// Bear should stay true (softer "非当日" weight) while BearToday must be false.
func TestDivergenceBearTodayDistinctFromBear(t *testing.T) {
	candles := make([]indicator.Candle, 25)
	results := make([]indicator.Result, 25)
	for i := range candles {
		candles[i] = indicator.Candle{Close: float64(100 + i), High: float64(101 + i), Low: float64(99 + i)}
		results[i] = indicator.Result{
			RSI:  indicator.RSI{RSI6: 50},
			MACD: indicator.MACD{DIF: 0.1},
		}
	}
	results[10].RSI.RSI6 = 80 // RSI peak
	// Bear condition holds two days ago (index 22), not today (index 24).
	results[22].RSI.RSI6 = 65
	results[24].RSI.RSI6 = 50 // back below 60 today — strict condition fails

	div := Divergence(candles, results, 24)
	if div.BearToday {
		t.Error("condition does not hold at index 24, expected BearToday false")
	}
	if !div.Bear {
		t.Error("condition held within recentWindow (index 22), expected Bear true")
	}
}

// TestDivergenceStale confirms Bear/BearToday both stay false once the
// triggering day falls outside recentWindow.
func TestDivergenceStale(t *testing.T) {
	candles := make([]indicator.Candle, 30)
	results := make([]indicator.Result, 30)
	for i := range candles {
		candles[i] = indicator.Candle{Close: float64(100 + i), High: float64(101 + i), Low: float64(99 + i)}
		results[i] = indicator.Result{
			RSI:  indicator.RSI{RSI6: 50},
			MACD: indicator.MACD{DIF: 0.1},
		}
	}
	results[10].RSI.RSI6 = 80
	results[20].RSI.RSI6 = 65 // triggers bear at index 20, well outside recentWindow of index 28

	div := Divergence(candles, results, 28)
	if div.Bear || div.BearToday {
		t.Error("bear divergence at index 20 is stale by index 28, expected both false")
	}
}

// TestWilsonBounds 锁定 Wilson 95% 置信区间的计算,它同时决定 screener 的准入
// 门槛与 PerfScale 的惩罚调权,是两处共用的显著性标准。
func TestWilsonBounds(t *testing.T) {
	tests := []struct {
		name      string
		winPct    float64
		n         int
		wantLower float64
		wantUpper float64
		tolerance float64
	}{
		{
			name:      "小样本区间极宽 N=10 win=40%",
			winPct:    40,
			n:         10,
			wantLower: 16.8,
			wantUpper: 68.7,
			tolerance: 0.1,
		},
		{
			name:      "大样本区间收窄 N=100 win=60%",
			winPct:    60,
			n:         100,
			wantLower: 50.1,
			wantUpper: 69.2,
			tolerance: 1.0,
		},
		{
			name:      "零样本返回全区间",
			winPct:    50,
			n:         0,
			wantLower: 0,
			wantUpper: 100,
			tolerance: 0,
		},
		{
			name:      "满胜率仍有上界",
			winPct:    100,
			n:         10,
			wantLower: 70,
			wantUpper: 100,
			tolerance: 5,
		},
		// 越界输入被 clamp 到 [0,1],不得产生 NaN
		{
			name:      "胜率越界被clamp不产生NaN",
			winPct:    150,
			n:         20,
			wantLower: 83.9,
			wantUpper: 100,
			tolerance: 0.5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lo, hi := WilsonBounds(tt.winPct, tt.n)
			if math.IsNaN(lo) || math.IsNaN(hi) {
				t.Fatalf("WilsonBounds(%v,%d) 返回 NaN: lo=%v hi=%v", tt.winPct, tt.n, lo, hi)
			}
			if math.Abs(lo-tt.wantLower) > tt.tolerance {
				t.Errorf("lower bound = %.1f, want %.1f±%.1f", lo, tt.wantLower, tt.tolerance)
			}
			if math.Abs(hi-tt.wantUpper) > tt.tolerance {
				t.Errorf("upper bound = %.1f, want %.1f±%.1f", hi, tt.wantUpper, tt.tolerance)
			}
		})
	}
}

// TestVolRatio 锁定量比口径 = 当日量 / 前5日(不含当日)均量。
//
// fixture 取自 2026-07-25 对腾讯 qt[49] 的实测反解,5 只标的全部吻合:
// 东材 0.77、工行 0.69、农行 0.79、华安 1.08、华天 0.96。
// 这里用工商银行的真实成交量序列做端到端验证——它同时是区分度最好的样本
// (旧的 Volume/MA20 口径在它身上得 0.758,与真值 0.69 差 10%)。
func TestVolRatio(t *testing.T) {
	// 工商银行 2026-07-09..07-24 真实成交量(手,取自腾讯 proxy K线),
	// 最后一根为 07-24。当日 3689522 / 前5日均 5317914.2 = 0.6938 ≈ qt[49]=0.69
	vols := []float64{
		4039925, 4068382, 6507835, 4764820, 4023618,
		3517401, 7151598, 6220762, 5758785, 4301543,
		3156883, 3689522,
	}
	candles := make([]indicator.Candle, len(vols))
	for i, v := range vols {
		candles[i] = indicator.Candle{Close: 7.75, Volume: v}
	}

	last := len(candles) - 1
	got := VolRatio(candles, last)
	if math.Abs(got-0.69) > 0.005 {
		t.Errorf("VolRatio = %.4f, want ≈0.69 (腾讯 qt[49] 实测值)", got)
	}

	// 旧口径 Volume/MA20(含当日) 会得到明显不同的值 —— 锁定两者确实有别,
	// 防止有人"顺手"改回 MeanTail(volumes,20)。
	if oldStyle := Ratio(candles[last].Volume, MeanTail(VolumeSeries(candles), 20)); math.Abs(oldStyle-got) < 0.01 {
		t.Errorf("旧口径 %.4f 与新口径 %.4f 过于接近，fixture 失去区分度", oldStyle, got)
	}

	// 边界: 无前日 / 越界 / 空输入一律返回 0
	if v := VolRatio(candles, 0); v != 0 {
		t.Errorf("VolRatio(i=0) = %v, want 0", v)
	}
	if v := VolRatio(candles, len(candles)); v != 0 {
		t.Errorf("VolRatio(越界) = %v, want 0", v)
	}
	if v := VolRatio(nil, 1); v != 0 {
		t.Errorf("VolRatio(nil) = %v, want 0", v)
	}

	// 窗口不满 5 日时用可得日数: i=2 → 前两日均量 (100+200)/2=150
	short := []indicator.Candle{{Volume: 100}, {Volume: 200}, {Volume: 300}}
	if v := VolRatio(short, 2); math.Abs(v-2.0) > 1e-9 {
		t.Errorf("VolRatio(窗口不满) = %v, want 2.0 (300/150)", v)
	}

	// 前5日均量为0时安全返回0(Ratio 保护),不 panic 不 Inf
	zero := []indicator.Candle{{Volume: 0}, {Volume: 0}, {Volume: 500}}
	if v := VolRatio(zero, 2); v != 0 {
		t.Errorf("VolRatio(均量0) = %v, want 0", v)
	}
}
