package api

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// StockInfo 个股东财基本面信息
type StockInfo struct {
	Code        string  // 股票代码(f57)
	Name        string  // 股票简称(f58)
	TotalShares float64 // 总股本(股,f84)
	FloatShares float64 // 流通股本(股,f85)
	Industry    string  // 行业(f127)
	Region      string  // 地区板块(f128)
	ListedDate  string  // 上市日期 YYYYMMDD(f189)
	TotalMC     float64 // 总市值(元,f116)
	FloatMC     float64 // 流通市值(元,f117)
	PE          float64 // 市盈率(f162)
	PB          float64 // 市净率(f167)
}

// 东财反限流: 随机 User-Agent 池 + NID cookie 预取。
// NID 是访问东财经纪网站时下发的 session cookie,后续 API 请求携带可降低限流概率。
var (
	emUA = [...]string{
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/138.0.0.0 Safari/537.36",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/138.0.0.0 Safari/537.36",
		"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/137.0.0.0 Safari/537.36",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:140.0) Gecko/20100101 Firefox/140.0",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/19.6 Safari/605.1.15",
	}
	emCookieOnce sync.Once
	emCookieJar  *cookieJar
)

// cookieJar 用于在首次请求东财经纪网站时保存 session cookies(NID 等),
// 后续 API 请求自动携带,降低被限流概率。
type cookieJar struct {
	mu      sync.RWMutex
	cookies []*http.Cookie
}

func (j *cookieJar) SetCookies(_ *url.URL, cookies []*http.Cookie) {
	j.mu.Lock()
	defer j.mu.Unlock()
	// 按 name 合并: 同名覆盖、新名追加, 避免预热请求发生重定向、多次 Set-Cookie 时
	// 直接赋值丢掉先前批次的 cookie。
	byName := make(map[string]int, len(j.cookies))
	for i, c := range j.cookies {
		byName[c.Name] = i
	}
	for _, c := range cookies {
		if idx, ok := byName[c.Name]; ok {
			j.cookies[idx] = c
		} else {
			byName[c.Name] = len(j.cookies)
			j.cookies = append(j.cookies, c)
		}
	}
}

func (j *cookieJar) Cookies(u *url.URL) []*http.Cookie {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.cookies
}

// emRandomUA 返回随机 User-Agent。
func emRandomUA() string {
	return emUA[rand.Intn(len(emUA))]
}

// emInitCookie 预热 NID cookie: 首次调用时访问东财经纪网站拿 session cookie。
// 后续调用直接复用。失败不致命——cookie 只是降低限流概率,不是必须。
func emInitCookie() {
	emCookieOnce.Do(func() {
		jar := &cookieJar{}
		c := &http.Client{
			Timeout: 10 * time.Second,
			Jar:     jar,
		}
		req, _ := http.NewRequest(http.MethodGet, "https://quote.eastmoney.com/", nil)
		req.Header.Set("User-Agent", emRandomUA())
		resp, err := c.Do(req)
		if err == nil {
			resp.Body.Close()
		}
		emCookieJar = jar
	})
}

// newEMRequest 创建带反限流 headers 的东财 API 请求。
// 自动注入随机 UA、Referer、Accept 和预取的 NID session cookie。
func newEMRequest(url string) (*http.Request, error) {
	emInitCookie()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", emRandomUA())
	req.Header.Set("Referer", "https://data.eastmoney.com/")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	if emCookieJar != nil {
		// 注入预取的 session cookies
		for _, c := range emCookieJar.Cookies(req.URL) {
			req.AddCookie(c)
		}
	}
	return req, nil
}

// NewEMRequest 创建带反限流 headers 的东财 API 请求(导出版)。
// 供 cmd 内联的东财请求复用(如换手率/日K fallback)。
func NewEMRequest(url string) (*http.Request, error) {
	return newEMRequest(url)
}

