package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// BacktestResult 单次信号回测结果
type BacktestResult struct {
	ID            int64  `json:"id"`
	BacktestRunID string `json:"backtest_run_id"`
	RunDate       string `json:"run_date"`
	Config        string `json:"config"`

	// 信号信息
	EntryDate  string  `json:"entry_date"`
	Code       string  `json:"code"`
	SignalType string  `json:"signal_type"`
	Direction  string  `json:"direction"`
	EntryPrice float64 `json:"entry_price"`

	// 回测结果
	ExitDate    string  `json:"exit_date"`
	ExitPrice   float64 `json:"exit_price"`
	HoldingDays int     `json:"holding_days"`
	ReturnPct   float64 `json:"return_pct"`
	Win         int     `json:"win"` // 0=亏损 1=盈利

	// 入场时快照
	ScoreTotal int     `json:"score_total"`
	ADX        float64 `json:"adx"`
	RSRankPct  float64 `json:"rs_rank_pct"`
	SARStance  string  `json:"sar_stance"`
	TDSetup    string  `json:"td_setup"`

	// 最大回撤
	MaxAdversePct float64 `json:"max_adverse_pct"` // 持有期间最大浮亏
}

// BacktestSummary 回测汇总统计
type BacktestSummary struct {
	BacktestRunID string `json:"backtest_run_id"`
	RunDate       string `json:"run_date"`
	Config        string `json:"config"`

	// 时间范围
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`

	// 整体表现
	TotalSignals int     `json:"total_signals"`
	WinCount     int     `json:"win_count"`
	WinRate      float64 `json:"win_rate"`
	AvgReturn    float64 `json:"avg_return"`
	MedianReturn float64 `json:"median_return"`
	BestReturn   float64 `json:"best_return"`
	WorstReturn  float64 `json:"worst_return"`

	// 按信号分层
	SignalStats string `json:"signal_stats"` // JSON

	// 按市场环境分层。单信号回测引擎(internal/backtest/engine.go)不做市场
	// 状态(牛/熊)分类, 这两列在其写入的行里恒为 0——不代表"两种环境下都是
	// 0 胜率"。组合回测(internal/backtest/portfolio.go)目前也不落这张表
	// (只打印 PortfolioStats, 不调用 SaveBacktestSummary), 故当前无任何写入
	// 路径会填充这两列。查询 backtest_summary 时不要把 0 当有效统计量读。
	BullMarketWinRate float64 `json:"bull_market_win_rate"`
	BearMarketWinRate float64 `json:"bear_market_win_rate"`

	// 风险指标。同上, 单信号引擎按独立交易记录收益, 没有可供计算回撤的权益
	// 曲线, 这两列同样恒为 0。组合回测的 PortfolioStats 有对应的真实计算
	// (calculateMaxDrawdown/夏普比率), 但结果目前不落库, 只在 CLI 打印。
	MaxDrawdown float64 `json:"max_drawdown"`
	SharpeRatio float64 `json:"sharpe_ratio"`

	// 耗时
	DurationMS int64 `json:"duration_ms"`
}

// InitBacktestTables 初始化回测相关表
func (s *Store) InitBacktestTables() error {
	schema := `
	CREATE TABLE IF NOT EXISTS backtest_result (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		backtest_run_id TEXT NOT NULL,
		run_date TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		config TEXT,

		-- 信号信息
		entry_date TEXT NOT NULL,
		code TEXT NOT NULL,
		signal_type TEXT,
		direction TEXT,
		entry_price REAL,

		-- 回测结果
		exit_date TEXT,
		exit_price REAL,
		holding_days INTEGER,
		return_pct REAL,
		win INTEGER,

		-- 入场时快照
		score_total INTEGER,
		adx REAL,
		rs_rank_pct REAL,
		sar_stance TEXT,
		td_setup TEXT,

		-- 最大回撤
		max_adverse_pct REAL
	);

	CREATE INDEX IF NOT EXISTS idx_backtest_run ON backtest_result(backtest_run_id);
	CREATE INDEX IF NOT EXISTS idx_signal_type ON backtest_result(signal_type);
	CREATE INDEX IF NOT EXISTS idx_code_bt ON backtest_result(code);
	CREATE INDEX IF NOT EXISTS idx_entry_date_bt ON backtest_result(entry_date);

	CREATE TABLE IF NOT EXISTS backtest_summary (
		backtest_run_id TEXT PRIMARY KEY,
		run_date TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		config TEXT,

		-- 时间范围
		start_date TEXT,
		end_date TEXT,

		-- 整体表现
		total_signals INTEGER,
		win_count INTEGER,
		win_rate REAL,
		avg_return REAL,
		median_return REAL,
		best_return REAL,
		worst_return REAL,

		-- 按信号分层
		signal_stats TEXT,

		-- 按市场环境分层
		bull_market_win_rate REAL,
		bear_market_win_rate REAL,

		-- 风险指标
		max_drawdown REAL,
		sharpe_ratio REAL,

		-- 耗时
		duration_ms INTEGER
	);
	`

	_, err := s.db.Exec(schema)
	return err
}

// SaveBacktestResults 批量保存回测结果
func (s *Store) SaveBacktestResults(results []BacktestResult) error {
	if len(results) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO backtest_result (
			backtest_run_id, config, entry_date, code, signal_type, direction,
			entry_price, exit_date, exit_price, holding_days, return_pct, win,
			score_total, adx, rs_rank_pct, sar_stance, td_setup, max_adverse_pct
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, r := range results {
		_, err = stmt.Exec(
			r.BacktestRunID, r.Config, r.EntryDate, r.Code, r.SignalType, r.Direction,
			r.EntryPrice, r.ExitDate, r.ExitPrice, r.HoldingDays, r.ReturnPct, r.Win,
			r.ScoreTotal, r.ADX, r.RSRankPct, r.SARStance, r.TDSetup, r.MaxAdversePct,
		)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// SaveBacktestSummary 保存回测汇总
func (s *Store) SaveBacktestSummary(summary BacktestSummary) error {
	_, err := s.db.Exec(`
		INSERT INTO backtest_summary (
			backtest_run_id, config, start_date, end_date,
			total_signals, win_count, win_rate, avg_return, median_return,
			best_return, worst_return, signal_stats,
			bull_market_win_rate, bear_market_win_rate,
			max_drawdown, sharpe_ratio, duration_ms
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, summary.BacktestRunID, summary.Config, summary.StartDate, summary.EndDate,
		summary.TotalSignals, summary.WinCount, summary.WinRate, summary.AvgReturn,
		summary.MedianReturn, summary.BestReturn, summary.WorstReturn, summary.SignalStats,
		summary.BullMarketWinRate, summary.BearMarketWinRate,
		summary.MaxDrawdown, summary.SharpeRatio, summary.DurationMS)

	return err
}

