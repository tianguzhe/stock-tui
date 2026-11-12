// Scratch: 11-12 holdings — state backtests, 40-day correlation, stop grid.
package main

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"time"

	"stock-tui/internal/api"
	"stock-tui/internal/indicator"
)

type acc struct {
	n, win, edge int
	sum, mae     float64
}

func wilson(k, n int) (float64, float64) {
	if n == 0 {
		return 0, 0
	}
	z := 1.96
	p := float64(k) / float64(n)
	nn := float64(n)
	d := 1 + z*z/nn
	c := (p + z*z/(2*nn)) / d
	h := z * math.Sqrt(p*(1-p)/nn+z*z/(4*nn*nn)) / d
	return 100 * (c - h), 100 * (c + h)
}

func (a acc) String() string {
	if a.n == 0 {
		return "n=0"
	}
	lo, hi := wilson(a.win, a.n)
	return fmt.Sprintf("n=%d(边沿%d) win10=%.0f%% [%.1f, %.1f] avg10=%+.2f%% MAE=%.2f%%",
		a.n, a.edge, 100*float64(a.win)/float64(a.n), lo, hi, a.sum/float64(a.n), a.mae/float64(a.n))
}

func corr(a, b []float64) float64 {
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
	return sab / math.Sqrt(saa*sbb)
}

func ma(cs []indicator.Candle, i, n int) float64 {
	s := 0.0
	for j := i - n + 1; j <= i; j++ {
		s += cs[j].Close
	}
	return s / float64(n)
}

