package api

import (
	"net/http"
	"testing"
)

func TestParseStockTencentQuote(t *testing.T) {
	raw := "1~贵州茅台~600519~1275.98~1303.00~1290.00~45890~21472~24418~1275.82~4~1275.60~3~1275.50~2~1275.40~2~1275.37~1~1275.98~71~1276.00~7~1276.30~2~1276.50~3~1276.55~1~~20260528161414~-27.02~-2.07~1304.00~1271.00~1275.98/45890/5895475019~45890~589548~0.37"

	got, err := parseStock("sh600519", raw)
	if err != nil {
		t.Fatalf("parseStock() error = %v", err)
	}

	assertFloat(t, "Price", got.Price, 1275.98)
	assertFloat(t, "Close", got.Close, 1303.00)
	assertFloat(t, "Open", got.Open, 1290.00)
	assertFloat(t, "High", got.High, 1304.00)
	assertFloat(t, "Low", got.Low, 1271.00)
	assertFloat(t, "Change", got.Change, -27.02)
	assertFloat(t, "ChangePct", got.ChangePct, -2.07)
	assertFloat(t, "Volume", got.Volume, 45890)
	assertFloat(t, "Amount", got.Amount, 589548)

	if got.Code != "sh600519" {
		t.Fatalf("Code = %q, want sh600519", got.Code)
	}
	if got.Name != "贵州茅台" {
		t.Fatalf("Name = %q, want 贵州茅台", got.Name)
	}
	if got.Precision != 2 {
		t.Fatalf("Precision = %d, want 2", got.Precision)
	}
}

func TestParseStocksPayloadUsesResponseCode(t *testing.T) {
	raw := `v_sz000001="1~贵州茅台~600519~1275.98~1303.00~1290.00~45890~21472~24418~1275.82~4~1275.60~3~1275.50~2~1275.40~2~1275.37~1~1275.98~71~1276.00~7~1276.30~2~1276.50~3~1276.55~1~~20260528161414~-27.02~-2.07~1304.00~1271.00~1275.98/45890/5895475019~45890~589548~0.37";
v_sh600519="1~贵州茅台~600519~1275.98~1303.00~1290.00~45890~21472~24418~1275.82~4~1275.60~3~1275.50~2~1275.40~2~1275.37~1~1275.98~71~1276.00~7~1276.30~2~1276.50~3~1276.55~1~~20260528161414~-27.02~-2.07~1304.00~1271.00~1275.98/45890/5895475019~45890~589548~0.37";`

	got := parseStocksPayload(raw)
	if len(got) != 2 {
		t.Fatalf("len(stocks) = %d, want 2", len(got))
	}
	if got[0].Code != "sz000001" {
		t.Fatalf("first Code = %q, want sz000001", got[0].Code)
	}
	if got[1].Code != "sh600519" {
		t.Fatalf("second Code = %q, want sh600519", got[1].Code)
	}
}

func TestCheckResponseStatusRejectsNonOK(t *testing.T) {
	err := checkResponseStatus(&http.Response{
		StatusCode: http.StatusBadGateway,
		Status:     "502 Bad Gateway",
	})
	if err == nil {
		t.Fatal("checkResponseStatus() error = nil, want error")
	}
}

func TestParseStockRejectsShortPayload(t *testing.T) {
	if _, err := parseStock("sh600519", "1~贵州茅台"); err == nil {
		t.Fatal("parseStock() error = nil, want error")
	}
}

func assertFloat(t *testing.T, name string, got, want float64) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
}
