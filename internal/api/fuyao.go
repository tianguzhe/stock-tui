package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// 同花顺扶摇金融数据服务（https://fuyao.aicubes.cn，文档 github.com/Hithink-Tech-Org/Financial-API）。
//
// 本项目只用它补齐腾讯/东财给不了的东西：除权除息事件、交易日历、当日异动原因，
// 以及回测用的、不受热榜池限制的连续日K。实时与落库主链路仍是腾讯 proxy。
//
// 口径陷阱（2026-11-19 实测，见 docs/reference/data-apis.md）：
//   - 前复权是**差值法**（前复权价 = 不复权价 − 累计每股分红），与腾讯 proxy 相同，不是等比；
//   - 个股 adjust=forward 按送转调整成交量、不调整成交额，Amount/Volume 不能直接当 VWAP；
//   - ETF/LOF 必须走基金接口，A 股接口会报 Unknown thscode；基金接口忽略 adjust=none。

// FuyaoKeyEnv 是 API Key 的环境变量名。Key 只从环境读取，绝不写进代码、配置或日志。
const FuyaoKeyEnv = "FUYAO_API_KEY"

const fuyaoBaseURL = "https://fuyao.aicubes.cn"

// ErrNoFuyaoKey 表示未配置 Key。调用方据此跳过可选功能，而不是当成故障。
var ErrNoFuyaoKey = errors.New(FuyaoKeyEnv + " 未设置")

// ErrFuyaoNotCovered 表示该类标的不在接口覆盖范围（如复权事件接口不含场内基金）。
var ErrFuyaoNotCovered = errors.New("扶摇该接口不覆盖此类标的")

// FuyaoError 是业务错误（HTTP 200 但 code != 0）。
type FuyaoError struct {
	Code      int
	Message   string
	RequestID string
}

func (e *FuyaoError) Error() string {
	return fmt.Sprintf("fuyao code=%d %s (request_id=%s)", e.Code, e.Message, e.RequestID)
}

// FuyaoClient 封装认证、响应信封解析与有界重试。
type FuyaoClient struct {
	http    *http.Client
	key     string
	baseURL string
	// backoff 为第 n 次重试前的等待；测试里置零。
	backoff func(n int) time.Duration
}

// NewFuyaoClient 从环境变量读取 Key；未设置时返回 ErrNoFuyaoKey。client 传 nil 用包内默认。
func NewFuyaoClient(client *http.Client) (*FuyaoClient, error) {
	key := strings.TrimSpace(os.Getenv(FuyaoKeyEnv))
	if key == "" {
		return nil, ErrNoFuyaoKey
	}
	return &FuyaoClient{
		http:    httpClientOrDefault(client),
		key:     key,
		baseURL: fuyaoBaseURL,
		backoff: func(n int) time.Duration { return time.Duration(1<<n) * time.Second },
	}, nil
}

// fuyaoMaxAttempts bounds retries for 4001(限流) / 5xxx / 网络错误。
// 1xxx/2xxx/3xxx 是调用方可修复或数据层面的错误，重试无意义，立即返回。
const fuyaoMaxAttempts = 3

func retryableFuyao(code int) bool { return code == 4001 || code >= 5000 }

// get 发起 GET 并把 data 解码到 out。
func (c *FuyaoClient) get(path string, params url.Values, out any) error {
	var lastErr error
	for attempt := 0; attempt < fuyaoMaxAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(c.backoff(attempt - 1))
		}
		retry, err := c.getOnce(path, params, out)
		if err == nil {
			return nil
		}
		lastErr = err
		if !retry {
			return err
		}
	}
	return fmt.Errorf("%s: 重试 %d 次仍失败: %w", path, fuyaoMaxAttempts, lastErr)
}

func (c *FuyaoClient) getOnce(path string, params url.Values, out any) (retry bool, err error) {
	req, err := http.NewRequest(http.MethodGet, c.baseURL+path+"?"+params.Encode(), nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("X-api-key", c.key)
	resp, err := c.http.Do(req)
	if err != nil {
		return true, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return true, err
	}
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests,
			fmt.Errorf("%s: HTTP %d", path, resp.StatusCode)
	}
	var env struct {
		Code      int             `json:"code"`
		Message   string          `json:"message"`
		RequestID string          `json:"request_id"`
		Data      json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return false, fmt.Errorf("%s: 解析响应: %w", path, err)
	}
	if env.Code != 0 {
		return retryableFuyao(env.Code), &FuyaoError{Code: env.Code, Message: env.Message, RequestID: env.RequestID}
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return false, fmt.Errorf("%s: 解析 data: %w", path, err)
	}
	return false, nil
}

