package main

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"stock-tui/internal/holdings"
	"stock-tui/internal/screener"
	"stock-tui/internal/store"
)

// resolveHoldings takes positions from the --holdings flag when it is given and
// falls back to the portfolio file otherwise, so the daily run needs no hand-
// assembled argument. Either way duplicate codes are merged by share-weighted
// cost — the same position held in two brokerage accounts must screen as one
// row, and hand-merging it every day is what this replaces.
//
// A missing portfolio file is not an error: screening still runs for candidates.
func resolveHoldings(raw, path string) ([]screener.Holding, error) {
	var (
		hs  []holdings.Holding
		err error
	)
	if strings.TrimSpace(raw) != "" {
		hs, err = holdings.Parse(raw)
	} else {
		hs, err = holdings.Load(path)
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "info: 未找到 %s，按无持仓处理\n", path)
			return nil, nil
		}
	}
	if err != nil {
		return nil, err
	}

	out := make([]screener.Holding, len(hs))
	for i, h := range hs {
		out[i] = screener.Holding{Code: h.Code, Cost: h.Cost, Shares: h.Shares}
	}
	return out, nil
}

func runScreen(holdings []screener.Holding, maxResults int, capital float64, dryRun bool) error {
	// Load snapshots
	date, candidates, rsCoverage, err := screener.LoadSnapshots(store.DefaultPath())
	if err != nil {
		return fmt.Errorf("load snapshots: %w", err)
	}

	if rsCoverage < screener.MinRSCoverage {
		return fmt.Errorf("❌ 错误：最新日 %s RS20 覆盖率仅 %.0f%%，`go run ./cmd/stockdb rs-rank` 后需达到 90%% 以上",
			date, rsCoverage)
	}

	// Build holding codes map
	holdingCodes := make(map[string]bool)
	for _, h := range holdings {
		holdingCodes[h.Code] = true
	}

	// Filter candidates (exclude holdings)
	var filteredCandidates []screener.Candidate
	for _, c := range candidates {
		if holdingCodes[c.Code] {
			continue
		}
		tier := screener.ComputeTier(&c)
		if tier == screener.TierNone {
			continue
		}
		c.Tier = tier
		c.SortKey = screener.SortKey(&c)
		filteredCandidates = append(filteredCandidates, c)
	}

	// Sort by sort key descending
	sort.Slice(filteredCandidates, func(i, j int) bool {
		return filteredCandidates[i].SortKey > filteredCandidates[j].SortKey
	})

	// Market breadth gating
	breadth := screener.MarketBreadth(candidates)
	gated := breadth < 40
	limit := maxResults - len(holdings)
	if limit <= 0 {
		fmt.Fprintf(os.Stderr, "warn: --max=%d 不大于持仓数(%d)，候选栏将为空；如需候选请调大 --max\n",
			maxResults, len(holdings))
	}
	if gated && limit > 0 {
		limit = max(1, limit/2)
	}

	// Select top candidates
	var selected []screener.Candidate
	for _, tier := range []screener.Tier{screener.TierStar3, screener.TierStar2, screener.TierWatch} {
		for _, c := range filteredCandidates {
			if c.Tier == tier && len(selected) < limit {
				selected = append(selected, c)
			}
		}
		if len(selected) >= limit {
			break
		}
	}

	// Render output
	fmt.Printf("## 多因子选股 %s（市场广度 %.1f%%", date, breadth)
	if gated {
		fmt.Print("，闸门触发：推荐上限减半）\n\n")
	} else {
		fmt.Print("）\n\n")
	}

	// Holdings table
	if len(holdings) > 0 {
		fmt.Println("### 持仓")
		fmt.Println()
		fmt.Println("| 标签 | 代码 | 名称 | 涨跌% | 量比 | S/A | RS20 | 市值 | 换手% | 热度 | PERF背离 | 止损(距%) | 关键信号 |")
		fmt.Println("|------|------|------|-------|------|-----|------|------|-------|------|----------|------------|----------|")

		// Load holding candidates
		candMap := make(map[string]screener.Candidate)
		for _, c := range candidates {
			candMap[c.Code] = c
		}

		for _, h := range holdings {
			c, ok := candMap[h.Code]
			if !ok {
				fmt.Printf("| 持仓 | %s | — | — | — | — | — | — | — | — | — | — | 无快照数据 |\n", h.Code)
				continue
			}
			label := "持仓"
			fmt.Print(formatRow(label, &c, h.Cost, h.Shares, 0))
		}
		fmt.Println()
	}

	// Candidates table
	if len(selected) > 0 {
		fmt.Println("### 候选")
		fmt.Println()
		fmt.Println("| 标签 | 代码 | 名称 | 涨跌% | 量比 | S/A | RS20 | 市值 | 换手% | 热度 | PERF背离 | 止损(距%) | 关键信号 |")
		fmt.Println("|------|------|------|-------|------|-----|------|------|-------|------|----------|------------|----------|")

		for _, c := range selected {
			fmt.Print(formatRow(string(c.Tier), &c, 0, 0, capital))
		}
		fmt.Println()
	} else {
		fmt.Println("### 候选")
		fmt.Println()
		fmt.Println("*（当前无符合筛选条件的候选）*")
		fmt.Println()
	}

	// Decision log persistence
	if !dryRun {
		st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()

		upsertedHoldings, upsertedSelected, skippedHoldings := saveDecisions(st, date, candidates, holdings, selected)
		fmt.Printf("\n> 📝 已写入 decision_log（更新/新增 %d 持仓 + %d 候选；无快照持仓 %d）\n",
			upsertedHoldings, upsertedSelected, skippedHoldings)

		// 给持仓打上 holdings 标记，豁免热榜的冷门清理——否则低热度持仓会被
		// 淘汰出 instrument，batch-save 停止更新，持仓行读到的快照越来越旧。
		codes := make([]string, 0, len(holdings))
		for _, h := range holdings {
			codes = append(codes, h.Code)
		}
		if n, err := st.MarkHoldings(codes); err != nil {
			fmt.Fprintf(os.Stderr, "warn: 标记持仓豁免失败: %v\n", err)
		} else if n > 0 {
			fmt.Printf("> 🔒 %d 只持仓已标记为豁免热榜清理\n", n)
		}
	} else {
		fmt.Println("\n> 🔍 dry-run 模式，未写入 decision_log")
	}

	return nil
}

