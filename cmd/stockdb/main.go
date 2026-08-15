// Command stockdb manages the tagged instrument list and queries analysis
// snapshots persisted by `indicator-analyze -save` into the SQLite store.
//
// Subcommands:
//
//	stockdb tag add <code> <标签>     attach a sector/group tag
//	stockdb tag rm  <code> <标签>     detach a tag
//	stockdb list --tag <标签>         list instruments under a tag
//	stockdb history <code> [-n 15]    show a symbol's snapshot history
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"stock-tui/internal/api"
	"stock-tui/internal/backtest"
	"stock-tui/internal/holdings"
	"stock-tui/internal/market"
	"stock-tui/internal/store"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return usageErr()
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "tag":
		return cmdTag(rest)
	case "list":
		return cmdList(rest)
	case "history":
		return cmdHistory(rest)
	case "rs-rank":
		return cmdRSRank(rest)
	case "backfill":
		return cmdBackfill(rest)
	case "backtest":
		return cmdBacktest(rest)
	case "backtest-portfolio":
		return cmdBacktestPortfolio(rest)
	case "screen":
		return cmdScreen(rest)
	case "hot":
		return cmdHot(rest)
	case "check-data":
		return cmdCheckData(rest)
	case "batch-save":
		return batchSaveCmd(rest)
	case "backfill-date":
		return backfillDateCmd(rest)
	case "repair-volratio":
		return repairVolRatioCmd(rest)
	case "repair-scores":
		return repairScoresCmd(rest)
	default:
		return usageErr()
	}
}

func usageErr() error {
	return fmt.Errorf(`usage:
  stockdb tag add <code> <标签>
  stockdb tag rm  <code> <标签>
  stockdb list --tag <标签>
  stockdb history <code> [-n 15]
  stockdb rs-rank [--date D]              compute RS20/RS60/RS120 percentile ranks
  stockdb backfill                        backfill decision_log outcomes from snapshots
  stockdb backtest [options]              run strategy backtest
  stockdb backtest-portfolio [options]    run portfolio backtest with position management
  stockdb screen [options]                multi-factor stock screening (replaces screen-stocks.py)
  stockdb hot [--top N]                   fetch THS hot list and import into instrument table
  stockdb check-data                      data quality checks (RS coverage, continuity, backfill progress)
  stockdb batch-save [-P N] [-n N]      batch-save analysis snapshots for all instruments
  stockdb backfill-date --date D        backfill snapshots for a missed trading day
  stockdb repair-volratio [--dry-run]   重算历史 snapshot 的 vol_ratio(修 qt[46] 误取市净率)
  stockdb repair-scores [--dry-run]     按完整日K重算历史 snapshot 的全部指标与评分
`)
}

func openStore() (*store.Store, error) {
	return store.Open(store.DefaultPath())
}

// normalize maps a user-typed code to the provider form, erroring on bad input.
func normalize(raw string) (string, error) {
	code, ok := market.NormalizeCode(raw)
	if !ok {
		return "", fmt.Errorf("invalid code: %s", raw)
	}
	return code, nil
}

func cmdTag(args []string) error {
	if len(args) != 3 {
		return fmt.Errorf("usage: stockdb tag <add|rm> <code> <标签>")
	}
	action := args[0]
	code, err := normalize(args[1])
	if err != nil {
		return err
	}
	tag := args[2]

	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	switch action {
	case "add":
		if err := st.AddTag(code, tag); err != nil {
			return err
		}
		fmt.Printf("tagged %s with %q\n", code, tag)
	case "rm":
		if err := st.RemoveTag(code, tag); err != nil {
			return err
		}
		fmt.Printf("removed tag %q from %s\n", tag, code)
	default:
		return fmt.Errorf("unknown tag action %q (want add|rm)", action)
	}
	return nil
}

func cmdList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	tag := fs.String("tag", "", "filter by tag")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *tag == "" {
		return fmt.Errorf("usage: stockdb list --tag <标签>")
	}

	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	items, err := st.ListByTag(*tag)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		fmt.Printf("(无标的属于标签 %q)\n", *tag)
		return nil
	}
	fmt.Printf("标签 %q (%d 只):\n", *tag, len(items))
	for _, in := range items {
		name := in.Name
		if name == "" {
			name = "(未分析)"
		}
		fmt.Printf("  %-10s %s\n", in.Code, name)
	}
	return nil
}

