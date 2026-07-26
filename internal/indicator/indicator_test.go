package indicator

import (
	"math"
	"testing"
)

func TestCalculateHandlesShortInput(t *testing.T) {
	got := Calculate([]Candle{{Close: 10}})

	if len(got) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(got))
	}
	if got[0].MACD.DIF != 0 || got[0].KDJ.K != 50 || got[0].RSI.RSI6 != 50 {
		t.Fatalf("short result = %+v, want neutral defaults", got[0])
	}
}

func TestCalculateConstantSeriesStaysNeutral(t *testing.T) {
	candles := make([]Candle, 40)
	for i := range candles {
		candles[i] = Candle{High: 10, Low: 10, Close: 10}
	}

	last := Calculate(candles)[len(candles)-1]
	assertNear(t, "KDJ.K", last.KDJ.K, 50, 1e-9)
	assertNear(t, "KDJ.D", last.KDJ.D, 50, 1e-9)
	assertNear(t, "MACD.DIF", last.MACD.DIF, 0, 1e-9)
	assertNear(t, "RSI6", last.RSI.RSI6, 50, 1e-9)
	assertNear(t, "WR", last.WR.WR10, 50, 1e-9)
	assertNear(t, "BIAS6", last.BIAS.BIAS6, 0, 1e-9)
	assertNear(t, "DMI.ADX", last.DMI.ADX, 0, 1e-9)
	assertNear(t, "CHOP", last.CHOP, 50, 1e-9)
	assertNear(t, "ATR14", last.ATR.ATR14, 0, 1e-9)
	assertNear(t, "BOLL.Mid", last.BOLL.Mid, 10, 1e-9)
	assertNear(t, "BOLL.PercentB", last.BOLL.PercentB, 50, 1e-9)
	assertNear(t, "Donchian.Upper20", last.Donchian.Upper20, 10, 1e-9)
	assertNear(t, "MFI", last.MFI, 50, 1e-9)
	// A flat series never breaks the SAR, so it stays long at the seed low (=10)
	// and Keltner collapses to the mean with no squeeze (ATR=0 → bands == mid).
	assertNear(t, "SAR.Value", last.SAR.Value, 10, 1e-9)
	if !last.SAR.Long {
		t.Fatalf("SAR.Long = false, want long on a flat series")
	}
	assertNear(t, "Keltner.Mid", last.Keltner.Mid, 10, 1e-9)
	if last.Keltner.Squeeze {
		t.Fatalf("Keltner.Squeeze = true, want false when bands collapse to the mean")
	}
	// A flat series never breaks SuperTrend either: it stays long at the band (=10).
	assertNear(t, "SuperTrend.Value", last.SuperTrend.Value, 10, 1e-9)
	if !last.SuperTrend.Long {
		t.Fatalf("SuperTrend.Long = false, want long on a flat series")
	}
}

func TestCalculateUptrendSignalsPositiveMomentum(t *testing.T) {
	candles := make([]Candle, 40)
	for i := range candles {
		close := 10 + float64(i)*0.5
		candles[i] = Candle{
			High:   close + 0.4,
			Low:    close - 0.3,
			Close:  close,
			Volume: 1000 + float64(i)*10,
		}
	}

	last := Calculate(candles)[len(candles)-1]
	if last.MACD.DIF <= 0 || last.MACD.Histogram <= 0 {
		t.Fatalf("MACD = %+v, want positive momentum", last.MACD)
	}
	if last.RSI.RSI6 <= 80 {
		t.Fatalf("RSI6 = %v, want strong uptrend", last.RSI.RSI6)
	}
	if last.BIAS.BIAS6 <= 0 {
		t.Fatalf("BIAS6 = %v, want positive bias", last.BIAS.BIAS6)
	}
	if last.DMI.PDI <= last.DMI.MDI {
		t.Fatalf("DMI = %+v, want PDI above MDI", last.DMI)
	}
	if last.WR.WR10 >= 20 {
		t.Fatalf("WR10 = %v, want near overbought top", last.WR.WR10)
	}
	if last.CHOP >= 50 {
		t.Fatalf("CHOP = %v, want trending (low choppiness)", last.CHOP)
	}
	if last.ATR.ATR14 <= 0 || last.ATR.Pct <= 0 {
		t.Fatalf("ATR = %+v, want positive volatility", last.ATR)
	}
	if last.BOLL.PercentB <= 90 {
		t.Fatalf("BOLL %%B = %v, want price near/above upper band in steady uptrend", last.BOLL.PercentB)
	}
	if last.Donchian.Upper20 != candles[len(candles)-1].High {
		t.Fatalf("Donchian.Upper20 = %v, want latest high %v", last.Donchian.Upper20, candles[len(candles)-1].High)
	}
	if last.MFI <= 80 {
		t.Fatalf("MFI = %v, want strong positive money flow", last.MFI)
	}
}

