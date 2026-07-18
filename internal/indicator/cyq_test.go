package indicator

import (
	"testing"
)

// ---------------------------------------------------------------------------
// costWeights — 持仓成本衰减模型
// ---------------------------------------------------------------------------

// TestCostWeightsTwoDay 两日换手率 0.5/0.5: 归一化后最新日权重约 0.6667,旧日 0.3333,
// WeightSum 在归一化前为 0.75(表示有 0.25 的 oldest 被衰减到窗口外)。
func TestCostWeightsTwoDay(t *testing.T) {
	w := costWeights([]float64{0.5, 0.5})
	if len(w) != 2 {
		t.Fatalf("len=%d, want 2", len(w))
	}
	assertNear(t, "w[0](old)", w[0], 1.0/3, 1e-4)
	assertNear(t, "w[1](new)", w[1], 2.0/3, 1e-4)
}

// TestCostWeightsZeroTurnover 全部换手率为 0: 权重全为 0(归一化退化)。
func TestCostWeightsZeroTurnover(t *testing.T) {
	w := costWeights([]float64{0, 0, 0})
	for i, v := range w {
		if v != 0 {
			t.Fatalf("w[%d]=%v, want 0", i, v)
		}
	}
}

// TestCostWeightsFullTurnover 最近一日换手 100%: 全部筹码集中于当日,剩余为 0。
func TestCostWeightsFullTurnover(t *testing.T) {
	w := costWeights([]float64{0.1, 0, 1.0})
	// i=2: t=1.0, w[2]=1.0*1=1.0, remaining=0
	// i=1: t=0,   w[1]=0, remaining=0
	// i=0: t=0.1, w[0]=0.1*0=0, remaining=0
	// sum=1.0 → 归一化不变
	assertNear(t, "w[2]", w[2], 1.0, 1e-9)
	assertNear(t, "w[1]", w[1], 0, 1e-9)
	assertNear(t, "w[0]", w[0], 0, 1e-9)
}

// TestCostWeightsClamp 换手率 >1 或 <0 被 clamp 到 [0,1],不 panic。
func TestCostWeightsClamp(t *testing.T) {
	// 负值视为 0,>1 视为 1
	w := costWeights([]float64{-0.5, 1.5, 0.5})
	if len(w) != 3 {
		t.Fatalf("len=%d, want 3", len(w))
	}
	sum := 0.0
	for _, v := range w {
		sum += v
	}
	assertNear(t, "sum", sum, 1.0, 1e-9)
}

// ---------------------------------------------------------------------------
// winnerPrice — 获利盘比例计算
// ---------------------------------------------------------------------------

// TestWinnerPricePartial 两日成本[10,30],权重[0.3333,0.6667]。
// target=20 → 仅成本 10<=20 → 0.3333 (部分获利)。
func TestWinnerPricePartial(t *testing.T) {
	avg := []float64{10, 30}
	w := []float64{1.0 / 3, 2.0 / 3}
	assertNear(t, "WINNER(20)", winnerPrice(20, avg, w), 1.0/3, 1e-4)
}

// TestWinnerPriceAllProfit target>=最大成本 → 全获利 → 1.0。
func TestWinnerPriceAllProfit(t *testing.T) {
	avg := []float64{10, 30}
	w := []float64{0.3, 0.7}
	assertNear(t, "WINNER(30)", winnerPrice(30, avg, w), 1.0, 1e-9)
}

// TestWinnerPriceAllLoss target<最小成本 → 全亏损 → 0。
func TestWinnerPriceAllLoss(t *testing.T) {
	avg := []float64{10, 30}
	w := []float64{0.3, 0.7}
	assertNear(t, "WINNER(5)", winnerPrice(5, avg, w), 0, 1e-9)
}

// TestWinnerPriceExactBoundary target 精确等于某成本价(<= 成立)。
func TestWinnerPriceExactBoundary(t *testing.T) {
	avg := []float64{10, 30}
	w := []float64{0.3, 0.7}
	assertNear(t, "WINNER(10)", winnerPrice(10, avg, w), 0.3, 1e-9)
}

