package main

import (
	"fmt"
	"os"
	"stock-tui/internal/api"
)

func init() {
	fmt.Println("--- TDX 基本面信息 ---")
	tests := []string{"sh601208", "sh512480", "sz000001", "bj920819"}
	for _, code := range tests {
		info := api.FetchTDXGatewayStockInfo(code)
		if info == nil {
			fmt.Fprintf(os.Stderr, "%s: ❌ 失败\n", code)
			continue
		}
		fmt.Printf("%s: 涨停=%.2f 跌停=%.2f PE=%.1f EPS=%.2f NAV=%.2f 市值=%.0f亿 换手=%.2f%%\n",
			code, info.ZTPrice, info.DTPrice, info.PE, info.EPS, info.NAV, info.MarketCap/1e8, info.HSL)
	}
	fmt.Println()
}