// GetBacktestSummary 获取回测汇总
func (s *Store) GetBacktestSummary(runID string) (*BacktestSummary, error) {
	var summary BacktestSummary
	err := s.db.QueryRow(`
		SELECT backtest_run_id, run_date, config, start_date, end_date,
			total_signals, win_count, win_rate, avg_return, median_return,
			best_return, worst_return, signal_stats,
			bull_market_win_rate, bear_market_win_rate,
			max_drawdown, sharpe_ratio, duration_ms
		FROM backtest_summary
		WHERE backtest_run_id = ?
	`, runID).Scan(
		&summary.BacktestRunID, &summary.RunDate, &summary.Config,
		&summary.StartDate, &summary.EndDate,
		&summary.TotalSignals, &summary.WinCount, &summary.WinRate,
		&summary.AvgReturn, &summary.MedianReturn, &summary.BestReturn, &summary.WorstReturn,
		&summary.SignalStats, &summary.BullMarketWinRate, &summary.BearMarketWinRate,
		&summary.MaxDrawdown, &summary.SharpeRatio, &summary.DurationMS,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("backtest run not found: %s", runID)
	}

	return &summary, err
}

// ListBacktestSummaries 列出所有回测汇总
func (s *Store) ListBacktestSummaries(limit int) ([]BacktestSummary, error) {
	if limit <= 0 {
		limit = 20
	}

	rows, err := s.db.Query(`
		SELECT backtest_run_id, run_date, config, start_date, end_date,
			total_signals, win_count, win_rate, avg_return, median_return,
			best_return, worst_return, signal_stats,
			bull_market_win_rate, bear_market_win_rate,
			max_drawdown, sharpe_ratio, duration_ms
		FROM backtest_summary
		ORDER BY run_date DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	summaries := []BacktestSummary{}
	for rows.Next() {
		var sum BacktestSummary
		err := rows.Scan(
			&sum.BacktestRunID, &sum.RunDate, &sum.Config,
			&sum.StartDate, &sum.EndDate,
			&sum.TotalSignals, &sum.WinCount, &sum.WinRate,
			&sum.AvgReturn, &sum.MedianReturn, &sum.BestReturn, &sum.WorstReturn,
			&sum.SignalStats, &sum.BullMarketWinRate, &sum.BearMarketWinRate,
			&sum.MaxDrawdown, &sum.SharpeRatio, &sum.DurationMS,
		)
		if err != nil {
			return nil, err
		}
		summaries = append(summaries, sum)
	}

	return summaries, rows.Err()
}

// GetBacktestResults 获取回测详细结果
func (s *Store) GetBacktestResults(runID string) ([]BacktestResult, error) {
	rows, err := s.db.Query(`
		SELECT id, backtest_run_id, run_date, config,
			entry_date, code, signal_type, direction, entry_price,
			exit_date, exit_price, holding_days, return_pct, win,
			score_total, adx, rs_rank_pct, sar_stance, td_setup, max_adverse_pct
		FROM backtest_result
		WHERE backtest_run_id = ?
		ORDER BY entry_date, code
	`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := []BacktestResult{}
	for rows.Next() {
		var r BacktestResult
		var runDate time.Time
		err := rows.Scan(
			&r.ID, &r.BacktestRunID, &runDate, &r.Config,
			&r.EntryDate, &r.Code, &r.SignalType, &r.Direction, &r.EntryPrice,
			&r.ExitDate, &r.ExitPrice, &r.HoldingDays, &r.ReturnPct, &r.Win,
			&r.ScoreTotal, &r.ADX, &r.RSRankPct, &r.SARStance, &r.TDSetup, &r.MaxAdversePct,
		)
		if err != nil {
			return nil, err
		}
		r.RunDate = runDate.Format("2006-01-02 15:04:05")
		results = append(results, r)
	}

	return results, rows.Err()
}

// SignalStat 信号统计
type SignalStat struct {
	Type      string    `json:"type"`
	Total     int       `json:"total"`
	Wins      int       `json:"wins"`
	WinRate   float64   `json:"win_rate"`
	AvgReturn float64   `json:"avg_return"`
	Returns   []float64 `json:"-"` // 临时存储，不序列化
}

// MarshalSignalStats 序列化信号统计
func MarshalSignalStats(stats map[string]SignalStat) (string, error) {
	// 计算统计值
	for key, stat := range stats {
		if stat.Total > 0 {
			stat.WinRate = float64(stat.Wins) / float64(stat.Total) * 100
			stat.AvgReturn = mean(stat.Returns)
		}
		stats[key] = stat
	}

	data, err := json.Marshal(stats)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// UnmarshalSignalStats 反序列化信号统计
func UnmarshalSignalStats(data string) (map[string]SignalStat, error) {
	var stats map[string]SignalStat
	err := json.Unmarshal([]byte(data), &stats)
	return stats, err
}

// 辅助函数：计算平均值
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
