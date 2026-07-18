// Package analysis 提供技术面评分、信号检测、背离分析和 PERF 回测引擎。
// 从 cmd/indicator-analyze 和 cmd/stockdb 共有的分析逻辑中抽取而来。
package analysis

import (
	"math"
	"sort"

	"stock-tui/internal/indicator"
)

// NDayReturn 计算 N 日涨跌幅(%)。不足 N+1 根或基准为 0 时返回 0。
func NDayReturn(candles []indicator.Candle, n int) float64 {
	last := len(candles) - 1
	base := last - n
	if base < 0 || candles[base].Close == 0 {
		return 0
	}
	return (candles[last].Close - candles[base].Close) / candles[base].Close * 100
}

// CloseSeries 提取收盘价序列。
func CloseSeries(candles []indicator.Candle) []float64 {
	values := make([]float64, len(candles))
	for i, candle := range candles {
		values[i] = candle.Close
	}
	return values
}

// VolumeSeries 提取成交量序列。
func VolumeSeries(candles []indicator.Candle) []float64 {
	values := make([]float64, len(candles))
	for i, candle := range candles {
		values[i] = candle.Volume
	}
	return values
}

// OBVSeries 计算经典 OBV(收涨加量/收跌减量/平盘持平)。
func OBVSeries(candles []indicator.Candle) []float64 {
	obv := make([]float64, len(candles))
	if len(candles) == 0 {
		return obv
	}
	obv[0] = candles[0].Volume
	for i := 1; i < len(candles); i++ {
		switch {
		case candles[i].Close > candles[i-1].Close:
			obv[i] = obv[i-1] + candles[i].Volume
		case candles[i].Close < candles[i-1].Close:
			obv[i] = obv[i-1] - candles[i].Volume
		default:
			obv[i] = obv[i-1]
		}
	}
	return obv
}

// RangeLowHigh 返回 [start, end) 范围内的最低价和最高价。
func RangeLowHigh(candles []indicator.Candle, start, end int) (float64, float64) {
	if start < 0 {
		start = 0
	}
	if end > len(candles) {
		end = len(candles)
	}
	if start >= end {
		if len(candles) > 0 {
			last := candles[len(candles)-1]
			return last.Low, last.High
		}
		return 0, 0
	}
	low, high := math.Inf(1), math.Inf(-1)
	for i := start; i < end; i++ {
		if candles[i].Low < low {
			low = candles[i].Low
		}
		if candles[i].High > high {
			high = candles[i].High
		}
	}
	return low, high
}

// WindowExtremes 返回窗口内最高价和最低价的索引。
func WindowExtremes(candles []indicator.Candle, end, period int) (int, int) {
	start := MaxInt(0, end-period+1)
	hiIdx, loIdx := start, start
	for i := start + 1; i <= end; i++ {
		if candles[i].High > candles[hiIdx].High {
			hiIdx = i
		}
		if candles[i].Low < candles[loIdx].Low {
			loIdx = i
		}
	}
	return hiIdx, loIdx
}

// MeanTail 计算末尾 N 个值的均值。
func MeanTail(values []float64, count int) float64 {
	if len(values) == 0 {
		return 0
	}
	start := MaxInt(0, len(values)-count)
	total := 0.0
	for _, value := range values[start:] {
		total += value
	}
	return total / float64(len(values)-start)
}

// MedianTail 计算末尾 N 个值的中位数。
func MedianTail(values []float64, count int) float64 {
	start := MaxInt(0, len(values)-count)
	cp := append([]float64(nil), values[start:]...)
	sort.Float64s(cp)
	if len(cp) == 0 {
		return 0
	}
	mid := len(cp) / 2
	if len(cp)%2 == 1 {
		return cp[mid]
	}
	return (cp[mid-1] + cp[mid]) / 2
}

// CloseMA 计算截至 end 索引的 period 日收盘价 SMA。
func CloseMA(candles []indicator.Candle, end, period int) float64 {
	start := MaxInt(0, end-period+1)
	total := 0.0
	for i := start; i <= end; i++ {
		total += candles[i].Close
	}
	return total / float64(end-start+1)
}

// VolumeMA 计算截至 end 索引的 period 日成交量 SMA。
func VolumeMA(candles []indicator.Candle, end, period int) float64 {
	start := MaxInt(0, end-period+1)
	total := 0.0
	for i := start; i <= end; i++ {
		total += candles[i].Volume
	}
	return total / float64(end-start+1)
}

// RecentVolumeHealth 统计近 N 日涨跌日的数量和均量。
func RecentVolumeHealth(candles []indicator.Candle, days int) (int, float64, int, float64) {
	upTotal, downTotal := 0.0, 0.0
	upCnt, downCnt := 0, 0
	start := MaxInt(1, len(candles)-days)
	for i := start; i < len(candles); i++ {
		if candles[i].Close > candles[i-1].Close {
			upTotal += candles[i].Volume
			upCnt++
		} else if candles[i].Close < candles[i-1].Close {
			downTotal += candles[i].Volume
			downCnt++
		}
	}
	return upCnt, Ratio(upTotal, float64(upCnt)), downCnt, Ratio(downTotal, float64(downCnt))
}