func main() {
	codes := []string{"sh601138", "sz000063", "sz002185", "sh603496", "sh601991", "sh512480"}
	shares := map[string]float64{"sh601138": 800, "sz000063": 300, "sz002185": 400, "sh603496": 200, "sh601991": 500, "sh512480": 800}
	cost := map[string]float64{"sh601138": 56.433, "sz000063": 34.703, "sz002185": 17.150, "sh603496": 25.586, "sh601991": 6.463, "sh512480": 1.351}
	budget := 662.842
	names := []string{
		"S1 SAR多/ST空+当日≤-4%(富联今日)",
		"S2 SAR当日翻多+ST空(恒为今日)",
		"S3 SAR多/ST空+收盘距SAR<1%(中兴今日)",
		"S4 SAR空/ST多+当日<0(华天今日)",
		"S5 SAR多/ST空+前日SAR翻多+当日≤-3%(富联今日)",
	}
	client := &http.Client{Timeout: 20 * time.Second}
	tot := make([]acc, len(names))
	const H = 10
	const W = 40
	rets := map[string][]float64{}
	var lastDates []string
	for _, c := range codes {
		k, err := api.FetchDailyKline(client, c, 800)
		if err != nil {
			fmt.Println(c, err)
			continue
		}
		cs := k.Candles
		rs := indicator.Calculate(cs)
		per := make([]acc, len(names))
		prev := make([]bool, len(names))
		for i := 60; i < len(cs); i++ {
			pct1 := cs[i].Close/cs[i-1].Close - 1
			sar, st := rs[i].SAR, rs[i].SuperTrend
			cond := []bool{
				sar.Long && !st.Long && pct1 <= -0.04,
				sar.Long && sar.Reversed && !st.Long,
				sar.Long && !st.Long && (cs[i].Close-sar.Value)/cs[i].Close < 0.01,
				!sar.Long && st.Long && pct1 < 0,
				sar.Long && !st.Long && rs[i-1].SAR.Reversed && rs[i-1].SAR.Long && pct1 <= -0.03,
			}
			for s := range cond {
				on := cond[s]
				if on && i+H < len(cs) {
					a := &per[s]
					r := cs[i+H].Close/cs[i].Close - 1
					a.n++
					a.sum += 100 * r
					if r > 0 {
						a.win++
					}
					if !prev[s] {
						a.edge++
					}
					lo := cs[i].Close
					for j := i + 1; j <= i+H; j++ {
						lo = math.Min(lo, cs[j].Low)
					}
					a.mae += 100 * (lo/cs[i].Close - 1)
				}
				prev[s] = on
			}
		}
		n := len(cs)
		i := n - 1
		cl := cs[i].Close
		r := rs[i]
		fmt.Printf("== %s %s (%s..%s)\n", c, k.Name, k.Dates[0], k.Dates[i])
		fmt.Printf("  今日 O=%.3f H=%.3f L=%.3f C=%.3f 日内分位=%.1f%% 开盘%+.2f%% | 昨SAR=%.3f(%v) 昨ST=%.3f 昨BOLL下=%.3f 昨Kelt下=%.3f\n",
			cs[i].Open, cs[i].High, cs[i].Low, cl, 100*(cl-cs[i].Low)/(cs[i].High-cs[i].Low), 100*(cs[i].Open/cs[i-1].Close-1),
			rs[i-1].SAR.Value, rs[i-1].SAR.Long, rs[i-1].SuperTrend.Value, rs[i-1].BOLL.Lower, rs[i-1].Keltner.Lower)
		for s := range names {
			fmt.Printf("  %-44s %v\n", names[s], per[s])
			t := &tot[s]
			p := per[s]
			t.n += p.n
			t.win += p.win
			t.edge += p.edge
			t.sum += p.sum
			t.mae += p.mae
		}
		atr := r.ATR.ATR14
		sh := shares[c]
		type lv struct {
			name string
			v    float64
		}
		lvs := []lv{{"MA5", ma(cs, i, 5)}, {"MA10", ma(cs, i, 10)}, {"MA20", ma(cs, i, 20)}, {"MA60", ma(cs, i, 60)},
			{"BOLL下", r.BOLL.Lower}, {"Kelt下", r.Keltner.Lower}, {"SAR", r.SAR.Value}, {"ST", r.SuperTrend.Value},
			{"D20低", r.Donchian.Lower20}, {"D55低", r.Donchian.Lower55}, {"兜底线", cost[c] * 0.8}}
		sort.Slice(lvs, func(a, b int) bool { return lvs[a].v > lvs[b].v })
		fmt.Printf("  ATR=%.3f 1ATR敞口=%.2f(%.2fx) 浮盈=%.2f(%.2f%%) 合规上限=%.0f股(按÷1.3)\n", atr, atr*sh, atr*sh/budget,
			(cl-cost[c])*sh, 100*(cl/cost[c]-1), budget/(1.3*atr))
		for _, l := range lvs {
			d := cl - l.v
			mark := ""
			if d > 0 && d/atr >= 1.3 && d*sh <= budget {
				mark = "✅合规"
			}
			fmt.Printf("    %-8s %8.3f 距%+6.2f%% ÷ATR=%5.2f 敞口=%8.2f (%.2fx) 100股=%.2fx %s\n", l.name, l.v, 100*(l.v/cl-1), d/atr, d*sh, d*sh/budget, d*100/budget, mark)
		}
		rr := make([]float64, W)
		for j := 0; j < W; j++ {
			rr[j] = cs[n-W+j].Close/cs[n-W+j-1].Close - 1
		}
		rets[c] = rr
		lastDates = k.Dates[n-W:]
	}
	fmt.Println("== 6 只合并")
	for s := range names {
		fmt.Printf("  %-44s %v\n", names[s], tot[s])
	}
	mv := map[string]float64{"sh601138": 34680, "sz000063": 8409, "sz002185": 5916, "sh603496": 3918, "sh601991": 2320, "sh512480": 650.4}
	for _, a := range codes {
		rest := make([]float64, W)
		var wsum float64
		for _, b := range codes {
			if b != a {
				wsum += mv[b]
			}
		}
		for _, b := range codes {
			if b == a {
				continue
			}
			for j := 0; j < W; j++ {
				rest[j] += mv[b] / wsum * rets[b][j]
			}
		}
		fmt.Printf("对其余部分 %s %+.3f\n", a, corr(rets[a], rest))
	}
	fmt.Printf("== 相关矩阵 %d 日收益 %s..%s\n", W, lastDates[0], lastDates[len(lastDates)-1])
	for _, a := range codes {
		fmt.Printf("%s", a)
		for _, b := range codes {
			fmt.Printf(" %6.3f", corr(rets[a], rets[b]))
		}
		fmt.Println()
	}
}
