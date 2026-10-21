package backtest

import (
	"database/sql"
	"fmt"
	"math"
	"os"
	"sort"
	"time"

	"github.com/google/uuid"
	"stock-tui/internal/market"
	"stock-tui/internal/store"
)

// unsupportedSignals are computed in evalSignals but never persisted to a snapshot
// column, so findSignals can only read them as the hardcoded 0 — backtesting them
// is a silent no-op.  Declared here so Run can warn when a user asks for one.
var unsupportedSignals = map[string]bool{
	"趋势跟随空头": true,
	"量价突破多头": true,
	"量价突破空头": true,
}

// Config 回测配置
type Config struct {
	StartDate   string
	EndDate     string
	Signals     []string               // 要测试的信号类型
	Filters     map[string]interface{} // 筛选条件
	HoldingDays int                    // 持有天数（默认10）
	StopLoss    float64                // 止损百分比（0=不止损）
	Verbose     bool                   // 详细日志
}

// Signal 信号数据
type Signal struct {
	Date       string
	Code       string
	Name       string // 判 ST 限幅用；已掉出热榜的标的 instrument 行已删，为空时按非 ST 处理
	SignalType string
	Direction  string
	Close      float64
	ChangePct  float64

	// 快照数据
	ScoreTotal int
	ADX        float64
	RSRankPct  float64
	SARStance  string
	TDSetup    string

	// 策略触发标志
	TrendBull  bool
	TrendBear  bool
	Oversold   bool
	Overbought bool
	BreakBull  bool
	BreakBear  bool
	DivBull    bool
	DivBear    bool
}

// Engine 回测引擎
type Engine struct {
	db     *sql.DB
	store  *store.Store
	config Config
}

// NewEngine 创建回测引擎
func NewEngine(db *sql.DB, st *store.Store, config Config) *Engine {
	// 设置默认值
	if config.HoldingDays == 0 {
		config.HoldingDays = 10
	}
	if len(config.Signals) == 0 {
		// Default list excludes 趋势跟随空头 / 量价突破多头 / 量价突破空头:
		// those have no persisted snapshot columns, so findSignals only reads
		// them as the hardcoded 0 and they would never fire.  trend_bear /
		// break_bull / break_bear are computed in evalSignals but not stored,
		// so backtesting them is silently a no-op.
		config.Signals = []string{
			"趋势跟随多头",
			"超买反转空头", "超卖反转多头",
			"顶背离空头", "底背离多头",
		}
	}

	return &Engine{
		db:     db,
		store:  st,
		config: config,
	}
}