// DonchianBreak 检测当日收盘是否突破前一根的 Donchian 通道。
func DonchianBreak(candles []indicator.Candle, results []indicator.Result, period int, bullish bool) bool {
	if len(candles) < 2 {
		return false
	}
	close := candles[len(candles)-1].Close
	prev := results[len(results)-2].Donchian
	if period == 55 {
		if bullish {
			return close > prev.Upper55
		}
		return close < prev.Lower55
	}
	if bullish {
		return close > prev.Upper20
	}
	return close < prev.Lower20
}

// OBVTrend 返回近 6 日 OBV 趋势文字描述。
func OBVTrend(obv []float64) string {
	if len(obv) < 6 {
		return "样本不足"
	}
	if obv[len(obv)-1] > obv[len(obv)-6] {
		return "上升(净流入)"
	}
	if obv[len(obv)-1] < obv[len(obv)-6] {
		return "下降(净流出)"
	}
	return "持平"
}

// TDSignalText 将 TD 信号映射为中文文字。
func TDSignalText(signal indicator.TDSignal) string {
	switch signal {
	case indicator.TDBuy:
		return "见底"
	case indicator.TDSell:
		return "见顶"
	default:
		return "-"
	}
}

// TDShort 返回 TD 信号的短格式(如 "C顶8", "S底9*")。
func TDShort(td indicator.TD) string {
	dir := func(signal indicator.TDSignal) string {
		if signal == indicator.TDSell {
			return "顶"
		}
		return "底"
	}
	switch {
	case td.CountdownCount > 0:
		return "C" + dir(td.CountdownSignal) + itoa(td.CountdownCount)
	case td.SetupCount > 0:
		text := "S" + dir(td.SetupSignal) + itoa(td.SetupCount)
		if td.SetupCount == 9 && td.SetupPerfected {
			text += "*"
		}
		return text
	default:
		return "-"
	}
}

// StreakValue 返回当前连涨/连跌天数(正=连涨,负=连跌,0=无)。
func StreakValue(candles []indicator.Candle) int {
	streak, direction := 0, 0
	for i := len(candles) - 1; i > 0; i-- {
		current := 0
		if candles[i].Close > candles[i-1].Close {
			current = 1
		} else if candles[i].Close < candles[i-1].Close {
			current = -1
		}
		if current == 0 {
			break
		}
		if streak == 0 {
			direction = current
		}
		if current != direction {
			break
		}
		streak++
	}
	return streak * direction
}

// ScoreLabel 将总分映射为文字标签。
func ScoreLabel(score int) string {
	switch {
	case score >= 85:
		return "技术极强"
	case score >= 70:
		return "技术偏强"
	case score >= 55:
		return "技术略偏强"
	case score >= 45:
		return "技术中性/方向不明"
	case score >= 31:
		return "技术略偏弱"
	case score >= 16:
		return "技术偏弱"
	default:
		return "技术极弱"
	}
}

// CountTrue 计算布尔值中 true 的个数。
func CountTrue(values ...bool) int {
	count := 0
	for _, value := range values {
		if value {
			count++
		}
	}
	return count
}

// Position 计算 value 在 [low, high] 区间中的百分比位置。
func Position(value, low, high float64) float64 {
	if high <= low {
		return 50
	}
	return (value - low) / (high - low) * 100
}

// Ratio 安全除法(denominator 为 0 时返回 0)。
func Ratio(numerator, denominator float64) float64 {
	if denominator == 0 {
		return 0
	}
	return numerator / denominator
}

// ClampInt 将整数限制在 [low, high] 范围内。
func ClampInt(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

// MaxInt 返回两个整数中较大的一个。
func MaxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// AbsInt 返回整数的绝对值。
func AbsInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// StochStagnation 检测 StochRSI 在 RSI 钝化区的交叉信号。
func StochStagnation(rsi6, kNow, dNow, kPrev, dPrev float64) (bull, bear bool) {
	crossDown := kPrev >= dPrev && kNow < dNow
	crossUp := kPrev <= dPrev && kNow > dNow
	bear = rsi6 > 75 && crossDown && kPrev > 80
	bull = rsi6 < 25 && crossUp && kPrev < 20
	return
}

// StagnationZone 标注 RSI6 钝化状态。
func StagnationZone(rsi6 float64) string {
	switch {
	case rsi6 > 75:
		return "高位"
	case rsi6 < 25:
		return "低位"
	default:
		return "正常"
	}
}

// StochTimingText 渲染 StochRSI 钝化时机信号文字。
func StochTimingText(bull, bear bool) string {
	switch {
	case bear:
		return "今日空头转向"
	case bull:
		return "今日多头转向"
	default:
		return "-"
	}
}

// itoa 简单整数转字符串(避免引入 strconv)。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := [20]byte{}
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