// TestWinnerPriceClamp 返回值 clamp 到 [0,1](虽然正常不可能越界)。
func TestWinnerPriceClamp(t *testing.T) {
	// 权重和为 1,正负不会越界; 但传递 >1 的 WeightSum 场景由 CalcCYQ 内部保证
	// 此测试验证 winnerPrice 总有界
	for _, target := range []float64{-1, 0, 100} {
		v := winnerPrice(target, []float64{10, 20}, []float64{0.5, 0.5})
		if v < 0 || v > 1 {
			t.Fatalf("winnerPrice(%v)=%v, want [0,1]", target, v)
		}
	}
}

// ---------------------------------------------------------------------------
// CalcCYQ 集成 — 多日场景
// ---------------------------------------------------------------------------

// TestCalcCYQBasic 两日序列: 旧日[10,50]低位成本,新日[30,70]高位成本。
// WINNER 含义: 在 target 价格卖出,有多少比例筹码获利。
// 预期: close=20 → 仅旧日获利 → 1/3; close=60 → 两日都获利 → 1.0

// TestCalcCYQWithOpen 修复上例 Open 字段,给真实开盘价验证 CYQK。
func TestCalcCYQWithOpen(t *testing.T) {
	candles := []Candle{
		{Open: 20, High: 50, Low: 10, Close: 10, Volume: 100, Amount: 1000},
		{Open: 40, High: 70, Low: 30, Close: 50, Volume: 100, Amount: 3000},
	}
	// 两日换手 0.5/0.5 → 权重[0.3333, 0.6667]; avgPrices=[10,30]
	// close=50 → WINNER(50) 成本 10<=50 + 成本 30<=50 → 1.0
	// open=40 → WINNER(40) 成本 10<=40 + 成本 30<=40 → 1.0
	// high=70 → WINNER(70) → 1.0
	// low=30 → WINNER(30) 成本 10<=30 + 成本 30<=30 → 1.0
	// CYQK_Length=(1.0 - 1.0)*100 = 0
	// 这个场景所有价格都在两个成本之上,全获利。
	// 要制造 WINNER 非 1 的场景必须让 target < 大成本。
	cyq := CalcCYQ(candles, []float64{0.5, 0.5})
	last := cyq[1]
	assertNear(t, "WinnerClose(50)", last.WinnerClose, 1.0, 1e-4)
	assertNear(t, "WinnerOpen(40)", last.WinnerOpen, 1.0, 1e-4)
	assertNear(t, "WinnerHigh(70)", last.WinnerHigh, 1.0, 1e-4)
	assertNear(t, "WinnerLow(30)", last.WinnerLow, 1.0, 1e-4)
	// CYQK Length = (1.0-1.0)*100 = 0
	assertNear(t, "CYQK_Length", last.CYQK_Length, 0, 1e-9)
	assertNear(t, "WeightSum", last.WeightSum, 1.0, 1e-9)
}

