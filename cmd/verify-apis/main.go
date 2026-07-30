package main

import (
	"fmt"
	"net/http"
	"os"
	"stock-tui/internal/api"
	"time"
)

var client = &http.Client{Timeout: 15 * time.Second}

func main() {
	// 多标的逐个验证
	tests := []struct {
		code string
		name string
	}{
		{"sh601208", "东材科技(个股)"},
		{"sh512480", "半导体ETF"},
		{"sh600522", "中天科技(个股)"},
		{"sz000001", "平安银行(深市)"},
	}
	for _, t := range tests {
		runTest(t.code, t.name)
		fmt.Printf("\n")
	}
}

func runTest(code, name string) {
	bars := 20

	// 1. 腾讯 proxy（默认 HTTP 路径）
	proxyData, err := api.FetchProxyKline(client, code, bars)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ %s %s proxy失败: %v\n", name, code, err)
	} else {
		printComparison("腾讯 proxy", proxyData)
	}

	// 2. TDX 网关（-tdx 路径）
	tdxData, err := api.FetchTDXGatewayKline(code, bars)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ %s %s TDX网关失败: %v\n", name, code, err)
	} else {
		printComparison("TDX 网关 ", tdxData)
	}

	// 3. 东财 K-line（直接调用）
	emData, err := api.FetchEMKline(client, code, bars)
	if err != nil {
		fmt.Fprintf(os.Stderr, "   %s %s 东财: %v\n", name, code, err)
	} else {
		printComparison("东财     ", emData)
	}

	// 4. 深度差异分析
	if proxyData.Dates != nil && tdxData.Dates != nil && len(proxyData.Candles) > 0 && len(tdxData.Candles) > 0 {
		diffAnalysis(fmt.Sprintf("【%s】%s", name, code), proxyData, tdxData, emData)
	}
}

func printComparison(label string, d api.KlineData) {
	n := len(d.Candles)
	if n == 0 {
		fmt.Printf("%s: 无数据\n", label)
		return
	}
	last := d.Candles[n-1]
	amt := ""
	if last.Amount > 0 {
		amt = fmt.Sprintf("额=%.0f亿", last.Amount/1e8)
	}
	fmt.Printf("%s | 日期=%s | O=%.2f H=%.2f L=%.2f C=%.2f Vol=%.0f万 %s\n",
		label, d.Dates[n-1], last.Open, last.High, last.Low, last.Close,
		float64(last.Volume)/10000, amt)

	start := n - 5
	if start < 0 {
		start = 0
	}
	fmt.Printf("%s | 换手(近5日):", label)
	for i := start; i < n; i++ {
		if i < len(d.Turnovers) {
			fmt.Printf(" %.2f%%", d.Turnovers[i]*100)
		}
	}
	fmt.Println()
}

func diffAnalysis(label string, proxy, tdx, em api.KlineData) {
	n := len(proxy.Candles)
	if t := len(tdx.Candles); t < n {
		n = t
	}
	if n > 10 {
		n = 10
	}
	if n == 0 {
		return
	}
	fmt.Printf("=== %s ===\n", label)

	hasEM := len(em.Candles) >= n && len(em.Dates) >= n

	for i := 0; i < n; i++ {
		pi := len(proxy.Candles) - n + i
		ti := len(tdx.Candles) - n + i
		ei := 0
		if hasEM {
			ei = len(em.Candles) - n + i
		}

		proxyDate := ""
		if pi >= 0 && pi < len(proxy.Dates) {
			proxyDate = proxy.Dates[pi]
		}
		tdxDate := ""
		if ti >= 0 && ti < len(tdx.Dates) {
			tdxDate = tdx.Dates[ti]
		}

		if proxyDate != tdxDate {
			continue
		}

		p := proxy.Candles[pi]
		t := tdx.Candles[ti]

		diffO := pctDiff(p.Open, t.Open)
		diffH := pctDiff(p.High, t.High)
		diffL := pctDiff(p.Low, t.Low)
		diffC := pctDiff(p.Close, t.Close)
		maxDiff := max4(diffO, diffH, diffL, diffC)

		fmt.Printf("  %s\n", proxyDate)
		fmt.Printf("    Proxy O=%.2f H=%.2f L=%.2f C=%.2f Vol=%.0f万\n",
			p.Open, p.High, p.Low, p.Close, float64(p.Volume)/10000)
		fmt.Printf("    TDX   O=%.2f H=%.2f L=%.2f C=%.2f Vol=%.0f万\n",
			t.Open, t.High, t.Low, t.Close, float64(t.Volume)/10000)
		if hasEM {
			e := em.Candles[ei]
			fmt.Printf("    EM    O=%.2f H=%.2f L=%.2f C=%.2f Vol=%.0f万\n",
				e.Open, e.High, e.Low, e.Close, float64(e.Volume)/10000)
		}

		if maxDiff > 0.5 {
			fmt.Printf("    ⚠️ OHLC 差异 > 0.5%%! max=%.2f%% (O=%.2f%% H=%.2f%% L=%.2f%% C=%.2f%%)\n",
				maxDiff, diffO, diffH, diffL, diffC)
		} else {
			fmt.Printf("    ✅ OHLC 一致 (max diff=%.2f%%)\n", maxDiff)
		}

		// 换手率对比
		tp := proxy.Turnovers[pi] * 100
		tt := tdx.Turnovers[ti] * 100
		fmt.Printf("    Proxy换手=%.4f%%  TDX换手=%.4f%%", tp, tt)
		if hasEM {
			te := em.Turnovers[ei] * 100
			fmt.Printf("  EM换手=%.4f%%", te)
		}
		fmt.Printf("\n")

		if tp > 0 && tt > 0 {
			trDiff := abs(tp - tt)
			if trDiff > 0.1 {
				fmt.Printf("    ⚠️ Proxy vs TDX 换手率差异 > 0.1%%! diff=%.4f%%\n", trDiff)
			}
		}
		fmt.Printf("\n")
	}

	fmt.Printf("  ---\n")
	fmt.Printf("  数据范围: Proxy=%s~%s (%d根)  TDX=%s~%s (%d根)",
		proxy.Dates[0], proxy.Dates[len(proxy.Dates)-1], len(proxy.Dates),
		tdx.Dates[0], tdx.Dates[len(tdx.Dates)-1], len(tdx.Dates))
	if hasEM {
		fmt.Printf("  EM=%s~%s (%d根)",
			em.Dates[0], em.Dates[len(em.Dates)-1], len(em.Dates))
	}
	fmt.Printf("\n")
}

func pctDiff(a, b float64) float64 {
	if a == 0 && b == 0 {
		return 0
	}
	if a == 0 || b == 0 {
		return 100
	}
	return abs((a - b) / ((a + b) / 2) * 100)
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func max4(a, b, c, d float64) float64 {
	v := a
	if b > v {
		v = b
	}
	if c > v {
		v = c
	}
	if d > v {
		v = d
	}
	return v
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}
