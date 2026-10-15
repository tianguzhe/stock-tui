package backtest

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"stock-tui/internal/store"
)

// PortfolioConfig 组合回测配置
type PortfolioConfig struct {
	Config                 // 继承基础配置
	InitialCapital float64 // 初始资金
	MaxPositions   int     // 最大持仓数
	PositionSize   float64 // 每笔仓位大小（百分比，如 0.1 = 10%）
	TakeProfit     float64 // 止盈百分比（0=不止盈）
	Commission     float64 // 手续费率（如 0.0003 = 万3），买卖双边
	StampTax       float64 // 卖出印花税率（默认 0.0005），基金/ETF 免征
}

// Position 持仓
type Position struct {
	EntryDate  string
	Code       string
	SignalType string
	EntryPrice float64
	Shares     int
	Cost       float64 // 总成本（含手续费）
}

// Trade 交易记录
type Trade struct {
	EntryDate  string
	ExitDate   string
	Code       string
	SignalType string
	EntryPrice float64
	ExitPrice  float64
	Shares     int
	PnL        float64
	ReturnPct  float64
	ExitReason string // "止损" | "止盈" | "到期" | exitReasonDataGap | "回测结束"
}

// exitReasonDataGap: snapshot 只覆盖热榜池，持仓标的掉出热榜后不再有行情。
// 旧实现在缺行时 continue，止损失效、持仓被冻结，回测结束再按入场价结算且
// 不计入交易——掉榜的多半是走弱的票，亏损于是从统计里消失。现按最后一条收盘平仓。
const exitReasonDataGap = "出池平仓"

// PortfolioEngine 组合回测引擎
type PortfolioEngine struct {
	db     *sql.DB
	store  *store.Store
	config PortfolioConfig
}

// NewPortfolioEngine 创建组合回测引擎
func NewPortfolioEngine(db *sql.DB, st *store.Store, config PortfolioConfig) *PortfolioEngine {
	// 设置默认值
	if config.InitialCapital == 0 {
		config.InitialCapital = 100000 // 默认10万
	}
	if config.MaxPositions == 0 {
		config.MaxPositions = 5 // 默认最多5只
	}
	if config.PositionSize == 0 {
		config.PositionSize = 0.2 // 默认每笔20%
	}
	if config.HoldingDays == 0 {
		config.HoldingDays = 10
	}
	if config.Commission == 0 {
		config.Commission = 0.0003 // 默认万3
	}
	if config.StampTax == 0 {
		config.StampTax = 0.0005
	}

	return &PortfolioEngine{
		db:     db,
		store:  st,
		config: config,
	}
}

// Run 执行组合回测
func (e *PortfolioEngine) Run() error {
	startTime := time.Now()

	fmt.Printf("=== 组合回测 ===\n")
	fmt.Printf("初始资金: %.0f 元\n", e.config.InitialCapital)
	fmt.Printf("最大持仓: %d 只\n", e.config.MaxPositions)
	fmt.Printf("仓位大小: %.0f%%\n", e.config.PositionSize*100)
	fmt.Printf("时间范围: %s ~ %s\n", e.config.StartDate, e.config.EndDate)
	fmt.Printf("持有天数: %d\n", e.config.HoldingDays)
	fmt.Printf("止损: %.1f%% | 止盈: %.1f%% | 手续费: %.4f%% | 印花税(卖出,基金免): %.4f%%\n\n",
		e.config.StopLoss, e.config.TakeProfit, e.config.Commission*100, e.config.StampTax*100)

	trades, dailyEquity, capital, err := e.simulate()
	if err != nil {
		return err
	}

	totalReturn := (capital - e.config.InitialCapital) / e.config.InitialCapital * 100

	// 5. 计算统计指标
	stats := e.calculateStats(trades, dailyEquity)

	// 6. 打印结果（所有持仓已在 simulate 中平仓，capital 即最终权益）
	e.printResults(totalReturn, capital, stats, trades, nil)

	fmt.Printf("\n耗时: %d ms\n", time.Since(startTime).Milliseconds())
	return nil
}

