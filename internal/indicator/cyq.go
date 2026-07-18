package indicator

import "math"

// CYQ (筹码分布) 衍生指标: WINNER / ASR / CYQK / 高控盘锁定法则
//
// 核心模型: 持仓成本衰减模型(holding decay)。
// 每日成交量(股)按当日 [Low, High] 区间入场,后续各日以换手率衰减留存。
// 最终计算每根 K 线的持仓成本权重→任意价格的获利盘比例 WINNER(price)。
//
// ⚠ 数据要求:
//   - 至少 60 根日K(WINNER 显著衰减的窗口),推荐 250+
//   - 换手率须与 K 线对应日,单位:小数(0.0646 = 6.46%)
//   - 价格须为前复权(否则除权跳空让成本分布错位)

// minCYQBars 是 CYQ 有意义的最低日K根数。少于 60 根时权重集中于近期几日,
// WINNER/ASR/PRY1 参考价值大幅降低,WeightSum 显著低于 1 即此信号。
const minCYQBars = 60

// CYQResult 单日 CYQ 指标
type CYQResult struct {
	// WINNER 获利盘比例(0~100),表示以该价格卖出有多少百分比的筹码获利
	WinnerClose float64 // WINNER(close)*100
	WinnerOpen  float64
	WinnerHigh  float64
	WinnerLow   float64

	// ASR 活动筹码:收盘价上下各 10% 区间内的筹码量(0~100)
	ASR float64

	// PRY1 近一年相对位置 %: 0 = 近一年最低,100 = 近一年最高
	PRY1 float64

	// CYQK 博弈K线(获利盘OHLC, 0~100)
	CYQK_Open   float64
	CYQK_High   float64
	CYQK_Low    float64
	CYQK_Close  float64
	CYQK_Length float64 // 收-开,正值=长阳,负值=长阴
	CYQK_Body   float64 // |收-开|,绝对值意义

	// 高控盘锁定信号
	VolumeLessBigKline bool // 无量长阳: CYQK_Length>18% 且 换手率<3%
	Ratio90v3          bool // 90比3: WinnerClose>90% 且 换手率<3%
	IsLowPosition      bool // PRY1 < 40% (近一年低位)

	// 诊断(仅用于调试/校验)
	WeightSum float64 // 权重合计,应≈1(接近1表示历史深度足够)
}

