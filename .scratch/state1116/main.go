// Scratch: 11-16 holdings — state backtests (1/5/10d), 40-day correlation, stop grid.
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
	w1, w5       int
	s1, s5       float64
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
	l1, h1 := wilson(a.w1, a.n)
	n := float64(a.n)
	return fmt.Sprintf("n=%d(边沿%d) win1=%.0f%% [%.1f, %.1f] avg1=%+.2f%% | win5=%.0f%% avg5=%+.2f%% | win10=%.0f%% [%.1f, %.1f] avg10=%+.2f%% MAE10=%.2f%%",
		a.n, a.edge, 100*float64(a.w1)/n, l1, h1, a.s1/n, 100*float64(a.w5)/n, a.s5/n, 100*float64(a.win)/n, lo, hi, a.sum/n, a.mae/n)
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
	codes := []string{"sh601138", "sz000063", "sz002185", "sh603496", "sh512480"}
	shares := map[string]float64{"sh601138": 1100, "sz000063": 300, "sz002185": 400, "sh603496": 200, "sh512480": 800}
	cost := map[string]float64{"sh601138": 51.448, "sz000063": 34.703, "sz002185": 17.150, "sh603496": 25.586, "sh512480": 1.351}
	budget := 593.3756
	names := []string{
		"S1 双空+当日≤-9%(富联今日跌停)",
		"S2 双空+2日累计≤-12%(富联今日-17.6%)",
		"S3 双空+ADX<15(中兴今日)",
		"S4 SAR空/ST多+MACD当日死叉(华天今日)",
		"S5 SAR多/ST空+当日≤-3%+量比≥1.5(恒为今日)",
		"S6 SAR多/ST空+当日≤-5%(半导体今日)",
		"S7 双空+深度超卖RSI6<25(富联今日)",
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
			pct2 := cs[i].Close/cs[i-2].Close - 1
			sar, st := rs[i].SAR, rs[i].SuperTrend
			v5 := 0.0
			for j := i - 5; j < i; j++ {
				v5 += cs[j].Volume
			}
			vr := cs[i].Volume / (v5 / 5)
			dualShort := !sar.Long && !st.Long
			macdDead := rs[i].MACD.Histogram < 0 && rs[i-1].MACD.Histogram >= 0
			cond := []bool{
				dualShort && pct1 <= -0.09,
				dualShort && pct2 <= -0.12,
				dualShort && rs[i].DMI.ADX < 15,
				!sar.Long && st.Long && macdDead,
				sar.Long && !st.Long && pct1 <= -0.03 && vr >= 1.5,
				sar.Long && !st.Long && pct1 <= -0.05,
				dualShort && rs[i].RSI.RSI6 < 25,
			}
			for s := range cond {
				on := cond[s]
				if on && i+H < len(cs) {
					a := &per[s]
					r := cs[i+H].Close/cs[i].Close - 1
					r1 := cs[i+1].Close/cs[i].Close - 1
					r5 := cs[i+5].Close/cs[i].Close - 1
					a.n++
					a.sum += 100 * r
					a.s1 += 100 * r1
					a.s5 += 100 * r5
					if r > 0 {
						a.win++
					}
					if r1 > 0 {
						a.w1++
					}
					if r5 > 0 {
						a.w5++
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
		fmt.Printf("== %s %s (%s..%s) 今日 C=%.3f pct=%+.2f%% 2日=%+.2f%% ADX=%.1f RSI6=%.1f MACD.H=%.4f/%.4f\n", c, k.Name, k.Dates[0], k.Dates[i], cl,
			100*(cl/cs[i-1].Close-1), 100*(cl/cs[i-2].Close-1), r.DMI.ADX, r.RSI.RSI6, r.MACD.Histogram, rs[i-1].MACD.Histogram)
		for s := range names {
			fmt.Printf("  %-44s %v\n", names[s], per[s])
			t := &tot[s]
			p := per[s]
			t.n += p.n
			t.win += p.win
			t.edge += p.edge
			t.sum += p.sum
			t.mae += p.mae
			t.w1 += p.w1
			t.w5 += p.w5
			t.s1 += p.s1
			t.s5 += p.s5
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
			fmt.Printf("    %-8s %8.3f 距%+6.2f%% ÷ATR=%5.2f 敞口=%8.2f (%.2fx) %s\n", l.name, l.v, 100*(l.v/cl-1), d/atr, d*sh, d*sh/budget, mark)
		}
		// 10 日最大单日跌幅分布：今日 ATR% 下，未来 10 日内出现 ≥1 ATR 不利收盘的频率
		hit := 0
		tot10 := 0
		for j := 60; j+10 < n; j++ {
			a := rs[j].ATR.ATR14
			lo := cs[j].Close
			for q := j + 1; q <= j+10; q++ {
				lo = math.Min(lo, cs[q].Close)
			}
			tot10++
			if cs[j].Close-lo >= 1.3*a {
				hit++
			}
		}
		fmt.Printf("  历史：任一日起 10 日内收盘回撤 ≥1.3 ATR 的比例 %.0f%% (%d/%d)\n", 100*float64(hit)/float64(tot10), hit, tot10)
		rr := make([]float64, W)
		for j := 0; j < W; j++ {
			rr[j] = cs[n-W+j].Close/cs[n-W+j-1].Close - 1
		}
		rets[c] = rr
		lastDates = k.Dates[n-W:]
	}
	fmt.Println("== 5 只合并")
	for s := range names {
		fmt.Printf("  %-44s %v\n", names[s], tot[s])
	}
	mv := map[string]float64{"sh601138": 39842, "sz000063": 8262, "sz002185": 5584, "sh603496": 3842, "sh512480": 599.2}
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
	// 组合 40 日日收益波动（按今日市值加权）
	var pv []float64
	var tw float64
	for _, c := range codes {
		tw += mv[c]
	}
	for j := 0; j < W; j++ {
		x := 0.0
		for _, c := range codes {
			x += mv[c] / tw * rets[c][j]
		}
		pv = append(pv, x)
	}
	m := 0.0
	for _, x := range pv {
		m += x
	}
	m /= float64(W)
	v := 0.0
	worst := 0.0
	for _, x := range pv {
		v += (x - m) * (x - m)
		worst = math.Min(worst, x)
	}
	fmt.Printf("组合 40 日日收益 σ=%.2f%% 最差=%.2f%% （按今日市值 %.0f）\n", 100*math.Sqrt(v/float64(W-1)), 100*worst, tw)
}
