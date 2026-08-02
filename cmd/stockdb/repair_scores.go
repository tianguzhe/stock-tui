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

// repairScoresCmd 按完整日K重算历史 snapshot 的全部指标与评分。
//
// 背景：`repair-volratio` 只订正了 vol_ratio 这一列，但 score 的 Volume 分项、
// EvalSignals 的 BreakBull/BreakBear 判据、以及由此派生的 PERF 统计与 score_adj
// 全都基于量比计算——历史行的这些字段仍是用错误量比（市净率）算出来的。
// 本命令按每一历史交易日重新走一遍完整的 buildSnapshot 流程。
//
// 口径保证：把 KlineData 截断到目标交易日后调用**同一个** buildSnapshot，
// 而不是另写一套重算逻辑，因此重算结果与当日 batch-save 的口径逐字段一致。
// 截断同时消除前视偏差——第 i 日只能看到 [0, i] 的数据。
//
// 保留字段：turnover_rate / market_cap / pe / inside_vol / outside_vol 来自
// 实时行情接口，历史不可追溯，一律沿用原行的值（内外盘方向颠倒的历史行已由
// repair-volratio 置 NULL）。rs20/60/120 由 rs-rank 横截面计算，不在
// SaveSnapshot 的写入列内，天然不受影响。
func repairScoresCmd(args []string) error {
	fs := flag.NewFlagSet("repair-scores", flag.ContinueOnError)
	dbPath := fs.String("db", "data/stock.db", "database path")
	bars := fs.Int("n", 800, "number of daily bars to fetch per stock")
	parallel := fs.Int("P", 4, "parallel fetch workers")
	dryRun := fs.Bool("dry-run", false, "只报告将要重算的行数，不写库")
	all := fs.Bool("all", false, "连同最新交易日一并重算（默认只修历史行）")
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

	var latest string
	if err := db.QueryRow(`SELECT MAX(trade_date) FROM snapshot`).Scan(&latest); err != nil {
		return fmt.Errorf("query latest trade_date: %w", err)
	}
	cutoff := latest
	if *all {
		cutoff = "" // 空串表示不设上界，全部重算
	}

	codes, rowTotal, err := codesNeedingScoreRepair(db, cutoff)
	if err != nil {
		return err
	}
	if len(codes) == 0 {
		fmt.Println("repair-scores: 无需重算的行")
		return nil
	}

	if *dryRun {
		scope := fmt.Sprintf("早于 %s 的历史行", latest)
		if *all {
			scope = "全部行（含最新交易日）"
		}
		fmt.Printf("repair-scores: dry-run —— 将重算 %d 只标的、%d 行（%s），未写库\n",
			len(codes), rowTotal, scope)
		return nil
	}
	fmt.Fprintf(os.Stderr, "repair-scores: %d 只标的、%d 行待重算\n", len(codes), rowTotal)

	var dbLock sync.Mutex
	var wg sync.WaitGroup
	jobs := make(chan string)
	var okCount, errCount, doneRows, skipRows int

	for w := 0; w < *parallel; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client := &http.Client{
				Timeout:   30 * time.Second,
				Transport: &http.Transport{DisableKeepAlives: true},
			}
			for code := range jobs {
				n, skipped, err := repairScoresOne(st, &dbLock, client, code, *bars, cutoff)
				dbLock.Lock()
				if err != nil {
					errCount++
					fmt.Fprintf(os.Stderr, "ERR %s: %v\n", code, err)
				} else {
					okCount++
					doneRows += n
					skipRows += skipped
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

	fmt.Printf("repair-scores: %d 只成功(%d 行重算, %d 行跳过), %d 只失败\n",
		okCount, doneRows, skipRows, errCount)
	if skipRows > 0 {
		fmt.Println("  跳过 = K线中无对应交易日（停牌/数据缺口）或该日样本不足，原值保留")
	}
	return nil
}

// codesNeedingScoreRepair 返回待重算的代码列表与总行数。cutoff 为空表示不设上界。
func codesNeedingScoreRepair(db *sql.DB, cutoff string) ([]string, int, error) {
	where, arg := "", []any{}
	if cutoff != "" {
		where = " WHERE trade_date < ?"
		arg = append(arg, cutoff)
	}
	rows, err := db.Query(`SELECT DISTINCT code FROM snapshot`+where+` ORDER BY code`, arg...)
	if err != nil {
		return nil, 0, fmt.Errorf("query codes: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, 0, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	var total int
	if err := db.QueryRow(`SELECT COUNT(*) FROM snapshot`+where, arg...).Scan(&total); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// preservedFields 是重算时必须沿用原行的实时行情字段（历史不可追溯）。
type preservedFields struct {
	turnoverRate, marketCap, pe sql.NullFloat64
	insideVol, outsideVol       sql.NullFloat64
}

// applyPreserved 把原行的实时行情字段回填进重算出的快照。
//
// buildSnapshot 只从日K计算，不产出这几个字段；若直接落库会把原有值清零。
// repair-scores 与 backfill-date 共用此函数，避免两处保留逻辑漂移。
func applyPreserved(snap *store.Snapshot, keep preservedFields) {
	if keep.turnoverRate.Valid {
		snap.TurnoverRate = keep.turnoverRate.Float64
	}
	if keep.marketCap.Valid {
		snap.MarketCap = keep.marketCap.Float64
	}
	if keep.pe.Valid {
		snap.PE = keep.pe.Float64
	}
	if keep.insideVol.Valid {
		snap.InsideVol = keep.insideVol.Float64
	}
	if keep.outsideVol.Valid {
		snap.OutsideVol = keep.outsideVol.Float64
	}
}

// repairScoresOne 重算单只标的的历史行，返回 (重算行数, 跳过行数)。
func repairScoresOne(st *store.Store, lock *sync.Mutex, client *http.Client,
	code string, bars int, cutoff string) (int, int, error) {

	ck, ok := market.NormalizeCode(code)
	if !ok {
		return 0, 0, fmt.Errorf("invalid code")
	}
	data, err := api.FetchDailyKline(client, ck, bars)
	if err != nil {
		return 0, 0, fmt.Errorf("fetch: %w", err)
	}
	if len(data.Candles) == 0 {
		return 0, 0, fmt.Errorf("empty klines")
	}

	idxByDate := make(map[string]int, len(data.Dates))
	for i, d := range data.Dates {
		idxByDate[d] = i
	}

	lock.Lock()
	defer lock.Unlock()

	targets, err := scoreRepairTargets(st.DB(), code, cutoff)
	if err != nil {
		return 0, 0, err
	}

	done, skipped := 0, 0
	for date, keep := range targets {
		i, ok := idxByDate[date]
		if !ok {
			skipped++ // K线无该交易日（停牌/缺口），保留原值
			continue
		}
		snap := buildSnapshot(truncateKline(data, i))
		if snap.TradeDate != date {
			skipped++ // 截断后末根日期不符，保守跳过
			continue
		}
		applyPreserved(&snap, keep)
		if err := st.SaveSnapshot(snap); err != nil {
			return done, skipped, fmt.Errorf("save %s@%s: %w", code, date, err)
		}
		done++
	}
	return done, skipped, nil
}

// scoreRepairTargets 读出待重算的交易日及其需保留的实时字段。
func scoreRepairTargets(db *sql.DB, code, cutoff string) (map[string]preservedFields, error) {
	q := `SELECT trade_date, turnover_rate, market_cap, pe, inside_vol, outside_vol
	      FROM snapshot WHERE code = ?`
	arg := []any{code}
	if cutoff != "" {
		q += ` AND trade_date < ?`
		arg = append(arg, cutoff)
	}
	rows, err := db.Query(q, arg...)
	if err != nil {
		return nil, fmt.Errorf("query targets %s: %w", code, err)
	}
	defer rows.Close()

	out := make(map[string]preservedFields)
	for rows.Next() {
		var d string
		var p preservedFields
		if err := rows.Scan(&d, &p.turnoverRate, &p.marketCap, &p.pe,
			&p.insideVol, &p.outsideVol); err != nil {
			return nil, err
		}
		out[d] = p
	}
	return out, rows.Err()
}

// truncateKline 把 KlineData 截断到索引 i（含），使 buildSnapshot 以该日为
// "最后一根"重算。
//
// VolRatioRT 必须清零：它是**当日实时**量比，套用到历史日会张冠李戴。清零后
// buildSnapshot 走 analysis.VolRatio 本地重算，口径与实时值一致（见该函数注释）。
// InsideVol/OutsideVol 同理清零，由调用方从原行回填。
func truncateKline(data api.KlineData, i int) api.KlineData {
	cut := func(s []float64) []float64 {
		if i+1 <= len(s) {
			return s[:i+1]
		}
		return s
	}
	return api.KlineData{
		Code:       data.Code,
		Name:       data.Name,
		Dates:      data.Dates[:i+1],
		Candles:    data.Candles[:i+1],
		Turnovers:  cut(data.Turnovers),
		Amplitudes: cut(data.Amplitudes),
		VolRatioRT: 0,
	}
}
