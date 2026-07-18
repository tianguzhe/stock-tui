package indicator

// CYC (成本均线) — 筹码面成交量加权均价
//
// 与传统 MA 不同(MA 按收盘价等权), CYC 按成交额/成交量加权:
//
//     CYC(N) = sum(Amount, N) / sum(Volume, N)
//
// 本质是 N 日 VWAP(成交量加权均价),反映市场参与者在 N 日内的平均持仓成本。
// 放量日对 CYC 影响大,缩量日影响小,成本感更强。
//
// 常用周期:
//   - CYC5:  短线成本支撑/压力
//   - CYC13: 波段成本
//   - CYC34: 中线成本
//   - CYC∞:  全部历史,整体市场持仓成本(近似牛熊分界线)

// CYCResult 单日 CYC 成本均线
type CYCResult struct {
	CYC5   float64 // 5 日成本均线
	CYC13  float64 // 13 日成本均线
	CYC34  float64 // 34 日成本均线
	CYCInf float64 // 无穷成本均线(全量历史加权)
}

// CalcCYC 计算 CYC 成本均线序列
//
// 参数:
//   - candles: 日K序列,需含 Amount(元)/Volume(股数)
//     Volume=0 时该日跳过(不参与加权,保持与 MA 等长)
//
// 返回:
//   - 每根日K对应的 CYCResult,前 N 日窗口不满时仍用已有数据计算(无 NaN)
func CalcCYC(candles []Candle) []CYCResult {
	n := len(candles)
	results := make([]CYCResult, n)
	if n == 0 {
		return results
	}

	// 累计成交额/成交量,用于 CYC∞ 和滑动窗口
	cumAmount := make([]float64, n)
	cumVolume := make([]float64, n)

	amountSum := 0.0
	volSum := 0.0
	for i, c := range candles {
		if c.Volume > 0 && c.Amount > 0 {
			amountSum += c.Amount
			volSum += c.Volume
		}
		// 即使 Volume=0 也保持前一累计值(跳过该日)
		cumAmount[i] = amountSum
		cumVolume[i] = volSum
	}

	for i := 0; i < n; i++ {
		r := &results[i]

		// CYC5: 5 日滑动 VWAP
		r.CYC5 = cycWindow(candles, i, cumAmount, cumVolume, 5)

		// CYC13: 13 日
		r.CYC13 = cycWindow(candles, i, cumAmount, cumVolume, 13)

		// CYC34: 34 日
		r.CYC34 = cycWindow(candles, i, cumAmount, cumVolume, 34)

		// CYC∞: 全部历史
		if cumVolume[i] > 0 {
			r.CYCInf = cumAmount[i] / cumVolume[i]
		} else {
			r.CYCInf = candles[i].Close // 无成交量数据时回退收盘价
		}
	}

	return results
}

// cycWindow 利用累计数组计算 i 日前 N 个有效(Volume>0)交易日的 VWAP
// 若窗口内无有效日,回退收盘价
func cycWindow(candles []Candle, i int, cumAmount, cumVolume []float64, n int) float64 {
	// 找前 N 个有成交量的日
	start := i
	count := 0
	for j := i; j >= 0; j-- {
		if candles[j].Volume > 0 && candles[j].Amount > 0 {
			count++
			if count == n {
				start = j
				break
			}
		}
	}
	if count < n {
		start = 0 // 窗口不满时从头开始,使用全部有效数据
	}

	amt := cumAmount[i]
	vol := cumVolume[i]
	if start > 0 {
		amt -= cumAmount[start-1]
		vol -= cumVolume[start-1]
	}

	if vol > 0 {
		return amt / vol
	}
	return candles[i].Close
}