// FuyaoThscode 把项目代码(sh600519)转成 thscode(600519.SH)。港股/美股不在服务覆盖内。
func FuyaoThscode(code string) (string, error) {
	if len(code) != 8 {
		return "", fmt.Errorf("不支持的代码 %q", code)
	}
	switch code[:2] {
	case "sh", "sz", "bj":
		return code[2:] + "." + strings.ToUpper(code[:2]), nil
	}
	return "", fmt.Errorf("扶摇不覆盖 %q（仅沪深北 A 股与场内基金）", code)
}

// codeFromThscode 是 FuyaoThscode 的逆变换。
func codeFromThscode(ths string) string {
	ticker, ex, ok := strings.Cut(ths, ".")
	if !ok {
		return ths
	}
	return strings.ToLower(ex) + ticker
}

// isExchangeFund 按代码段判定场内基金：沪 51/56/58，深 15/16（与 backtest.isFund 同段）。
// 这些代码必须走基金接口，A 股接口不认。
func isExchangeFund(code string) bool {
	if len(code) < 4 {
		return false
	}
	switch code[:4] {
	case "sh51", "sh56", "sh58", "sz15", "sz16":
		return true
	}
	return false
}

var shanghai = time.FixedZone("CST", 8*3600)

func msToDate(ms int64) string { return time.UnixMilli(ms).In(shanghai).Format("2006-01-02") }

func dateToMs(date string) (int64, error) {
	t, err := time.ParseInLocation("2006-01-02", date, shanghai)
	if err != nil {
		return 0, err
	}
	return t.UnixMilli(), nil
}

// ExRightEvent 是一次除权除息事件。
type ExRightEvent struct {
	ExDate           string  // 除权除息日 YYYY-MM-DD
	DividendPerShare float64 // 每股现金分红（税前，元）
	BonusPerShare    float64 // 每股送转股数
}

// AdjustmentFactors 返回标的在 [from, to] 内的除权除息事件，按除权日升序。from/to 可空。
func (c *FuyaoClient) AdjustmentFactors(code, from, to string) ([]ExRightEvent, error) {
	if isExchangeFund(code) {
		// 实测 512480 返回 1002 Unknown thscode：该接口只有 A 股。
		return nil, fmt.Errorf("%s: %w", code, ErrFuyaoNotCovered)
	}
	ths, err := FuyaoThscode(code)
	if err != nil {
		return nil, err
	}
	p := url.Values{"thscode": {ths}}
	if from != "" {
		p.Set("from", from)
	}
	if to != "" {
		p.Set("to", to)
	}
	var data struct {
		Item []struct {
			ExDateMs int64   `json:"ex_date_ms"`
			Dividend float64 `json:"dividend_per_share"`
			Bonus    float64 `json:"per_share_bonus"`
		} `json:"item"`
	}
	if err := c.get("/api/a-share/corporate-actions/adjustment-factors", p, &data); err != nil {
		// 区间内没有事件时服务端回 3002「No adjustment events」而不是空数组，
		// 这是合法的空结果，不能当失败上报（实测中兴、华天、招行均如此）。
		var fe *FuyaoError
		if errors.As(err, &fe) && fe.Code == 3002 {
			return nil, nil
		}
		return nil, err
	}
	out := make([]ExRightEvent, 0, len(data.Item))
	// 接口按除权日降序返回，翻转成升序便于按时间读。
	for i := len(data.Item) - 1; i >= 0; i-- {
		it := data.Item[i]
		out = append(out, ExRightEvent{ExDate: msToDate(it.ExDateMs), DividendPerShare: it.Dividend, BonusPerShare: it.Bonus})
	}
	return out, nil
}

// TradingDays 返回 [start, end] 内的 A 股交易日（YYYY-MM-DD，升序）。服务端只覆盖近一年。
func (c *FuyaoClient) TradingDays(start, end string) ([]string, error) {
	p := url.Values{
		"start_date": {strings.ReplaceAll(start, "-", "")},
		"end_date":   {strings.ReplaceAll(end, "-", "")},
	}
	var data struct {
		Item []struct {
			DateMs int64 `json:"date_ms"`
		} `json:"item"`
	}
	if err := c.get("/api/a-share/calendar/trading-days", p, &data); err != nil {
		return nil, err
	}
	out := make([]string, len(data.Item))
	for i, it := range data.Item {
		out[i] = msToDate(it.DateMs)
	}
	return out, nil
}