// simulate 逐日推进组合，返回全部已平仓交易、每日权益与最终资金。
func (e *PortfolioEngine) simulate() (trades []Trade, dailyEquity []float64, capital float64, err error) {
	capital = e.config.InitialCapital
	positions := []Position{}

	// 获取交易日列表
	tradingDays, err := e.getTradingDays()
	if err != nil {
		return nil, nil, 0, err
	}

	fmt.Printf("总交易日: %d 天\n", len(tradingDays))

	closePosition := func(pos Position, exitDate string, exitPrice float64, reason string) {
		revenue := float64(pos.Shares) * exitPrice
		fees := revenue * sellCostRate(e.config, pos.Code)
		capital += revenue - fees
		trades = append(trades, Trade{
			EntryDate:  pos.EntryDate,
			ExitDate:   exitDate,
			Code:       pos.Code,
			SignalType: pos.SignalType,
			EntryPrice: pos.EntryPrice,
			ExitPrice:  exitPrice,
			Shares:     pos.Shares,
			PnL:        revenue - pos.Cost - fees,
			ReturnPct:  (exitPrice - pos.EntryPrice) / pos.EntryPrice * 100,
			ExitReason: reason,
		})
	}

	// 按日期遍历
	for i, date := range tradingDays {
		if e.config.Verbose && (i+1)%20 == 0 {
			fmt.Printf("进度: %d/%d 天\n", i+1, len(tradingDays))
		}

		// 1. 检查现有持仓（止损/止盈/到期）
		for j := len(positions) - 1; j >= 0; j-- {
			pos := positions[j]
			closePrice, low, high, err := e.getPriceRange(pos.Code, date)
			if errors.Is(err, sql.ErrNoRows) {
				// 当日无行情：之后再无行情 = 已掉出热榜，按最后一条收盘平仓；
				// 之后还有行情 = 停牌/暂时出池，继续持有。
				later, err := e.hasRowAfter(pos.Code, date)
				if err != nil {
					return nil, nil, 0, err
				}
				if later {
					continue
				}
				lastDate, lastClose, err := e.lastClose(pos.Code, date)
				if err != nil {
					return nil, nil, 0, err
				}
				closePosition(pos, lastDate, lastClose, exitReasonDataGap)
				positions = append(positions[:j], positions[j+1:]...)
				continue
			}
			if err != nil {
				return nil, nil, 0, fmt.Errorf("查询 %s@%s 行情: %w", pos.Code, date, err)
			}

			daysHeld, err := e.countTradingDays(pos.EntryDate, date)
			if err != nil {
				return nil, nil, 0, fmt.Errorf("统计 %s 持有天数: %w", pos.Code, err)
			}

			// 退出条件按优先级：盘中止损（看当日最低）> 盘中止盈（看当日最高）> 到期（收盘）。
			// 持仓为多头，成交价取触发价（止损/止盈线）或到期收盘价。
			shouldExit := false
			exitReason := ""
			exitPrice := closePrice

			switch {
			case e.config.StopLoss > 0 && (low-pos.EntryPrice)/pos.EntryPrice*100 <= -e.config.StopLoss:
				shouldExit = true
				exitReason = "止损"
				exitPrice = pos.EntryPrice * (1 - e.config.StopLoss/100)
			case e.config.TakeProfit > 0 && (high-pos.EntryPrice)/pos.EntryPrice*100 >= e.config.TakeProfit:
				shouldExit = true
				exitReason = "止盈"
				exitPrice = pos.EntryPrice * (1 + e.config.TakeProfit/100)
			case daysHeld >= e.config.HoldingDays:
				shouldExit = true
				exitReason = "到期"
				exitPrice = closePrice
			}

			if shouldExit {
				closePosition(pos, date, exitPrice, exitReason)
				positions = append(positions[:j], positions[j+1:]...)
			}
		}
		// 2. 检查新信号（如果有空闲仓位）
		if len(positions) < e.config.MaxPositions {
			signals, err := e.getSignals(date)
			if err != nil {
				return nil, nil, 0, fmt.Errorf("查询 %s 信号: %w", date, err)
			}

			for _, sig := range signals {
				if len(positions) >= e.config.MaxPositions {
					break
				}
				// 组合只做多（A 股无法融券做空）。getSignals 与单信号回测共用
				// findSignals，会带出超买反转空头/顶背离空头等看空信号，不能拿来建仓。
				if sig.Direction != "多头" {
					continue
				}

				// 同一代码已持有一份仓位时跳过：信号可能连续多日成立，重复建仓
				// 会让资金集中在少数标的却仍占用 MaxPositions 的"分散持仓槽"，
				// 扭曲组合回测的分散化假设与收益/风险统计。
				alreadyHeld := false
				for _, pos := range positions {
					if pos.Code == sig.Code {
						alreadyHeld = true
						break
					}
				}
				if alreadyHeld {
					continue
				}

				// 计算买入股数
				positionValue := capital * e.config.PositionSize
				shares := int(positionValue/sig.Close/100) * 100 // 向下取整到100股

				if shares >= 100 {
					cost := float64(shares)*sig.Close + float64(shares)*sig.Close*e.config.Commission

					if capital >= cost {
						// 开仓
						capital -= cost

						positions = append(positions, Position{
							EntryDate:  date,
							Code:       sig.Code,
							SignalType: sig.SignalType,
							EntryPrice: sig.Close,
							Shares:     shares,
							Cost:       cost,
						})
					}
				}
			}
		}

		// 3. 记录当日权益。当日无行情(停牌/暂时出池)的持仓按最近一条收盘估值，
		// 不能按 0 计，否则权益曲线出现假回撤。
		equity := capital
		for _, pos := range positions {
			_, price, err := e.lastClose(pos.Code, date)
			if err != nil {
				return nil, nil, 0, err
			}
			equity += float64(pos.Shares) * price
		}
		dailyEquity = append(dailyEquity, equity)
	}

	// 4. 关闭剩余持仓（按截至 EndDate 的最后一条收盘平仓，纳入胜率统计）
	for _, pos := range positions {
		lastDate, lastClose, err := e.lastClose(pos.Code, e.config.EndDate)
		if err != nil {
			return nil, nil, 0, err
		}
		closePosition(pos, lastDate, lastClose, "回测结束")
	}
	return trades, dailyEquity, capital, nil
}

