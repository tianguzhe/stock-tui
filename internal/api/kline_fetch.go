package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"stock-tui/internal/indicator"

	tdx "github.com/quantbeing/tdx"
	"github.com/quantbeing/tdx/model"
)

// KlineData 是日K数据统一容器，proxy 和东财 fallback 共用。
type KlineData struct {
	Code       string
	Name       string
	Dates      []string
	Candles    []indicator.Candle
	Turnovers  []float64 // 换手率(小数, 0.0047 = 0.47%)
	Amplitudes []float64 // 振幅 % = (高-低)/昨收 × 100
	VolRatioRT float64   // 量比(实时, qt[46])
	InsideVol  float64   // 内盘(手, 主动卖)
	OutsideVol float64   // 外盘(手, 主动买)
}

// proxyQuote 是 proxy.qq.com JSONP 响应的 data[code] 结构。
type proxyQuote struct {
	Qfqday [][]json.RawMessage          `json:"qfqday"`
	Day    [][]json.RawMessage          `json:"day"`
	Qt     map[string][]json.RawMessage `json:"qt"`
}

// proxyResponse 是 proxy.qq.com JSONP 响应的顶层结构。
type proxyResponse struct {
	Data map[string]proxyQuote `json:"data"`
}

// emKlineResponse 是东财 kline API 的 JSON 响应结构。
type emKlineResponse struct {
	Data struct {
		Klines []string `json:"klines"`
	} `json:"data"`
}

// FetchDailyKline 是日K获取总入口：proxy 优先 + 换手率兜底 + 东财 fallback。
// client 传 nil 时使用包内默认 httpClient。
func FetchDailyKline(client *http.Client, code string, bars int) (KlineData, error) {
	c := httpClientOrDefault(client)
	data, err := FetchProxyKline(c, code, bars)
	if err == nil {
		if !TurnoverUseful(data.Turnovers) {
			turnovers := FetchEMTurnover(c, code, len(data.Candles), data.Dates)
			if turnovers == nil {
				turnovers = FetchTDXTurnover(code, data.Candles)
			}
			if turnovers != nil {
				data.Turnovers = turnovers
			}
		}
		return data, nil
	}
	fmt.Fprintf(os.Stderr, "info: 腾讯日K失败(%v),切换到东财日K\n", err)
	return FetchEMKline(c, code, bars)
}

// FetchProxyKline 从腾讯 proxy.finance.qq.com newfqkline 拉前复权日K。
// 字段单位换算见 ParseProxyDailyBars(万元→元、换手%→小数、振幅本地算)。
func FetchProxyKline(client *http.Client, code string, bars int) (KlineData, error) {
	c := httpClientOrDefault(client)
	url := fmt.Sprintf(
		"https://proxy.finance.qq.com/ifzqgtimg/appstock/app/newfqkline/get?_var=kline_dayqfq&param=%s,day,,,%d,qfq",
		code, bars,
	)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return KlineData{}, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := c.Do(req)
	if err != nil {
		return KlineData{}, fmt.Errorf("fetch %s: %w", code, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return KlineData{}, fmt.Errorf("fetch %s: HTTP %s", code, resp.Status)
	}

	rawBody, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return KlineData{}, err
	}

	body := StripJSONP(rawBody)

	var parsed proxyResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return KlineData{}, err
	}

	raw, ok := parsed.Data[code]
	if !ok {
		return KlineData{}, fmt.Errorf("no klines for %s", code)
	}

	rows := raw.Qfqday
	if len(rows) == 0 {
		rows = raw.Day
	}
	if len(rows) == 0 {
		return KlineData{}, fmt.Errorf("no klines for %s", code)
	}

	name := code
	var volRatioRT, insideVol, outsideVol float64
	if q, ok := raw.Qt[code]; ok && len(q) > 1 {
		name = rawJSONCell(q[1])
		// proxy.qq.com qt: [7]内盘手 [8]外盘手 [46]量比
		if len(q) > 46 {
			volRatioRT = parseFloatCell(rawJSONCell(q[46]))
		}
		if len(q) > 8 {
			insideVol = parseFloatCell(rawJSONCell(q[7]))
			outsideVol = parseFloatCell(rawJSONCell(q[8]))
		}
	}

	parsedBars, err := ParseProxyDailyBars(rows)
	if err != nil {
		return KlineData{}, err
	}
	dates := make([]string, len(parsedBars))
	candles := make([]indicator.Candle, len(parsedBars))
	amplitudes := make([]float64, len(parsedBars))
	turnovers := make([]float64, len(parsedBars))
	for i, b := range parsedBars {
		dates[i] = b.Date
		candles[i] = indicator.Candle{
			Open: b.Open, Close: b.Close, High: b.High, Low: b.Low,
			Volume: b.Volume, Amount: b.Amount,
		}
		amplitudes[i] = b.Amplitude
		turnovers[i] = b.Turnover
	}

	return KlineData{
		Code: code, Name: name, Dates: dates, Candles: candles,
		Turnovers: turnovers, Amplitudes: amplitudes,
		VolRatioRT: volRatioRT, InsideVol: insideVol, OutsideVol: outsideVol,
	}, nil
}

