// Scratch: 6 holdings x 800 daily bars, forward 10-day stats for 11-09 states.
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
	recl         int
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
	return fmt.Sprintf("n=%d(边沿%d) win10=%.0f%% [%.1f, %.1f] avg10=%+.2f%% MAE=%.2f%% 10日内收复ST=%.0f%%",
		a.n, a.edge, 100*float64(a.win)/float64(a.n), lo, hi, a.sum/float64(a.n), a.mae/float64(a.n), 100*float64(a.recl)/float64(a.n))
}

func main() {
	codes := []string{"sh601138", "sz000063", "sz002185", "sh603496", "sh601991", "sh512480"}
	names := []string{"S1 双空+当日≥+4%", "S2 SAR当日翻多+ST空", "S3 SAR多/ST空 全部日", "S0 双空 全部日"}
	client := &http.Client{Timeout: 20 * time.Second}
	tot := make([]acc, 4)
	const H = 10
	for _, c := range codes {
		k, err := api.FetchDailyKline(client, c, 800)
		if err != nil {
			fmt.Println(c, err)
			continue
		}
		cs := k.Candles
		rs := indicator.Calculate(cs)
		per := make([]acc, 4)
		prev := make([]bool, 4)
		for i := 60; i < len(cs); i++ {
			pct := cs[i].Close/cs[i-1].Close - 1
			sar, st := rs[i].SAR, rs[i].SuperTrend
			cond := []bool{
				!sar.Long && !st.Long && pct >= 0.04,
				sar.Reversed && sar.Long && !st.Long,
				sar.Long && !st.Long,
				!sar.Long && !st.Long,
			}
			for s := 0; s < 4; s++ {
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
					rec := false
					for j := i + 1; j <= i+H; j++ {
						lo = math.Min(lo, cs[j].Low)
						if rs[j].SuperTrend.Long {
							rec = true
						}
					}
					a.mae += 100 * (lo/cs[i].Close - 1)
					if rec {
						a.recl++
					}
				}
				prev[s] = on
			}
		}
		fmt.Printf("== %s %s (%s..%s)\n", c, k.Name, k.Dates[0], k.Dates[len(k.Dates)-1])
		for s := 0; s < 4; s++ {
			fmt.Printf("  %-22s %v\n", names[s], per[s])
			t := &tot[s]
			p := per[s]
			t.n += p.n
			t.win += p.win
			t.edge += p.edge
			t.sum += p.sum
			t.mae += p.mae
			t.recl += p.recl
		}
	}
	fmt.Println("== 6 只合并")
	for s := 0; s < 4; s++ {
		fmt.Printf("  %-22s %v\n", names[s], tot[s])
	}
}