func formatRow(label string, c *screener.Candidate, cost float64, shares int, capital float64) string {
	sa := fmt.Sprintf("%d / %.1f", c.ScoreTotal, c.ADX)
	chg := fmt.Sprintf("%+.2f%%", c.ChangePct)
	vr := "—"
	if c.VolRatio > 0 {
		vr = fmt.Sprintf("%.2f", c.VolRatio)
	}
	rs20 := "—"
	if c.RS20.Valid {
		rs20 = fmt.Sprintf("%.0f", c.RS20.Float64)
	}
	mc := "—"
	if c.MarketCap > 0 {
		mc = fmt.Sprintf("%.0f亿", c.MarketCap)
	}
	tr := "—"
	if c.TurnoverRate > 0 {
		tr = fmt.Sprintf("%.2f%%", c.TurnoverRate)
	}
	hot := "—"
	if c.HotScore > 0 {
		hot = fmt.Sprintf("%d", c.HotScore)
	}

	// PERF divergence
	perf := "—"
	if c.PerfDivBearN.Valid && c.PerfDivBearN.Int64 > 0 {
		if c.PerfDivBearWin10.Valid {
			perf = fmt.Sprintf("N=%d,W=%.0f%%", c.PerfDivBearN.Int64, c.PerfDivBearWin10.Float64)
		} else {
			perf = fmt.Sprintf("N=%d", c.PerfDivBearN.Int64)
		}
	}

	stop := screener.StopText(c)
	signals := formatSignals(c, cost, shares, capital)

	return fmt.Sprintf("| %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |\n",
		label, c.Code, c.Name, chg, vr, sa, rs20, mc, tr, hot, perf, stop, signals)
}

