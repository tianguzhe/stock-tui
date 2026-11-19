package main

import (
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"stock-tui/internal/api"
	"stock-tui/internal/holdings"
	"stock-tui/internal/market"
)

// cmdExRight 列出除权除息事件（扶摇 adjustment-factors），解决三类老问题：
//  1. 月度对账漏记红利：2026-08 漏记大唐 46.50 元（500 股 × 每股 0.093，08-26 除权），
//     排查三版才找到。此命令按 .holdings 股数直接算出区间内应收红利；
//  2. 除权检测：替代「腾讯 qfq/bfq 各拉一次比收盘差」的手工流程；
//  3. 前复权基准断点：snapshot 逐日累积，除权日之前的行是旧基准、之后是新基准，
//     --pool 列出 snapshot 区间内发生除权的标的，即序列里存在人为跳空的标的。
func cmdExRight(args []string) error {
	fs := flag.NewFlagSet("exright", flag.ContinueOnError)
	days := fs.Int("days", 7, "预警窗口：今天之后 N 个自然日内的除权")
	from := fs.String("from", "", "已除权区间起点 YYYY-MM-DD（默认本月 1 日）")
	to := fs.String("to", "", "已除权区间终点 YYYY-MM-DD（默认今天）")
	codesFlag := fs.String("codes", "", "额外代码，逗号分隔（如红利仓 sh601166,sh600036；不在 .holdings 里）")
	pool := fs.Bool("pool", false, "同时扫描 instrument 全池（约 400 次请求）")
	holdingsFile := fs.String("holdings-file", holdings.DefaultPath, "持仓文件")
	if err := fs.Parse(args); err != nil {
		return err
	}
	client, err := api.NewFuyaoClient(nil)
	if err != nil {
		return fmt.Errorf("%w（在 ~/.zshrc 里 export %s=... 后重开终端）", err, api.FuyaoKeyEnv)
	}

	today := time.Now().In(cst).Format("2006-01-02")
	win := exWindow{Today: today, Until: addDays(today, *days), From: *from, To: *to}
	if win.From == "" {
		win.From = today[:8] + "01"
	}
	if win.To == "" {
		win.To = today
	}
	if win.From > win.To {
		return fmt.Errorf("--from %s 晚于 --to %s", win.From, win.To)
	}

	shares := map[string]int{} // 股（不是手）
	hs, err := holdings.Load(*holdingsFile)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, h := range hs {
		shares[h.Code] = h.Shares * 100
	}
	codes := make([]string, 0, len(hs))
	for _, h := range hs {
		codes = append(codes, h.Code)
	}
	codes = append(codes, market.NormalizeCodes(strings.Split(*codesFlag, ","))...)

	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	names := map[string]string{}
	rows, err := st.DB().Query(`SELECT code, COALESCE(name, '') FROM instrument`)
	if err != nil {
		return err
	}
	var poolCodes []string
	for rows.Next() {
		var c, n string
		if err := rows.Scan(&c, &n); err != nil {
			rows.Close()
			return err
		}
		names[c] = n
		poolCodes = append(poolCodes, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	var snapFrom string
	if *pool {
		codes = append(codes, poolCodes...)
		if err := st.DB().QueryRow(`SELECT COALESCE(MIN(trade_date), '') FROM snapshot`).Scan(&snapFrom); err != nil {
			return err
		}
	}
	codes = dedupe(codes)

	fetchFrom := win.From
	if snapFrom != "" && snapFrom < fetchFrom {
		fetchFrom = snapFrom
	}
	events, failed := fetchExRights(client, codes, fetchFrom, win.Until, 4)
	var uncovered []string
	for code, err := range failed {
		if errors.Is(err, api.ErrFuyaoNotCovered) {
			uncovered = append(uncovered, code)
			delete(failed, code)
			continue
		}
		fmt.Fprintf(os.Stderr, "warn: %s 拉取除权事件失败: %v\n", code, err)
	}
	if len(uncovered) > 0 {
		sort.Strings(uncovered)
		fmt.Printf("ℹ️  场内基金不在复权事件接口覆盖内，未检查：%s\n\n", strings.Join(uncovered, " "))
	}

	upcoming, past := classifyExRights(events, win, shares)
	fmt.Printf("## 即将除权（%s 之后 ~ %s，共 %d 条）\n", win.Today, win.Until, len(upcoming))
	printExRows(upcoming, names)
	fmt.Printf("\n## 区间内已除权（%s ~ %s，共 %d 条）\n", win.From, win.To, len(past))
	printExRows(past, names)
	if total := sumReceivable(past); total > 0 {
		fmt.Printf("\n应收红利合计（按 .holdings 当前股数、税前）：%.2f 元\n", total)
		fmt.Println("⚠️ 股数取 .holdings **当前值**；区间内有买卖时须按除权日前一交易日的实际股数重算。")
		fmt.Println("   红利税不在入账时扣：卖出时按持股期补缴（<1 月 20% / 1 月~1 年 10%），扣在卖出次日。")
	}
	if *pool && snapFrom != "" {
		breaks := baselineBreaks(events, snapFrom, win.Today)
		var drifted []driftHit
		for _, b := range breaks {
			for _, d := range b.Dates {
				hit, ok, err := snapshotDrift(st.DB(), b.Code, d)
				if err != nil {
					return err
				}
				if ok && math.Abs(hit.DriftPct) >= driftThresholdPct {
					drifted = append(drifted, hit)
				}
			}
		}
		fmt.Printf("\n## snapshot 前复权基准断点（%s 起：区间内除权 %d 只，复核后仍有跳空 %d 处）\n", snapFrom, len(breaks), len(drifted))
		fmt.Println("复核口径：除权日 close 与前一相邻交易日 close 的环比 − 当日 change_pct；已被 repair-scores --all 统一过的除权日差值为 0，不列出。")
		for _, h := range drifted {
			fmt.Printf("  %s %s %s 漂移 %+.2fpt（序列 %+.2f%% vs 实际 %+.2f%%）\n", h.Code, names[h.Code], h.Date, h.DriftPct, h.SeriesPct, h.ChangePct)
		}
		if len(drifted) > 0 {
			fmt.Println("修复：go run ./cmd/stockdb repair-scores --all -P 4，之后对全部交易日重排 rs-rank（见 CLAUDE.md「前复权基准漂移」）")
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("%d 只标的拉取失败（见上方 warn），结果不完整", len(failed))
	}
	return nil
}

var cst = time.FixedZone("CST", 8*3600)

func addDays(date string, n int) string {
	t, err := time.ParseInLocation("2006-01-02", date, cst)
	if err != nil {
		return date
	}
	return t.AddDate(0, 0, n).Format("2006-01-02")
}

func dedupe(codes []string) []string {
	seen := map[string]bool{}
	out := codes[:0:0]
	for _, c := range codes {
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	return out
}

type exRightFetcher interface {
	AdjustmentFactors(code, from, to string) ([]api.ExRightEvent, error)
}

// fetchExRights 并发拉取；单只失败计入 failed 而不中断其余标的。
func fetchExRights(f exRightFetcher, codes []string, from, to string, workers int) (map[string][]api.ExRightEvent, map[string]error) {
	events := map[string][]api.ExRightEvent{}
	failed := map[string]error{}
	var mu sync.Mutex
	ch := make(chan string)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for code := range ch {
				ev, err := f.AdjustmentFactors(code, from, to)
				mu.Lock()
				if err != nil {
					failed[code] = err
				} else {
					events[code] = ev
				}
				mu.Unlock()
			}
		}()
	}
	for _, c := range codes {
		ch <- c
	}
	close(ch)
	wg.Wait()
	return events, failed
}

type exWindow struct {
	Today, Until string // 预警：Today < ex <= Until
	From, To     string // 已除权：From <= ex <= To
}

type exRow struct {
	Code       string
	Event      api.ExRightEvent
	Shares     int     // 股；0 = 不在持仓
	Receivable float64 // 应收现金红利（税前，元）
}

func classifyExRights(events map[string][]api.ExRightEvent, w exWindow, shares map[string]int) (upcoming, past []exRow) {
	for code, evs := range events {
		for _, ev := range evs {
			row := exRow{Code: code, Event: ev, Shares: shares[code]}
			row.Receivable = float64(row.Shares) * ev.DividendPerShare
			if ev.ExDate > w.Today && ev.ExDate <= w.Until {
				upcoming = append(upcoming, row)
			}
			if ev.ExDate >= w.From && ev.ExDate <= w.To {
				past = append(past, row)
			}
		}
	}
	less := func(rs []exRow) func(i, j int) bool {
		return func(i, j int) bool {
			if rs[i].Event.ExDate != rs[j].Event.ExDate {
				return rs[i].Event.ExDate < rs[j].Event.ExDate
			}
			return rs[i].Code < rs[j].Code
		}
	}
	sort.Slice(upcoming, less(upcoming))
	sort.Slice(past, less(past))
	return upcoming, past
}

func sumReceivable(rows []exRow) float64 {
	t := 0.0
	for _, r := range rows {
		t += r.Receivable
	}
	return t
}

type baselineBreak struct {
	Code  string
	Dates []string
}

// baselineBreaks 返回 snapshot 区间 [from, today] 内发生过除权的标的及其除权日。
func baselineBreaks(events map[string][]api.ExRightEvent, from, today string) []baselineBreak {
	var out []baselineBreak
	for code, evs := range events {
		var ds []string
		for _, ev := range evs {
			if ev.ExDate > from && ev.ExDate <= today {
				ds = append(ds, ev.ExDate)
			}
		}
		if len(ds) > 0 {
			sort.Strings(ds)
			out = append(out, baselineBreak{Code: code, Dates: ds})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

func printExRows(rows []exRow, names map[string]string) {
	if len(rows) == 0 {
		fmt.Println("  （无）")
		return
	}
	fmt.Println("| 代码 | 名称 | 除权日 | 每股分红 | 每股送转 | 持股 | 应收红利(税前) |")
	fmt.Println("|---|---|---|---:|---:|---:|---:|")
	for _, r := range rows {
		hold, recv := "—", "—"
		if r.Shares > 0 {
			hold = fmt.Sprint(r.Shares)
			recv = fmt.Sprintf("%.2f", r.Receivable)
		}
		fmt.Printf("| %s | %s | %s | %.4f | %.4f | %s | %s |\n", r.Code, names[r.Code], r.Event.ExDate, r.Event.DividendPerShare, r.Event.BonusPerShare, hold, recv)
	}
}

// driftThresholdPct: |序列环比 − change_pct| 达到该值才算基准断点。snapshot close
// 是 3 位小数、change_pct 来自实时报价，正常行的差值在 0.01pt 量级；最小的真实
// 分红断点（每股 0.006 元、价格 20 元）约 0.03pt，故取 0.02。
const driftThresholdPct = 0.02

type driftHit struct {
	Code, Date                     string
	SeriesPct, ChangePct, DriftPct float64
}

// snapshotDrift 比较除权日 close 相对**前一相邻交易日** close 的环比与当日 change_pct。
// 标的在除权日或前一交易日不在池（无快照行）时无法判定，ok=false——缺行时 LAG
// 会跨日取值，正是 CLAUDE.md 记录的伪阳性来源。
func snapshotDrift(db *sql.DB, code, exDate string) (driftHit, bool, error) {
	hit := driftHit{Code: code, Date: exDate}
	var prevDay sql.NullString
	if err := db.QueryRow(`SELECT MAX(trade_date) FROM snapshot WHERE trade_date < ?`, exDate).Scan(&prevDay); err != nil {
		return hit, false, err
	}
	if !prevDay.Valid {
		return hit, false, nil
	}
	var c0, pct, c1 float64
	err := db.QueryRow(`SELECT close, change_pct FROM snapshot WHERE code = ? AND trade_date = ?`, code, exDate).Scan(&c0, &pct)
	if errors.Is(err, sql.ErrNoRows) {
		return hit, false, nil
	}
	if err != nil {
		return hit, false, err
	}
	err = db.QueryRow(`SELECT close FROM snapshot WHERE code = ? AND trade_date = ?`, code, prevDay.String).Scan(&c1)
	if errors.Is(err, sql.ErrNoRows) || c1 == 0 {
		return hit, false, nil
	}
	if err != nil {
		return hit, false, err
	}
	hit.SeriesPct = (c0/c1 - 1) * 100
	hit.ChangePct = pct
	hit.DriftPct = hit.SeriesPct - pct
	return hit, true, nil
}
