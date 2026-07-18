package api

import (
	"encoding/json"
	"math"
	"testing"
)

// 茅台 2026-07-16/17 实测 proxy 行(curl 交叉验证):
// row[7]=换手率%  row[8]=成交额万元  振幅需本地算
const (
	fixturePrevClose = 1258.99 // 2026-07-16 close
	fixtureDate      = "2026-07-17"
	fixtureOpen      = "1269.01"
	fixtureClose     = "1253.00"
	fixtureHigh      = "1269.33"
	fixtureLow       = "1238.98"
	fixtureVolHand   = "58417.00"
	fixtureTurnPct   = "0.47"
	fixtureAmtWan    = "732273.27"
)

func TestParseProxyDailyBarCellsAmountTurnoverAmplitude(t *testing.T) {
	cells := []string{
		fixtureDate, fixtureOpen, fixtureClose, fixtureHigh, fixtureLow,
		fixtureVolHand, "", fixtureTurnPct, fixtureAmtWan, "",
	}
	bar, err := ParseProxyDailyBarCells(cells, fixturePrevClose)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	wantAmt := 732273.27 * 10000
	if math.Abs(bar.Amount-wantAmt) > 0.01 {
		t.Fatalf("Amount=%v want %v (万元×10000)", bar.Amount, wantAmt)
	}
	wantTurn := 0.47 / 100
	if math.Abs(bar.Turnover-wantTurn) > 1e-9 {
		t.Fatalf("Turnover=%v want %v", bar.Turnover, wantTurn)
	}
	// (1269.33-1238.98)/1258.99*100 ≈ 2.4107
	wantAmp := (1269.33 - 1238.98) / fixturePrevClose * 100
	if math.Abs(bar.Amplitude-wantAmp) > 1e-6 {
		t.Fatalf("Amplitude=%v want %v", bar.Amplitude, wantAmp)
	}
	wantVol := 58417.0 * 100
	if bar.Volume != wantVol {
		t.Fatalf("Volume=%v want %v", bar.Volume, wantVol)
	}
	// VWAP 应贴近收盘(万元额换算后)
	vwap := bar.Amount / bar.Volume
	if math.Abs(vwap-1253.0)/1253.0 > 0.01 {
		t.Fatalf("VWAP=%v far from close 1253", vwap)
	}
}

// 若误把 row[8] 当元、row[7] 当振幅,本测必须失败——锁死单位换算。
func TestParseProxyDailyBarCellsRejectsWrongUnits(t *testing.T) {
	cells := []string{
		fixtureDate, fixtureOpen, fixtureClose, fixtureHigh, fixtureLow,
		fixtureVolHand, "", fixtureTurnPct, fixtureAmtWan, "",
	}
	bar, err := ParseProxyDailyBarCells(cells, fixturePrevClose)
	if err != nil {
		t.Fatal(err)
	}
	// 错误写法1: 成交额当元 → Amount 小 10000 倍, VWAP≈0.125
	wrongAmt := 732273.27
	if math.Abs(bar.Amount-wrongAmt) < 1 {
		t.Fatalf("Amount looks like 万元未×10000: %v", bar.Amount)
	}
	// 错误写法2: 换手当振幅 → Amplitude 会是 0.47
	if math.Abs(bar.Amplitude-0.47) < 1e-9 {
		t.Fatalf("Amplitude looks like 换手率误用: %v", bar.Amplitude)
	}
	// 正确振幅应在 2~3% 量级(该日实测 qt[43]=2.41)
	if bar.Amplitude < 2 || bar.Amplitude > 3 {
		t.Fatalf("Amplitude=%v out of expected 2~3%% band", bar.Amplitude)
	}
}

func TestParseProxyDailyBarCellsFirstBarUsesOpenAsPrev(t *testing.T) {
	cells := []string{
		"2026-07-17", "100", "105", "110", "95", "1000", "", "1.5", "10.5", "",
	}
	// prevClose=0 → 用 open=100 算振幅: (110-95)/100*100 = 15
	bar, err := ParseProxyDailyBarCells(cells, 0)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(bar.Amplitude-15) > 1e-9 {
		t.Fatalf("Amplitude=%v want 15", bar.Amplitude)
	}
	if bar.Turnover != 0.015 {
		t.Fatalf("Turnover=%v want 0.015", bar.Turnover)
	}
	// amount: 10.5 万元 = 105000 元; vol=1000*100=100000 股
	if bar.Amount != 105000 {
		t.Fatalf("Amount=%v want 105000", bar.Amount)
	}
}

func TestParseProxyDailyBarCellsAmountFallback(t *testing.T) {
	// 无 row[8] 时回退 Close×股数
	cells := []string{"2026-07-17", "10", "12", "13", "9", "100"}
	bar, err := ParseProxyDailyBarCells(cells, 11)
	if err != nil {
		t.Fatal(err)
	}
	// vol=100*100=10000, amount=12*10000=120000
	if bar.Amount != 120000 {
		t.Fatalf("Amount=%v want 120000", bar.Amount)
	}
}

func TestParseProxyDailyBarsFromJSONRaw(t *testing.T) {
	// 模拟 JSON 数组行(含 {} 占位)
	raw := []byte(`[
		["2026-07-16","1252.00","1258.99","1267.97","1245.05","47611.00",{},"0.38","598757.09",""],
		["2026-07-17","1269.01","1253.00","1269.33","1238.98","58417.00",{},"0.47","732273.27",""]
	]`)
	var rows [][]json.RawMessage
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatal(err)
	}
	bars, err := ParseProxyDailyBars(rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(bars) != 2 {
		t.Fatalf("len=%d", len(bars))
	}
	// 第二根振幅相对昨收 1258.99
	wantAmp := (1269.33 - 1238.98) / 1258.99 * 100
	if math.Abs(bars[1].Amplitude-wantAmp) > 1e-6 {
		t.Fatalf("bars[1].Amplitude=%v want %v", bars[1].Amplitude, wantAmp)
	}
	if math.Abs(bars[1].Amount-732273.27*10000) > 0.01 {
		t.Fatalf("bars[1].Amount=%v", bars[1].Amount)
	}
	if math.Abs(bars[1].Turnover-0.0047) > 1e-9 {
		t.Fatalf("bars[1].Turnover=%v", bars[1].Turnover)
	}
}

func TestTurnoverUseful(t *testing.T) {
	if TurnoverUseful(nil) {
		t.Fatal("nil should be false")
	}
	if TurnoverUseful([]float64{0, 0, 0}) {
		t.Fatal("all-zero should be false")
	}
	if !TurnoverUseful([]float64{0, 0.01, 0}) {
		t.Fatal("any positive should be true")
	}
}

func TestStripJSONP(t *testing.T) {
	raw := []byte(`kline_dayqfq={"code":0}`)
	got := StripJSONP(raw)
	if string(got) != `{"code":0}` {
		t.Fatalf("got %q", got)
	}
	// already pure JSON
	pure := []byte(`{"a":1}`)
	if string(StripJSONP(pure)) != `{"a":1}` {
		t.Fatal("pure JSON should pass through")
	}
}