// Run 执行回测
func (e *Engine) Run() (string, error) {
	runID := uuid.New().String()
	startTime := time.Now()

	if e.config.Verbose {
		fmt.Printf("=== 开始回测 ===\n")
		fmt.Printf("运行ID: %s\n", runID)
		fmt.Printf("时间范围: %s ~ %s\n", e.config.StartDate, e.config.EndDate)
		fmt.Printf("信号类型: %v\n", e.config.Signals)
		fmt.Printf("持有天数: %d\n", e.config.HoldingDays)
		fmt.Printf("止损设置: %.1f%%\n\n", e.config.StopLoss)
	}

	// Warn about signal types with no persisted snapshot column — they would run
	// silently as 0 hits.  Surface this before the work so the user can fix flags.
	for _, s := range e.config.Signals {
		if unsupportedSignals[s] {
			fmt.Fprintf(os.Stderr, "warning: 信号 %q 无 snapshot 落库列,回测不会产生结果(已从默认列表移除)\n", s)
		}
	}

	// 1. 查询符合条件的所有信号
	signals, err := e.findSignals()
	if err != nil {
		return "", fmt.Errorf("查询信号失败: %w", err)
	}

	if e.config.Verbose {
		fmt.Printf("找到 %d 条信号\n", len(signals))
	}

	if len(signals) == 0 {
		return "", fmt.Errorf("未找到符合条件的信号")
	}

	// 2. 对每个信号计算 T+N 收益
	results := []store.BacktestResult{}
	outcomes := map[tradeOutcome]int{}
	for i, sig := range signals {
		if e.config.Verbose && (i+1)%100 == 0 {
			fmt.Printf("处理中... %d/%d\n", i+1, len(signals))
		}

		result, outcome := e.simulateTrade(sig)
		outcomes[outcome]++
		result.BacktestRunID = runID
		result.Config = e.configJSON()

		results = append(results, result)
	}

	// 3. 存储详细结果
	if err := e.store.SaveBacktestResults(results); err != nil {
		return "", fmt.Errorf("保存结果失败: %w", err)
	}

	// 4. 计算汇总统计
	summary := e.calculateSummary(results)
	summary.BacktestRunID = runID
	summary.Config = e.configJSON()
	summary.DurationMS = time.Since(startTime).Milliseconds()

	if err := e.store.SaveBacktestSummary(summary); err != nil {
		return "", fmt.Errorf("保存汇总失败: %w", err)
	}

	// 5. 打印结果
	e.printSummary(summary)
	if n := outcomes[outcomeDataGap]; n > 0 {
		fmt.Printf("注: %d/%d 笔在持有窗口内行情中断(掉出热榜或停牌),已按最后一条收盘平仓并计入统计\n",
			n, summary.TotalSignals)
	}
	if n := outcomes[outcomePending] + outcomes[outcomeNoData] + outcomes[outcomeLimitLocked]; n > 0 {
		fmt.Printf("注: 未计入 %d 笔(待结算 %d、窗口内无行情 %d、信号日封板无法成交 %d)\n",
			n, outcomes[outcomePending], outcomes[outcomeNoData], outcomes[outcomeLimitLocked])
	}

	return runID, nil
}

// findSignals 查询符合条件的信号
func (e *Engine) findSignals() ([]Signal, error) {
	// 构建查询（字段名对应实际数据库列名）
	query := `
		SELECT
			trade_date, code, close, COALESCE(change_pct, 0),
			COALESCE((SELECT name FROM instrument i WHERE i.code = snapshot.code), ''),
			COALESCE(score_total, 0), COALESCE(adx, 0), COALESCE(rs20, 0) as rs_rank_pct,
			CASE WHEN sar_long = 1 THEN '多' ELSE '空' END as sar_stance,
			COALESCE(td_setup, ''),
			COALESCE(sig_trend_bull, 0), 0 as trendBear,
			COALESCE(sig_oversold, 0), COALESCE(sig_overbought, 0),
			0 as breakBull, 0 as breakBear,
			COALESCE(div_bull, 0), COALESCE(div_bear, 0)
		FROM snapshot
		WHERE trade_date >= ? AND trade_date <= ?
	`

	args := []interface{}{e.config.StartDate, e.config.EndDate}

	// 添加筛选条件（列名白名单防注入）
	allowedFilterCols := map[string]bool{
		"code": true, "trade_date": true, "close": true,
		"score_total": true, "score_adj": true, "adx": true, "sar_long": true,
		"st_long": true, "supertrend_long": true, "obv_up": true, "obv_up3": true,
		"div_bull": true, "div_bear": true, "rs20": true, "rs60": true, "rs120": true,
		"sig_trend_bull": true, "sig_overbought": true, "sig_oversold": true,
	}
	for key, val := range e.config.Filters {
		if !allowedFilterCols[key] {
			return nil, fmt.Errorf("不允许的筛选列: %q", key)
		}
		col := key
		if col == "st_long" {
			col = "supertrend_long"
		}
		switch v := val.(type) {
		case string:
			query += fmt.Sprintf(" AND %s = ?", col)
			args = append(args, v)
		case float64:
			query += fmt.Sprintf(" AND %s >= ?", col)
			args = append(args, v)
		case int:
			query += fmt.Sprintf(" AND %s >= ?", col)
			args = append(args, v)
		}
	}

	query += " ORDER BY trade_date, code"

	rows, err := e.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	signals := []Signal{}
	for rows.Next() {
		var s Signal
		var sarStance string
		var tdSetup string
		var trendBull, trendBear, oversold, overbought int
		var breakBull, breakBear, divBull, divBear int

		err := rows.Scan(
			&s.Date, &s.Code, &s.Close, &s.ChangePct, &s.Name,
			&s.ScoreTotal, &s.ADX, &s.RSRankPct,
			&sarStance, &tdSetup,
			&trendBull, &trendBear, &oversold, &overbought,
			&breakBull, &breakBear, &divBull, &divBear,
		)
		if err != nil {
			return nil, err
		}

		// 处理值
		s.SARStance = sarStance
		s.TDSetup = tdSetup

		// 策略触发标志
		s.TrendBull = trendBull == 1
		s.TrendBear = trendBear == 1
		s.Oversold = oversold == 1
		s.Overbought = overbought == 1
		s.BreakBull = breakBull == 1
		s.BreakBear = breakBear == 1
		s.DivBull = divBull == 1
		s.DivBear = divBear == 1

		// 根据触发标志生成信号
		e.appendSignals(&signals, s)
	}

	return signals, rows.Err()
}

