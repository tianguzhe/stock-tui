package main

import (
	"fmt"

	"stock-tui/internal/api"
)

// fetchViaTDX 通过 TDX HTTP 网关获取历史日K,作为备选数据源。
// 网关返回前复权 OHLC + Amount + Volume + 换手率。
// 失败时调用方应回退到 HTTP 方案。
func fetchViaTDX(code string, count int) (api.KlineData, error) {
	data, err := api.FetchTDXGatewayKline(code, count)
	if err != nil {
		return api.KlineData{}, fmt.Errorf("tdx 网关: %w", err)
	}
	return data, nil
}