func cmdHistory(args []string) error {
	fs := flag.NewFlagSet("history", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	limit := fs.Int("n", 15, "number of recent snapshots")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: stockdb history <code> [-n 15]")
	}
	code, err := normalize(fs.Arg(0))
	if err != nil {
		return err
	}
	if *limit <= 0 {
		return fmt.Errorf("-n must be positive")
	}

	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	rows, err := st.History(code, *limit)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Printf("(%s 暂无快照, 先用 indicator-analyze -save %s)\n", code, code)
		return nil
	}
	fmt.Printf("%s 近 %d 条快照演变:\n", code, len(rows))
	fmt.Printf("%-12s %8s %7s %7s %6s %6s %6s  %s\n", "date", "close", "pct%", "SCORE", "Δ", "J", "ADX", "TD")
	for _, r := range rows {
		fmt.Printf("%-12s %8.3f %+6.2f %5d %+5d %6.1f %6.1f  setup=%s cd=%s\n",
			r.TradeDate, r.Close, r.ChangePct, r.ScoreTotal, r.ScoreDelta, r.KDJ_J, r.ADX, r.TDSetup, r.TDCountdown)
	}
	return nil
}

func cmdRSRank(args []string) error {
	fs := flag.NewFlagSet("rs-rank", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	date := fs.String("date", "", "指定交易日 YYYY-MM-DD（默认最新交易日）")
	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	// 指定日期用于补算 backfill-date 补出来的历史行——它们的 rs 列为 0。
	var n int
	target := "最新交易日"
	if *date != "" {
		target = *date
		n, err = st.UpdateRSRankingsForDate(*date)
	} else {
		n, err = st.UpdateRSRankings()
	}
	if err != nil {
		return fmt.Errorf("rs-rank: %w", err)
	}
	fmt.Printf("rs-rank: updated %d stocks (%s)\n", n, target)
	return nil
}

func cmdBackfill(_ []string) error {
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	pending, err := st.PendingDecisions()
	if err != nil {
		return err
	}
	if len(pending) == 0 {
		fmt.Println("backfill: 无待回填的决策记录")
		return nil
	}

	updated, skipped := 0, 0
	for _, d := range pending {
		close10, date10, err := st.CloseAfter(d.Code, d.LogDate, 10)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warn: %s: %v\n", d.Code, err)
			skipped++
			continue
		}
		if close10 == 0 {
			skipped++
			continue
		}
		// Fetch entry close from snapshot on log_date.
		entryClose, err := st.CloseOnDate(d.Code, d.LogDate)
		if err != nil || entryClose == 0 {
			fmt.Fprintf(os.Stderr, "warn: %s@%s: no entry close\n", d.Code, d.LogDate)
			skipped++
			continue
		}
		pct := (close10/entryClose - 1) * 100
		correct := pct > 0
		if err := st.BackfillDecision(d.ID, pct, date10, correct); err != nil {
			fmt.Fprintf(os.Stderr, "warn: backfill %d: %v\n", d.ID, err)
			skipped++
			continue
		}
		updated++
	}

	fmt.Printf("backfill: %d 条已回填, %d 条跳过（数据不足）\n", updated, skipped)

	// Print summary stats.
	stats, err := st.StatsByTier()
	if err != nil {
		return err
	}
	if len(stats) > 0 {
		fmt.Printf("\n%-10s %5s %5s %10s %8s\n", "tier", "N", "wins", "avg_ret%", "win%")
		for _, s := range stats {
			fmt.Printf("%-10s %5d %5d %+10.2f %7.1f%%\n",
				s.Tier, s.Count, s.Wins, s.AvgReturn, s.WinRate)
		}
	}
	return nil
}

