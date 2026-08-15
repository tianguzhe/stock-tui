package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"stock-tui/internal/indicator"
)

// tdxGatewayURL is the TDX HTTP MCP gateway URL.
const tdxGatewayURL = "http://tdxhub.icfqs.com:7615/TQLEX"

// tdxGatewayClient is the HTTP client for TDX gateway calls.
var tdxGatewayClient = &http.Client{Timeout: 15 * time.Second}

// tdxPBHQResponse models the TdxShare.PBHQInfo response.
type tdxPBHQResponse struct {
	Code    string      `json:"Code"`
	Setcode string      `json:"Setcode"`
	HQInfo  *tdxHQInfo  `json:"HQInfo,omitempty"`
	ExtInfo *tdxExtInfo `json:"ExtInfo,omitempty"`
}

type tdxHQInfo struct {
	Now     float64 `json:"Now"`
	Close   float64 `json:"Close"`
	Open    float64 `json:"Open"`
	MaxP    float64 `json:"MaxP"`
	MinP    float64 `json:"MinP"`
	Volume  string  `json:"Volume"` // 字符串如 "772258" (手)
	Amount  float64 `json:"Amount"`
	Yield   float64 `json:"Yield"`
	HSL     float64 `json:"HSL"`     // 换手率%, 小数
	LB      float64 `json:"LB"`      // 量比
	Inside  string  `json:"Inside"`  // 字符串如 "395768" (手)
	Outside string  `json:"Outside"` // 字符串如 "376490" (手)
	Jjjz    float64 `json:"Jjjz"`    // 基金单位净值(仅 ETF/基金有意义;非基金股票返回流通股本等无关值)
	Average float64 `json:"Average"` // 均价/VWAP
}

type tdxExtInfo struct {
	ZTPrice  float64 `json:"ZTPrice"`  // 涨停价
	DTPrice  float64 `json:"DTPrice"`  // 跌停价
	ZGB      float64 `json:"ZGB"`      // 总股本(万)
	LTGB     float64 `json:"LTGB"`     // 流通股本(万)
	ZSZ      float64 `json:"ZSZ"`      // 总市值(元)
	SYL      float64 `json:"SYL"`      // 市盈率
	MGSY     float64 `json:"MGSY"`     // 每股收益
	MGJZC    float64 `json:"MGJZC"`    // 每股净资产
	FreeLtgb float64 `json:"FreeLtgb"` // 自由流通股本(万)
}

// tdxPBFXTResponse models the TdxShare.PBFXT K-line response.
type tdxPBFXTResponse struct {
	Setcode    int            `json:"Setcode"`
	Code       string         `json:"Code"`
	Period     int            `json:"Period"`
	ListItem   []tdxPBFXTItem `json:"ListItem"`
	AttachInfo *tdxAttachInfo `json:"AttachInfo,omitempty"`
}

type tdxPBFXTItem struct {
	Item []string `json:"Item"`
}

type tdxAttachInfo struct {
	Name     string  `json:"Name"`
	Close    float64 `json:"Close"`
	Open     float64 `json:"Open"`
	MaxP     float64 `json:"MaxP"`
	MinP     float64 `json:"MinP"`
	Now      float64 `json:"Now"`
	Volume   string  `json:"Volume"` // 字符串, 如 "772258" (万? 还是手?)
	Amount   float64 `json:"Amount"`
	FHSL     float64 `json:"fHSL"`     // 当日换手率(%)
	FAverage float64 `json:"fAverage"` // 当日均价
}

// codeToTDXSetcode 映射 sh/sz/bj 到 TDX 网关 setcode。
// sh→1, sz→0, bj→2。
func codeToTDXSetcode(code string) string {
	switch {
	case strings.HasPrefix(code, "sh"):
		return "1"
	case strings.HasPrefix(code, "sz"):
		return "0"
	case strings.HasPrefix(code, "bj"):
		return "2"
	default:
		return ""
	}
}

// codeToTDXSetcodeInt 返回 int 版的 setcode（用于 PBFXT 请求）。
func codeToTDXSetcodeInt(code string) int {
	switch {
	case strings.HasPrefix(code, "sh"):
		return 1
	case strings.HasPrefix(code, "sz"):
		return 0
	case strings.HasPrefix(code, "bj"):
		return 2
	default:
		return -1
	}
}

