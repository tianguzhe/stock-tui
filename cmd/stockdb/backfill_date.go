package main

import (
	"database/sql"
	"flag"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"stock-tui/internal/api"
	"stock-tui/internal/market"
	"stock-tui/internal/store"
)

// backfillDateCmd 补写某个漏跑交易日的 snapshot。
//
// 背景：batch-save 只写"日K最后一根"对应的当日快照，某天漏跑后再跑也只会写当天，
// 缺失日无法补回。2026-07-20（周一）即因此在库中完全缺失，导致跨该日的窗口计算出现空洞。
//
// 与 repair-scores 的区别：后者遍历 snapshot **已有行**做重算，缺失日一行都没有、
// 因而不会被处理；本命令从相邻交易日的标的集合反推当日应有的股票池。
//
// 口径保证：与 repair-scores 一样把 KlineData 截断到目标交易日后调用**同一个**
// buildSnapshot，因此结果与当日 batch-save 逐字段一致，且天然无前视偏差。
//
// 两处刻意不做：
//   - 不写 instrument：目标日之后可能有标的被热榜清理，补历史快照不应把它们加回池子
//   - 不填 turnover_rate/market_cap/pe：来自实时行情接口，历史不可追溯（同 repair-scores）
func backfillDateCmd(args []string) error {
	fs := flag.NewFlagSet("backfill-date", flag.ContinueOnError)
	dbPath := fs.String("db", "data/stock.db", "database path")
	date := fs.String("date", "", "目标交易日 YYYY-MM-DD（必填）")
	bars := fs.Int("n", 800, "number of daily bars to fetch per stock")
	parallel := fs.Int("P", 4, "parallel fetch workers")
	dryRun := fs.Bool("dry-run", false, "只报告将要补写的标的数，不写库")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *date == "" {
		return fmt.Errorf("--date is required, e.g. --date 2026-07-20")
	}
	if *bars <= 0 {
		return fmt.Errorf("-n must be positive")
	}
	if *parallel <= 0 {
		return fmt.Errorf("-P must be positive")
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	db := st.DB()

	var existing int
	if err := db.QueryRow(`SELECT COUNT(*) FROM snapshot WHERE trade_date = ?`, *date).Scan(&existing); err != nil {
		return fmt.Errorf("count existing: %w", err)
	}

	codes, prev, next, err := codesAroundDate(db, *date)
	if err != nil {
		return err
	}
	if len(codes) == 0 {
		return fmt.Errorf("找不到 %s 相邻交易日的标的，无法反推股票池", *date)
	}

	fmt.Fprintf(os.Stderr, "backfill-date %s: 相邻交易日 %s / %s，股票池 %d 只（当前该日已有 %d 行）\n",
		*date, prev, next, len(codes), existing)
	if *dryRun {
		fmt.Printf("backfill-date: dry-run —— 将尝试补写 %d 只标的，未写库\n", len(codes))
		return nil
	}

	var dbLock sync.Mutex
	var wg sync.WaitGroup
	jobs := make(chan string)
	var okCount, skipCount, errCount int
	var lastReport time.Time // 与计数器同受 dbLock 保护

	for w := 0; w < *parallel; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client := &http.Client{
				Timeout:   30 * time.Second,
				Transport: &http.Transport{DisableKeepAlives: true},
			}
			for code := range jobs {
				written, err := backfillDateOne(st, &dbLock, client, code, *date, *bars)
				dbLock.Lock()
				switch {
				case err != nil:
					errCount++
					fmt.Fprintf(os.Stderr, "ERR %s: %v\n", code, err)
				case written:
					okCount++
				default:
					skipCount++
				}
				// 全池数百只时单次运行需数分钟，无输出会让人误以为卡死（同 batch-save）。
				if done := okCount + skipCount + errCount; time.Since(lastReport) > 5*time.Second {
					fmt.Fprintf(os.Stderr, "progress: %d/%d (%d 写入, %d 跳过, %d 失败)\n",
						done, len(codes), okCount, skipCount, errCount)
					lastReport = time.Now()
				}
				dbLock.Unlock()
			}
		}()
	}
	for _, c := range codes {
		jobs <- c
	}
	close(jobs)
	wg.Wait()

	fmt.Printf("backfill-date %s: %d 写入, %d 跳过, %d 失败（共 %d 只）\n",
		*date, okCount, skipCount, errCount, len(codes))
	if skipCount > 0 {
		fmt.Println("  跳过 = 日K中无该交易日（停牌/当时未上市）")
	}
	return nil
}

