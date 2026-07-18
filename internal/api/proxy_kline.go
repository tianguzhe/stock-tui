package api

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// ProxyDailyBar 是 proxy.finance.qq.com newfqkline 解析后的单日 K。
//
// 字段布局（2026-07 curl 实测: sh600519/sz000001/sh601138/sz002916/sz159915/sh512480）:
//
//	[0]日期 [1]开 [2]收 [3]高 [4]低 [5]量(手) [6]恒为{} [7]换手率% [8]成交额(万元) [9]空串
//
// 振幅不在 K 行内: (High-Low)/昨收×100; 首根无昨收时用开盘价。
// Amount 写入时已从万元 ×10000 转元; Turnover 已从 % 转小数。
type ProxyDailyBar struct {
	Date      string
	Open      float64
	Close     float64
	High      float64
	Low       float64
	Volume    float64 // 股
	Amount    float64 // 元
	Turnover  float64 // 小数, 0.0047 = 0.47%
	Amplitude float64 // %
}

// ParseProxyDailyBars 解析 proxy newfqkline 的 qfqday/day 行序列。
// rows 元素为 JSON 数组元素(字符串数字或 {} 对象)。
func ParseProxyDailyBars(rows [][]json.RawMessage) ([]ProxyDailyBar, error) {
	out := make([]ProxyDailyBar, 0, len(rows))
	var prevClose float64
	for i, row := range rows {
		if len(row) < 6 {
			return nil, fmt.Errorf("proxy kline row %d short: len=%d", i, len(row))
		}
		cells := make([]string, len(row))
		for j, raw := range row {
			cells[j] = rawJSONCell(raw)
		}
		bar, err := ParseProxyDailyBarCells(cells, prevClose)
		if err != nil {
			return nil, fmt.Errorf("proxy kline row %d: %w", i, err)
		}
		out = append(out, bar)
		prevClose = bar.Close
	}
	return out, nil
}

// ParseProxyDailyBarCells 解析单行字符串单元格(便于 fixture 单测)。
// prevClose 为昨收; <=0 时用当日开盘价算振幅。
func ParseProxyDailyBarCells(cells []string, prevClose float64) (ProxyDailyBar, error) {
	if len(cells) < 6 {
		return ProxyDailyBar{}, fmt.Errorf("short row: len=%d", len(cells))
	}
	open := parseFloatCell(cells[1])
	close := parseFloatCell(cells[2])
	high := parseFloatCell(cells[3])
	low := parseFloatCell(cells[4])
	vol := parseFloatCell(cells[5]) * 100 // 手→股

	amount := 0.0
	if len(cells) > 8 {
		// row[8] 单位是万元 → 元
		amount = parseFloatCell(cells[8]) * 10000
	}
	if amount <= 0 {
		amount = close * vol // 回退: Close × 股数
	}

	turnover := 0.0
	if len(cells) > 7 {
		turnover = parseFloatCell(cells[7]) / 100 // %→小数
	}

	prev := prevClose
	if prev <= 0 {
		prev = open
	}
	amp := 0.0
	if prev > 0 {
		amp = (high - low) / prev * 100
	}

	return ProxyDailyBar{
		Date:      cells[0],
		Open:      open,
		Close:     close,
		High:      high,
		Low:       low,
		Volume:    vol,
		Amount:    amount,
		Turnover:  turnover,
		Amplitude: amp,
	}, nil
}

// TurnoverUseful 判断换手率序列是否有有效正值(全 0/nil 视为不可用,触发东财/TDX 兜底)。
func TurnoverUseful(t []float64) bool {
	for _, v := range t {
		if v > 0 {
			return true
		}
	}
	return false
}

// StripJSONP 剥离 kline_dayqfq={...} 前缀,返回纯 JSON 字节。
func StripJSONP(raw []byte) []byte {
	if idx := indexByte(raw, '{'); idx > 0 {
		return raw[idx:]
	}
	return raw
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}

// rawJSONCell 把 JSON 元素转成字符串: 字符串去引号, 数字原样, 对象/数组得空串。
func rawJSONCell(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	// {} / [] / null → 空,调用方不读 [6]
	return ""
}

func parseFloatCell(s string) float64 {
	if s == "" {
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return f
}