// appendSignals 根据触发标志追加信号
func (e *Engine) appendSignals(signals *[]Signal, s Signal) {
	// 趋势跟随多头
	if s.TrendBull && e.contains("趋势跟随多头") {
		sig := s
		sig.SignalType = "趋势跟随多头"
		sig.Direction = "多头"
		*signals = append(*signals, sig)
	}

	// 趋势跟随空头
	if s.TrendBear && e.contains("趋势跟随空头") {
		sig := s
		sig.SignalType = "趋势跟随空头"
		sig.Direction = "空头"
		*signals = append(*signals, sig)
	}

	// 超买反转空头
	if s.Overbought && e.contains("超买反转空头") {
		sig := s
		sig.SignalType = "超买反转空头"
		sig.Direction = "空头"
		*signals = append(*signals, sig)
	}

	// 超卖反转多头
	if s.Oversold && e.contains("超卖反转多头") {
		sig := s
		sig.SignalType = "超卖反转多头"
		sig.Direction = "多头"
		*signals = append(*signals, sig)
	}

	// 量价突破多头
	if s.BreakBull && e.contains("量价突破多头") {
		sig := s
		sig.SignalType = "量价突破多头"
		sig.Direction = "多头"
		*signals = append(*signals, sig)
	}

	// 量价突破空头
	if s.BreakBear && e.contains("量价突破空头") {
		sig := s
		sig.SignalType = "量价突破空头"
		sig.Direction = "空头"
		*signals = append(*signals, sig)
	}

	// 底背离多头
	if s.DivBull && e.contains("底背离多头") {
		sig := s
		sig.SignalType = "底背离多头"
		sig.Direction = "多头"
		*signals = append(*signals, sig)
	}

	// 顶背离空头
	if s.DivBear && e.contains("顶背离空头") {
		sig := s
		sig.SignalType = "顶背离空头"
		sig.Direction = "空头"
		*signals = append(*signals, sig)
	}
}

// simulateTrade 模拟单次交易
// tradeOutcome 区分平仓方式。outcomeDataGap 不写入 backtest_result(表无此列)，
// 只在汇总时计数提示——它是「掉出热榜/停牌后按末次行情平仓」的近似值。
type tradeOutcome int

const (
	outcomePending     tradeOutcome = iota // 全市场交易日不足 N 天，尚未到期
	outcomeNoData                          // 信号日后窗口内该标的一条行情也没有，无法定价
	outcomeStop                            // 盘中触发止损
	outcomeExpired                         // 持有满 N 个交易日按收盘平仓
	outcomeDataGap                         // 窗口内行情中断(出池/停牌)，按最后一条收盘平仓
	outcomeLimitLocked                     // 信号日封板(多头涨停/空头跌停)，按收盘价无法成交
)