// FetchTDXGatewayTurnover 通过 TDX HTTP 网关获取流通股本（万股），
// 本地计算换手率 turnover = Volume(股) / (LTGB_万 * 10000)。
// 替代原有的 FetchTDXTurnover（TDX TCP 协议版）。
// 失败时返回 nil。
func FetchTDXGatewayTurnover(code string, candles []indicator.Candle) []float64 {
	setcode := codeToTDXSetcode(code)
	if setcode == "" {
		return nil
	}
	rawCode := code[2:]

	ltgb, err := tdxFetchLTGB(rawCode, setcode)
	if err != nil || ltgb <= 0 {
		return nil
	}

	turnovers := make([]float64, len(candles))
	ltgbShares := ltgb * 10000 // 万股 → 股
	for i, c := range candles {
		if c.Volume > 0 {
			turnovers[i] = float64(c.Volume) / ltgbShares
		}
	}
	return turnovers
}

// tdxFetchLTGB 调用 PBHQInfo 获取流通股本（万股）。
func tdxFetchLTGB(code, setcode string) (float64, error) {
	body := map[string]interface{}{
		"Head":       map[string]interface{}{"Target": "0", "CharSet": "UTF8"},
		"Code":       code,
		"Setcode":    setcode,
		"HasHQInfo":  "0",
		"HasExtInfo": "1",
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}

	req, err := http.NewRequest(http.MethodPost,
		tdxGatewayURL+"?Entry=TdxShare.PBHQInfo",
		strings.NewReader(string(payload)))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := tdxGatewayClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, err
	}

	var result tdxPBHQResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return 0, err
	}
	if result.ExtInfo == nil {
		return 0, fmt.Errorf("tdx gateway: no ExtInfo for %s", code)
	}
	return result.ExtInfo.LTGB, nil
}

// TDXStockInfo 是 TDX 网关 PBHQInfo 返回的股票基本信息。
type TDXStockInfo struct {
	Code              string  // 代码
	Name              string  // 名称
	Price             float64 // 现价(元)
	Close             float64 // 昨收(元)
	ZTPrice           float64 // 涨停价
	DTPrice           float64 // 跌停价
	PE                float64 // 市盈率(倍)
	EPS               float64 // 每股收益(元)
	NAV               float64 // 每股净资产(元)
	MarketCap         float64 // 总市值(元)
	CirculatingShares float64 // 流通股本(万股)
	FreeCirculShares  float64 // 自由流通股本(万股)
	HSL               float64 // 换手率(% 小数)
	LB                float64 // 量比
	Average           float64 // 均价/VWAP(元)

}

// FetchTDXGatewayStockInfo 从 TDX 网关获取股票基本面和实时行情信息。
// 用于补充腾讯 qt 不提供的字段: 涨跌停价、每股收益、每股净资产、总市值等。
// 失败时返回 nil。
func FetchTDXGatewayStockInfo(code string) *TDXStockInfo {
	setcode := codeToTDXSetcode(code)
	if setcode == "" {
		return nil
	}
	rawCode := code[2:]

	body := map[string]interface{}{
		"Head":        map[string]interface{}{"Target": "0", "CharSet": "UTF8"},
		"Code":        rawCode,
		"Setcode":     setcode,
		"HasHQInfo":   "1",
		"HasExtInfo":  "1",
		"BspNum":      "0",
		"HasProInfo":  "0",
		"HasCalcInfo": "0",
		"HasCwInfo":   "0",
		"HasStatInfo": "0",
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil
	}

	req, err := http.NewRequest(http.MethodPost,
		tdxGatewayURL+"?Entry=TdxShare.PBHQInfo",
		strings.NewReader(string(payload)))
	if err != nil {
		return nil
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := tdxGatewayClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil
	}

	var result tdxPBHQResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil
	}

	info := &TDXStockInfo{
		Code: code,
	}
	if hq := result.HQInfo; hq != nil {
		info.Price = hq.Now
		info.Close = hq.Close
		info.HSL = hq.HSL
		info.LB = hq.LB
		info.Average = hq.Average
	}
	if ext := result.ExtInfo; ext != nil {
		info.ZTPrice = ext.ZTPrice
		info.DTPrice = ext.DTPrice
		info.PE = ext.SYL
		info.EPS = ext.MGSY
		info.NAV = ext.MGJZC
		info.MarketCap = ext.ZSZ
		info.CirculatingShares = ext.LTGB
		info.FreeCirculShares = ext.FreeLtgb
	}
	return info
}

