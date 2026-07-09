package main

import (
	"fmt"
)

// cmdCheckData runs data quality checks on the snapshot database.
func cmdCheckData(args []string) error {
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	fmt.Println("=== 数据质量检查 ===")

	// 1. Check RS20 coverage
	fmt.Println("## 1. RS20 覆盖率检查")
	var latestDate string
	var totalSnaps, coveredRS int
	err = st.DB().QueryRow(`
		SELECT trade_date, COUNT(*) as total, SUM(CASE WHEN rs20 IS NOT NULL THEN 1 ELSE 0 END) as covered
		FROM snapshot
		WHERE trade_date = (SELECT MAX(trade_date) FROM snapshot)
		GROUP BY trade_date
	`).Scan(&latestDate, &totalSnaps, &coveredRS)
	if err != nil {
		fmt.Printf("❌ 查询失败: %v\n\n", err)
	} else {
		coverage := float64(coveredRS) / float64(totalSnaps) * 100
		status := "✅"
		if coverage < 90 {
			status = "⚠️"
		}
		fmt.Printf("%s 最新日期: %s\n", status, latestDate)
		fmt.Printf("%s RS20 覆盖率: %.1f%% (%d/%d)\n", status, coverage, coveredRS, totalSnaps)
		if coverage < 90 {
			fmt.Printf("   建议运行: go run ./cmd/stockdb rs-rank\n")
		}
		fmt.Println()
	}

	// 2. Check snapshot continuity (last 3 trading days)
	fmt.Println("## 2. Snapshot 连续性检查（最近3个交易日）")
	rows, err := st.DB().Query(`
		SELECT trade_date, COUNT(*) as count
		FROM snapshot
		WHERE trade_date >= (
			SELECT DISTINCT trade_date FROM snapshot
			ORDER BY trade_date DESC LIMIT 1 OFFSET 2
		)
		GROUP BY trade_date
		ORDER BY trade_date DESC
		LIMIT 3
	`)
	if err != nil {
		fmt.Printf("❌ 查询失败: %v\n\n", err)
	} else {
		defer rows.Close()
		dates := make(map[string]int)
		for rows.Next() {
			var date string
			var count int
			rows.Scan(&date, &count)
			dates[date] = count
		}
		if len(dates) >= 3 {
			fmt.Printf("✅ 最近3个交易日数据完整\n")
			for date, count := range dates {
				fmt.Printf("   %s: %d 条快照\n", date, count)
			}
		} else if len(dates) == 0 {
			fmt.Printf("❌ 无快照数据\n")
		} else {
			fmt.Printf("⚠️  仅有 %d 个交易日数据\n", len(dates))
			for date, count := range dates {
				fmt.Printf("   %s: %d 条快照\n", date, count)
			}
		}
		fmt.Println()
	}

	// 3. Check decision_log backfill progress
	fmt.Println("## 3. decision_log 回填进度")
	var totalDecisions, backfilledDecisions int
	err = st.DB().QueryRow(`
		SELECT
			COUNT(*) as total,
			SUM(CASE WHEN outcome_date IS NOT NULL AND outcome_date != '' THEN 1 ELSE 0 END) as backfilled
		FROM decision_log
	`).Scan(&totalDecisions, &backfilledDecisions)
	if err != nil {
		fmt.Printf("❌ 查询失败: %v\n\n", err)
	} else {
		if totalDecisions == 0 {
			fmt.Printf("ℹ️  暂无决策记录\n\n")
		} else {
			backfillRate := float64(backfilledDecisions) / float64(totalDecisions) * 100
			status := "✅"
			if backfillRate < 80 {
				status = "⚠️"
			}
			fmt.Printf("%s 总决策数: %d\n", status, totalDecisions)
			fmt.Printf("%s 已回填: %d (%.1f%%)\n", status, backfilledDecisions, backfillRate)
			pendingCount := totalDecisions - backfilledDecisions
			if pendingCount > 0 {
				fmt.Printf("   待回填: %d 条\n", pendingCount)
				fmt.Printf("   建议运行: go run ./cmd/stockdb backfill\n")
			}
			fmt.Println()
		}
	}

	// 4. Check backtest valid results
	fmt.Println("## 4. Backtest 有效结果数")
	var validResults, totalResults int
	err = st.DB().QueryRow(`
		SELECT
			COUNT(*) as total,
			SUM(CASE WHEN exit_date IS NOT NULL AND exit_date != '' THEN 1 ELSE 0 END) as valid
		FROM backtest_result
	`).Scan(&totalResults, &validResults)
	if err != nil {
		fmt.Printf("❌ 查询失败: %v\n\n", err)
	} else {
		if totalResults == 0 {
			fmt.Printf("ℹ️  暂无回测结果（需运行 backtest 命令）\n\n")
		} else {
			validRate := float64(validResults) / float64(totalResults) * 100
			status := "✅"
			if validRate < 50 {
				status = "⚠️"
			}
			fmt.Printf("%s 总回测结果: %d\n", status, totalResults)
			fmt.Printf("%s 有效结果（exit_date 完整）: %d (%.1f%%)\n", status, validResults, validRate)
			if validRate < 50 {
				fmt.Printf("   说明: 数据积累不足 T+N 天，继续每日更新即可\n")
			}
			fmt.Println()
		}
	}

	// 5. Check data accumulation days
	fmt.Println("## 5. 数据积累天数")
	var minDate, maxDate string
	var distinctDays int
	err = st.DB().QueryRow(`
		SELECT
			MIN(trade_date) as min_date,
			MAX(trade_date) as max_date,
			COUNT(DISTINCT trade_date) as days
		FROM snapshot
	`).Scan(&minDate, &maxDate, &distinctDays)
	if err != nil {
		fmt.Printf("❌ 查询失败: %v\n\n", err)
	} else {
		status := "✅"
		milestone := ""
		if distinctDays < 10 {
			status = "⚠️"
			milestone = "（需 10 天可回测 T+10）"
		} else if distinctDays < 60 {
			status = "ℹ️"
			milestone = "（需 60 天完整策略回测）"
		} else if distinctDays >= 120 {
			milestone = "（已达 120 天，统计显著）"
		} else {
			milestone = "（接近 120 天目标）"
		}
		fmt.Printf("%s 数据范围: %s → %s\n", status, minDate, maxDate)
		fmt.Printf("%s 累积天数: %d %s\n", status, distinctDays, milestone)
		fmt.Println()
	}

	// 6. Check instrument hot_score distribution
	fmt.Println("## 6. 热榜池热度分布")
	rows, err = st.DB().Query(`
		SELECT hot_score, COUNT(*) as count
		FROM instrument
		GROUP BY hot_score
		ORDER BY hot_score DESC
	`)
	if err != nil {
		fmt.Printf("❌ 查询失败: %v\n\n", err)
	} else {
		defer rows.Close()
		fmt.Printf("热度分 | 股票数\n")
		fmt.Printf("-------|-------\n")
		totalStocks := 0
		for rows.Next() {
			var score, count int
			rows.Scan(&score, &count)
			totalStocks += count
			fmt.Printf("  %d    | %d\n", score, count)
		}
		fmt.Printf("\n✅ 热榜池总数: %d 只\n", totalStocks)
		fmt.Println()
	}

	fmt.Println("=== 检查完成 ===")
	return nil
}