func TestCalculateSampleValues(t *testing.T) {
	candles := []Candle{
		{High: 10.5, Low: 9.8, Close: 10.0},
		{High: 10.8, Low: 10.0, Close: 10.6},
		{High: 11.1, Low: 10.4, Close: 10.9},
		{High: 11.3, Low: 10.7, Close: 11.0},
		{High: 11.6, Low: 10.8, Close: 11.4},
		{High: 11.8, Low: 11.0, Close: 11.2},
		{High: 12.1, Low: 11.1, Close: 11.9},
		{High: 12.4, Low: 11.5, Close: 12.0},
		{High: 12.6, Low: 11.7, Close: 12.4},
		{High: 12.8, Low: 12.0, Close: 12.2},
	}

	last := Calculate(candles)[len(candles)-1]
	assertNear(t, "sample K", last.KDJ.K, 83.0368, 1e-4)
	assertNear(t, "sample DIF", last.MACD.DIF, 0.5482, 1e-4)
	assertNear(t, "sample RSI6", last.RSI.RSI6, 80.5201, 1e-4)
	assertNear(t, "sample WR10", last.WR.WR10, 20.0000, 1e-4)
	assertNear(t, "sample PDI", last.DMI.PDI, 30.5731, 1e-4)
	assertNear(t, "sample CMI", last.CMI, 73.3333, 1e-4)
	assertNear(t, "sample BIAS6", last.BIAS.BIAS6, 2.9536, 1e-4)
	assertNear(t, "sample CHOP", last.CHOP, 42.5969, 1e-4)
	assertNear(t, "sample ATR14", last.ATR.ATR14, 0.4259, 1e-4)
	assertNear(t, "sample ATR%", last.ATR.Pct, 3.4908, 1e-4)
	assertNear(t, "sample BOLL mid", last.BOLL.Mid, 11.36, 1e-4)
	assertNear(t, "sample BOLL upper", last.BOLL.Upper, 12.8138, 1e-4)
	assertNear(t, "sample BOLL lower", last.BOLL.Lower, 9.9062, 1e-4)
	assertNear(t, "sample BOLL %B", last.BOLL.PercentB, 78.8894, 1e-4)
	assertNear(t, "sample BOLL bandwidth", last.BOLL.Bandwidth, 25.5955, 1e-4)
	assertNear(t, "sample Donchian upper20", last.Donchian.Upper20, 12.8, 1e-4)
	assertNear(t, "sample Donchian lower20", last.Donchian.Lower20, 9.8, 1e-4)
	assertNear(t, "sample Donchian upper55", last.Donchian.Upper55, 12.8, 1e-4)
	assertNear(t, "sample Donchian lower55", last.Donchian.Lower55, 9.8, 1e-4)
	assertNear(t, "sample MFI", last.MFI, 50, 1e-4)
	assertNear(t, "sample SAR", last.SAR.Value, 11.1929, 1e-4)
	if !last.SAR.Long {
		t.Fatalf("sample SAR.Long = false, want long in a rising sample")
	}
	assertNear(t, "sample Keltner mid", last.Keltner.Mid, 10.9841, 1e-4)
	assertNear(t, "sample Keltner upper", last.Keltner.Upper, 11.4714, 1e-4)
	assertNear(t, "sample Keltner lower", last.Keltner.Lower, 10.4968, 1e-4)
	if last.Keltner.Squeeze {
		t.Fatalf("sample Keltner.Squeeze = true, want false")
	}
	assertNear(t, "sample SuperTrend", last.SuperTrend.Value, 10.7996, 1e-4)
	if !last.SuperTrend.Long {
		t.Fatalf("sample SuperTrend.Long = false, want long in a rising sample")
	}
}