// CalcCYQ 计算全量 CYQ 衍生指标
//
// 参数:
//   - candles: 日K序列,需含 High/Low/Close/Volume(股数)/Amount(元)
//     Volume=0 时日度成本价回退为 (H+L)/2; 否则使用 VWAP = Amount/Volume
//   - turnovers: 换手率序列(小数,如 0.0646),长度须与 candles 一致
//
// 返回:
//   - 每根日K对应的 CYQResult,只有最后一日指标有意义(前 N 日未满历史窗口)
//     当 len(candles) < minCYQBars 时结果仍数学有效但参考价值大幅降低:
//     WeightSum 显著低于 1 即此信号。
func CalcCYQ(candles []Candle, turnovers []float64) []CYQResult {
	n := minInt(len(candles), len(turnovers))
	results := make([]CYQResult, n)
	if n == 0 {
		return results
	}

	// 1) 计算每根日K的持仓成本权重
	weights := costWeights(turnovers)
	sumW := 0.0
	for _, w := range weights {
		sumW += w
	}

	// 2) 计算每根日K的平均价格(成本价)
	//    优先 VWAP(Amount/Volume),volume=0 时回退 (H+L)/2
	avgPrices := make([]float64, n)
	for i := 0; i < n; i++ {
		if candles[i].Volume > 0 && candles[i].Amount > 0 {
			avgPrices[i] = candles[i].Amount / candles[i].Volume
		} else {
			avgPrices[i] = (candles[i].High + candles[i].Low) / 2
		}
	}

	// 3) 对每根日K计算 WINNER 系指标
	//    注意:权重的意义是"今日持仓中,源自各日的比例"
	//    WINNER(price) = 累计权重(成本价 <= price)
	//    需要针对每个 target price 重新累计

	// 按 avgPrice 排序的权重累计计算(以直线扫描)
	// 对每根 K 线的 close/open/high/low 算 WINNER
	for i := 0; i < n; i++ {
		r := &results[i]
		r.WinnerClose = winnerPrice(candles[i].Close, avgPrices, weights)
		r.WinnerOpen = winnerPrice(candles[i].Open, avgPrices, weights)
		r.WinnerHigh = winnerPrice(candles[i].High, avgPrices, weights)
		r.WinnerLow = winnerPrice(candles[i].Low, avgPrices, weights)
		r.WeightSum = sumW

		// ASR: ±10%
		closeUp := candles[i].Close * 1.1
		closeDn := candles[i].Close * 0.9
		wUp := winnerPrice(closeUp, avgPrices, weights)
		wDn := winnerPrice(closeDn, avgPrices, weights)
		r.ASR = clampF((wUp-wDn)*100, 0, 100)

		// CYQK 博弈K线(值域 0~100)
		r.CYQK_Open = r.WinnerOpen * 100
		r.CYQK_High = r.WinnerHigh * 100
		r.CYQK_Low = r.WinnerLow * 100
		r.CYQK_Close = r.WinnerClose * 100
		r.CYQK_Length = (r.WinnerClose - r.WinnerOpen) * 100
		r.CYQK_Body = math.Abs(r.CYQK_Length)

		// PRY1 近一年相对位置
		r.PRY1 = calcPRY1(candles, i)

		// 高控盘信号(仅最后日有意义,这里对所有日都算以便调试)
		turnPct := 100.0
		if i < len(turnovers) {
			turnPct = turnovers[i] * 100 // 转 %
		}
		r.VolumeLessBigKline = r.CYQK_Length > 18 && turnPct < 3
		r.Ratio90v3 = r.WinnerClose > 0.90 && turnPct < 3
		r.IsLowPosition = r.PRY1 < 40
	}
	return results
}

// winnerPrice 计算给定 targetPrice 的获利盘比例(0~1)
// 遍历所有日K的平均成本价,累加 成本价 <= targetPrice 的权重
func winnerPrice(targetPrice float64, avgPrices []float64, weights []float64) float64 {
	total := 0.0
	for i := range avgPrices {
		if avgPrices[i] <= targetPrice {
			total += weights[i]
		}
	}
	return clampF(total, 0, 1)
}

// costWeights 持仓成本衰减模型
// 从最近一日向前递推,每天成交量留存率 = (1 - 当日换手率)
// 最终权重: sharesFraction[i] = turn[i] * product_{j>i} (1-turn[j])
// 经归一化; 权重和≈1(当历史深度足够)
func costWeights(turns []float64) []float64 {
	n := len(turns)
	w := make([]float64, n)
	remaining := 1.0 // 最后一日"今日"全部留存
	for i := n - 1; i >= 0; i-- {
		// 限制换手率有效范围防止数值溢出(>1 按 1 算,<0 按 0 算)
		t := clampF(turns[i], 0, 1)
		w[i] = t * remaining
		remaining *= (1 - t)
	}
	// 归一化
	sum := 0.0
	for _, v := range w {
		sum += v
	}
	if sum > 0 {
		for i := range w {
			w[i] /= sum
		}
	}
	return w
}

// calcPRY1 计算近一年相对位置(0~100)
// PRY1 = (当前价 - 近一年最低) / (近一年最高 - 近一年最低) * 100
func calcPRY1(candles []Candle, i int) float64 {
	if i < 0 {
		return 50
	}
	start := i - 250 + 1 // 约一年交易日
	if start < 0 {
		start = 0
	}
	low, high := candles[start].Low, candles[start].High
	for j := start; j <= i; j++ {
		if candles[j].Low < low {
			low = candles[j].Low
		}
		if candles[j].High > high {
			high = candles[j].High
		}
	}
	span := high - low
	if span == 0 {
		return 50
	}
	return (candles[i].Close - low) / span * 100
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func clampF(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