// emFetchWithRetry 东财请求重试封装(内部版)。
//
// client 为 nil 时回退包内默认 client，与 FetchProxyKline / FetchEMKline 等
// 导出入口的约定保持一致——否则导出的 FetchEMWithRetry 传 nil 会直接 panic
// 在 client.Do 上，而同包其他函数都接受 nil。
func emFetchWithRetry(client *http.Client, url string) ([]byte, error) {
	client = httpClientOrDefault(client)
	for attempt := 0; attempt < 3; attempt++ {
		req, err := newEMRequest(url)
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		if err != nil {
			if resp != nil {
				resp.Body.Close()
				resp = nil
			}
			if attempt < 2 {
				delay := time.Duration(300*pow3(attempt)) * time.Millisecond
				jitter := time.Duration(rand.Intn(100*(attempt+1))) * time.Millisecond
				time.Sleep(delay + jitter)
			}
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			if attempt < 2 {
				delay := time.Duration(300*pow3(attempt)) * time.Millisecond
				jitter := time.Duration(rand.Intn(100*(attempt+1))) * time.Millisecond
				time.Sleep(delay + jitter)
			}
			continue
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
		if err != nil {
			return nil, fmt.Errorf("读取响应失败: %w", err)
		}
		return body, nil
	}
	return nil, fmt.Errorf("东财请求失败(重试3次)")
}

// pow3 返回 3^n 用于指数退避。
func pow3(n int) int {
	switch n {
	case 0:
		return 1
	case 1:
		return 3
	case 2:
		return 9
	default:
		return 1
	}
}

// FetchEMWithRetry 东财请求重试封装(导出版): 指数退避 + 随机 jitter。
// 共 3 次尝试, 两次重试间隔 300ms→900ms(+jitter); 仍失败则返回 error。
// 供 cmd 内联的东财请求复用。
func FetchEMWithRetry(client *http.Client, url string) ([]byte, error) {
	return emFetchWithRetry(client, url)
}

// FetchStockInfo 从东方财富获取个股基本面信息(总股本/流通股本/行业/上市日期等)。
// 使用 push2.eastmoney.com/api/qt/stock/get 接口,带反限流措施。
func FetchStockInfo(code string) (*StockInfo, error) {
	prefix := "0"
	if len(code) >= 2 && code[:2] == "sh" {
		prefix = "1"
	}
	rawCode := code
	if len(code) >= 2 && (code[:2] == "sh" || code[:2] == "sz" || code[:2] == "bj") {
		rawCode = code[2:]
	}
	secid := prefix + "." + rawCode

	fields := "f57,f58,f84,f85,f116,f117,f127,f128,f162,f167,f189"
	url := fmt.Sprintf(
		"https://push2.eastmoney.com/api/qt/stock/get?secid=%s&fltt=2&invt=2&fields=%s",
		secid, fields,
	)

	body, err := emFetchWithRetry(httpClient, url)
	if err != nil {
		return nil, err
	}

	var payload struct {
		Data struct {
			F57  string  `json:"f57"`  // 代码
			F58  string  `json:"f58"`  // 简称
			F84  float64 `json:"f84"`  // 总股本
			F85  float64 `json:"f85"`  // 流通股本
			F116 float64 `json:"f116"` // 总市值
			F117 float64 `json:"f117"` // 流通市值
			F127 string  `json:"f127"` // 行业
			F128 string  `json:"f128"` // 地区板块
			F162 float64 `json:"f162"` // 市盈率
			F167 float64 `json:"f167"` // 市净率
			F189 float64 `json:"f189"` // 上市日期 YYYYMMDD(东财返回 int,不可用 string)
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("JSON解析失败: %w", err)
	}
	if payload.Data.F57 == "" {
		return nil, fmt.Errorf("无数据: %s", code)
	}

	info := &StockInfo{
		Code:        payload.Data.F57,
		Name:        strings.TrimSpace(payload.Data.F58),
		TotalShares: payload.Data.F84,
		FloatShares: payload.Data.F85,
		Industry:    strings.TrimSpace(payload.Data.F127),
		Region:      strings.TrimSpace(payload.Data.F128),
		ListedDate:  fmt.Sprintf("%.0f", payload.Data.F189),
		TotalMC:     payload.Data.F116,
		FloatMC:     payload.Data.F117,
		PE:          payload.Data.F162,
		PB:          payload.Data.F167,
	}
	return info, nil
}