// FetchEMKline 从东财 push2his 拉前复权日K，用作腾讯日K的 HTTP fallback。
// 东财 Amount(f57) 已是元(无需×10000)、振幅 f58 直给、换手 f61 直给;
// 无 qt 内外盘/实时量比。
func FetchEMKline(client *http.Client, code string, bars int) (KlineData, error) {
	c := httpClientOrDefault(client)
	secid, err := CodeToSecid(code)
	if err != nil {
		return KlineData{}, err
	}

	// K 线接口每行固定 11 列(fields2=f51..f61); f116(总市值)是实时行情 stock/get
	// 的字段, K 线接口会忽略它——切勿加入, 否则列数假设错位。
	url := fmt.Sprintf(
		"https://push2his.eastmoney.com/api/qt/stock/kline/get?secid=%s&fields1=f1,f2,f3,f4,f5,f6&fields2=f51,f52,f53,f54,f55,f56,f57,f58,f59,f60,f61&klt=101&fqt=1&end=20500101&lmt=%d",
		secid, bars,
	)

	body, err := FetchEMWithRetry(c, url)
	if err != nil {
		return KlineData{}, fmt.Errorf("东财日K %s: %w", code, err)
	}

	var emResp emKlineResponse
	if err := json.Unmarshal(body, &emResp); err != nil {
		return KlineData{}, fmt.Errorf("东财JSON解析失败: %w", err)
	}
	if len(emResp.Data.Klines) == 0 {
		return KlineData{}, fmt.Errorf("东财no klines for %s", code)
	}

	dates, candles, turnovers, amplitudes := parseEMKlines(emResp.Data.Klines)
	if len(candles) == 0 {
		return KlineData{}, fmt.Errorf("东财no valid klines for %s", code)
	}

	return KlineData{
		Code: code, Name: code, Dates: dates, Candles: candles,
		Turnovers: turnovers, Amplitudes: amplitudes,
	}, nil
}

// FetchEMTurnover 从东财轻量拉换手率(f51+f61),按日期对齐到 dates 序列。
// 全部失败/无数据时返回 nil。
func FetchEMTurnover(client *http.Client, code string, count int, dates []string) []float64 {
	c := httpClientOrDefault(client)
	secid, err := CodeToSecid(code)
	if err != nil {
		return nil
	}

	url := fmt.Sprintf(
		"https://push2his.eastmoney.com/api/qt/stock/kline/get?secid=%s&fields1=f1,f2,f3&fields2=f51,f61&klt=101&fqt=1&end=20500101&lmt=%d",
		secid, count)

	body, err := FetchEMWithRetry(c, url)
	if err != nil {
		return nil
	}

	var emResp emKlineResponse
	if err := json.Unmarshal(body, &emResp); err != nil {
		return nil
	}

	return alignEMTurnovers(emResp.Data.Klines, dates)
}

