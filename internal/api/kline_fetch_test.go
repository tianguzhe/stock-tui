package api

import (
	"math"
	"testing"
)

func TestCodeToSecid(t *testing.T) {
	cases := []struct {
		code string
		want string
		err  bool
	}{
		{"sh600522", "1.600522", false},
		{"sz000001", "0.000001", false},
		{"bj920819", "0.920819", false},
		{"usAAPL", "", true},
		{"s", "", true},
	}
	for _, tc := range cases {
		got, err := CodeToSecid(tc.code)
		if tc.err {
			if err == nil {
				t.Errorf("CodeToSecid(%q) = %q, nil; want error", tc.code, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("CodeToSecid(%q) error: %v", tc.code, err)
			continue
		}
		if got != tc.want {
			t.Errorf("CodeToSecid(%q) = %q, want %q", tc.code, got, tc.want)
		}
	}
}

func TestCodeToTDXMarket(t *testing.T) {
	cases := []struct {
		code    string
		wantRaw string
		err     bool
	}{
		{"sh600522", "600522", false},
		{"sz000001", "000001", false},
		{"bj920819", "920819", false},
		{"usAAPL", "", true},
	}
	for _, tc := range cases {
		_, raw, err := CodeToTDXMarket(tc.code)
		if tc.err {
			if err == nil {
				t.Errorf("CodeToTDXMarket(%q) want error", tc.code)
			}
			continue
		}
		if err != nil {
			t.Errorf("CodeToTDXMarket(%q) error: %v", tc.code, err)
			continue
		}
		if raw != tc.wantRaw {
			t.Errorf("CodeToTDXMarket(%q) raw = %q, want %q", tc.code, raw, tc.wantRaw)
		}
	}
}

func TestParseEMFloat(t *testing.T) {
	if got := parseEMFloat("123.45"); got != 123.45 {
		t.Errorf("parseEMFloat(%q) = %v, want 123.45", "123.45", got)
	}
	if got := parseEMFloat("0.47"); got != 0.47 {
		t.Errorf("parseEMFloat(%q) = %v, want 0.47", "0.47", got)
	}
	if got := parseEMFloat(""); got != 0 {
		t.Errorf("parseEMFloat(%q) = %v, want 0", "", got)
	}
	if got := parseEMFloat("abc"); got != 0 {
		t.Errorf("parseEMFloat(%q) = %v, want 0", "abc", got)
	}
}

// 茅台 2026-07-15..17 的真实东财 kline 行(11 列, fields2=f51..f61, curl 交叉验证)。
// 锁定"东财 kline 每行 11 列"假设: 若门槛误设回 <12 会让 len==0, 本测失败(P0 回归)。
func TestParseEMKlines(t *testing.T) {
	klines := []string{
		"2026-07-15,1203.66,1251.06,1256.60,1198.66,71944,8922861367.00,4.77,2.98,36.18,0.58",
		"2026-07-16,1252.00,1258.99,1267.97,1245.05,47611,5987570858.00,1.83,0.63,7.93,0.38",
		"2026-07-17,1269.01,1253.00,1269.33,1238.98,58417,7322732709.00,2.41,-0.48,-5.99,0.47",
	}
	dates, candles, turnovers, amplitudes := parseEMKlines(klines)
	if len(candles) != 3 {
		t.Fatalf("len(candles)=%d, want 3 (11 列不应被跳过——P0 回归)", len(candles))
	}
	if dates[2] != "2026-07-17" {
		t.Errorf("dates[2]=%q, want 2026-07-17", dates[2])
	}
	if candles[2].Close != 1253.00 {
		t.Errorf("Close=%v, want 1253.00", candles[2].Close)
	}
	if candles[2].Volume != 58417*100 { // 手→股
		t.Errorf("Volume=%v, want %v", candles[2].Volume, 58417*100)
	}
	if candles[2].Amount != 7322732709.00 { // 元, 直给
		t.Errorf("Amount=%v, want 7322732709", candles[2].Amount)
	}
	if math.Abs(amplitudes[2]-2.41) > 1e-9 {
		t.Errorf("Amplitude=%v, want 2.41", amplitudes[2])
	}
	if math.Abs(turnovers[2]-0.0047) > 1e-9 { // 0.47%→小数
		t.Errorf("Turnover=%v, want 0.0047", turnovers[2])
	}
	// VWAP 贴近收盘 → 验证 volume 手→股 与 amount 元 口径自洽(该文档标 volume=股 有误)
	vwap := candles[2].Amount / candles[2].Volume
	if math.Abs(vwap-1253.0)/1253.0 > 0.02 {
		t.Errorf("VWAP=%v far from close 1253 (volume/amount 单位错配?)", vwap)
	}
}

// 短行(<11 列)与 OHLC 非正值的坏行必须跳过, 不产生 0 价 K 线污染指标(P1)。
func TestParseEMKlinesSkipsBadRows(t *testing.T) {
	klines := []string{
		"2026-07-15,1203.66,1251.06,1256.60,1198.66,71944,8922861367.00,4.77,2.98,36.18,0.58", // 好
		"2026-07-16,1252.00,0,1267.97,1245.05,47611,5987570858.00,1.83,0.63,7.93,0.38",        // close=0 坏
		"2026-07-17,1269.01,1253.00,1269.33,1238.98,58417",                                    // 6 列 短
	}
	_, candles, _, _ := parseEMKlines(klines)
	if len(candles) != 1 {
		t.Fatalf("len(candles)=%d, want 1 (坏行/短行应跳过)", len(candles))
	}
	if candles[0].Close != 1251.06 {
		t.Errorf("survived candle Close=%v, want 1251.06", candles[0].Close)
	}
}

// 换手率匹配率低于阈值返回 nil(触发 TDX 兜底), 达标时按日期对齐(P2)。
func TestAlignEMTurnovers(t *testing.T) {
	klines := []string{"2026-07-15,0.58", "2026-07-16,0.38", "2026-07-17,0.47"}

	turns := alignEMTurnovers(klines, []string{"2026-07-15", "2026-07-16", "2026-07-17"})
	if turns == nil {
		t.Fatal("full match should not return nil")
	}
	if math.Abs(turns[2]-0.0047) > 1e-9 {
		t.Errorf("turns[2]=%v, want 0.0047", turns[2])
	}

	// 匹配率 3/5=60% < 80% → nil
	if got := alignEMTurnovers(klines, []string{"2026-07-15", "2026-07-16", "2026-07-17", "2026-07-18", "2026-07-19"}); got != nil {
		t.Errorf("low match rate should return nil, got %v", got)
	}

	// 空 dates → nil
	if got := alignEMTurnovers(klines, nil); got != nil {
		t.Errorf("empty dates should return nil, got %v", got)
	}
}
