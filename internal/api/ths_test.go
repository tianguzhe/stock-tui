package api

import (
	"testing"
)

// Sample response from THS hot list API — code is a string (like the real API).
const sampleTHSResponse = `{
  "data": {
    "stock_list": [
      {"code": "600519", "name": "贵州茅台", "market": 17, "order": 1},
      {"code": "000088", "name": "盐田港", "market": 33, "order": 2},
      {"code": "300750", "name": "宁德时代", "market": 33, "order": 3},
      {"code": "688981", "name": "中芯国际", "market": 17, "order": 4},
      {"code": "000001", "name": "平安银行", "market": 33, "order": 5},
      {"code": "601398", "name": "建设银行", "market": 17, "order": 6}
    ]
  }
}`

func TestParseHotStocksPayload(t *testing.T) {
	stocks, err := parseHotStocksPayload([]byte(sampleTHSResponse))
	if err != nil {
		t.Fatalf("parseHotStocksPayload: %v", err)
	}

	// Should only contain mainboard stocks (excluded 创业板 300750, 科创板 688981)
	if len(stocks) != 4 {
		t.Fatalf("expected 4 mainboard stocks, got %d", len(stocks))
	}

	// Verify codes — no zero-padding, raw string concatenation
	if stocks[0].Code != "sh600519" {
		t.Errorf("stocks[0].Code = %q, want %q", stocks[0].Code, "sh600519")
	}
	if stocks[0].Name != "贵州茅台" {
		t.Errorf("stocks[0].Name = %q, want %q", stocks[0].Name, "贵州茅台")
	}
	if stocks[1].Code != "sz000088" {
		t.Errorf("stocks[1].Code = %q, want %q", stocks[1].Code, "sz000088")
	}
	if stocks[2].Code != "sz000001" {
		t.Errorf("stocks[2].Code = %q, want %q", stocks[2].Code, "sz000001")
	}
	if stocks[3].Code != "sh601398" {
		t.Errorf("stocks[3].Code = %q, want %q", stocks[3].Code, "sh601398")
	}
}

func TestIsMainboardTHS(t *testing.T) {
	tests := []struct {
		market int
		code   string
		want   bool
	}{
		{17, "600519", true},  // 沪市主板
		{33, "000001", true},  // 深市主板 (平安银行)
		{17, "000088", true},  // 沪市短码 (工商银行)
		{33, "300750", false}, // 创业板
		{17, "688981", false}, // 科创板
		{33, "300001", false}, // 创业板
		{17, "688001", false}, // 科创板
		{17, "601001", true},  // 沪市主板
		{33, "000002", true},  // 深市主板
		{0, "600519", false},  // 未知市场
		{99, "600519", false}, // 未知市场
	}

	for _, tt := range tests {
		got := isMainboardTHS(tt.market, tt.code)
		if got != tt.want {
			t.Errorf("isMainboardTHS(%d, %q) = %v, want %v", tt.market, tt.code, got, tt.want)
		}
	}
}

func TestMarketFromTHS(t *testing.T) {
	if got := marketFromTHS(17); got != "sh" {
		t.Errorf("marketFromTHS(17) = %q, want %q", got, "sh")
	}
	if got := marketFromTHS(33); got != "sz" {
		t.Errorf("marketFromTHS(33) = %q, want %q", got, "sz")
	}
	if got := marketFromTHS(0); got != "sz" {
		t.Errorf("marketFromTHS(0) = %q, want %q (default)", got, "sz")
	}
}

func TestParseHotStocksPayloadInvalidJSON(t *testing.T) {
	_, err := parseHotStocksPayload([]byte(`{invalid`))
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

func TestParseHotStocksPayloadEmpty(t *testing.T) {
	stocks, err := parseHotStocksPayload([]byte(`{"data":{"stock_list":[]}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stocks) != 0 {
		t.Errorf("expected 0 stocks, got %d", len(stocks))
	}
}
