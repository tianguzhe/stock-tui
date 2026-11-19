package main

import (
	"database/sql"
	"fmt"
	"sort"

	"stock-tui/internal/api"
)

// thinRatio: a day whose snapshot count is below this fraction of the median of
// its neighbouring trading days is reported as a partial batch-save failure.
// Real cases were far below it (07-13 218 vs ~600, 07-15 148 vs ~630), while
// normal day-to-day pool drift stays within ±15%.
const thinRatio = 0.6

// thinNeighbours is how many trading days on each side form the comparison set.
const thinNeighbours = 3

type calendarReport struct {
	CoverFrom string   // first trading day the calendar covers (service keeps ~1 year)
	Missing   []string // trading days with zero snapshot rows
	Extra     []string // snapshot dates that are not trading days
	Thin      []thinDay
}

type thinDay struct {
	Date   string
	Count  int
	Median int
}

// analyzeCalendar compares snapshot row counts per date with the trading
// calendar over [snapshot min, snapshot max]. tradingDays must be ascending and
// is assumed to cover only part of history; dates before its first element are
// not judged (neither missing nor extra).
func analyzeCalendar(tradingDays []string, counts map[string]int) calendarReport {
	var rep calendarReport
	if len(tradingDays) == 0 || len(counts) == 0 {
		return rep
	}
	snapDates := make([]string, 0, len(counts))
	for d := range counts {
		snapDates = append(snapDates, d)
	}
	sort.Strings(snapDates)
	lo, hi := snapDates[0], snapDates[len(snapDates)-1]
	rep.CoverFrom = tradingDays[0]

	isTrading := make(map[string]bool, len(tradingDays))
	var inRange []string
	for _, d := range tradingDays {
		isTrading[d] = true
		if d >= lo && d <= hi {
			inRange = append(inRange, d)
		}
	}
	for _, d := range snapDates {
		if d >= rep.CoverFrom && !isTrading[d] {
			rep.Extra = append(rep.Extra, d)
		}
	}
	for i, d := range inRange {
		n := counts[d]
		if n == 0 {
			rep.Missing = append(rep.Missing, d)
			continue
		}
		var nb []int
		for j := i - thinNeighbours; j <= i+thinNeighbours; j++ {
			if j < 0 || j >= len(inRange) || j == i {
				continue
			}
			if c := counts[inRange[j]]; c > 0 {
				nb = append(nb, c)
			}
		}
		if len(nb) == 0 {
			continue
		}
		sort.Ints(nb)
		med := nb[len(nb)/2]
		if float64(n) < thinRatio*float64(med) {
			rep.Thin = append(rep.Thin, thinDay{Date: d, Count: n, Median: med})
		}
	}
	return rep
}

func checkCalendar(db *sql.DB) error {
	client, err := api.NewFuyaoClient(nil)
	if err != nil {
		return err
	}
	counts := map[string]int{}
	rows, err := db.Query(`SELECT trade_date, COUNT(*) FROM snapshot GROUP BY trade_date`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var lo, hi string
	for rows.Next() {
		var d string
		var n int
		if err := rows.Scan(&d, &n); err != nil {
			return err
		}
		counts[d] = n
		if lo == "" || d < lo {
			lo = d
		}
		if d > hi {
			hi = d
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(counts) == 0 {
		fmt.Println("ℹ️  无快照数据")
		return nil
	}
	days, err := client.TradingDays(lo, hi)
	if err != nil {
		return err
	}
	rep := analyzeCalendar(days, counts)
	if rep.CoverFrom > lo {
		fmt.Printf("ℹ️  交易日历只覆盖 %s 起（服务端约一年），%s ~ %s 未判定\n", rep.CoverFrom, lo, rep.CoverFrom)
	}
	ok := true
	if len(rep.Missing) > 0 {
		ok = false
		fmt.Printf("❌ 缺失交易日 %d 个（全池无快照）: %v\n", len(rep.Missing), rep.Missing)
		fmt.Println("   补跑: go run ./cmd/stockdb backfill-date --date YYYY-MM-DD，之后 rs-rank --date 同日")
	}
	if len(rep.Thin) > 0 {
		ok = false
		fmt.Printf("⚠️  快照数明显偏少 %d 天（< 相邻 %d 个交易日中位数的 %.0f%%）:\n", len(rep.Thin), 2*thinNeighbours, thinRatio*100)
		for _, t := range rep.Thin {
			fmt.Printf("   %s: %d 条（相邻中位 %d）\n", t.Date, t.Count, t.Median)
		}
	}
	if len(rep.Extra) > 0 {
		ok = false
		fmt.Printf("⚠️  非交易日却有快照 %d 天: %v\n", len(rep.Extra), rep.Extra)
	}
	if ok {
		fmt.Printf("✅ %s ~ %s 每个交易日都有快照，且无明显残缺日\n", maxStr(lo, rep.CoverFrom), hi)
	}
	fmt.Println()
	return nil
}

func maxStr(a, b string) string {
	if a > b {
		return a
	}
	return b
}
