package api

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

type Stock struct {
	Code      string
	Name      string
	Price     float64
	Open      float64
	Close     float64 // 昨收
	High      float64
	Low       float64
	Change    float64 // 涨跌额
	ChangePct float64 // 涨跌幅%
	Volume    float64 // 成交量(手)
	Amount    float64 // 成交额(万元)
	Precision int     // 价格小数位数（从原始字符串检测）
	UpdatedAt time.Time
}

var reStock = regexp.MustCompile(`v_[a-z]{2}\d+="([^"]+)"`)

func FetchStocks(codes []string) ([]Stock, error) {
	url := fmt.Sprintf("https://qt.gtimg.cn/q=%s", strings.Join(codes, ","))

	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")
	req.Header.Set("Referer", "https://finance.qq.com")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	reader := transform.NewReader(resp.Body, simplifiedchinese.GBK.NewDecoder())
	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("解码失败: %w", err)
	}

	matches := reStock.FindAllStringSubmatch(string(body), -1)
	stocks := make([]Stock, 0, len(matches))

	for i, m := range matches {
		if len(m) < 2 {
			continue
		}
		s, err := parseStock(codes[i], m[1])
		if err != nil {
			continue
		}
		stocks = append(stocks, s)
	}

	return stocks, nil
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

	return Stock{
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
	}, nil
}

// strPrecision 从原始价格字符串检测小数位数，如 "2.01"→2，"1275.9"→1
func strPrecision(s string) int {
	idx := strings.Index(s, ".")
	if idx < 0 {
		return 0
	}
	return len(s) - idx - 1
}