// FetchTDXTurnover 东财换手率失败时的 TDX 兜底: 只拉流通股本,
// 用 HTTP 源的 Volume 本地算 turnover = Vol / LiutongGuben。
// 失败返回 nil。
func FetchTDXTurnover(code string, candles []indicator.Candle) []float64 {
	marketID, rawCode, err := CodeToTDXMarket(code)
	if err != nil {
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

// CodeToSecid 将 sh/sz/bj 代码映射为东财 secid（如 "sh600522" → "1.600522"）。
func CodeToSecid(code string) (string, error) {
	if len(code) < 3 {
		return "", fmt.Errorf("invalid code: %s", code)
	}
	prefix := ""
	switch code[:2] {
	case "sh":
		prefix = "1"
	case "sz", "bj":
		prefix = "0"
	default:
		return "", fmt.Errorf("unsupported code prefix: %s", code)
	}
	return prefix + "." + code[2:], nil
}

// CodeToTDXMarket 将 sh/sz/bj 代码映射为 TDX market ID 和裸码。
func CodeToTDXMarket(code string) (model.Market, string, error) {
	if len(code) < 3 {
		return 0, "", fmt.Errorf("invalid code: %s", code)
	}
	rawCode := code[2:]
	switch code[:2] {
	case "sh":
		return model.MarketSH, rawCode, nil
	case "sz", "bj":
		return model.MarketSZ, rawCode, nil
	default:
		return 0, "", fmt.Errorf("unsupported code prefix: %s", code)
	}
}

// httpClientOrDefault 返回 client 或包内默认 httpClient。
func httpClientOrDefault(client *http.Client) *http.Client {
	if client != nil {
		return client
	}
	return httpClient
}

// emTurnoverMinMatch 是东财换手率对齐 dates 的最低匹配率。
// 低于此值说明东财返回与 K 线序列日期对不齐(数据残缺), 返回 nil 触发 TDX 兜底,
// 避免用大量 0 换手的残缺序列冒充"有效"污染 CYQ。
const emTurnoverMinMatch = 0.8

// parseEMKlines 解析东财 push2his kline 的 klines 字符串数组。
// 每行固定 11 列(fields2=f51..f61): 日期,开,收,高,低,量(手),额(元),振幅%,涨跌幅%,涨跌额,换手率%。
// OHLC 任一 <=0 视为坏行(解析失败或异常价)跳过, 避免静默产生 0 价 K 线污染下游指标。
func parseEMKlines(klines []string) (dates []string, candles []indicator.Candle, turnovers, amplitudes []float64) {
	for _, ks := range klines {
		parts := strings.Split(ks, ",")
		if len(parts) < 11 {
			continue
		}
		open := parseEMFloat(parts[1])
		close := parseEMFloat(parts[2])
		high := parseEMFloat(parts[3])
		low := parseEMFloat(parts[4])
		if open <= 0 || close <= 0 || high <= 0 || low <= 0 {
			continue
		}
		dates = append(dates, parts[0])
		candles = append(candles, indicator.Candle{
			Open:   open,
			Close:  close,
			High:   high,
			Low:    low,
			Volume: parseEMFloat(parts[5]) * 100, // 手→股
			Amount: parseEMFloat(parts[6]),       // 元
		})
		amplitudes = append(amplitudes, parseEMFloat(parts[7]))    // f58 振幅 %
		turnovers = append(turnovers, parseEMFloat(parts[10])/100) // f61 %→小数
	}
	return dates, candles, turnovers, amplitudes
}

// alignEMTurnovers 把东财 klines(f51 日期, f61 换手%)按日期对齐到 dates 序列。
// 匹配率低于 emTurnoverMinMatch 或 dates 为空时返回 nil(触发 TDX 兜底)。
func alignEMTurnovers(klines []string, dates []string) []float64 {
	if len(dates) == 0 {
		return nil
	}
	turnMap := make(map[string]float64, len(klines))
	for _, ks := range klines {
		commaPos := strings.IndexByte(ks, ',')
		if commaPos < 0 || commaPos >= len(ks)-1 {
			continue
		}
		turnMap[ks[:commaPos]] = parseEMFloat(ks[commaPos+1:])
	}

	turns := make([]float64, len(dates))
	matched := 0
	for i, d := range dates {
		if v, ok := turnMap[d]; ok {
			turns[i] = v / 100 // 东财 f61 是 %,转小数
			matched++
		}
	}
	if float64(matched) < float64(len(dates))*emTurnoverMinMatch {
		return nil
	}
	return turns
}

// parseEMFloat 解析东财 CSV 字段为 float64。
func parseEMFloat(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}