// TestCalcCYQPartialProfit 两日成本[10, 30],当前 close=20 介于中间,
// 验证部分获利盘的精确值。
func TestCalcCYQPartialProfit(t *testing.T) {
	candles := []Candle{
		{Open: 15, High: 15, Low: 5, Close: 10, Volume: 100, Amount: 1000},  // avgPrice=10
		{Open: 35, High: 35, Low: 25, Close: 20, Volume: 100, Amount: 3000}, // avgPrice=30
	}
	cyq := CalcCYQ(candles, []float64{0.5, 0.5})
	last := cyq[1]

	// avgPrices=[10,30], w=[0.3333, 0.6667]
	// WinnerClose(20): 成本10<=20 → 0.3333; 成本30>20 → 不包含 → 0.3333
	assertNear(t, "WinnerClose(20)", last.WinnerClose, 1.0/3, 1e-4)
	// WinnerOpen(35): 10<=35 + 30<=35 → 1.0
	assertNear(t, "WinnerOpen(35)", last.WinnerOpen, 1.0, 1e-4)
	// WinnerLow(25): 10<=25 + 30<=25 → 1.0
	assertNear(t, "WinnerLow(25)", last.WinnerLow, 1.0/3, 1e-4)
	// WinnerHigh(35): 同 open → 1.0
	assertNear(t, "WinnerHigh(35)", last.WinnerHigh, 1.0, 1e-4)

	// CYQK: WINNER*100
	assertNear(t, "CYQK_Close", last.CYQK_Close, 100.0/3, 1e-1)
	assertNear(t, "CYQK_Open", last.CYQK_Open, 100.0, 1e-1)
	// Length = (1/3 - 1)*100 = -66.6667
	assertNear(t, "CYQK_Length", last.CYQK_Length, -200.0/3, 1e-1)
}

// TestCalcCYQVolumeZeroFallback Volume=0 时回退到 (H+L)/2,不应除零 panic。
func TestCalcCYQVolumeZeroFallback(t *testing.T) {
	candles := []Candle{
		{Open: 10, High: 12, Low: 8, Close: 10, Volume: 0, Amount: 0},  // avgPrice=(12+8)/2=10
		{Open: 30, High: 32, Low: 28, Close: 20, Volume: 0, Amount: 0}, // avgPrice=(32+28)/2=30
	}
	// 不应 panic,且结果合理
	cyq := CalcCYQ(candles, []float64{0.5, 0.5})
	if len(cyq) != 2 {
		t.Fatalf("len=%d, want 2", len(cyq))
	}
	last := cyq[1]
	// avgPrice=30 (第二日), close=20 → 在第一日的 avgPrice=10<=20 → 权重 1/3
	assertNear(t, "WinnerClose Volume=0", last.WinnerClose, 1.0/3, 1e-4)
}

// TestCalcCYQEmptyInput 空输入返回空切片,不 panic。
func TestCalcCYQEmptyInput(t *testing.T) {
	cyq := CalcCYQ(nil, nil)
	if len(cyq) != 0 {
		t.Fatalf("len=%d, want 0", len(cyq))
	}
	cyq = CalcCYQ([]Candle{}, []float64{})
	if len(cyq) != 0 {
		t.Fatalf("len=%d, want 0", len(cyq))
	}
}

// TestCalcCYQSingleDay 单日: 权重=1.0(归一化不变)。
func TestCalcCYQSingleDay(t *testing.T) {
	candles := []Candle{
		{Open: 10, High: 15, Low: 5, Close: 12, Volume: 100, Amount: 1000},
	}
	cyq := CalcCYQ(candles, []float64{0.5})
	if len(cyq) != 1 {
		t.Fatalf("len=%d, want 1", len(cyq))
	}
	r := cyq[0]
	// avgPrice=10, close=12 → 成本<=12 → 1.0
	assertNear(t, "WinnerClose singe-day", r.WinnerClose, 1.0, 1e-9)
	// ASR ±10%:[10.8,13.2]; 成本 10<=13.2 → wUp=1, 成本 10<=10.8 → wDn=1 → (1-1)*100=0
	assertNear(t, "ASR single-day", r.ASR, 0, 1e-9)
	// WeightSum = sum before normalize = 0.5 (1*0.5)
	assertNear(t, "WeightSum", r.WeightSum, 1.0, 1e-9)
}

// ---------------------------------------------------------------------------
// ASR 活动筹码
// ---------------------------------------------------------------------------

