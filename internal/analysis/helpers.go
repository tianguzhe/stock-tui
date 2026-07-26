// Package analysis 提供技术面评分、信号检测、背离分析和 PERF 回测引擎。
// 从 cmd/indicator-analyze 和 cmd/stockdb 共有的分析逻辑中抽取而来。
package analysis

import (
	"math"
	"sort"

	"stock-tui/internal/indicator"
)

// WilsonBounds 计算胜率的 Wilson 95% 置信区间,返回 (下界%, 上界%)。
//
// 小样本会自动失去判别力: N=10、win=40% → 下界≈17%、上界≈69%,区间跨越
// 50% 两侧,统计上说明不了任何问题。用法约定:
//   - 断言「信号显著优于抛硬币」须 下界 > 阈值
//   - 断言「历史确实差」须 上界 < 阈值
//
// screener 的准入门槛与 PerfScale 的惩罚调权共用本函数,避免两处各设一套
// 显著性标准(旧实现 PerfScale 用 n>=10 硬门槛,n=10/win=30% 就敢把惩罚砍半,
// 而同一组数据的 Wilson 上界高达 60%,远不足以断定「历史差」)。
func WilsonBounds(winPct float64, n int) (lower, upper float64) {
	const z = 1.96 // 95% confidence
	if n == 0 {
		return 0.0, 100.0
	}
	// Clamp to [0,1] to avoid NaN from sqrt of negative value.
	p := math.Max(0, math.Min(1, winPct/100.0))
	denom := 1 + z*z/float64(n)
	centre := p + z*z/(2*float64(n))
	margin := z * math.Sqrt(p*(1-p)/float64(n)+z*z/(4*float64(n)*float64(n)))
	lower = (centre - margin) / denom * 100
	upper = (centre + margin) / denom * 100
	return
}

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

// GapThreshold 是跳空的判定阈值(开盘价相对昨收的偏离)。
const GapThreshold = 3.0

// PriceAction 描述当日 K 线本身的极端程度——不经任何指标转换。
//
// 存在理由: 其余各维度(趋势/动量/超买超卖/资金/择时/背离)全部是**指标衍生**的,
// 它们会漏掉最强烈的市场信号。2026-07-26 实测: 大唐发电当日跌停 -10.01%,
// 六维投出 bullW=4 / bearW=0「偏多」——跌停这个事实没有进入任何一票。
//
// 只在**极端**情形取值,日常波动一律返回零值,以免与资金维度的量价判断
// (放量上涨/下跌)重复计票。
type PriceAction struct {
	LimitUp   bool    // 接近涨停(距板块限幅 0.5 个百分点内)
	LimitDown bool    // 接近跌停
	GapPct    float64 // 跳空幅度%(开盘 vs 昨收),|GapPct| <= GapThreshold 时为 0
	Weight    int     // 综合权重: 正=看多, 负=看空, 0=无极端信号
	Label     string  // 供展示的说明文字, Weight==0 时为空
}

// EvalPriceAction 判定索引 i 处的价格行为。limitPct 为该标的的板块涨跌幅限制
// (由 market.PriceLimitPct 提供); 传 market.NoPriceLimit(0) 表示无涨跌停制度
// (如港股), 此时只判跳空。
//
// 权重口径:
//   - 涨停/跌停: ±3 —— 封板意味着当日无法成交(涨停)或恐慌性抛压(跌停),
//     是单根 K 线能给出的最强信号
//   - 跳空 > GapThreshold: ±2 —— 跳空改变持仓成本结构,且常伴随事件驱动
//   - 两者叠加时取较强者而非相加(同属"当日价格行为"一轴,不自我重复计票)
func EvalPriceAction(candles []indicator.Candle, i int, limitPct float64) PriceAction {
	var pa PriceAction
	if i <= 0 || i >= len(candles) {
		return pa
	}
	prevClose := candles[i-1].Close
	if prevClose <= 0 {
		return pa
	}
	pct := (candles[i].Close - prevClose) / prevClose * 100

	if limitPct > 0 {
		near := limitPct - 0.5
		switch {
		case pct >= near:
			pa.LimitUp = true
			pa.Weight = 3
			pa.Label = "涨停"
		case pct <= -near:
			pa.LimitDown = true
			pa.Weight = -3
			pa.Label = "跌停"
		}
	}

	// Open<=0 表示该根缺开盘价(部分数据源/合成数据),不能据此判跳空——
	// 否则会算出 -100% 的假跳空。缺数据时不产生信号。
	if candles[i].Open <= 0 {
		return pa
	}
	if gap := (candles[i].Open - prevClose) / prevClose * 100; math.Abs(gap) > GapThreshold {
		pa.GapPct = gap
		w, label := 2, "向上跳空"
		if gap < 0 {
			w, label = -2, "向下跳空"
		}
		// 同一轴内不叠加: 已有更强的涨跌停信号时保留它。
		if AbsInt(w) > AbsInt(pa.Weight) {
			pa.Weight, pa.Label = w, label
		}
	}
	return pa
}