// TestStochRSI verifies the stochastic-of-RSI semantics by boundary cases
// rather than a brittle golden number: a flat RSI series must read neutral 50,
// outputs stay within [0,100], and a fresh rally after a decline pins %K high.
func TestStochRSI(t *testing.T) {
	// Flat closes -> every RSI6 == 50 -> window min==max -> neutral 50.
	flat := make([]Candle, 16)
	for i := range flat {
		flat[i] = Candle{High: 10.2, Low: 9.8, Close: 10.0}
	}
	flatLast := Calculate(flat)[len(flat)-1]
	assertNear(t, "flat StochRSI K", flatLast.StochRSI.K, 50, 1e-9)
	assertNear(t, "flat StochRSI D", flatLast.StochRSI.D, 50, 1e-9)

	// Decline (low RSI base) then a sustained rally: the latest RSI6 readings sit
	// at the top of the lookback window, so smoothed %K must be high.
	closes := []float64{
		20, 19.5, 19, 18.5, 18, 17.5, 17, 16.5, 16, // 9-bar decline
		16.6, 17.4, 18.3, 19.3, 20.4, 21.6, 22.9, 24.3, // 8-bar rally
	}
	rally := make([]Candle, len(closes))
	for i, c := range closes {
		rally[i] = Candle{High: c + 0.2, Low: c - 0.2, Close: c}
	}
	for i, r := range Calculate(rally) {
		if r.StochRSI.K < 0 || r.StochRSI.K > 100 || r.StochRSI.D < 0 || r.StochRSI.D > 100 {
			t.Fatalf("bar %d StochRSI out of [0,100]: K=%v D=%v", i, r.StochRSI.K, r.StochRSI.D)
		}
	}
	rallyLast := Calculate(rally)[len(rally)-1]
	if rallyLast.StochRSI.K <= 80 {
		t.Fatalf("rally StochRSI K = %v, want > 80 (RSI6 near window top)", rallyLast.StochRSI.K)
	}
}

// TestCalculateCHOPStaysNonNegative guards the warmup window: the first bar's
// high/low feed rangeHL, so its true range must also feed sumTR, otherwise an
// early bar with a wide range drives sum(TR)/rangeHL below 1 and CHOP negative.
// Choppiness Index is defined on [0,100]; a negative value is meaningless.
func TestCalculateCHOPStaysNonNegative(t *testing.T) {
	// First bar has a very wide range; the rest are calm. This is the shape
	// that produced CHOP < 0 when the first bar was dropped from sum(TR).
	candles := []Candle{{High: 110, Low: 90, Close: 100}}
	for i := 1; i < 20; i++ {
		candles = append(candles, Candle{High: 101, Low: 99, Close: 100})
	}

	for i, r := range Calculate(candles) {
		if r.CHOP < 0 || r.CHOP > 100 {
			t.Fatalf("CHOP[%d] = %v, want within [0,100]", i, r.CHOP)
		}
	}
}

func TestCalculateMFIDownFlow(t *testing.T) {
	candles := make([]Candle, 20)
	for i := range candles {
		close := 30 - float64(i)
		candles[i] = Candle{High: close + 0.5, Low: close - 0.5, Close: close, Volume: 1000}
	}

	last := Calculate(candles)[len(candles)-1]
	assertNear(t, "MFI", last.MFI, 0, 1e-9)
}