// TestCalcCYQASR 测试 ASR 在筹码密集区的值。
func TestCalcCYQASR(t *testing.T) {
	candles := []Candle{
		{Open: 9, High: 11, Low: 9, Close: 10, Volume: 100, Amount: 1000},   // avgPrice=10
		{Open: 11, High: 13, Low: 11, Close: 20, Volume: 100, Amount: 2000}, // avgPrice=20
	}
	// 换手 0.5/0.5 → w=[0.3333, 0.6667], avgPrices=[10, 20]
	// close=20 → ±10% = [18, 22]
	// wUp=WINNER(22): 成本10<=22+成本20<=22 = 1.0
	// wDn=WINNER(18): 成本10<=18, 成本20>18 → 0.3333
	// ASR=(1.0-0.3333)*100 = 66.67
	cyq := CalcCYQ(candles, []float64{0.5, 0.5})
	assertNear(t, "ASR", cyq[1].ASR, 66.67, 1e-1)
}

// ---------------------------------------------------------------------------
// PRY1 近一年相对位置
// ---------------------------------------------------------------------------

// TestCalcPRY1 5日序列[8,12,10,14,11]: 低=8,高=14,close=11 → (11-8)/(14-8)*100 = 50。
func TestCalcPRY1(t *testing.T) {
	candles := []Candle{
		{High: 9, Low: 8, Close: 8},
		{High: 13, Low: 11, Close: 12},
		{High: 11, Low: 9, Close: 10},
		{High: 14, Low: 13, Close: 14},
		{High: 12, Low: 10, Close: 11},
	}
	v := calcPRY1(candles, 4)
	assertNear(t, "PRY1", v, 50, 1e-9)
}

// TestCalcPRY1SpanZero 全部同价: 低=高 → 返回 50。
func TestCalcPRY1SpanZero(t *testing.T) {
	candles := []Candle{
		{High: 10, Low: 10, Close: 10},
		{High: 10, Low: 10, Close: 10},
	}
	v := calcPRY1(candles, 1)
	assertNear(t, "PRY1 flat", v, 50, 1e-9)
}

// TestCalcPRY1NegativeIndex i<0 → 返回 50。
func TestCalcPRY1NegativeIndex(t *testing.T) {
	v := calcPRY1([]Candle{{Close: 10}}, -1)
	assertNear(t, "PRY1 negative idx", v, 50, 1e-9)
}

// TestCalcPRY1Extremes 在窗口最低 close=2 → 0; 最高 close=20 → 100。
func TestCalcPRY1Extremes(t *testing.T) {
	candles := []Candle{
		{High: 20, Low: 2, Close: 2}, // 近一年最低
		{High: 20, Low: 2, Close: 10},
		{High: 20, Low: 2, Close: 20}, // 近一年最高
	}
	assertNear(t, "PRY1=0", calcPRY1(candles, 0), 0, 1e-9)
	assertNear(t, "PRY1=100", calcPRY1(candles, 2), 100, 1e-9)
}

// ---------------------------------------------------------------------------
// 高控盘信号
// ---------------------------------------------------------------------------

// TestCYQVolumeLessBigKline 无量长阳: CYQK_Length>18%且换手<3%。
// 构造两日: 成本[5,50], close=55(全获利), open=10(仅部分获利) → CYQK_Length>50%
func TestCYQVolumeLessBigKline(t *testing.T) {
	candles := []Candle{
		{Open: 1, High: 8, Low: 1, Close: 5, Volume: 100, Amount: 500},      // avgPrice=5
		{Open: 10, High: 60, Low: 10, Close: 55, Volume: 100, Amount: 5000}, // avgPrice=50
	}
	// turns=[0.01, 0.01] → w=[~0.5, ~0.5]
	// 精确算: costWeights([0.01,0.01]):
	//   i=1: t=0.01, w[1]=0.01*1=0.01, remaining=0.99
	//   i=0: t=0.01, w[0]=0.01*0.99=0.0099, remaining=0.9801
	//   sum=0.0199, 归一化: w[0]=0.0099/0.0199=0.4975, w[1]=0.01/0.0199=0.5025
	// avgPrices=[5, 50]
	// 第三日(索引 2)close=55, WinnerClose(55): 5<=55(0.4975), 50<=55(0.5025) → 1.0
	// 第三日open=10, WinnerOpen(10): 5<=10(0.4975), 50>10(不) → 0.4975
	// CYQK_Length=(1.0-0.4975)*100=50.25 > 18 ✅
	// turnPct = 0.01*100 = 1% < 3% ✅

	// 要三根 K 线,因为 candles 的前 N-1 根也参与计算,但只有最后根有意义。
	// cycle: cyq[0] 混入未满历史,cyq[1] 同理。直接用 cyq[len-1]。
	cyq := CalcCYQ(candles, []float64{0.01, 0.01})
	last := cyq[len(cyq)-1]
	if !last.VolumeLessBigKline {
		t.Fatalf("VolumeLessBigKline=false, want true (Length=%.2f > 18, turn=1%%)", last.CYQK_Length)
	}
}

