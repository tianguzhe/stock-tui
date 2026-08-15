package snapshot

import (
	"fmt"
	"testing"

	"stock-tui/internal/api"
	"stock-tui/internal/indicator"
)

func syntheticKline(n int) api.KlineData {
	candles := make([]indicator.Candle, n)
	dates := make([]string, n)
	turnovers := make([]float64, n)
	amplitudes := make([]float64, n)
	price := 10.0
	for i := 0; i < n; i++ {
		switch {
		case i%50 < 30:
			price *= 1.006
		case i%2 == 0:
			price *= 1.003
		default:
			price *= 0.994
		}
		open := price * 0.998
		high := price * 1.015
		low := price * 0.985
		vol := 1_000_000.0 + float64(i%7)*50_000
		candles[i] = indicator.Candle{
			Open: open, High: high, Low: low, Close: price,
			Volume: vol, Amount: vol * price,
		}
		dates[i] = fmt.Sprintf("2025-%02d-%02d", (i%12)+1, (i%28)+1)
		turnovers[i] = 0.01 + float64(i%5)*0.002
		amplitudes[i] = (high - low) / low * 100
	}
	return api.KlineData{
		Code: "sh600000", Name: "浦发银行",
		Dates: dates, Candles: candles,
		Turnovers: turnovers, Amplitudes: amplitudes,
		InsideVol: 12345, OutsideVol: 54321,
	}
}

func TestBuildEmptyKlineReturnsCodeOnly(t *testing.T) {
	b := Build(api.KlineData{Code: "sh600000"})
	if b.N != 0 {
		t.Fatalf("N = %d, want 0", b.N)
	}
	if b.Snap.Code != "sh600000" || b.Snap.TradeDate != "" {
		t.Fatalf("Snap = %+v, want only Code set", b.Snap)
	}
}

func TestBuildPopulatesSnapshotFromLastBar(t *testing.T) {
	data := syntheticKline(260)
	b := Build(data)

	if b.N != len(data.Candles) {
		t.Fatalf("N = %d, want %d", b.N, len(data.Candles))
	}
	last := data.Candles[len(data.Candles)-1]
	if b.Snap.TradeDate != data.Dates[len(data.Dates)-1] {
		t.Fatalf("Snap.TradeDate = %q, want %q", b.Snap.TradeDate, data.Dates[len(data.Dates)-1])
	}
	if b.Snap.Close != last.Close || b.Snap.Low != last.Low || b.Snap.High != last.High {
		t.Fatalf("Snap OHLC = (close=%.3f low=%.3f high=%.3f), want last bar (%.3f/%.3f/%.3f)",
			b.Snap.Close, b.Snap.Low, b.Snap.High, last.Close, last.Low, last.High)
	}
	if b.Snap.InsideVol != data.InsideVol || b.Snap.OutsideVol != data.OutsideVol {
		t.Fatalf("Snap in/out vol = (%.0f/%.0f), want (%.0f/%.0f)",
			b.Snap.InsideVol, b.Snap.OutsideVol, data.InsideVol, data.OutsideVol)
	}
	wantAmp := data.Amplitudes[len(data.Amplitudes)-1]
	if b.Snap.Amplitude != wantAmp {
		t.Fatalf("Snap.Amplitude = %.4f, want %.4f", b.Snap.Amplitude, wantAmp)
	}
	// Score/ScoreAdj must land within the documented 0..100 clamp.
	if b.Snap.ScoreTotal < 0 || b.Snap.ScoreTotal > 100 || b.Snap.ScoreAdj < 0 || b.Snap.ScoreAdj > 100 {
		t.Fatalf("score out of [0,100]: total=%d adj=%d", b.Snap.ScoreTotal, b.Snap.ScoreAdj)
	}
	// Intermediate fields the CLI report prints must be populated too, not
	// just the persisted Snap — this is the whole point of sharing Build
	// between the printing and the non-printing caller.
	if len(b.Results) != b.N || len(b.TDs) != b.N || len(b.Volumes) != b.N {
		t.Fatalf("intermediate series length mismatch: results=%d tds=%d volumes=%d, want %d",
			len(b.Results), len(b.TDs), len(b.Volumes), b.N)
	}
}

func TestBuildAmplitudeLeftZeroWhenLengthMismatch(t *testing.T) {
	data := syntheticKline(150)
	data.Amplitudes = data.Amplitudes[:100] // shorter than Candles
	b := Build(data)
	if b.Snap.Amplitude != 0 {
		t.Fatalf("Snap.Amplitude = %.4f, want 0 when Amplitudes shorter than Candles", b.Snap.Amplitude)
	}
}
