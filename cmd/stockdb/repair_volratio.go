package main

import (
	"database/sql"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"stock-tui/internal/analysis"
	"stock-tui/internal/api"
	"stock-tui/internal/market"
	"stock-tui/internal/store"
)

// repairVolRatioCmd 重算历史 snapshot 的 vol_ratio。
//
// 背景：2026-07-25 之前，proxy qt 的量比错取了 qt[46]（市净率）。个股 PB 在
// 1~7 量级，远高于 VolSurge=1.5，导致落库的 vol_ratio 把几乎所有个股标成
// "放量"；ETF 无 PB 返回 0.00 触发本地回退，因而不受影响。batch-save 只写
// 当日行，历史交易日的错误值不会被覆盖，需要本命令回填。
//
// 修复口径：用完整日K按 analysis.VolRatio 重算（当日量 / 前5日均量），
// 该式已对照腾讯 qt[49] 实测吻合，因此重算值与实时值同口径、可直接比较。
//
// ⚠ inside_vol / outside_vol 无法修复：它们是当日实时盘口快照，历史不可追溯。
// 旧行的这两列方向是颠倒的，本命令会把受影响的历史行置 NULL，
// 让下游按"无数据"处理，而不是继续读到反的值。
func repairVolRatioCmd(args []string) error {
	fs := flag.NewFlagSet("repair-volratio", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dbPath := fs.String("db", "data/stock.db", "database path")
	bars := fs.Int("n", 800, "number of daily bars to fetch per stock")
	parallel := fs.Int("P", 4, "parallel fetch workers")
	dryRun := fs.Bool("dry-run", false, "只报告将要修改的行，不写库")
	keepFlow := fs.Bool("keep-flow", false, "保留 inside_vol/outside_vol（默认置 NULL，因方向已颠倒且不可追溯）")
	if err := fs.Parse(args); err != nil {
		return err
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

	// 只修最新交易日之前的行：最新日已由修正后的 batch-save 正确写入。
	var latest string
	if err := db.QueryRow(`SELECT MAX(trade_date) FROM snapshot`).Scan(&latest); err != nil {
		return fmt.Errorf("query latest trade_date: %w", err)
	}

	codes, err := codesNeedingRepair(db, latest)
	if err != nil {
		return err
	}
	if len(codes) == 0 {
		fmt.Println("repair-volratio: 无需修复的历史行")
		return nil
	}
	fmt.Fprintf(os.Stderr, "repair-volratio: %d 只标的有 %s 之前的历史行待修\n", len(codes), latest)

	if *dryRun {
		var rows int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM snapshot WHERE trade_date < ?`, latest,
		).Scan(&rows); err != nil {
			return err
		}
		fmt.Printf("repair-volratio: dry-run —— 将重算 %d 只标的、%d 行的 vol_ratio", len(codes), rows)
		if !*keepFlow {
			fmt.Printf("，并将其 inside_vol/outside_vol 置 NULL")
		}
		fmt.Println("，未写库")
		return nil
	}

	var dbLock sync.Mutex
	var wg sync.WaitGroup
	jobs := make(chan string)
	var okCount, errCount, rowCount int

	for w := 0; w < *parallel; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// 每 worker 独立 client，DisableKeepAlives 规避东财反限流（同 batch-save）。
			client := &http.Client{
				Timeout:   30 * time.Second,
				Transport: &http.Transport{DisableKeepAlives: true},
			}
			for code := range jobs {
				n, err := repairOneCode(db, &dbLock, client, code, *bars, latest, *keepFlow)
				dbLock.Lock()
				if err != nil {
					errCount++
					fmt.Fprintf(os.Stderr, "ERR %s: %v\n", code, err)
				} else {
					okCount++
					rowCount += n
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

	fmt.Printf("repair-volratio: %d 只成功(%d 行重算), %d 只失败\n", okCount, rowCount, errCount)
	return nil
}

// codesNeedingRepair 返回存在历史行（早于 latest）的代码列表。
func codesNeedingRepair(db *sql.DB, latest string) ([]string, error) {
	rows, err := db.Query(
		`SELECT DISTINCT code FROM snapshot WHERE trade_date < ? ORDER BY code`, latest)
	if err != nil {
		return nil, fmt.Errorf("query codes: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// repairOneCode 拉取单只标的的完整日K，按日期重算历史行的 vol_ratio。
// 返回实际更新的行数。
func repairOneCode(db *sql.DB, lock *sync.Mutex, client *http.Client, code string,
	bars int, latest string, keepFlow bool) (int, error) {

	ck, ok := market.NormalizeCode(code)
	if !ok {
		return 0, fmt.Errorf("invalid code")
	}
	data, err := api.FetchDailyKline(client, ck, bars)
	if err != nil {
		return 0, fmt.Errorf("fetch: %w", err)
	}
	if len(data.Candles) == 0 {
		return 0, fmt.Errorf("empty klines")
	}

	// 按日期索引重算值，避免依赖 snapshot 与 K 线的行序一致。
	byDate := make(map[string]float64, len(data.Dates))
	for i := range data.Candles {
		if vr := analysis.VolRatio(data.Candles, i); vr > 0 {
			byDate[data.Dates[i]] = vr
		}
	}
	if len(byDate) == 0 {
		return 0, fmt.Errorf("no computable vol_ratio")
	}

	lock.Lock()
	defer lock.Unlock()

	dates, err := historyDates(db, code, latest)
	if err != nil {
		return 0, err
	}

	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	stmt := `UPDATE snapshot SET vol_ratio = ? WHERE code = ? AND trade_date = ?`
	if !keepFlow {
		stmt = `UPDATE snapshot SET vol_ratio = ?, inside_vol = NULL, outside_vol = NULL
		        WHERE code = ? AND trade_date = ?`
	}
	updated := 0
	for _, d := range dates {
		vr, ok := byDate[d]
		if !ok {
			continue // K 线不含该日（停牌/数据缺口），保持原值
		}
		if _, err := tx.Exec(stmt, vr, code, d); err != nil {
			return 0, fmt.Errorf("update %s@%s: %w", code, d, err)
		}
		updated++
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return updated, nil
}

// historyDates 返回该代码早于 latest 的所有交易日。
func historyDates(db *sql.DB, code, latest string) ([]string, error) {
	rows, err := db.Query(
		`SELECT trade_date FROM snapshot WHERE code = ? AND trade_date < ? ORDER BY trade_date`,
		code, latest)
	if err != nil {
		return nil, fmt.Errorf("query dates %s: %w", code, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
