package analysis

import (
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
	// n < 10 passes through
	if got := PerfScale(-7, 50, 5, 35, 55); got != -7 {
		t.Errorf("PerfScale(-7,n=5) = %d, want -7", got)
	}
	// win < 35 -> halved
	if got := PerfScale(-7, 20, 20, 35, 55); got != -3 {
		t.Errorf("PerfScale(-7,win=20) = %d, want -3", got)
	}
	// win > 55 -> amplified
	if got := PerfScale(-4, 60, 20, 35, 55); got != -6 {
		t.Errorf("PerfScale(-4,win=60) = %d, want -6", got)
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
}
