package api

import (
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

type Stock struct {
	Code         string
	Name         string
	Price        float64
	Open         float64
	Close        float64 // 昨收
	High         float64
	Low          float64
	Change       float64 // 涨跌额
	ChangePct    float64 // 涨跌幅%
	Volume       float64 // 成交量(手)
	Amount       float64 // 成交额(万元)
	TurnoverRate float64 // 换手率 % (field index 38)
	// PE 口径存疑(field index 39): 2026-07-25 实测东材科技腾讯[39]=110.79,
	// 而东财 f162(动态市盈率)=56.39,约差 2 倍(疑为静态 PE 按上年报计);
	// 工商银行两者接近(7.44)因银行利润稳定。目前 PE 仅落库展示、不进任何
	// 筛选逻辑,故未深究。若要用于选股须先确认口径。
	PE        float64
	MarketCap float64 // 总市值 亿元 (field index 45)
	Precision int     // 价格小数位数（从原始字符串检测）
	UpdatedAt time.Time
}

var reStock = regexp.MustCompile(`v_([a-z]{2}\d+)="([^"]+)"`)

// IncompleteQuotesError 表示部分代码没拿到行情。FetchStocks 返回它时仍附带已解析
// 的行情：盯盘界面应照常显示拿到的部分，同时把缺失清单提示出来——静默丢掉一只
// 持仓会让组合少算一笔且没有任何症状。
type IncompleteQuotesError struct {
	Missing map[string]string // 代码 → 原因
}

func (e *IncompleteQuotesError) Error() string {
	codes := make([]string, 0, len(e.Missing))
	for c := range e.Missing {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	parts := make([]string, len(codes))
	for i, c := range codes {
		parts[i] = fmt.Sprintf("%s(%s)", c, e.Missing[c])
	}
	return fmt.Sprintf("%d 只行情缺失: %s", len(codes), strings.Join(parts, ", "))
}

// incompleteQuotes 汇总解析失败与请求了却未返回的代码；全部到齐时返回 nil。
func incompleteQuotes(requested []string, stocks []Stock, failed map[string]string) error {
	missing := make(map[string]string, len(failed))
	for c, reason := range failed {
		missing[c] = reason
	}
	got := make(map[string]bool, len(stocks))
	for _, s := range stocks {
		got[s.Code] = true
	}
	for _, c := range requested {
		if !got[c] && missing[c] == "" {
			missing[c] = "无返回"
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return &IncompleteQuotesError{Missing: missing}
}

// FetchStocks 批量拉实时报价。部分代码缺失时返回已拿到的行情 + *IncompleteQuotesError。
func FetchStocks(codes []string) ([]Stock, error) {
	url := fmt.Sprintf("https://qt.gtimg.cn/q=%s", strings.Join(codes, ","))

	req, err := newTencentRequest(url)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()
	if err := checkResponseStatus(resp); err != nil {
		return nil, err
	}

	reader := transform.NewReader(io.LimitReader(resp.Body, 10<<20), simplifiedchinese.GBK.NewDecoder())
	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("解码失败: %w", err)
	}

	stocks, failed := parseStocksPayload(string(body))
	return stocks, incompleteQuotes(codes, stocks, failed)
}

// parseStocksPayload 解析响应；解析失败的代码连同原因放进 failed。
func parseStocksPayload(body string) (stocks []Stock, failed map[string]string) {
	matches := reStock.FindAllStringSubmatch(body, -1)
	stocks = make([]Stock, 0, len(matches))
	failed = map[string]string{}

	for _, m := range matches {
		if len(m) < 3 {
			continue
		}
		s, err := parseStock(m[1], m[2])
		if err != nil {
			failed[m[1]] = err.Error()
			continue
		}
		stocks = append(stocks, s)
	}

	return stocks, failed
}

func parseStock(code, raw string) (Stock, error) {
	fields := strings.Split(raw, "~")
	if len(fields) < 38 {
		return Stock{}, fmt.Errorf("字段不足: %d", len(fields))
	}

	toF := func(s string) float64 {
		v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
		return v
	}

	s := Stock{
		Code:      code,
		Name:      fields[1],
		Price:     toF(fields[3]),
		Close:     toF(fields[4]),
		Open:      toF(fields[5]),
		Volume:    toF(fields[6]),
		High:      toF(fields[33]),
		Low:       toF(fields[34]),
		Change:    toF(fields[31]),
		ChangePct: toF(fields[32]),
		Amount:    toF(fields[37]),
		Precision: strPrecision(strings.TrimSpace(fields[3])),
		UpdatedAt: time.Now(),
	}
	if len(fields) > 38 {
		s.TurnoverRate = toF(fields[38])
	}
	if len(fields) > 39 {
		s.PE = toF(fields[39])
	}
	if len(fields) > 45 {
		s.MarketCap = toF(fields[45])
	}
	return s, nil
}

// strPrecision 从原始价格字符串检测小数位数，如 "2.01"→2，"1275.9"→1
func strPrecision(s string) int {
	idx := strings.Index(s, ".")
	if idx < 0 {
		return 0
	}
	return len(s) - idx - 1
}
