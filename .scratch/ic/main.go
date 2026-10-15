// Model validity check (2026-10 review): Rank IC / quintile / tier excess returns on the hot-stock pool, plus the pre-registered over-extension rule test. See CLAUDE.md「2026-10-15 有效性检验」.
// Lives under .scratch/ so ./... patterns (build/test/vet) skip it; run with `go run ./.scratch/ic`.
package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"sort"
	"sync"

	"stock-tui/internal/api"
	"stock-tui/internal/screener"

	_ "modernc.org/sqlite"
)

var extraCols = []string{"rsi6", "kdj_j", "wr14", "bias6", "boll_pb", "boll_bw", "mfi", "cmi", "chop", "pdi", "mdi", "ret20", "ret60"}

const (
	dbPath    = "data/stock.db"
	cachePath = "/tmp/ic-kline-cache.json"
	bars      = 200
	workers   = 4
	maxRetry  = 3
	calCode   = "sh601398" // never suspended; defines the market trading calendar
)

type kl struct {
	Name   string
	Dates  []string
	Closes []float64
}

type row struct {
	date string
	c    screener.Candidate
	raw  int // score_total (fixed scale)
	adj  int // COALESCE(score_adj, score_total)
	sig  map[string]bool
	x    map[string]float64
	ret  map[int]float64
	ex   map[int]float64
	ok   map[int]bool
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func fetchAll(codes []string) map[string]kl {
	cache := map[string]kl{}
	if b, err := os.ReadFile(cachePath); err == nil {
		must(json.Unmarshal(b, &cache))
	}
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	var mu sync.Mutex
	ch := make(chan string)
	var wg sync.WaitGroup
	var failed []string
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for code := range ch {
				var d api.KlineData
				var err error
				for i := 0; i < maxRetry; i++ {
					if d, err = api.FetchDailyKline(client, code, bars); err == nil {
						break
					}
				}
				mu.Lock()
				if err != nil {
					failed = append(failed, code)
				} else {
					k := kl{Name: d.Name, Dates: d.Dates}
					for _, c := range d.Candles {
						k.Closes = append(k.Closes, c.Close)
					}
					cache[code] = k
				}
				mu.Unlock()
			}
		}()
	}
	n := 0
	for _, c := range codes {
		if _, ok := cache[c]; !ok {
			ch <- c
			n++
		}
	}
	close(ch)
	wg.Wait()
	fmt.Fprintf(os.Stderr, "fetched %d, failed %d %v\n", n, len(failed), failed)
	b, _ := json.Marshal(cache)
	must(os.WriteFile(cachePath, b, 0o644))
	return cache
}

func rank(x []float64) []float64 {
	idx := make([]int, len(x))
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool { return x[idx[a]] < x[idx[b]] })
	r := make([]float64, len(x))
	for i := 0; i < len(idx); {
		j := i
		for j+1 < len(idx) && x[idx[j+1]] == x[idx[i]] {
			j++
		}
		avg := float64(i+j)/2 + 1
		for k := i; k <= j; k++ {
			r[idx[k]] = avg
		}
		i = j + 1
	}
	return r
}

func pearson(a, b []float64) float64 {
	n := float64(len(a))
	var ma, mb float64
	for i := range a {
		ma += a[i]
		mb += b[i]
	}
	ma /= n
	mb /= n
	var sab, saa, sbb float64
	for i := range a {
		sab += (a[i] - ma) * (b[i] - mb)
		saa += (a[i] - ma) * (a[i] - ma)
		sbb += (b[i] - mb) * (b[i] - mb)
	}
	if saa == 0 || sbb == 0 {
		return math.NaN()
	}
	return sab / math.Sqrt(saa*sbb)
}