// entryBlocked 判断信号日收盘价能否成交：多头遇涨停买不到、空头遇跌停卖不出。
// 阈值与选股闸门同口径(板块限幅 - 0.5，见 market.PriceLimitPct)，港股无限幅不拦。
func entryBlocked(sig Signal) bool {
	limit := market.PriceLimitPct(sig.Code, sig.Name)
	if limit == market.NoPriceLimit {
		return false
	}
	near := limit - 0.5
	if sig.Direction == "多头" {
		return sig.ChangePct >= near
	}
	return sig.ChangePct <= -near
}

// stopFillPrice 返回触发止损当日的成交价。盘中才穿过止损价时按止损价成交；
// 整日都在止损价外侧(跳空穿越)时实际成交在开盘价，按止损价会低估亏损。
// snapshot 无 open 列，多头取当日最高价、空头取最低价——它们是开盘价的边界，
// 结果仍略偏乐观，但比止损价接近真实成交。
func stopFillPrice(entry, stopPct, low, high float64, long bool) float64 {
	if long {
		stop := entry * (1 - stopPct/100)
		return math.Min(stop, high)
	}
	stop := entry * (1 + stopPct/100)
	return math.Max(stop, low)
}

func (e *Engine) simulateTrade(sig Signal) (store.BacktestResult, tradeOutcome) {
	result := store.BacktestResult{
		EntryDate:  sig.Date,
		Code:       sig.Code,
		SignalType: sig.SignalType,
		Direction:  sig.Direction,
		EntryPrice: sig.Close,
		ScoreTotal: sig.ScoreTotal,
		ADX:        sig.ADX,
		RSRankPct:  sig.RSRankPct,
		SARStance:  sig.SARStance,
		TDSetup:    sig.TDSetup,
	}
	if entryBlocked(sig) {
		return result, outcomeLimitLocked
	}

	// 持有窗口按**全市场**交易日计，而非该标的的行数：snapshot 只覆盖热榜池，
	// 标的掉出热榜后不再有行情。旧实现按标的行数 LIMIT N，掉出热榜的标的凑不满
	// N 行就被当作「数据不足」整笔丢弃——掉榜的多半是走弱的票，于是亏损从统计里
	// 系统性消失(幸存者偏差)。
	windowEnd, complete, err := e.marketWindowEnd(sig.Date)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: 查询交易日失败 %s %s: %v\n", sig.Code, sig.Date, err)
		return result, outcomeNoData
	}

	// 查询窗口内的价格。用盘中 low/high（而非仅收盘）量回撤与止损，
	// 与 recordPerf 的 MaxAdverse 口径一致；缺失的旧行 low/high 为 NULL，
	// COALESCE 回退到 close 以保持向后兼容。
	query := `
		SELECT trade_date, close,
		       COALESCE(low, close)  AS low,
		       COALESCE(high, close) AS high
		FROM snapshot
		WHERE code = ? AND trade_date > ? AND trade_date <= ?
		ORDER BY trade_date ASC
	`

	rows, err := e.db.Query(query, sig.Code, sig.Date, windowEnd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: 查询失败 %s %s: %v\n", sig.Code, sig.Date, err)
		return result, outcomeNoData
	}
	defer rows.Close()

	dayCount := 0
	maxAdverse := 0.0
	var lastDate string
	var lastClose float64

	for rows.Next() {
		var date string
		var close, low, high float64
		if err := rows.Scan(&date, &close, &low, &high); err != nil {
			fmt.Fprintf(os.Stderr, "warning: 扫描失败 %s: %v\n", sig.Code, err)
			return result, outcomeNoData
		}

		if e.config.Verbose && dayCount == 0 {
			fmt.Printf("DEBUG: %s 第一个后续价格: %s %.2f\n", sig.Code, date, close)
		}

		dayCount++
		lastDate, lastClose = date, close

		// 盘中最大不利波动：多头看当日最低、空头看当日最高（真实日内回撤）
		var adverse float64
		if sig.Direction == "多头" {
			adverse = (low - sig.Close) / sig.Close * 100
		} else {
			adverse = (sig.Close - high) / sig.Close * 100
		}
		if adverse < maxAdverse {
			maxAdverse = adverse
		}

		// 盘中止损：用日内极值判定（而非收盘），成交价见 stopFillPrice
		if e.config.StopLoss > 0 && adverse <= -e.config.StopLoss {
			long := sig.Direction == "多头"
			result.ExitDate = date
			result.HoldingDays = dayCount
			result.ExitPrice = stopFillPrice(sig.Close, e.config.StopLoss, low, high, long)
			if long {
				result.ReturnPct = (result.ExitPrice - sig.Close) / sig.Close * 100
			} else {
				result.ReturnPct = (sig.Close - result.ExitPrice) / sig.Close * 100
			}
			result.MaxAdversePct = maxAdverse
			result.Win = 0
			return result, outcomeStop
		}
	}
	if err := rows.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: 迭代失败 %s: %v\n", sig.Code, err)
		return result, outcomeNoData
	}

	result.HoldingDays = dayCount
	result.MaxAdversePct = maxAdverse
	switch {
	case !complete:
		return result, outcomePending
	case dayCount == 0:
		if e.config.Verbose {
			fmt.Printf("警告: %s %s 窗口内没有后续数据\n", sig.Code, sig.Date)
		}
		return result, outcomeNoData
	}

	outcome := outcomeExpired
	if lastDate != windowEnd {
		outcome = outcomeDataGap
	}
	result.ExitDate = lastDate
	result.ExitPrice = lastClose
	if sig.Direction == "多头" {
		result.ReturnPct = (lastClose - sig.Close) / sig.Close * 100
	} else {
		result.ReturnPct = (sig.Close - lastClose) / sig.Close * 100
	}
	if result.ReturnPct > 0 {
		result.Win = 1
	}
	return result, outcome
}