// TestCYQRatio90v3 90 比 3: WinnerClose>90% 且换手<3%。
// 构造两日: avgPrice=[3,20], close=95 → 全获利超过 0.90
func TestCYQRatio90v3(t *testing.T) {
	candles := []Candle{
		{Open: 2, High: 5, Low: 2, Close: 5, Volume: 100, Amount: 300},     // avgPrice=3
		{Open: 2, High: 100, Low: 2, Close: 95, Volume: 100, Amount: 2000}, // avgPrice=20, 换手小
	}
	// turns=[0.01, 0.01], costWeights:
	//   i=1: t=0.01, w[1]=0.01*1=0.01, remaining=0.99
	//   i=0: t=0.01, w[0]=0.01*0.99=0.0099, sum=0.0199
	//   normalized: w[0]=0.4975, w[1]=0.5025
	// avgPrices=[3, 20]
	// last WinnerClose(95): 3<=95(0.4975) + 20<=95(0.5025) → 1.0 > 0.90 ✅
	// turnPct = 0.01*100 = 1% < 3% ✅
	cyq := CalcCYQ(candles, []float64{0.01, 0.01})
	last := cyq[len(cyq)-1]
	if !last.Ratio90v3 {
		t.Fatalf("Ratio90v3=false, want true (WinnerClose=%.2f > 0.90, turn=1%%)", last.WinnerClose)
	}
}

// TestCYQIsLowPosition PRY1<40 → 低位判定。
// 窗口内收盘价位于下半区即可。
func TestCYQIsLowPosition(t *testing.T) {
	candles := []Candle{
		{High: 100, Low: 0, Close: 10, Volume: 100, Amount: 500},
		{High: 100, Low: 0, Close: 15, Volume: 100, Amount: 1000},
	}
	// 先验证 PRY1 真的 < 40
	v := calcPRY1(candles, 1) // close=15, low=0, high=100 → (15-0)/(100-0)*100=15<40 ✅
	if v >= 40 {
		t.Fatalf("PRY1=%v, want < 40", v)
	}
	turns := []float64{0.5, 0.5}
	cyq := CalcCYQ(candles, turns)
	if !cyq[1].IsLowPosition {
		t.Fatalf("IsLowPosition=false, want true (PRY1=%.1f < 40)", v)
	}
}

// ---------------------------------------------------------------------------
// 辅助函数
// ---------------------------------------------------------------------------

func TestMinInt(t *testing.T) {
	if got := minInt(3, 5); got != 3 {
		t.Fatalf("minInt(3,5)=%d, want 3", got)
	}
	if got := minInt(7, 2); got != 2 {
		t.Fatalf("minInt(7,2)=%d, want 2", got)
	}
	if got := minInt(4, 4); got != 4 {
		t.Fatalf("minInt(4,4)=%d, want 4", got)
	}
}