// nwT is a Newey-West (Bartlett, lag L) t-stat on the full daily series, so
// overlapping h-day windows are used without overstating significance.
func nwT(x []float64, L int) (float64, int) {
	var v []float64
	for _, a := range x {
		if !math.IsNaN(a) {
			v = append(v, a)
		}
	}
	n := len(v)
	if n < 10 {
		return math.NaN(), n
	}
	var m float64
	for _, a := range v {
		m += a
	}
	m /= float64(n)
	gamma := func(k int) float64 {
		var s float64
		for i := k; i < n; i++ {
			s += (v[i] - m) * (v[i-k] - m)
		}
		return s / float64(n)
	}
	lr := gamma(0)
	for k := 1; k <= L; k++ {
		lr += 2 * (1 - float64(k)/float64(L+1)) * gamma(k)
	}
	return m / math.Sqrt(lr/float64(n)), n
}

func tstat(x []float64) (mean, t float64, n, pos int) {
	var v []float64
	for _, a := range x {
		if !math.IsNaN(a) {
			v = append(v, a)
		}
	}
	n = len(v)
	if n < 2 {
		return math.NaN(), math.NaN(), n, 0
	}
	for _, a := range v {
		mean += a
		if a > 0 {
			pos++
		}
	}
	mean /= float64(n)
	var ss float64
	for _, a := range v {
		ss += (a - mean) * (a - mean)
	}
	sd := math.Sqrt(ss / float64(n-1))
	return mean, mean / (sd / math.Sqrt(float64(n))), n, pos
}

// metric computes one cross-sectional statistic for a date's rows and horizon.
type metric struct {
	name string
	fn   func(rs []*row, h int) float64
}

func ic(val func(*row) float64) func([]*row, int) float64 {
	return func(rs []*row, h int) float64 {
		var a, b []float64
		for _, r := range rs {
			if r.ok[h] {
				a = append(a, val(r))
				b = append(b, r.ex[h])
			}
		}
		if len(a) < 20 {
			return math.NaN()
		}
		return pearson(rank(a), rank(b))
	}
}

func quintile(val func(*row) float64) func([]*row, int) float64 {
	return func(rs []*row, h int) float64 {
		var v []*row
		for _, r := range rs {
			if r.ok[h] {
				v = append(v, r)
			}
		}
		if len(v) < 50 {
			return math.NaN()
		}
		sort.SliceStable(v, func(i, j int) bool { return val(v[i]) < val(v[j]) })
		q := len(v) / 5
		var lo, hi float64
		for i := 0; i < q; i++ {
			lo += v[i].ex[h]
			hi += v[len(v)-1-i].ex[h]
		}
		return (hi - lo) / float64(q)
	}
}

// group returns the mean excess of members (excess is already vs the pool mean,
// so this is "group minus pool"). Needs >=3 members to count as a section.
func group(in func(*row) bool, eligible func(*row) bool) func([]*row, int) float64 {
	return func(rs []*row, h int) float64 {
		var s, base float64
		var n, nb int
		for _, r := range rs {
			if !r.ok[h] || (eligible != nil && !eligible(r)) {
				continue
			}
			base += r.ex[h]
			nb++
			if in(r) {
				s += r.ex[h]
				n++
			}
		}
		if n < 3 {
			return math.NaN()
		}
		// Restricted universe: compare against that universe's own mean.
		return s/float64(n) - base/float64(nb)
	}
}