// sellCostRate 卖出费率：佣金 + 印花税。基金/ETF/LOF 免征印花税。
func sellCostRate(cfg PortfolioConfig, code string) float64 {
	if isFund(code) {
		return cfg.Commission
	}
	return cfg.Commission + cfg.StampTax
}

// isFund 按代码段判定场内基金(ETF/LOF)：沪 51/56/58，深 15/16。
func isFund(code string) bool {
	if len(code) < 4 {
		return false
	}
	switch code[:4] {
	case "sh51", "sh56", "sh58", "sz15", "sz16":
		return true
	}
	return false
}

// hasRowAfter 判断标的在 date 之后是否还有快照(用于区分停牌与掉出热榜)。
func (e *PortfolioEngine) hasRowAfter(code, date string) (bool, error) {
	var n int
	err := e.db.QueryRow(`SELECT COUNT(*) FROM snapshot WHERE code = ? AND trade_date > ?`, code, date).Scan(&n)
	return n > 0, err
}

// lastClose 返回标的在 date(含)之前最后一条快照的日期与收盘价。持仓标的至少
// 有建仓日那一行，故正常情况下不会 ErrNoRows。
func (e *PortfolioEngine) lastClose(code, date string) (string, float64, error) {
	var d string
	var c float64
	err := e.db.QueryRow(`
		SELECT trade_date, close FROM snapshot
		WHERE code = ? AND trade_date <= ?
		ORDER BY trade_date DESC LIMIT 1
	`, code, date).Scan(&d, &c)
	if err != nil {
		return "", 0, fmt.Errorf("查询 %s 截至 %s 的最后收盘价: %w", code, date, err)
	}
	return d, c, nil
}