// marketWindowEnd 返回信号日之后第 HoldingDays 个全市场交易日；不足 N 天时返回
// 已有的最后一天且 complete=false。
func (e *Engine) marketWindowEnd(signalDate string) (end string, complete bool, err error) {
	rows, err := e.db.Query(`
		SELECT DISTINCT trade_date FROM snapshot
		WHERE trade_date > ?
		ORDER BY trade_date ASC
		LIMIT ?
	`, signalDate, e.config.HoldingDays)
	if err != nil {
		return "", false, err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		if err := rows.Scan(&end); err != nil {
			return "", false, err
		}
		n++
	}
	return end, n == e.config.HoldingDays, rows.Err()
}

// calculateSummary 计算汇总统计
func (e *Engine) calculateSummary(results []store.BacktestResult) store.BacktestSummary {
	// 过滤有效结果（有退出日期的）
	validResults := []store.BacktestResult{}
	for _, r := range results {
		if r.ExitDate != "" {
			validResults = append(validResults, r)
		}
	}

	total := len(validResults)
	wins := 0
	returns := []float64{}

	for _, r := range validResults {
		if r.Win == 1 {
			wins++
		}
		returns = append(returns, r.ReturnPct)
	}

	// BullMarketWinRate/BearMarketWinRate/MaxDrawdown/SharpeRatio 留空(零值):
	// 这个引擎按独立信号收益统计, 没有市场状态分类也没有权益曲线, 无法算出
	// 这四项。见 store.BacktestSummary 上的字段注释。
	summary := store.BacktestSummary{
		Config:       e.configJSON(),
		StartDate:    e.config.StartDate,
		EndDate:      e.config.EndDate,
		TotalSignals: total,
		WinCount:     wins,
	}

	if total > 0 {
		summary.WinRate = float64(wins) / float64(total) * 100
		summary.AvgReturn = mean(returns)
		summary.MedianReturn = median(returns)
		summary.BestReturn = max(returns)
		summary.WorstReturn = min(returns)
	}

	// 按信号类型分层统计
	signalStats := make(map[string]store.SignalStat)
	for _, r := range validResults {
		stat := signalStats[r.SignalType]
		stat.Type = r.SignalType
		stat.Total++
		if r.Win == 1 {
			stat.Wins++
		}
		stat.Returns = append(stat.Returns, r.ReturnPct)
		signalStats[r.SignalType] = stat
	}

	statsJSON, _ := store.MarshalSignalStats(signalStats)
	summary.SignalStats = statsJSON

	return summary
}