// volRatioLookback 是量比的基准窗口: 腾讯口径取**前 5 个交易日**(不含当日)。
const volRatioLookback = 5

// VolRatio 计算索引 i 处的量比 = 当日成交量 / 前 5 日(不含当日)平均成交量。
//
// 口径经实测反解: 2026-07-25 用 5 只标的对照腾讯 qt[49] 实时量比,
// 本式全部吻合到小数点后两位(东材 0.767/0.77、工行 0.694/0.69、
// 农行 0.792/0.79、华安 1.081/1.08、华天 0.958/0.96)。
//
// ⚠ 关键在于**不含当日**且窗口是 5 而非 20。旧实现用 Volume/MA20(含当日),
// 与腾讯实时量比不是同一指标(工行实测 0.758 vs 真值 0.69,差 10%),
// 导致落库的 vol_ratio 在"取 qt"与"本地回退"两条路径上口径不一致——
// 同一个 0.8/1.5 阈值卡在两套分布上。现统一为本式。
//
// 样本不足(i<=0)时返回 0,由调用方按"无量比"处理;窗口不满 5 日时用可得日数,
// 与项目其他指标的 warmup 风格一致。
func VolRatio(candles []indicator.Candle, i int) float64 {
	if i <= 0 || i >= len(candles) {
		return 0
	}
	start := i - volRatioLookback
	if start < 0 {
		start = 0
	}
	total := 0.0
	for j := start; j < i; j++ {
		total += candles[j].Volume
	}
	return Ratio(candles[i].Volume, total/float64(i-start))
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

// obvLookback 是 OBV 净流入判据的回看根数: 拿 obv[i] 与 obv[i-obvLookback]
// 比较。OBVUpLast(落库 obv_up)、OBVUp3Day(落库 obv_up3)、OBVTrend 三者**都**
// 从这里取窗口,改一处即全体同步——否则"单日净流入""3日持续净流入""趋势文字"
// 会各按一套窗口判断,选股表上就会出现自相矛盾的组合。
const obvLookback = 5

// OBVDelta 返回最后一根 OBV 相对 obvLookback 根前的变化量: 正=净流入、
// 负=净流出、0=持平或样本不足。所有 OBV 方向判断都应经由它取窗口。
func OBVDelta(obv []float64) float64 {
	if len(obv) <= obvLookback {
		return 0
	}
	return obv[len(obv)-1] - obv[len(obv)-1-obvLookback]
}

// OBVUpLast 判断最后一根日K是否 OBV 净流入,即落库的 snapshot.obv_up。
// 样本不足时返回 false(不臆断方向)。
func OBVUpLast(obv []float64) bool {
	return OBVDelta(obv) > 0
}

// OBVUp3Day 判断最近 3 根日K是否每根都满足 OBV 净流入(持续净流入)。
//
// 与单日 OBVUp 的关系: OBVUp 只看最后一根是否满足,本函数要求连续 3 根都满足,
// 两者用同一个 obvLookback 窗口。screener 的 star 分层用它,单日成立仅给
// watch(2026-06 回测: 单日组 70.2% 胜率 /+8.87%,3 日组 82.6% /+21.02%)。
//
// 须由完整日K序列计算后落库,不可用 snapshot 最近 3 行统计——snapshot 逐日
// 累积且股票池逐步扩张,新进池个股行数不足会恒为 false,把本该 star 的标的
// 全压成 watch;缺跑的交易日也会让"最近 3 行"跨越远超 3 天的区间。
// 样本不足(最后 3 根都要能回看 obvLookback 根)时返回 false。
func OBVUp3Day(obv []float64) bool {
	if len(obv) < obvLookback+3 {
		return false
	}
	for i := len(obv) - 3; i < len(obv); i++ {
		if obv[i] <= obv[i-obvLookback] {
			return false
		}
	}
	return true
}

// OBVTrend 返回 OBV 趋势文字描述,窗口同 obvLookback。
func OBVTrend(obv []float64) string {
	if len(obv) <= obvLookback {
		return "样本不足"
	}
	switch d := OBVDelta(obv); {
	case d > 0:
		return "上升(净流入)"
	case d < 0:
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
