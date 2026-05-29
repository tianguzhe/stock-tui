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
	assertNear(t, "sample CHOP", last.CHOP, 38.6202, 1e-4)
}

func assertNear(t *testing.T, name string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
}