func cmdBacktest(args []string) error {
	fs := flag.NewFlagSet("backtest", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	startDate := fs.String("start", "2025-01-01", "开始日期 (YYYY-MM-DD)")
	endDate := fs.String("end", time.Now().Format("2006-01-02"), "结束日期 (YYYY-MM-DD)")
	signals := fs.String("signals", "all", "信号类型（逗号分隔，或 all）")
	holdingDays := fs.Int("days", 10, "持有天数")
	stopLoss := fs.Float64("stop-loss", 0, "止损百分比（0=不止损）")
	verbose := fs.Bool("verbose", false, "详细日志")

	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	// 初始化回测表
	if err := st.InitBacktestTables(); err != nil {
		return fmt.Errorf("初始化回测表失败: %w", err)
	}

	// 解析信号类型
	var signalList []string
	if *signals == "all" {
		// 趋势跟随空头 / 量价突破多头 / 量价突破空头 暂无 snapshot 落库列,
		// findSignals 只能读为硬编码 0、永不触发,故 all 不含这三类。
		signalList = []string{
			"趋势跟随多头",
			"超买反转空头", "超卖反转多头",
			"顶背离空头", "底背离多头",
		}
	} else {
		signalList = strings.Split(*signals, ",")
	}

	config := backtest.Config{
		StartDate:   *startDate,
		EndDate:     *endDate,
		Signals:     signalList,
		HoldingDays: *holdingDays,
		StopLoss:    *stopLoss,
		Verbose:     *verbose,
	}

	engine := backtest.NewEngine(st.DB(), st, config)
	runID, err := engine.Run()
	if err != nil {
		return err
	}

	fmt.Printf("\n回测完成！运行ID: %s\n", runID)
	fmt.Printf("查看详情: SELECT * FROM backtest_result WHERE backtest_run_id='%s' LIMIT 20;\n", runID)
	fmt.Printf("查看汇总: SELECT * FROM backtest_summary WHERE backtest_run_id='%s';\n", runID)

	return nil
}

func cmdBacktestPortfolio(args []string) error {
	fs := flag.NewFlagSet("backtest-portfolio", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	startDate := fs.String("start", "2025-01-01", "开始日期 (YYYY-MM-DD)")
	endDate := fs.String("end", time.Now().Format("2006-01-02"), "结束日期 (YYYY-MM-DD)")
	signals := fs.String("signals", "all", "信号类型（逗号分隔，或 all）")
	holdingDays := fs.Int("days", 10, "持有天数")
	stopLoss := fs.Float64("stop-loss", 8.0, "止损百分比")
	takeProfit := fs.Float64("take-profit", 0, "止盈百分比（0=不止盈）")
	capital := fs.Float64("capital", 100000, "初始资金")
	maxPos := fs.Int("max-positions", 5, "最大持仓数")
	posSize := fs.Float64("position-size", 0.2, "单笔仓位比例（0.2=20%）")
	verbose := fs.Bool("verbose", false, "详细日志")

	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	// 解析信号类型
	var signalList []string
	if *signals == "all" {
		// 量价突破多头 无 snapshot 落库列,回测永不触发,故 all 不含。
		signalList = []string{
			"趋势跟随多头", "超买反转空头", "超卖反转多头",
			"顶背离空头", "底背离多头",
		}
	} else {
		signalList = strings.Split(*signals, ",")
	}

	config := backtest.PortfolioConfig{
		Config: backtest.Config{
			StartDate:   *startDate,
			EndDate:     *endDate,
			Signals:     signalList,
			HoldingDays: *holdingDays,
			StopLoss:    *stopLoss,
			Verbose:     *verbose,
		},
		InitialCapital: *capital,
		MaxPositions:   *maxPos,
		PositionSize:   *posSize,
		TakeProfit:     *takeProfit,
	}

	engine := backtest.NewPortfolioEngine(st.DB(), st, config)
	return engine.Run()
}

func cmdHot(args []string) error {
	fs := flag.NewFlagSet("hot", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	topN := fs.Int("top", 0, "only import the top N hottest stocks (0=all)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	stocks, err := api.FetchHotStocks()
	if err != nil {
		return fmt.Errorf("hot: %w", err)
	}
	if *topN > 0 && len(stocks) > *topN {
		stocks = stocks[:*topN]
	}

	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	entries := make([]store.HotStockEntry, len(stocks))
	for i, s := range stocks {
		entries[i] = store.HotStockEntry{
			Code:   s.Code,
			Name:   s.Name,
			Market: s.Market,
		}
	}

	res, err := st.ImportHotStocks(entries)
	if err != nil {
		return fmt.Errorf("hot: %w", err)
	}

	fmt.Printf("热榜共 %d 只，大盘主板 %d 只，新增入库 %d 只，热度更新 %d 只",
		len(stocks), len(entries), res.Imported, res.Refreshed)
	if res.Decayed {
		fmt.Printf("，清理冷门 %d 只", res.Pruned)
	}
	fmt.Println()
	for i, s := range stocks {
		if i >= 10 {
			fmt.Printf("  ...（及另外 %d 只）\n", len(stocks)-10)
			break
		}
		fmt.Printf("  %s  %s\n", s.Code, s.Name)
	}
	return nil
}

func cmdScreen(args []string) error {
	fs := flag.NewFlagSet("screen", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	holdingsFlag := fs.String("holdings", "", "持仓，格式：代码:成本:手数(1手=100股),...；省略时读取 .holdings 文件")
	holdingsFile := fs.String("holdings-file", holdings.DefaultPath, "持仓文件路径（--holdings 未指定时使用）")
	maxResults := fs.Int("max", 0, "持仓+候选总上限（默认：持仓数+7）")
	capital := fs.Float64("capital", 0, "总资金（元）；提供时按单笔风险1%/止损距离输出候选建议仓位")
	dryRun := fs.Bool("dry-run", false, "仅输出不写入decision_log")

	if err := fs.Parse(args); err != nil {
		return err
	}

	hs, err := resolveHoldings(*holdingsFlag, *holdingsFile)
	if err != nil {
		return err
	}

	// 默认值：持仓数 + 7 个候选
	max := *maxResults
	if max == 0 {
		max = len(hs) + 7
	}

	return runScreen(hs, max, *capital, *dryRun)
}
