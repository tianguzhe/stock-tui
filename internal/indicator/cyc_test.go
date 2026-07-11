package indicator

import "testing"

// ---------------------------------------------------------------------------
// CalcCYC — 成本均线基本计算
// ---------------------------------------------------------------------------

// TestCalcCYC_Simple 3 日简单数据: 每日成交额=收盘价×100,成交量=100
// 所以 VWAP=收盘价 → CYC5/CYC13 应等于同周期 MA(但这里窗口不满也正常计算)
func TestCalcCYC_Simple(t *testing.T) {
	candles := []Candle{
		{Close: 10, Volume: 100, Amount: 1000},
		{Close: 12, Volume: 100, Amount: 1200},
		{Close: 11, Volume: 100, Amount: 1100},
		{Close: 13, Volume: 100, Amount: 1300},
		{Close: 14, Volume: 100, Amount: 1400},
	}
	results := CalcCYC(candles)
	if len(results) != 5 {
		t.Fatalf("len=%d, want 5", len(results))
	}

	// 第3日 i=3,往前只有4个有效日(满5日不够):
	// (1200+1100+1300)/(100*3) = 3600/300 = 12? 不对,从 i=3 往前找有效日。
	// 有效日: j=3,2,1,0 共4个 → (1000+1200+1100+1300)/400 = 4600/400 = 11.5
	assertNear(t, "cyc5[3]", results[3].CYC5, 11.5, 1e-9)

	// 第4日 CYC5 = (1000+1200+1100+1300+1400)/500 = 6000/500 = 12.0
	assertNear(t, "cyc5[4]", results[4].CYC5, 12.0, 1e-9)

	// CYC∞ = 全部成交额/全部成交量
	inf := (1000 + 1200 + 1100 + 1300 + 1400) / 500.0
	assertNear(t, "cycinf[4]", results[4].CYCInf, inf, 1e-9)
}

// TestCalcCYC_VWAPvsMA 验证 CYC 不是简单 MA:
// 成交量不均匀时 CYC 向放量日偏移
func TestCalcCYC_VWAPvsMA(t *testing.T) {
	candles := []Candle{
		{Close: 10, Volume: 10, Amount: 100},     // 均价=10
		{Close: 20, Volume: 1000, Amount: 20000}, // 均价=20, 放量
		{Close: 15, Volume: 10, Amount: 150},     // 均价=15
	}
	results := CalcCYC(candles)
	if len(results) != 3 {
		t.Fatalf("len=%d, want 3", len(results))
	}

	// CYC3 (窗口=3) = (100+20000+150)/(10+1000+10) = 20250/1020 ≈ 19.85
	// 应接近 20(放量日主导),而非 MA3=(10+20+15)/3=15
	cyc3 := results[2].CYC5 // 5日窗口但只有3个有效日
	if cyc3 < 19 {
		t.Fatalf("cyc5[2]=%v, want ~19.85 (volume-weighted toward 20)", cyc3)
	}
}

// TestCalcCYC_ZeroVolume 零成交量日被跳过,不参与加权
func TestCalcCYC_ZeroVolume(t *testing.T) {
	candles := []Candle{
		{Close: 10, Volume: 100, Amount: 1000},
		{Close: 12, Volume: 0, Amount: 0}, // 无成交量,跳过
		{Close: 14, Volume: 100, Amount: 1400},
	}
	results := CalcCYC(candles)
	// CYC5[2] = (1000+1400)/(100+100) = 12.0 (跳过中间日)
	assertNear(t, "cyc5[2]", results[2].CYC5, 12.0, 1e-9)

	// CYC∞[2] = (1000+1400)/(100+100) = 12.0
	assertNear(t, "cycinf[2]", results[2].CYCInf, 12.0, 1e-9)
}

// TestCalcCYC_AllZeroVolume 全部无成交量→回退收盘价
func TestCalcCYC_AllZeroVolume(t *testing.T) {
	candles := []Candle{
		{Close: 10, Volume: 0, Amount: 0},
		{Close: 12, Volume: 0, Amount: 0},
	}
	results := CalcCYC(candles)
	assertNear(t, "cyc5[0]", results[0].CYC5, 10, 1e-9)
	assertNear(t, "cyc5[1]", results[1].CYC5, 12, 1e-9)
	assertNear(t, "cycinf[1]", results[1].CYCInf, 12, 1e-9)
}

// TestCalcCYC_SingleCandle 单根K线: 全部退化为该日数据
func TestCalcCYC_SingleCandle(t *testing.T) {
	candles := []Candle{{Close: 15, Volume: 50, Amount: 750}}
	results := CalcCYC(candles)
	assertNear(t, "cyc5", results[0].CYC5, 15, 1e-9)
	assertNear(t, "cyc13", results[0].CYC13, 15, 1e-9)
	assertNear(t, "cyc34", results[0].CYC34, 15, 1e-9)
	assertNear(t, "cycinf", results[0].CYCInf, 15, 1e-9)
}

// TestCalcCYC_Empty 空输入不 panic
func TestCalcCYC_Empty(t *testing.T) {
	results := CalcCYC(nil)
	if len(results) != 0 {
		t.Fatalf("len=%d, want 0", len(results))
	}
	results = CalcCYC([]Candle{})
	if len(results) != 0 {
		t.Fatalf("len=%d, want 0", len(results))
	}
}

// TestCYC34_FullWindow 34日窗口满时验证与手动计算结果一致
func TestCYC34_FullWindow(t *testing.T) {
	n := 40
	candles := make([]Candle, n)
	for i := 0; i < n; i++ {
		price := float64(10 + i)   // 10..49
		vol := float64(100 + i*10) // 100..490
		candles[i] = Candle{Close: price, Volume: vol, Amount: price * vol}
	}
	results := CalcCYC(candles)

	// 验证第39(0-index)日的 CYC34
	// 后34个有效日: i=6..39
	sumAmt := 0.0
	sumVol := 0.0
	for i := 6; i <= 39; i++ {
		sumAmt += candles[i].Amount
		sumVol += candles[i].Volume
	}
	want := sumAmt / sumVol
	assertNear(t, "cyc34[39]", results[39].CYC34, want, 1e-9)
}
