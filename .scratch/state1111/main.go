// Scratch: 6 holdings x 800 daily bars — forward 10-day stats for 11-11 states + 40-day correlation.
package main

import (
	"fmt"
	"math"
	"net/http"
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

func main() {
	codes := []string{"sh601138", "sz000063", "sz002185", "sh603496", "sh601991", "sh512480"}
	names := []string{
		"S1 SAR当日翻多+ST空(富联今日)",
		"S2 SAR当日翻多+ST空+当日≥+4%(富联今日)",
		"S3 SAR空/ST多+当日≥+2%(华天今日)",
		"S4 双空+MACD当日金叉(恒为今日)",
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
			h, hp := rs[i].MACD.Histogram, rs[i-1].MACD.Histogram
			cross := h > 0 && hp <= 0
			cond := []bool{
				sar.Long && sar.Reversed && !st.Long,
				sar.Long && sar.Reversed && !st.Long && pct1 >= 0.04,
				!sar.Long && st.Long && pct1 >= 0.02,
				!sar.Long && !st.Long && cross,
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
		fmt.Printf("== %s %s (%s..%s)\n", c, k.Name, k.Dates[0], k.Dates[len(k.Dates)-1])
		for s := range names {
			fmt.Printf("  %-40s %v\n", names[s], per[s])
			t := &tot[s]
			p := per[s]
			t.n += p.n
			t.win += p.win
			t.edge += p.edge
			t.sum += p.sum
			t.mae += p.mae
		}
		n := len(cs)
		r := make([]float64, W)
		for j := 0; j < W; j++ {
			r[j] = cs[n-W+j].Close/cs[n-W+j-1].Close - 1
		}
		rets[c] = r
		lastDates = k.Dates[n-W:]
	}
	fmt.Println("== 6 只合并")
	for s := range names {
		fmt.Printf("  %-40s %v\n", names[s], tot[s])
	}
	mv := map[string]float64{"sh601138": 36448, "sz000063": 8580, "sz002185": 5980, "sh603496": 3970, "sh601991": 2360, "sh512480": 647.2}
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
