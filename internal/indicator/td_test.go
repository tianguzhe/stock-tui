package indicator

import "testing"

// tdCandles builds candles from closes with a symmetric ±1 high/low band,
// enough for setup counting (close-only) and the default countdown/perfection
// checks. Tests that probe perfection with custom highs/lows tweak the result.
func tdCandles(closes []float64) []Candle {
	candles := make([]Candle, len(closes))
	for i, c := range closes {
		candles[i] = Candle{High: c + 1, Low: c - 1, Close: c}
	}
	return candles
}

func TestTDSequentialEmptyAndShort(t *testing.T) {
	if got := TDSequential(nil); len(got) != 0 {
		t.Fatalf("TDSequential(nil) len = %d, want 0", len(got))
	}
	// i<4 cannot compare close[i-4], so no setup can begin.
	for i, td := range TDSequential(tdCandles([]float64{10, 11, 12, 13})) {
		if td.SetupCount != 0 || td.SetupSignal != TDNeutral {
			t.Fatalf("bar %d = %+v, want neutral", i, td)
		}
	}
}

// TestTDSequentialBuySetupAndCountdown rises for 6 bars (so a price flip can
// fire) then falls 4/bar. The buy setup completes at bar 14; the continued
// decline makes close <= low[i-2] every bar, so the buy countdown reaches 13
// at bar 26.
func TestTDSequentialBuySetupAndCountdown(t *testing.T) {
	closes := []float64{100, 101, 102, 103, 104, 105}
	for i := 6; i <= 26; i++ {
		closes = append(closes, 105-4*float64(i-5))
	}
	got := TDSequential(tdCandles(closes))

	if got[14].SetupCount != 9 || got[14].SetupSignal != TDBuy {
		t.Fatalf("bar14 = %+v, want SetupCount 9 / TDBuy", got[14])
	}
	if !got[14].SetupPerfected {
		t.Fatalf("bar14 SetupPerfected = false, want true")
	}
	if got[26].CountdownCount != 13 || got[26].CountdownSignal != TDBuy {
		t.Fatalf("bar26 = %+v, want CountdownCount 13 / TDBuy", got[26])
	}
}

// TestTDSequentialSetupRequiresPriceFlip feeds a strictly declining series:
// every bar satisfies close < close[i-4], but the prior bar never closed above
// its own 4-bars-earlier close, so the bullish price flip never fires and no
// setup may start.
func TestTDSequentialSetupRequiresPriceFlip(t *testing.T) {
	closes := make([]float64, 20)
	for i := range closes {
		closes[i] = 100 - float64(i)
	}
	for i, td := range TDSequential(tdCandles(closes)) {
		if td.SetupCount != 0 {
			t.Fatalf("bar %d = %+v, want no setup without a price flip", i, td)
		}
	}
}

// TestTDSequentialSellSetupPerfection completes a sell setup at bar 14, then
// checks both perfection outcomes by controlling bar highs.
func TestTDSequentialSellSetupPerfection(t *testing.T) {
	closes := []float64{100, 99, 98, 97, 96, 95}
	for i := 6; i <= 14; i++ {
		closes = append(closes, 95+4*float64(i-5))
	}

	// Perfected: default rising highs (close+1) let bars 8/9 exceed bars 6/7.
	got := TDSequential(tdCandles(closes))
	if got[14].SetupCount != 9 || got[14].SetupSignal != TDSell {
		t.Fatalf("bar14 = %+v, want SetupCount 9 / TDSell", got[14])
	}
	if !got[14].SetupPerfected {
		t.Fatalf("bar14 SetupPerfected = false, want true")
	}

	// Non-perfected: tower the highs of bars 6 & 7 (idx 11,12) and flatten the
	// highs of bars 8 & 9 (idx 13,14) so they cannot exceed bars 6 & 7.
	candles := tdCandles(closes)
	candles[11].High, candles[12].High = 200, 200
	candles[13].High, candles[14].High = closes[13], closes[14]
	got2 := TDSequential(candles)
	if got2[14].SetupCount != 9 || got2[14].SetupSignal != TDSell {
		t.Fatalf("bar14(non-perfected) = %+v, want SetupCount 9 / TDSell", got2[14])
	}
	if got2[14].SetupPerfected {
		t.Fatalf("bar14(non-perfected) SetupPerfected = true, want false")
	}
}

// TestTDSequentialCountdownFlipsOnOppositeSetup opens a buy countdown (bar 14),
// then a sharp reversal builds a sell setup that completes at bar 23 and must
// cancel/flip the running buy countdown to a sell countdown.
func TestTDSequentialCountdownFlipsOnOppositeSetup(t *testing.T) {
	closes := []float64{100, 101, 102, 103, 104, 105, 101, 97, 93, 89, 85, 81, 77, 73, 69}
	for i := 15; i <= 23; i++ {
		closes = append(closes, 105+float64(i))
	}
	got := TDSequential(tdCandles(closes))

	if got[14].CountdownSignal != TDBuy || got[14].CountdownCount != 1 {
		t.Fatalf("bar14 = %+v, want buy countdown count 1", got[14])
	}
	if got[23].SetupCount != 9 || got[23].SetupSignal != TDSell {
		t.Fatalf("bar23 = %+v, want sell setup 9", got[23])
	}
	if got[23].CountdownSignal != TDSell {
		t.Fatalf("bar23 CountdownSignal = %v, want TDSell (buy countdown cancelled)", got[23].CountdownSignal)
	}
}