// FetchTDXGatewayKline 通过 TDX HTTP 网关获取历史日K数据。
// 返回前复权 OHLC + Amount + Volume(股) + Turnovers(小数)。
// 替代 cmd/indicator-analyze/tdx.go 中的 fetchViaTDX（TCP 版）。
func FetchTDXGatewayKline(code string, bars int) (KlineData, error) {
	setcode := codeToTDXSetcodeInt(code)
	if setcode < 0 {
		return KlineData{}, fmt.Errorf("tdx gateway: 未知前缀 %s", code)
	}
	rawCode := code[2:]

	body := map[string]interface{}{
		"Head":          map[string]interface{}{"Target": 0, "CharSet": "UTF8"},
		"Code":          rawCode,
		"Setcode":       setcode,
		"Period":        4,
		"Startxh":       0,
		"WantNum":       bars,
		"TQFlag":        1,
		"MPData":        0,
		"HasAttachInfo": 1,
		"HasLtgb":       1,
		"ForRefresh":    0,
		"HasIpoPrice":   0,
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return KlineData{}, err
	}

	req, err := http.NewRequest(http.MethodPost,
		tdxGatewayURL+"?Entry=TdxShare.PBFXT",
		strings.NewReader(string(payload)))
	if err != nil {
		return KlineData{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := tdxGatewayClient.Do(req)
	if err != nil {
		return KlineData{}, fmt.Errorf("tdx gateway K线请求 %s: %w", code, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return KlineData{}, fmt.Errorf("tdx gateway 读取 %s: %w", code, err)
	}

	var result tdxPBFXTResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return KlineData{}, fmt.Errorf("tdx gateway JSON解析 %s: %w", code, err)
	}
	if len(result.ListItem) == 0 {
		return KlineData{}, fmt.Errorf("tdx gateway: 无K线数据 %s", code)
	}

	// Item 字段索引: [0]=日期, [1]=?, [2]=O, [3]=H, [4]=L, [5]=C, [6]=金额(元), [7]=金额dup, [8]=量(股,已由网关统一为股), [9]=流通股本(万股)
	const (
		idxDate   = 0
		idxOpen   = 2
		idxHigh   = 3
		idxLow    = 4
		idxClose  = 5
		idxAmount = 6
		idxVolume = 8
		idxLTGB   = 9
		minFields = idxLTGB + 1
	)

	var ltgbShares float64 // 万股 → 股
	if first := result.ListItem[0].Item; len(first) >= minFields {
		ltgbShares = parseFloat64(first[idxLTGB]) * 10000
	}

	n := len(result.ListItem)
	dates := make([]string, 0, n)
	candles := make([]indicator.Candle, 0, n)
	turnovers := make([]float64, 0, n)

	for _, item := range result.ListItem {
		fields := item.Item
		// 网关某行字段不全或日期格式异常时跳过该行, 不中断整批解析(与 proxy/东财路径一致)。
		if len(fields) < minFields || len(fields[idxDate]) < 8 {
			continue
		}
		date := fields[idxDate]
		o := parseFloat64(fields[idxOpen])
		h := parseFloat64(fields[idxHigh])
		l := parseFloat64(fields[idxLow])
		c := parseFloat64(fields[idxClose])
		if o <= 0 || h <= 0 || l <= 0 || c <= 0 {
			continue
		}
		amt := parseFloat64(fields[idxAmount])
		vol := parseFloat64(fields[idxVolume])

		dates = append(dates, fmt.Sprintf("%s-%s-%s", date[:4], date[4:6], date[6:8]))
		candles = append(candles, indicator.Candle{
			Open:   o,
			High:   h,
			Low:    l,
			Close:  c,
			Volume: vol,
			Amount: amt,
		})
		var turnover float64
		if ltgbShares > 0 && vol > 0 {
			turnover = vol / ltgbShares
		}
		turnovers = append(turnovers, turnover)
	}

	return KlineData{
		Code:      code,
		Name:      code,
		Dates:     dates,
		Candles:   candles,
		Turnovers: turnovers,
	}, nil
}

// parseFloat64 转换字符串到 float64（所有负值视为 0），
// 为空时返回 0。
func parseFloat64(s string) float64 {
	if s == "" || s == "-" {
		return 0
	}
	var v float64
	if _, err := fmt.Sscanf(s, "%f", &v); err != nil {
		return 0
	}
	if v < 0 {
		return 0
	}
	return v
}