// TestCalculateSARUptrendThenReverse climbs steadily (SAR must trail below price
// in a long stance) then drops sharply: somewhere in the decline the price must
// pierce the SAR, flipping it to a short stance with Reversed=true on that bar.
func TestCalculateSARUptrendThenReverse(t *testing.T) {
	var candles []Candle
	for i := 0; i < 15; i++ {
		c := 10 + float64(i) // rising leg
		candles = append(candles, Candle{High: c + 0.5, Low: c - 0.5, Close: c})
	}
	for i := 0; i < 10; i++ {
		c := 24 - float64(i)*2 // sharp decline
		candles = append(candles, Candle{High: c + 0.5, Low: c - 0.5, Close: c})
	}

	res := Calculate(candles)
	if !res[14].SAR.Long {
		t.Fatalf("bar14 SAR.Long = false, want long during the uptrend")
	}
	if res[14].SAR.Value >= candles[14].Close {
		t.Fatalf("bar14 SAR.Value = %v, want below close %v", res[14].SAR.Value, candles[14].Close)
	}
	reversed, short := false, false
	for i := 15; i < len(candles); i++ {
		if res[i].SAR.Reversed {
			reversed = true
		}
		if !res[i].SAR.Long {
			short = true
		}
	}
	if !reversed || !short {
		t.Fatalf("decline never flipped SAR to short (reversed=%v short=%v)", reversed, short)
	}
}

// TestCalculateSARReversalRespectsPenetrationRule guards Wilder's rule that the
// SAR may never sit inside the price range of the reversal bar or the two bars
// before it. On a flip the SAR jumps to the old EP, which can land inside the
// current bar — without re-clamping in the NEW direction the next bar pierces it
// immediately and the stance flips straight back (whipsaw).
//
// Invariant checked on every reversal bar: a fresh short SAR must be at or above
// the recent highs, a fresh long SAR at or below the recent lows.
func TestCalculateSARReversalRespectsPenetrationRule(t *testing.T) {
	// The clamp only bites when the reversal bar ALSO makes a new extreme: the
	// SAR jumps to the old EP, and an EP below this bar's high lands inside the
	// bar. Build exactly that — a steady advance, then one huge outside bar that
	// prints a new high (above EP) while collapsing through the trailing stop.
	var candles []Candle
	for i := 0; i < 15; i++ {
		c := 10 + float64(i)
		candles = append(candles, Candle{High: c + 0.5, Low: c - 0.5, Close: c})
	}
	// EP is now 24.5 (bar14 high). This bar tops it at 26 yet crashes to 20.
	candles = append(candles, Candle{High: 26, Low: 20, Close: 21})
	// A bounce that clears the OLD ep (24.5) but not the properly clamped SAR.
	candles = append(candles, Candle{High: 25, Low: 21, Close: 24})
	candles = append(candles, Candle{High: 24, Low: 20, Close: 21})

	res := Calculate(candles)
	if res[15].SAR.Long || !res[15].SAR.Reversed {
		t.Fatalf("bar15 should flip to short: long=%v reversed=%v", res[15].SAR.Long, res[15].SAR.Reversed)
	}
	if res[15].SAR.Value < candles[15].High {
		t.Errorf("bar15 short SAR=%v sits below the bar's own high %v — old EP left inside the bar",
			res[15].SAR.Value, candles[15].High)
	}
	if res[16].SAR.Reversed {
		t.Errorf("bar16 flipped straight back to long (whipsaw): SAR=%v vs bar16 high %v",
			res[16].SAR.Value, candles[16].High)
	}
	reversals := 0
	for i := 1; i < len(candles); i++ {
		if !res[i].SAR.Reversed {
			continue
		}
		reversals++
		hi, lo := candles[i].High, candles[i].Low
		if candles[i-1].High > hi {
			hi = candles[i-1].High
		}
		if candles[i-1].Low < lo {
			lo = candles[i-1].Low
		}
		if i >= 2 {
			if candles[i-2].High > hi {
				hi = candles[i-2].High
			}
			if candles[i-2].Low < lo {
				lo = candles[i-2].Low
			}
		}
		if res[i].SAR.Long {
			if res[i].SAR.Value > lo {
				t.Errorf("bar%d flipped long but SAR=%v sits above recent low %v (penetrates price)",
					i, res[i].SAR.Value, lo)
			}
		} else if res[i].SAR.Value < hi {
			t.Errorf("bar%d flipped short but SAR=%v sits below recent high %v (penetrates price)",
				i, res[i].SAR.Value, hi)
		}
		// The flip must stick for at least one bar — an immediate flip-back is
		// exactly the whipsaw the clamp exists to prevent.
		if i+1 < len(res) && res[i+1].SAR.Reversed {
			t.Errorf("bar%d reversal immediately flipped back on bar%d (whipsaw)", i, i+1)
		}
	}
	if reversals == 0 {
		t.Fatal("fixture produced no reversal — the invariant above was never exercised")
	}
}