// AnomalyReason 是当日个股异动解读。
type AnomalyReason struct {
	Code     string // 项目代码，如 sh603496
	Name     string
	Tag      string // 涨停/跌停/大涨/大跌/快速拉升/快速下挫
	Keywords []string
	Content  string
}

// fuyaoAnomalyBatch 是 anomaly-analysis-stock 单次请求的代码上限。
const fuyaoAnomalyBatch = 50

// AnomalyReasons 查询**当日**异动原因（服务端只给当天快照，查历史日无意义）。
// 当日无异动的标的不出现在结果里。
func (c *FuyaoClient) AnomalyReasons(codes []string) ([]AnomalyReason, error) {
	var out []AnomalyReason
	for i := 0; i < len(codes); i += fuyaoAnomalyBatch {
		end := min(i+fuyaoAnomalyBatch, len(codes))
		ths := make([]string, 0, end-i)
		for _, code := range codes[i:end] {
			t, err := FuyaoThscode(code)
			if err != nil {
				continue // 港股等不覆盖的代码直接略过
			}
			ths = append(ths, t)
		}
		if len(ths) == 0 {
			continue
		}
		var data struct {
			Item []struct {
				Thscode  string   `json:"thscode"`
				Name     string   `json:"stock_name"`
				Tag      string   `json:"tag_name"`
				Keywords []string `json:"keyword_list"`
				Content  string   `json:"analysis_content"`
			} `json:"item"`
		}
		if err := c.get("/api/a-share/special-data/anomaly-analysis-stock", url.Values{"thscodes": {strings.Join(ths, ",")}}, &data); err != nil {
			return nil, err
		}
		for _, it := range data.Item {
			out = append(out, AnomalyReason{Code: codeFromThscode(it.Thscode), Name: it.Name, Tag: it.Tag, Keywords: it.Keywords, Content: it.Content})
		}
	}
	return out, nil
}

// FuyaoBar 是一根日K。价格为前复权（差值法），与腾讯 proxy 同口径。
type FuyaoBar struct {
	Date                   string
	Open, High, Low, Close float64
}

// fuyaoMaxSpan 是历史K线单次请求的最大跨度（服务端限 10 年）。
const fuyaoMaxSpan = 10*365*24*time.Hour - 24*time.Hour

// DailyBars 返回 [start, end] 内的前复权日K（升序）。场内基金自动改走基金接口。
// 前复权基准是**请求当天**的最新价：同一次回测里所有价格都取自同一次拉取，
// 才不会像逐日累积的 snapshot 那样混入多套基准。
func (c *FuyaoClient) DailyBars(code, start, end string) ([]FuyaoBar, error) {
	ths, err := FuyaoThscode(code)
	if err != nil {
		return nil, err
	}
	s, err := dateToMs(start)
	if err != nil {
		return nil, err
	}
	e, err := dateToMs(end)
	if err != nil {
		return nil, err
	}
	// end 是当天 00:00，加一天把当天这根K线包含进来。
	e += 24 * 3600 * 1000
	if time.Duration(e-s)*time.Millisecond > fuyaoMaxSpan {
		return nil, fmt.Errorf("区间 %s~%s 超过 10 年上限", start, end)
	}
	path := "/api/a-share/prices/historical"
	if isExchangeFund(code) {
		path = "/api/fund/market/historical"
	}
	p := url.Values{"thscode": {ths}, "interval": {"1d"}, "start": {fmt.Sprint(s)}, "end": {fmt.Sprint(e)}, "adjust": {"forward"}}
	var data struct {
		Item []struct {
			DateMs int64   `json:"date_ms"`
			Open   float64 `json:"open_price"`
			High   float64 `json:"high_price"`
			Low    float64 `json:"low_price"`
			Close  float64 `json:"close_price"`
		} `json:"item"`
	}
	if err := c.get(path, p, &data); err != nil {
		return nil, err
	}
	out := make([]FuyaoBar, 0, len(data.Item))
	for _, it := range data.Item {
		out = append(out, FuyaoBar{Date: msToDate(it.DateMs), Open: it.Open, High: it.High, Low: it.Low, Close: it.Close})
	}
	return out, nil
}