// printSummary 打印汇总结果
func (e *Engine) printSummary(summary store.BacktestSummary) {
	fmt.Printf("\n=== 回测结果 ===\n")
	fmt.Printf("时间范围：%s ~ %s\n", summary.StartDate, summary.EndDate)
	fmt.Printf("总信号数：%d\n", summary.TotalSignals)
	fmt.Printf("胜率：%.1f%% (%d/%d)\n", summary.WinRate, summary.WinCount, summary.TotalSignals)
	fmt.Printf("平均收益：%.2f%%\n", summary.AvgReturn)
	fmt.Printf("中位数收益：%.2f%%\n", summary.MedianReturn)
	fmt.Printf("最佳：%.2f%%\n", summary.BestReturn)
	fmt.Printf("最差：%.2f%%\n", summary.WorstReturn)

	// 打印各信号类型统计
	stats, _ := store.UnmarshalSignalStats(summary.SignalStats)
	if len(stats) > 0 {
		fmt.Printf("\n=== 按信号类型分层 ===\n")

		// 按胜率排序
		type kv struct {
			Key   string
			Value store.SignalStat
		}
		var sorted []kv
		for k, v := range stats {
			sorted = append(sorted, kv{k, v})
		}
		sort.Slice(sorted, func(i, j int) bool {
			return sorted[i].Value.WinRate > sorted[j].Value.WinRate
		})

		for _, item := range sorted {
			stat := item.Value
			status := ""
			if stat.WinRate >= 60 {
				status = "✅"
			} else if stat.WinRate < 50 {
				status = "❌"
			} else {
				status = "⚠️"
			}
			fmt.Printf("%s %s: 样本=%d 胜率=%.1f%% 平均收益=%.2f%%\n",
				status, stat.Type, stat.Total, stat.WinRate, stat.AvgReturn)
		}
	}

	fmt.Printf("\n耗时：%d ms\n", summary.DurationMS)
}

// 辅助方法
func (e *Engine) contains(signal string) bool {
	for _, s := range e.config.Signals {
		if s == signal {
			return true
		}
	}
	return false
}

func (e *Engine) configJSON() string {
	// 简化配置输出
	return fmt.Sprintf("start=%s,end=%s,days=%d,stop=%.1f",
		e.config.StartDate, e.config.EndDate, e.config.HoldingDays, e.config.StopLoss)
}

// 统计函数
func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := make([]float64, len(values))
	copy(sorted, values)
	sort.Float64s(sorted)

	n := len(sorted)
	if n%2 == 0 {
		return (sorted[n/2-1] + sorted[n/2]) / 2
	}
	return sorted[n/2]
}

func max(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	m := values[0]
	for _, v := range values {
		if v > m {
			m = v
		}
	}
	return m
}

func min(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	m := values[0]
	for _, v := range values {
		if v < m {
			m = v
		}
	}
	return m
}

func stdDev(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	m := mean(values)
	sum := 0.0
	for _, v := range values {
		sum += math.Pow(v-m, 2)
	}
	return math.Sqrt(sum / float64(len(values)))
}