func main() {
	db, err := sql.Open("sqlite", dbPath)
	must(err)
	defer db.Close()

	rows, err := db.Query(`
		SELECT s.trade_date, s.code, COALESCE(i.name, ''),
		       s.score_total, COALESCE(s.score_adj, s.score_total),
		       s.adx, s.change_pct, s.close,
		       s.sar_long, s.supertrend_long, s.obv_up, COALESCE(s.obv_up3, 0),
		       s.macd_hist, s.vol_ratio, s.td_setup, s.td_countdown,
		       s.div_bear, s.sig_overbought,
		       COALESCE(s.turnover_rate, 0), COALESCE(s.market_cap, 0), COALESCE(s.pe, 0),
		       s.rs20, s.rs60, s.rs120, s.bias24, s.atr_pct, s.streak, s.ma20,
		       COALESCE(s.low20, 0),
		       s.perf_trend_follow_bull_win10, s.perf_overbought_bear_win10, s.perf_div_bear_win10,
		       s.perf_trend_follow_bull_n, s.perf_overbought_bear_n, s.perf_div_bear_n,
		       s.perf_trend_follow_bull_avg10,
		       s.sig_trend_bull, s.sig_oversold, s.div_bull,
		       COALESCE(s.rsi6, 0), COALESCE(s.kdj_j, 0), COALESCE(s.wr14, 0), COALESCE(s.bias6, 0), COALESCE(s.boll_pb, 0), COALESCE(s.boll_bw, 0), COALESCE(s.mfi, 0), COALESCE(s.cmi, 0), COALESCE(s.chop, 0), COALESCE(s.pdi, 0), COALESCE(s.mdi, 0), COALESCE(s.ret20, 0), COALESCE(s.ret60, 0)
		FROM snapshot s LEFT JOIN instrument i ON i.code = s.code`)
	must(err)
	var all []*row
	codeSet := map[string]bool{}
	for rows.Next() {
		r := &row{sig: map[string]bool{}, x: map[string]float64{}, ret: map[int]float64{}, ex: map[int]float64{}, ok: map[int]bool{}}
		c := &r.c
		var sar, st, obv, obv3, db_, ob, tb, os_, dbu int
		var td, tc sql.NullString
		var ex0, ex1, ex2, ex3, ex4, ex5, ex6, ex7, ex8, ex9, ex10, ex11, ex12 float64
		must(rows.Scan(&r.date, &c.Code, &c.Name, &r.raw, &r.adj,
			&c.ADX, &c.ChangePct, &c.Close, &sar, &st, &obv, &obv3,
			&c.MACDHist, &c.VolRatio, &td, &tc, &db_, &ob,
			&c.TurnoverRate, &c.MarketCap, &c.PE,
			&c.RS20, &c.RS60, &c.RS120, &c.Bias24, &c.ATRPct, &c.Streak, &c.MA20, &c.Low20,
			&c.PerfTrendFollowBullWin10, &c.PerfOverboughtBearWin10, &c.PerfDivBearWin10,
			&c.PerfTrendFollowBullN, &c.PerfOverboughtBearN, &c.PerfDivBearN,
			&c.PerfTrendFollowBullAvg10, &tb, &os_, &dbu,
			&ex0, &ex1, &ex2, &ex3, &ex4, &ex5, &ex6, &ex7, &ex8, &ex9, &ex10, &ex11, &ex12))
		for i, name := range extraCols {
			r.x[name] = []float64{ex0, ex1, ex2, ex3, ex4, ex5, ex6, ex7, ex8, ex9, ex10, ex11, ex12}[i]
		}
		c.TDSetup, c.TDCountdown = td.String, tc.String
		c.SARLong, c.SuperTrendLong, c.OBVUp, c.OBVUp3Day = sar == 1, st == 1, obv == 1, obv3 == 1
		c.DivBear, c.SigOverbought = db_ == 1, ob == 1
		c.ScoreTotal = r.adj // screener reads COALESCE(score_adj, score_total)
		r.sig["sig_trend_bull"], r.sig["sig_overbought"], r.sig["sig_oversold"] = tb == 1, ob == 1, os_ == 1
		r.sig["div_bull"], r.sig["div_bear"] = dbu == 1, db_ == 1
		all = append(all, r)
		codeSet[c.Code] = true
	}
	must(rows.Err())

	codes := []string{calCode}
	for c := range codeSet {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	k := fetchAll(codes)

	var first string
	must(db.QueryRow(`SELECT MIN(trade_date) FROM snapshot`).Scan(&first))
	var cal []string
	for _, d := range k[calCode].Dates {
		if d >= first {
			cal = append(cal, d)
		}
	}
	calIdx := map[string]int{}
	for i, d := range cal {
		calIdx[d] = i
	}

	horizons := []int{5, 10}
	byDate := map[string][]*row{}
	var noEntry, noKline int
	for _, r := range all {
		kd, ok := k[r.c.Code]
		if !ok {
			noKline++
			continue
		}
		if r.c.Name == "" {
			r.c.Name = kd.Name // dropped from pool: instrument row is gone, need name for IsST
		}
		pos := map[string]int{}
		for i, d := range kd.Dates {
			pos[d] = i
		}
		ei, ok := pos[r.date]
		ci, okc := calIdx[r.date]
		if !ok || !okc {
			noEntry++
			continue
		}
		entry := kd.Closes[ei]
		for _, h := range horizons {
			if ci+h >= len(cal) {
				continue
			}
			exitDate := cal[ci+h]
			// Last close on or before the market exit date (suspension carries the price).
			j := sort.SearchStrings(kd.Dates, exitDate)
			if j == len(kd.Dates) || kd.Dates[j] != exitDate {
				j--
			}
			if j <= ei {
				continue
			}
			r.ret[h] = kd.Closes[j]/entry - 1
			r.ok[h] = true
		}
		r.c.Tier = screener.ComputeTier(&r.c)
		byDate[r.date] = append(byDate[r.date], r)
	}
	for _, rs := range byDate {
		for _, h := range horizons {
			var s float64
			var n int
			for _, r := range rs {
				if r.ok[h] {
					s += r.ret[h]
					n++
				}
			}
			for _, r := range rs {
				if r.ok[h] {
					r.ex[h] = r.ret[h] - s/float64(n)
				}
			}
		}
	}
	fmt.Printf("calendar %s..%s (%d days), rows %d, no kline %d, no entry bar %d\n\n",
		cal[0], cal[len(cal)-1], len(cal), len(all), noKline, noEntry)

	coreTech := func(r *row) bool {
		return r.c.SARLong && r.c.SuperTrendLong && r.c.OBVUp && r.c.MACDHist > 0
	}
	hasFund := func(r *row) bool { return r.c.MarketCap > 0 && r.c.TurnoverRate > 0 }
	metrics := []metric{
		{"IC score_total", ic(func(r *row) float64 { return float64(r.raw) })},
		{"IC score_adj", ic(func(r *row) float64 { return float64(r.adj) })},
		{"IC rs20", ic(func(r *row) float64 { return r.c.RS20.Float64 })},
		{"Q5-Q1 score_total", quintile(func(r *row) float64 { return float64(r.raw) })},
		{"Q5-Q1 score_adj", quintile(func(r *row) float64 { return float64(r.adj) })},
		{"coreTech", group(coreTech, nil)},
		{"星级技术面", group(func(r *row) bool {
			return r.adj >= 65 && r.c.ADX >= 35 && coreTech(r) && r.c.ChangePct >= 0
		}, nil)},
		{"ADX>=35", group(func(r *row) bool { return r.c.ADX >= 35 }, nil)},
		{"sig_trend_bull", group(func(r *row) bool { return r.sig["sig_trend_bull"] }, nil)},
		{"sig_overbought", group(func(r *row) bool { return r.sig["sig_overbought"] }, nil)},
		{"sig_oversold", group(func(r *row) bool { return r.sig["sig_oversold"] }, nil)},
		{"div_bull", group(func(r *row) bool { return r.sig["div_bull"] }, nil)},
		{"div_bear", group(func(r *row) bool { return r.sig["div_bear"] }, nil)},
		{"Tier ⭐⭐/⭐⭐⭐*", group(func(r *row) bool {
			return r.c.Tier == screener.TierStar2 || r.c.Tier == screener.TierStar3
		}, hasFund)},
		{"Tier 任意(含观察)*", group(func(r *row) bool { return r.c.Tier != screener.TierNone }, hasFund)},
	}

	for _, h := range horizons {
		fmt.Printf("=== %d 日超额收益（每 %d 个交易日取一个截面；offset 0 为主结果，其余 offset 给 t 的范围）===\n", h, h)
		fmt.Printf("%-22s %9s %7s %8s  %-18s %s\n", "检验", "均值", "t", "为正", "t 范围(各 offset)", "全日 NW t")
		for _, m := range metrics {
			var res [](struct {
				mean, t float64
				n, pos  int
			})
			for off := 0; off < h; off++ {
				var xs []float64
				for i := off; i+h < len(cal); i += h {
					rs := byDate[cal[i]]
					if len(rs) == 0 {
						continue
					}
					xs = append(xs, m.fn(rs, h))
				}
				mean, t, n, pos := tstat(xs)
				res = append(res, struct {
					mean, t float64
					n, pos  int
				}{mean, t, n, pos})
			}
			lo, hi := math.Inf(1), math.Inf(-1)
			for _, r := range res {
				if !math.IsNaN(r.t) {
					lo, hi = math.Min(lo, r.t), math.Max(hi, r.t)
				}
			}
			var daily []float64
			for i := 0; i+h < len(cal); i++ {
				if rs := byDate[cal[i]]; len(rs) > 0 {
					daily = append(daily, m.fn(rs, h))
				}
			}
			nw, nd := nwT(daily, h-1)
			r0 := res[0]
			unit, mean := "%", r0.mean*100
			if len(m.name) > 2 && m.name[:2] == "IC" {
				unit, mean = "", r0.mean
			}
			fmt.Printf("%-22s %8.3f%s %7.2f %4d/%-3d  [%5.2f, %5.2f]     %6.2f (%d日)\n", m.name, mean, unit, r0.t, r0.pos, r0.n, lo, hi, nw, nd)
		}
		fmt.Println()
	}

	// Pooled member counts so tiny groups are visible.
	cnt := map[string]int{}
	for _, rs := range byDate {
		for _, r := range rs {
			if r.c.Tier == screener.TierStar2 || r.c.Tier == screener.TierStar3 {
				cnt["star"]++
			}
			if r.c.Tier != screener.TierNone {
				cnt["anytier"]++
			}
			if r.sig["sig_overbought"] {
				cnt["overbought"]++
			}
		}
	}
	fmt.Printf("全样本成员行数：⭐⭐/⭐⭐⭐ %d，任意 tier %d，超买 %d\n", cnt["star"], cnt["anytier"], cnt["overbought"])
	fmt.Println("* tier 检验仅在有市值/换手的行内比较（实时字段缺失日 fundOK 必然判否）")

	// ── 分项检验：哪个指标在拖后腿 ──
	b2f := func(b bool) float64 {
		if b {
			return 1
		}
		return 0
	}
	feats := []struct {
		name string
		f    func(*row) float64
	}{
		{"score_total", func(r *row) float64 { return float64(r.raw) }},
		{"rs20", func(r *row) float64 { return r.c.RS20.Float64 }},
		{"rs60", func(r *row) float64 { return r.c.RS60.Float64 }},
		{"ret20", func(r *row) float64 { return r.x["ret20"] }},
		{"ret60", func(r *row) float64 { return r.x["ret60"] }},
		{"change_pct(当日)", func(r *row) float64 { return r.c.ChangePct }},
		{"streak", func(r *row) float64 { return float64(r.c.Streak) }},
		{"adx", func(r *row) float64 { return r.c.ADX }},
		{"pdi-mdi", func(r *row) float64 { return r.x["pdi"] - r.x["mdi"] }},
		{"cmi", func(r *row) float64 { return r.x["cmi"] }},
		{"chop", func(r *row) float64 { return r.x["chop"] }},
		{"macd_hist/close", func(r *row) float64 { return r.c.MACDHist / r.c.Close }},
		{"rsi6", func(r *row) float64 { return r.x["rsi6"] }},
		{"kdj_j", func(r *row) float64 { return r.x["kdj_j"] }},
		{"wr14(大=超卖)", func(r *row) float64 { return r.x["wr14"] }},
		{"bias6", func(r *row) float64 { return r.x["bias6"] }},
		{"bias24", func(r *row) float64 { return r.c.Bias24 }},
		{"bias24/atr", func(r *row) float64 {
			if r.c.ATRPct <= 0 {
				return 0
			}
			return r.c.Bias24 / r.c.ATRPct
		}},
		{"boll_pb", func(r *row) float64 { return r.x["boll_pb"] }},
		{"boll_bw", func(r *row) float64 { return r.x["boll_bw"] }},
		{"atr_pct", func(r *row) float64 { return r.c.ATRPct }},
		{"mfi", func(r *row) float64 { return r.x["mfi"] }},
		{"vol_ratio", func(r *row) float64 { return r.c.VolRatio }},
		{"sar_long", func(r *row) float64 { return b2f(r.c.SARLong) }},
		{"supertrend_long", func(r *row) float64 { return b2f(r.c.SuperTrendLong) }},
		{"obv_up", func(r *row) float64 { return b2f(r.c.OBVUp) }},
		{"obv_up3", func(r *row) float64 { return b2f(r.c.OBVUp3Day) }},
	}
	for _, h := range horizons {
		fmt.Printf("\n=== 分项 Rank IC（%d 日超额；正=值越大后续越好）===\n", h)
		fmt.Printf("%-18s %8s %8s %8s\n", "指标", "均值IC", "NW t", "不重叠t")
		for _, ft := range feats {
			var daily, nonov []float64
			for i := 0; i+h < len(cal); i++ {
				if rs := byDate[cal[i]]; len(rs) > 0 {
					v := ic(ft.f)(rs, h)
					daily = append(daily, v)
					if i%h == 0 {
						nonov = append(nonov, v)
					}
				}
			}
			nw, _ := nwT(daily, h-1)
			_, t0, _, _ := tstat(nonov)
			m, _, _, _ := tstat(daily)
			fmt.Printf("%-18s %8.3f %8.2f %8.2f\n", ft.name, m, nw, t0)
		}
	}

	// ── 五分位剖面：反向集中在顶部(过度延伸)还是单调 ──
	fmt.Printf("\n=== 五分位平均超额（10 日，逐日分组后跨日平均；Q1=最低 Q5=最高）===\n")
	for _, ft := range feats {
		switch ft.name {
		case "score_total", "rs20", "rs60", "bias24/atr", "rsi6", "pdi-mdi", "mfi", "vol_ratio", "adx":
		default:
			continue
		}
		var q [5][]float64
		for i := 0; i+10 < len(cal); i++ {
			var v []*row
			for _, r := range byDate[cal[i]] {
				if r.ok[10] {
					v = append(v, r)
				}
			}
			if len(v) < 50 {
				continue
			}
			sort.SliceStable(v, func(a, b int) bool { return ft.f(v[a]) < ft.f(v[b]) })
			for k := 0; k < 5; k++ {
				lo, hi := k*len(v)/5, (k+1)*len(v)/5
				var sum float64
				for _, r := range v[lo:hi] {
					sum += r.ex[10]
				}
				q[k] = append(q[k], sum/float64(hi-lo))
			}
		}
		fmt.Printf("%-14s", ft.name)
		for k := 0; k < 5; k++ {
			m, _, _, _ := tstat(q[k])
			fmt.Printf("  Q%d=%+.2f%%", k+1, m*100)
		}
		fmt.Println()
	}

	// ── 过度延伸规则：训练(6~8 月, 窗口不跨入 9 月)选规则，9 月起检验 ──
	const testStart = "2026-09-01"
	const H = 10
	inU := func(r *row) bool {
		return r.c.RS20.Valid && r.c.RS20.Float64 >= 60 && (!r.c.RS60.Valid || r.c.RS60.Float64 >= 45) && coreTech(r)
	}
	biasATR := func(r *row) float64 {
		if r.c.ATRPct <= 0 {
			return 0
		}
		return r.c.Bias24 / r.c.ATRPct
	}
	dmi := func(r *row) float64 { return r.x["pdi"] - r.x["mdi"] }
	var trainIdx, testIdx []int
	for i := 0; i+H < len(cal); i++ {
		if cal[i+H] < testStart {
			trainIdx = append(trainIdx, i)
		} else if cal[i] >= testStart {
			testIdx = append(testIdx, i)
		}
	}
	q80 := func(f func(*row) float64) float64 {
		var v []float64
		for _, i := range trainIdx {
			for _, r := range byDate[cal[i]] {
				v = append(v, f(r))
			}
		}
		sort.Float64s(v)
		return v[len(v)*4/5]
	}
	tBias, tDMI := q80(biasATR), q80(dmi)
	rules := []struct {
		name string
		hit  func(*row) bool
	}{
		{"RS20>=80", func(r *row) bool { return r.c.RS20.Float64 >= 80 }},
		{fmt.Sprintf("bias24/atr>=%.2f", tBias), func(r *row) bool { return biasATR(r) >= tBias }},
		{fmt.Sprintf("pdi-mdi>=%.1f", tDMI), func(r *row) bool { return dmi(r) >= tDMI }},
	}
	evalRule := func(hit func(*row) bool, idx []int) (mean, nw float64, n int, hitRate float64) {
		var diffs []float64
		var nh, nu int
		for _, i := range idx {
			var sh, su float64
			var ch, cu int
			for _, r := range byDate[cal[i]] {
				if !r.ok[H] || !inU(r) {
					continue
				}
				nu++
				if hit(r) {
					sh += r.ex[H]
					ch++
					nh++
				} else {
					su += r.ex[H]
					cu++
				}
			}
			if ch >= 3 && cu >= 3 {
				diffs = append(diffs, sh/float64(ch)-su/float64(cu))
			} else {
				diffs = append(diffs, math.NaN())
			}
		}
		mean, _, n, _ = tstat(diffs)
		nw, _ = nwT(diffs, H-1)
		return mean, nw, n, float64(nh) / float64(nu)
	}
	fmt.Printf("\n=== 过度延伸规则（范围：RS20>=60 且 RS60>=45 且 coreTech；命中组 − 未命中组的 10 日超额）===\n")
	fmt.Printf("训练 %s..%s (%d 天)  检验 %s..%s (%d 天)\n",
		cal[trainIdx[0]], cal[trainIdx[len(trainIdx)-1]], len(trainIdx), cal[testIdx[0]], cal[testIdx[len(testIdx)-1]], len(testIdx))
	for _, rl := range rules {
		m1, t1, n1, h1 := evalRule(rl.hit, trainIdx)
		m2, t2, n2, h2 := evalRule(rl.hit, testIdx)
		fmt.Printf("%-20s 训练: %+.2f%% NW t=%5.2f (%d天, 命中率 %.0f%%)   检验: %+.2f%% NW t=%5.2f (%d天, 命中率 %.0f%%)\n",
			rl.name, m1*100, t1, n1, h1*100, m2*100, t2, n2, h2*100)
	}
	// 当前星级里会被各规则降级的比例
	for _, rl := range rules {
		var star, hit int
		for _, rs := range byDate {
			for _, r := range rs {
				if r.c.Tier == screener.TierStar2 || r.c.Tier == screener.TierStar3 {
					star++
					if rl.hit(r) {
						hit++
					}
				}
			}
		}
		fmt.Printf("%-20s 命中现有 ⭐⭐/⭐⭐⭐ %d/%d\n", rl.name, hit, star)
	}

	// ── 按月拆开：反向是常态还是某段行情 ──
	fmt.Printf("\n=== 按月 10 日 Rank IC 均值（score_total / rs20 / 全池等权 10 日绝对收益）===\n")
	months := map[string][3][]float64{}
	var order []string
	for i := 0; i+10 < len(cal); i++ {
		rs := byDate[cal[i]]
		if len(rs) == 0 {
			continue
		}
		mo := cal[i][:7]
		v, seen := months[mo]
		if !seen {
			order = append(order, mo)
		}
		v[0] = append(v[0], ic(func(r *row) float64 { return float64(r.raw) })(rs, 10))
		v[1] = append(v[1], ic(func(r *row) float64 { return r.c.RS20.Float64 })(rs, 10))
		var s float64
		var n int
		for _, r := range rs {
			if r.ok[10] {
				s += r.ret[10]
				n++
			}
		}
		v[2] = append(v[2], s/float64(n))
		months[mo] = v
	}
	for _, mo := range order {
		v := months[mo]
		a, _, n, _ := tstat(v[0])
		b, _, _, _ := tstat(v[1])
		c, _, _, _ := tstat(v[2])
		fmt.Printf("%s  天数=%2d  IC(score)=%+.3f  IC(rs20)=%+.3f  池均10日=%+.2f%%\n", mo, n, a, b, c*100)
	}
}