func formatSignals(c *screener.Candidate, cost float64, shares int, capital float64) string {
	var parts []string

	// 加仓闸门排在最前：它是操作禁令，跟在十几个信号后面就等于没有。
	if blocked, reason := screener.AddOnBlocked(c, cost); blocked {
		parts = append(parts, reason)
	}

	// TD
	cdwn := c.TDCountdown
	td := cdwn
	if cdwn == "" || cdwn == "-/0" {
		td = c.TDSetup
	}
	if strings.Contains(td, "底") {
		parts = append(parts, fmt.Sprintf("底部序列(%s)", td))
	} else if td != "" && td != "-/0" {
		parts = append(parts, td)
	}

	// Trend stance
	sar, st := c.SARLong, c.SuperTrendLong
	if sar && st {
		parts = append(parts, "SAR/ST双多")
	} else if sar {
		parts = append(parts, "SAR多/⚠️ST翻空")
	} else if st {
		parts = append(parts, "ST多/⚠️SAR翻空")
	} else {
		parts = append(parts, "⚠️SAR/ST双空")
	}

	if c.OBVUp3Day {
		parts = append(parts, "OBV3日净流入")
	} else if c.OBVUp {
		parts = append(parts, "OBV单日净流入")
	}
	if c.MACDHist > 0 {
		parts = append(parts, fmt.Sprintf("MACD H=%.2f", c.MACDHist))
	}

	// Donchian breakout
	if c.DonchBreak55Bull {
		parts = append(parts, "破D55")
	} else if c.DonchBreak20Bull {
		parts = append(parts, "破D20")
	}

	if c.KeltnerSqueeze {
		parts = append(parts, "Squeeze压缩")
	}
	if c.DivBear {
		parts = append(parts, "⚠️顶背离")
	}

	// TD countdown warning
	cdwnTop := screener.CdwnTopN(c.TDCountdown)
	if cdwnTop > 0 {
		parts = append(parts, fmt.Sprintf("⚠️C顶%d", cdwnTop))
	}

	// Trend follow PERF
	if c.PerfTrendFollowBullAvg10.Valid && c.PerfTrendFollowBullN.Valid && c.PerfTrendFollowBullN.Int64 >= 10 {
		parts = append(parts, fmt.Sprintf("趋势A10=%+.1f%%", c.PerfTrendFollowBullAvg10.Float64))
	}

	// Profit or position hint
	if cost > 0 && shares > 0 {
		// shares is in 手 (1手=100股); scale to shares to compute yuan PnL.
		profit := (c.Close - cost) * float64(shares) * 100
		profitPct := (c.Close/cost - 1) * 100
		parts = append(parts, fmt.Sprintf("浮盈%+.0f（%+.1f%%）", profit, profitPct))
	} else if capital > 0 {
		if hint := screener.PositionHint(c, capital); hint != "" {
			parts = append(parts, hint)
		}
	}

	if len(parts) == 0 {
		return "—"
	}
	return strings.Join(parts, "，")
}

func saveDecisions(st *store.Store, date string, candidates []screener.Candidate,
	holdings []screener.Holding, selected []screener.Candidate) (upsertedHoldings, upsertedSelected, skippedHoldings int) {

	// Build candidate map
	candMap := make(map[string]screener.Candidate)
	for _, c := range candidates {
		candMap[c.Code] = c
	}

	now := time.Now().Format(time.RFC3339)

	// Save holdings
	for _, h := range holdings {
		c, ok := candMap[h.Code]
		if !ok {
			skippedHoldings++
			continue
		}
		_, err := st.DB().Exec(`
			INSERT INTO decision_log (code, log_date, action, tier, score_total, adx, sar_long, st_long, obv_up, macd_hist, td_countdown, signals, created_at)
			VALUES (?, ?, 'hold', '持仓', ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(code, log_date, action) DO UPDATE SET
				tier='持仓', score_total=excluded.score_total, adx=excluded.adx,
				sar_long=excluded.sar_long, st_long=excluded.st_long, obv_up=excluded.obv_up,
				macd_hist=excluded.macd_hist, td_countdown=excluded.td_countdown, signals=excluded.signals
		`, c.Code, date, c.ScoreTotal, c.ADX, boolToInt(c.SARLong), boolToInt(c.SuperTrendLong),
			boolToInt(c.OBVUp), c.MACDHist, c.TDCountdown, formatSignals(&c, h.Cost, h.Shares, 0), now)
		if err != nil {
			// Never swallow — report which holding failed so the user sees misses,
			// since the summary count otherwise hides dropped rows.
			fmt.Fprintf(os.Stderr, "warning: 持仓 %s 写入 decision_log 失败: %v\n", c.Code, err)
			continue
		}
		upsertedHoldings++
	}

	// Save selected candidates
	for _, c := range selected {
		_, err := st.DB().Exec(`
			INSERT INTO decision_log (code, log_date, action, tier, score_total, adx, sar_long, st_long, obv_up, macd_hist, td_countdown, signals, created_at)
			VALUES (?, ?, 'select', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(code, log_date, action) DO UPDATE SET
				tier=excluded.tier, score_total=excluded.score_total, adx=excluded.adx,
				sar_long=excluded.sar_long, st_long=excluded.st_long, obv_up=excluded.obv_up,
				macd_hist=excluded.macd_hist, td_countdown=excluded.td_countdown, signals=excluded.signals
		`, c.Code, date, c.Tier, c.ScoreTotal, c.ADX, boolToInt(c.SARLong), boolToInt(c.SuperTrendLong),
			boolToInt(c.OBVUp), c.MACDHist, c.TDCountdown, formatSignals(&c, 0, 0, 0), now)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: 候选 %s 写入 decision_log 失败: %v\n", c.Code, err)
			continue
		}
		upsertedSelected++
	}

	return
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