// TestCalculateKeltnerSqueeze holds the close flat (std=0 → BOLL collapses to the
// mean) while every bar carries an intraday range (ATR>0 → Keltner stays wide).
// The Bollinger band then sits entirely inside the Keltner channel = squeeze on,
// the classic volatility-compression / breakout-pending state.
func TestCalculateKeltnerSqueeze(t *testing.T) {
	candles := make([]Candle, 40)
	for i := range candles {
		candles[i] = Candle{High: 100.5, Low: 99.5, Close: 100}
	}

	last := Calculate(candles)[len(candles)-1]
	assertNear(t, "Keltner.Mid", last.Keltner.Mid, 100, 1e-9)
	if !(last.Keltner.Upper > last.BOLL.Upper && last.Keltner.Lower < last.BOLL.Lower) {
		t.Fatalf("Keltner %v..%v should bracket BOLL %v..%v",
			last.Keltner.Lower, last.Keltner.Upper, last.BOLL.Lower, last.BOLL.Upper)
	}
	if !last.Keltner.Squeeze {
		t.Fatalf("Keltner.Squeeze = false, want true (BOLL inside Keltner)")
	}
}

// TestCalculateSuperTrendUptrendThenReverse: a long steady climb keeps SuperTrend
// below price in a long stance, then a sharp decline must flip it to short with
// Reversed=true on the flip bar (it trails by ATR*3, so the legs are long enough
// for the band to settle before the flip).
func TestCalculateSuperTrendUptrendThenReverse(t *testing.T) {
	var candles []Candle
	for i := 0; i < 20; i++ {
		c := 10 + float64(i) // rising leg
		candles = append(candles, Candle{High: c + 0.5, Low: c - 0.5, Close: c})
	}
	for i := 0; i < 15; i++ {
		c := 29 - float64(i)*2 // sharp decline
		candles = append(candles, Candle{High: c + 0.5, Low: c - 0.5, Close: c})
	}

	res := Calculate(candles)
	if !res[19].SuperTrend.Long {
		t.Fatalf("bar19 SuperTrend.Long = false, want long during the uptrend")
	}
	if res[19].SuperTrend.Value >= candles[19].Close {
		t.Fatalf("bar19 SuperTrend.Value = %v, want below close %v", res[19].SuperTrend.Value, candles[19].Close)
	}
	reversed, short := false, false
	for i := 20; i < len(candles); i++ {
		if res[i].SuperTrend.Reversed {
			reversed = true
		}
		if !res[i].SuperTrend.Long {
			short = true
		}
	}
	if !reversed || !short {
		t.Fatalf("decline never flipped SuperTrend to short (reversed=%v short=%v)", reversed, short)
	}
}

func assertNear(t *testing.T, name string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
}