// getTradingDays 获取交易日列表
func (e *PortfolioEngine) getTradingDays() ([]string, error) {
	rows, err := e.db.Query(`
		SELECT DISTINCT trade_date
		FROM snapshot
		WHERE trade_date >= ? AND trade_date <= ?
		ORDER BY trade_date
	`, e.config.StartDate, e.config.EndDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	days := []string{}
	for rows.Next() {
		var date string
		if err := rows.Scan(&date); err != nil {
			return nil, err
		}
		days = append(days, date)
	}

	return days, rows.Err()
}

// getSignals 获取当日信号
func (e *PortfolioEngine) getSignals(date string) ([]Signal, error) {
	engine := NewEngine(e.db, e.store, e.config.Config)
	engine.config.StartDate = date
	engine.config.EndDate = date

	return engine.findSignals()
}

// getPriceRange 获取指定日期的收盘价与盘中 low/high（旧行缺失时回退到 close）。
// 用日内极值判定止损/止盈，避免仅用收盘价低估盘中风险。
func (e *PortfolioEngine) getPriceRange(code, date string) (close, low, high float64, err error) {
	err = e.db.QueryRow(`
		SELECT close, COALESCE(low, close), COALESCE(high, close)
		FROM snapshot
		WHERE code = ? AND trade_date = ?
	`, code, date).Scan(&close, &low, &high)

	return close, low, high, err
}

// getPrice 获取指定日期的收盘价（用于建仓与逐日权益估值）。
func (e *PortfolioEngine) getPrice(code, date string) (float64, error) {
	close, _, _, err := e.getPriceRange(code, date)
	return close, err
}

// countTradingDays 计算两个日期之间的交易日数
func (e *PortfolioEngine) countTradingDays(from, to string) (int, error) {
	var count int
	err := e.db.QueryRow(`
		SELECT COUNT(DISTINCT trade_date)
		FROM snapshot
		WHERE trade_date > ? AND trade_date <= ?
	`, from, to).Scan(&count)

	return count, err
}

// PortfolioStats 组合统计
type PortfolioStats struct {
	TotalTrades  int
	WinTrades    int
	WinRate      float64
	AvgReturn    float64
	MaxDrawdown  float64
	SharpeRatio  float64
	CalmarRatio  float64
	ProfitFactor float64
	AvgWin       float64
	AvgLoss      float64
}

// calculateStats 计算统计指标
func (e *PortfolioEngine) calculateStats(trades []Trade, dailyEquity []float64) PortfolioStats {
	stats := PortfolioStats{
		TotalTrades: len(trades),
	}

	if len(trades) == 0 {
		return stats
	}

	// 胜率和平均收益
	returns := []float64{}
	wins := 0
	totalProfit := 0.0
	totalLoss := 0.0
	winReturns := []float64{}
	lossReturns := []float64{}

	for _, t := range trades {
		returns = append(returns, t.ReturnPct)
		if t.PnL > 0 {
			wins++
			totalProfit += t.PnL
			winReturns = append(winReturns, t.ReturnPct)
		} else {
			totalLoss += -t.PnL
			lossReturns = append(lossReturns, t.ReturnPct)
		}
	}

	stats.WinTrades = wins
	stats.WinRate = float64(wins) / float64(len(trades)) * 100
	stats.AvgReturn = mean(returns)

	if len(winReturns) > 0 {
		stats.AvgWin = mean(winReturns)
	}
	if len(lossReturns) > 0 {
		stats.AvgLoss = mean(lossReturns)
	}

	// 盈亏比
	if totalLoss > 0 {
		stats.ProfitFactor = totalProfit / totalLoss
	}

	// 最大回撤
	if len(dailyEquity) > 0 {
		stats.MaxDrawdown = e.calculateMaxDrawdown(dailyEquity)
	}

	// 夏普比率（假设无风险利率为0）；样本收益率全相同时 stdDev=0，避免 +Inf/NaN
	if len(returns) > 1 {
		if sd := stdDev(returns); sd > 0 {
			stats.SharpeRatio = mean(returns) / sd
		}
	}

	// 卡尔玛比率
	if stats.MaxDrawdown != 0 {
		annualReturn := stats.AvgReturn * 252 / float64(e.config.HoldingDays) // 年化
		stats.CalmarRatio = annualReturn / (-stats.MaxDrawdown)
	}

	return stats
}

// calculateMaxDrawdown 计算最大回撤
func (e *PortfolioEngine) calculateMaxDrawdown(equity []float64) float64 {
	if len(equity) == 0 {
		return 0
	}

	maxDrawdown := 0.0
	peak := equity[0]

	for _, value := range equity {
		if value > peak {
			peak = value
		}
		drawdown := (value - peak) / peak * 100
		if drawdown < maxDrawdown {
			maxDrawdown = drawdown
		}
	}

	return maxDrawdown
}

// printResults 打印结果
func (e *PortfolioEngine) printResults(totalReturn, finalEquity float64, stats PortfolioStats, trades []Trade, positions []Position) {
	fmt.Printf("\n=== 组合回测结果 ===\n")
	fmt.Printf("初始资金: %.0f 元\n", e.config.InitialCapital)
	fmt.Printf("最终权益: %.0f 元\n", finalEquity)
	fmt.Printf("总收益: %.2f%% (%.0f 元)\n", totalReturn, finalEquity-e.config.InitialCapital)

	fmt.Printf("\n=== 交易统计 ===\n")
	fmt.Printf("总交易数: %d\n", stats.TotalTrades)
	fmt.Printf("胜率: %.1f%% (%d/%d)\n", stats.WinRate, stats.WinTrades, stats.TotalTrades)
	fmt.Printf("平均收益: %.2f%%\n", stats.AvgReturn)
	fmt.Printf("平均盈利: %.2f%% | 平均亏损: %.2f%%\n", stats.AvgWin, stats.AvgLoss)
	fmt.Printf("盈亏比: %.2f\n", stats.ProfitFactor)

	fmt.Printf("\n=== 风险指标 ===\n")
	fmt.Printf("最大回撤: %.2f%%\n", stats.MaxDrawdown)
	fmt.Printf("夏普比率: %.2f\n", stats.SharpeRatio)
	fmt.Printf("卡尔玛比率: %.2f\n", stats.CalmarRatio)

	// 按退出原因分组
	stopLoss := 0
	takeProfit := 0
	expire := 0
	dataGap := 0
	for _, t := range trades {
		switch t.ExitReason {
		case "止损":
			stopLoss++
		case "止盈":
			takeProfit++
		case "到期":
			expire++
		case exitReasonDataGap:
			dataGap++
		}
	}

	fmt.Printf("\n=== 退出原因 ===\n")
	fmt.Printf("止损: %d (%.1f%%)\n", stopLoss, float64(stopLoss)/float64(stats.TotalTrades)*100)
	fmt.Printf("止盈: %d (%.1f%%)\n", takeProfit, float64(takeProfit)/float64(stats.TotalTrades)*100)
	fmt.Printf("到期: %d (%.1f%%)\n", expire, float64(expire)/float64(stats.TotalTrades)*100)
	fmt.Printf("出池平仓(掉出热榜后按末次收盘): %d (%.1f%%)\n", dataGap, float64(dataGap)/float64(stats.TotalTrades)*100)

	// 持仓明细
	if len(positions) > 0 {
		fmt.Printf("\n=== 未平仓持仓 (%d 只) ===\n", len(positions))
		for _, pos := range positions {
			currentPrice, _ := e.getPrice(pos.Code, e.config.EndDate)
			pnlPct := (currentPrice - pos.EntryPrice) / pos.EntryPrice * 100
			fmt.Printf("%s: 入场=%s 价格=%.2f 股数=%d 浮盈=%.2f%%\n",
				pos.Code, pos.EntryDate, pos.EntryPrice, pos.Shares, pnlPct)
		}
	}

	// 最佳/最差交易
	if len(trades) > 0 {
		bestTrade := trades[0]
		worstTrade := trades[0]
		for _, t := range trades {
			if t.ReturnPct > bestTrade.ReturnPct {
				bestTrade = t
			}
			if t.ReturnPct < worstTrade.ReturnPct {
				worstTrade = t
			}
		}

		fmt.Printf("\n=== 最佳交易 ===\n")
		fmt.Printf("%s: %s ~ %s, %.2f → %.2f, 收益 %.2f%%\n",
			bestTrade.Code, bestTrade.EntryDate, bestTrade.ExitDate,
			bestTrade.EntryPrice, bestTrade.ExitPrice, bestTrade.ReturnPct)

		fmt.Printf("\n=== 最差交易 ===\n")
		fmt.Printf("%s: %s ~ %s, %.2f → %.2f, 收益 %.2f%%\n",
			worstTrade.Code, worstTrade.EntryDate, worstTrade.ExitDate,
			worstTrade.EntryPrice, worstTrade.ExitPrice, worstTrade.ReturnPct)
	}
}
