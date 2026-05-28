package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
)

type MinutePoint struct {
	Time   string
	Price  float64 // 每分钟收盘价
	Volume float64
}

type MinuteResult struct {
	Code      string
	Name      string
	PClose    float64 // 昨收价
	Precision int     // 价格小数位数
	Points    []MinutePoint
}

func FetchMinute(code string) (*MinuteResult, error) {
	url := fmt.Sprintf(
		"https://ifzq.gtimg.cn/appstock/app/kline/mkline?param=%s,m1,,240",
		code,
	)

	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")
	req.Header.Set("Referer", "https://finance.qq.com")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	return parseMinutePayload(code, body)
}

func parseMinutePayload(code string, body []byte) (*MinuteResult, error) {
	// 用 RawMessage 处理数组中混有 string 和 {} 的情况
	var payload struct {
		Code json.Number `json:"code"`
		Data map[string]struct {
			Qt map[string][]string `json:"qt"`
			M1 [][]json.RawMessage `json:"m1"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("JSON解析失败: %w", err)
	}

	stockData, ok := payload.Data[code]
	if !ok {
		return nil, fmt.Errorf("无此股票数据: %s", code)
	}

	// 从 qt 提取名称、昨收价、精度
	// qt 格式: [market, name, code, price, pclose, open, ...]
	var name string
	var pclose float64
	prec := 2 // 默认 2 位小数
	if qtArr, ok := stockData.Qt[code]; ok && len(qtArr) > 4 {
		name = qtArr[1]
		pclose, _ = strconv.ParseFloat(qtArr[4], 64)
		if len(qtArr) > 3 {
			prec = strPrecision(qtArr[3]) // 从当前价字符串检测
		}
	}

	result := &MinuteResult{
		Code:      code,
		Name:      name,
		PClose:    pclose,
		Precision: prec,
		Points:    make([]MinutePoint, 0, len(stockData.M1)),
	}

	// m1 格式: [datetime, open, close, high, low, volume, {}, amount]
	// datetime 例: "202605280931"
	getRaw := func(bar []json.RawMessage, i int) string {
		if i >= len(bar) {
			return ""
		}
		var s string
		if err := json.Unmarshal(bar[i], &s); err != nil {
			return ""
		}
		return s
	}

	for _, bar := range stockData.M1 {
		dt := getRaw(bar, 0)
		closeStr := getRaw(bar, 2)
		volStr := getRaw(bar, 5)

		price, _ := strconv.ParseFloat(closeStr, 64)
		if price == 0 || len(dt) < 12 {
			continue
		}

		vol, _ := strconv.ParseFloat(volStr, 64)
		t := dt[8:10] + ":" + dt[10:12]

		result.Points = append(result.Points, MinutePoint{
			Time:   t,
			Price:  price,
			Volume: vol,
		})
	}

	return result, nil
}
