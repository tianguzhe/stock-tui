package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// HotStock represents a stock from the THS (同花顺) hot list.
type HotStock struct {
	Code   string // with market prefix, e.g. "sh600519" (no zero-padding)
	Name   string
	Market string // "sh" or "sz"
}

// thsHotListURL is the 同花顺人气榜 API endpoint.
const thsHotListURL = "https://dq.10jqka.com.cn/fuyao/hot_list_data/out/hot_list/v1/stock?stock_type=a&type=hour&list_type=skyrocket"

// FetchHotStocks retrieves the THS hot stock list and returns mainboard A-share
// stocks. Code is the raw concatenation of market prefix + raw code string
// (no zero-padding), matching the original Python import script.
func FetchHotStocks() ([]HotStock, error) {
	req, err := http.NewRequest(http.MethodGet, thsHotListURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch THS hot list: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("THS hot list: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, fmt.Errorf("read THS response: %w", err)
	}

	return parseHotStocksPayload(body)
}

// thsResponse models the JSON returned by the THS hot list API.
// Code is a plain string (e.g. "002354", "600519") — no zero-padding needed.
type thsResponse struct {
	Data struct {
		StockList []struct {
			Code   string `json:"code"`   // raw digits, e.g. "002354", "600519"
			Name   string `json:"name"`
			Market int    `json:"market"` // 17=沪市, 33=深市
			Order  int    `json:"order"`
		} `json:"stock_list"`
	} `json:"data"`
}

// parseHotStocksPayload parses the THS JSON and filters to mainboard stocks.
func parseHotStocksPayload(body []byte) ([]HotStock, error) {
	var resp thsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse THS JSON: %w", err)
	}

	stocks := make([]HotStock, 0, len(resp.Data.StockList))
	for _, s := range resp.Data.StockList {
		if !isMainboardTHS(s.Market, s.Code) {
			continue
		}
		mkt := marketFromTHS(s.Market)
		stocks = append(stocks, HotStock{
			Code:   mkt + s.Code, // "sh" + "600519" = "sh600519"
			Name:   s.Name,
			Market: mkt,
		})
	}
	return stocks, nil
}

// isMainboardTHS returns true for 沪市(17) / 深市(33) mainboard stocks,
// excluding 创业板 (3xx) and 科创板 (688xxx).
// Code is the raw numeric string from the API (no zero-padding).
func isMainboardTHS(market int, code string) bool {
	if market != 17 && market != 33 {
		return false
	}
	if len(code) > 0 && code[0] == '3' {
		return false // 创业板
	}
	if len(code) >= 3 && code[:3] == "688" {
		return false // 科创板
	}
	return true
}

// marketFromTHS maps THS market code to sh/sz prefix.
func marketFromTHS(market int) string {
	if market == 17 {
		return "sh"
	}
	return "sz"
}