// codesAroundDate 用目标日前后最近交易日出现过的代码并集，反推当日应有的股票池。
//
// 不用 instrument 表：该表随热榜每日增删，补历史日时它反映的是"现在"的池子，
// 会漏掉当时在池、之后被清理的标的。
func codesAroundDate(db *sql.DB, date string) ([]string, string, string, error) {
	var prev, next sql.NullString
	if err := db.QueryRow(
		`SELECT MAX(trade_date) FROM snapshot WHERE trade_date < ?`, date).Scan(&prev); err != nil {
		return nil, "", "", fmt.Errorf("query prev trade_date: %w", err)
	}
	if err := db.QueryRow(
		`SELECT MIN(trade_date) FROM snapshot WHERE trade_date > ?`, date).Scan(&next); err != nil {
		return nil, "", "", fmt.Errorf("query next trade_date: %w", err)
	}
	if !prev.Valid && !next.Valid {
		return nil, "", "", fmt.Errorf("snapshot 表中没有 %s 前后的任何交易日", date)
	}

	rows, err := db.Query(
		`SELECT DISTINCT code FROM snapshot WHERE trade_date IN (?, ?) ORDER BY code`,
		prev, next)
	if err != nil {
		return nil, "", "", fmt.Errorf("query codes: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, "", "", err
		}
		out = append(out, c)
	}
	return out, prev.String, next.String, rows.Err()
}

// backfillDateOne 补写单只标的在 date 当日的快照。
// 返回 (是否写入, error)；日K中无该交易日时返回 (false, nil) 表示跳过。
func backfillDateOne(st *store.Store, lock *sync.Mutex, client *http.Client,
	code, date string, bars int) (bool, error) {

	ck, ok := market.NormalizeCode(code)
	if !ok {
		return false, fmt.Errorf("invalid code")
	}
	data, err := api.FetchDailyKline(client, ck, bars)
	if err != nil {
		return false, fmt.Errorf("fetch: %w", err)
	}
	if len(data.Candles) == 0 {
		return false, fmt.Errorf("empty klines")
	}
	// truncateKline 按 Dates 的下标切 Candles，两者不等长会越界 panic。
	if len(data.Dates) != len(data.Candles) {
		return false, fmt.Errorf("kline dates/candles 长度不一致: %d vs %d",
			len(data.Dates), len(data.Candles))
	}

	idx := -1
	for i, d := range data.Dates {
		if d == date {
			idx = i
			break
		}
	}
	if idx < 0 {
		// 目标日早于取到的最早一根K线 = -n 不够回溯，而非该标的当日无交易。
		// 两者都表现为"找不到该日"，但前者是可纠正的调用错误，必须报出来，
		// 否则整批标的会被静默计入"跳过"，看起来像正常结果。
		if date < data.Dates[0] {
			return false, fmt.Errorf("目标日早于最早K线 %s，需增大 -n（当前 %d）",
				data.Dates[0], bars)
		}
		return false, nil // 停牌或当时未上市
	}

	snap := buildSnapshot(truncateKline(data, idx))
	if snap.TradeDate != date {
		return false, nil // 截断后末根日期不符，保守跳过
	}

	lock.Lock()
	defer lock.Unlock()

	// 该日已有行时保留其实时行情字段：本命令也用于补"部分缺失"的交易日，
	// buildSnapshot 不产出这些值，直接覆盖会把已有的 turnover_rate 等清零。
	keep, found, err := existingPreserved(st.DB(), code, date)
	if err != nil {
		return false, err
	}
	if found {
		applyPreserved(&snap, keep)
	}

	if err := st.SaveSnapshot(snap); err != nil {
		return false, fmt.Errorf("save: %w", err)
	}
	return true, nil
}

// existingPreserved 读出 code@date 已有行的实时行情字段；无该行时返回 found=false。
func existingPreserved(db *sql.DB, code, date string) (preservedFields, bool, error) {
	var p preservedFields
	err := db.QueryRow(
		`SELECT turnover_rate, market_cap, pe, inside_vol, outside_vol
		   FROM snapshot WHERE code = ? AND trade_date = ?`, code, date,
	).Scan(&p.turnoverRate, &p.marketCap, &p.pe, &p.insideVol, &p.outsideVol)
	if err == sql.ErrNoRows {
		return p, false, nil
	}
	if err != nil {
		return p, false, fmt.Errorf("read existing %s@%s: %w", code, date, err)
	}
	return p, true, nil
}
