package indicator

import "testing"

func TestFibRetracementEmptyAndLookbackClamp(t *testing.T) {
	if got := FibRetracementOf(nil, 60); got.Levels != nil {
		t.Fatalf("FibRetracementOf(nil) Levels = %v, want nil", got.Levels)
	}
	// lookback beyond the series falls back to the whole series.
	got := FibRetracementOf([]Candle{{High: 12, Low: 8, Close: 10}, {High: 14, Low: 9, Close: 13}}, 999)
	if len(got.Levels) != 7 {
		t.Fatalf("Levels len = %d, want 7", len(got.Levels))
	}
}

// TestFibRetracementUptrend: swing low first (idx0), swing high last (idx3) →
// uptrend, 0% anchored at the high, levels descend as supports.
func TestFibRetracementUptrend(t *testing.T) {
	candles := []Candle{
		{High: 11, Low: 10, Close: 10.5}, // swing low = 10
		{High: 13, Low: 11, Close: 12},
		{High: 16, Low: 14, Close: 15},
		{High: 20, Low: 18, Close: 19}, // swing high = 20
	}
	got := FibRetracementOf(candles, 0) // whole series

	if !got.Uptrend {
		t.Fatalf("Uptrend = false, want true")
	}
	if got.High != 20 || got.Low != 10 || got.HighIndex != 3 || got.LowIndex != 0 {
		t.Fatalf("swing = %+v, want High 20 @3 / Low 10 @0", got)
	}
	// range = 10; uptrend price = high - ratio*range.
	assertNear(t, "0%", got.Levels[0].Price, 20, 1e-9)
	assertNear(t, "38.2%", got.Levels[2].Price, 16.18, 1e-9)
	assertNear(t, "50%", got.Levels[3].Price, 15, 1e-9)
	assertNear(t, "61.8%", got.Levels[4].Price, 13.82, 1e-9)
	assertNear(t, "100%", got.Levels[6].Price, 10, 1e-9)
}

// TestFibRetracementDowntrend: swing high first (idx0), swing low last (idx3) →
// downtrend, 0% anchored at the low, levels ascend as resistances.
func TestFibRetracementDowntrend(t *testing.T) {
	candles := []Candle{
		{High: 20, Low: 18, Close: 19}, // swing high = 20
		{High: 16, Low: 14, Close: 15},
		{High: 13, Low: 11, Close: 12},
		{High: 11, Low: 10, Close: 10.5}, // swing low = 10
	}
	got := FibRetracementOf(candles, 0)

	if got.Uptrend {
		t.Fatalf("Uptrend = true, want false")
	}
	if got.High != 20 || got.Low != 10 || got.HighIndex != 0 || got.LowIndex != 3 {
		t.Fatalf("swing = %+v, want High 20 @0 / Low 10 @3", got)
	}
	// range = 10; downtrend price = low + ratio*range.
	assertNear(t, "0%", got.Levels[0].Price, 10, 1e-9)
	assertNear(t, "61.8%", got.Levels[4].Price, 16.18, 1e-9)
	assertNear(t, "100%", got.Levels[6].Price, 20, 1e-9)
}

// TestFibRetracementLookbackWindow: an extreme old bar (idx0) sits outside the
// lookback window and must not affect the swing taken from the recent window.
func TestFibRetracementLookbackWindow(t *testing.T) {
	candles := []Candle{
		{High: 100, Low: 1, Close: 50}, // extreme old bar, excluded by lookback=3
		{High: 11, Low: 10, Close: 10.5},
		{High: 14, Low: 12, Close: 13},
		{High: 20, Low: 18, Close: 19},
	}
	got := FibRetracementOf(candles, 3) // window = idx 1..3

	if got.High != 20 || got.Low != 10 || got.HighIndex != 3 || got.LowIndex != 1 {
		t.Fatalf("swing = %+v, want High 20 @3 / Low 10 @1 (old bar excluded)", got)
	}
	if !got.Uptrend {
		t.Fatalf("Uptrend = false, want true")
	}
}

// TestFibRetracementFlatWindow: a no-range window collapses every level to the
// single price without panicking.
func TestFibRetracementFlatWindow(t *testing.T) {
	candles := []Candle{{High: 10, Low: 10, Close: 10}, {High: 10, Low: 10, Close: 10}}
	got := FibRetracementOf(candles, 2)
	for _, lv := range got.Levels {
		assertNear(t, "flat", lv.Price, 10, 1e-9)
	}
}
