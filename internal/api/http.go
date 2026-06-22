package api

import (
	"fmt"
	"net/http"
	"time"
)

// Shared HTTP client with 15s timeout. All API calls should use this
// instead of http.DefaultClient to prevent indefinite blocking on slow
// or hung remote servers.
var httpClient = &http.Client{Timeout: 15 * time.Second}

func newTencentRequest(url string) (*http.Request, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")
	req.Header.Set("Referer", "https://finance.qq.com")
	return req, nil
}

func checkResponseStatus(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("响应状态异常: %s", resp.Status)
}