func TestClampF(t *testing.T) {
	assertNear(t, "clamp lo", clampF(-1, 0, 100), 0, 1e-9)
	assertNear(t, "clamp hi", clampF(200, 0, 100), 100, 1e-9)
	assertNear(t, "clamp ok", clampF(50, 0, 100), 50, 1e-9)
	assertNear(t, "clamp edge lo", clampF(0, 0, 100), 0, 1e-9)
	assertNear(t, "clamp edge hi", clampF(100, 0, 100), 100, 1e-9)
}

// ---------------------------------------------------------------------------
// 空 / 退化输入
// ---------------------------------------------------------------------------

// TestCalcCYQAllZeroTurnover 换手率全 0 → 权重全 0 → WINNER=0。
func TestCalcCYQAllZeroTurnover(t *testing.T) {
	candles := []Candle{
		{Open: 10, High: 20, Low: 5, Close: 15, Volume: 100, Amount: 1000},
		{Open: 20, High: 30, Low: 15, Close: 25, Volume: 100, Amount: 2000},
	}
	cyq := CalcCYQ(candles, []float64{0, 0})
	assertNear(t, "WinnerClose zero turn", cyq[1].WinnerClose, 0, 1e-9)
	assertNear(t, "WeightSum zero turn", cyq[1].WeightSum, 0, 1e-9)
}

// TestCalcCYQLengthMismatch candles 比 turnovers 长 → 截断到 min
func TestCalcCYQLengthMismatch(t *testing.T) {
	candles := []Candle{
		{Open: 10, High: 15, Low: 5, Close: 12, Volume: 100, Amount: 1000},
		{Open: 20, High: 25, Low: 15, Close: 22, Volume: 100, Amount: 2000},
	}
	// turnovers 更短
	cyq := CalcCYQ(candles, []float64{0.5})
	if len(cyq) != 1 {
		t.Fatalf("len=%d, want 1 (min of len(candles)=2, len(turns)=1)", len(cyq))
	}
}

// TestCalcCYQAmountZeroVolumeNonZero Volume>0 但 Amount=0 → 回退 (H+L)/2
func TestCalcCYQAmountZeroVolumeNonZero(t *testing.T) {
	candles := []Candle{
		{Open: 10, High: 10, Low: 10, Close: 10, Volume: 100, Amount: 0}, // Amount=0 → 走 (H+L)/2=10
		{Open: 20, High: 20, Low: 20, Close: 20, Volume: 100, Amount: 0}, // Amount=0 → avgPrice=20
	}
	cyq := CalcCYQ(candles, []float64{0.5, 0.5})
	last := cyq[1]
	// avgPrices=[10, 20], w=[0.3333, 0.6667]
	// close=20 → WINNER=1.0
	assertNear(t, "WinnerClose Amount=0", last.WinnerClose, 1.0, 1e-4)
}

// TestCalcCYQNoAmountNoVolume Volume=0 && Amount=0 → 回退 (H+L)/2, 不除零
func TestCalcCYQNoAmountNoVolume(t *testing.T) {
	candles := []Candle{
		{Open: 10, High: 12, Low: 8, Close: 10, Volume: 0, Amount: 0},
	}
	cyq := CalcCYQ(candles, []float64{0.5})
	if len(cyq) != 1 {
		t.Fatalf("len=%d, want 1", len(cyq))
	}
	// 不 panic 就算过
}

// ---------------------------------------------------------------------------
// WeightSum 接近 1 — 长序列的行为(超出短的隔离测试,但快速验证)
// 换手率全 0.05 的 200 日序列 → 衰减窗口足够长,权重和接近 1
// ---------------------------------------------------------------------------
func TestWeightSumConvergesToOne(t *testing.T) {
	n := 200
	turns := make([]float64, n)
	for i := range turns {
		turns[i] = 0.05
	}
	w := costWeights(turns)
	sum := 0.0
	for _, v := range w {
		sum += v
	}
	// 200 日换手 5% 的衰减: remaining≈0.95^200≈0.000035 → 几乎全部捕获
	assertNear(t, "WeightSum convergence", sum, 1.0, 0.01)
}
