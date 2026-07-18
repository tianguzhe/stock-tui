package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"stock-tui/internal/api"
	"stock-tui/internal/indicator"

	tdx "github.com/quantbeing/tdx"
	"github.com/quantbeing/tdx/model"
)

// fetchViaTDX 通过通达信 TCP 协议获取历史日K,作为主力数据源。
// 一次调用即返回 OHLCV + Amount + 换手率(通过流通股本本地算)。
// 失败时调用方应回退到 HTTP 方案。
func fetchViaTDX(code string, count int) (api.KlineData, error) {
	// 映射 sh600522 → (MarketSH, "600522")
	var marketID model.Market
	rawCode := code
	if strings.HasPrefix(code, "sh") {
		marketID = model.MarketSH
		rawCode = code[2:]
	} else if strings.HasPrefix(code, "sz") || strings.HasPrefix(code, "bj") {
		marketID = model.MarketSZ
		rawCode = code[2:]
	} else {
		return api.KlineData{}, fmt.Errorf("tdx: 未知前缀 %s", code)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := tdx.FromBestHost(ctx, tdx.Options{
		MaxAttempts: 2,
		Timeout:     15 * time.Second,
	})
	if err != nil {
		return api.KlineData{}, fmt.Errorf("tdx FromBestHost: %w", err)
	}
	defer client.Close()

	// 拉日K
	bars, err := client.GetSecurityBars(ctx, marketID, rawCode, model.KlineDay, 0, count)
	if err != nil {
		return api.KlineData{}, fmt.Errorf("tdx GetSecurityBars: %w", err)
	}
	if len(bars) == 0 {
		return api.KlineData{}, fmt.Errorf("tdx: 无数据")
	}

	// 拉流通股本(用于换手率),非致命
	freeShares := 0.0
	if fin, err := client.GetFinanceInfo(ctx, marketID, rawCode); err == nil {
		freeShares = fin.LiutongGuben
	}

	dates := make([]string, len(bars))
	candles := make([]indicator.Candle, len(bars))
	turnovers := make([]float64, len(bars))
	for i, b := range bars {
		dates[i] = fmt.Sprintf("%04d-%02d-%02d", b.Year, b.Month, b.Day)
		candles[i] = indicator.Candle{
			Open:   b.Open.Float64(),
			Close:  b.Close.Float64(),
			High:   b.High.Float64(),
			Low:    b.Low.Float64(),
			Volume: b.Vol,
			Amount: b.Amount,
		}
		if freeShares > 0 && b.Vol > 0 {
			turnovers[i] = b.Vol / freeShares // 小数(0.0646=6.46%)
		}
	}

	return api.KlineData{Code: code, Name: code, Dates: dates, Candles: candles, Turnovers: turnovers}, nil
}

// fetchTDXTurnoverFallback 东财换手率失败时的 TDX 兜底: 只拉流通股本,
// 用 HTTP 源的 Volume 本地算 turnover = Vol / LiutongGuben。
// 不拉日K(价格仍用 HTTP 前复权),仅建立 TCP 取 GetFinanceInfo。
func fetchTDXTurnoverFallback(code string, candles []indicator.Candle) []float64 {
	var marketID model.Market
	rawCode := code
	if strings.HasPrefix(code, "sh") {
		marketID = model.MarketSH
		rawCode = code[2:]
	} else if strings.HasPrefix(code, "sz") || strings.HasPrefix(code, "bj") {
		marketID = model.MarketSZ
		rawCode = code[2:]
	} else {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	client, err := tdx.FromBestHost(ctx, tdx.Options{MaxAttempts: 3, Timeout: 15 * time.Second})
	if err != nil {
		return nil
	}
	defer client.Close()

	fin, err := client.GetFinanceInfo(ctx, marketID, rawCode)
	if err != nil {
		return nil
	}
	if fin.LiutongGuben <= 0 {
		return nil
	}

	turnovers := make([]float64, len(candles))
	for i, c := range candles {
		if c.Volume > 0 {
			turnovers[i] = c.Volume / fin.LiutongGuben
		}
	}
	return turnovers
}
