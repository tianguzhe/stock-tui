package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestFuyao(t *testing.T, h http.HandlerFunc) *FuyaoClient {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &FuyaoClient{http: srv.Client(), key: "test-key", baseURL: srv.URL, backoff: func(int) time.Duration { return 0 }}
}

func TestNewFuyaoClientWithoutKey(t *testing.T) {
	t.Setenv(FuyaoKeyEnv, "  ")
	if _, err := NewFuyaoClient(nil); !errors.Is(err, ErrNoFuyaoKey) {
		t.Fatalf("want ErrNoFuyaoKey, got %v", err)
	}
}

func TestFuyaoThscode(t *testing.T) {
	cases := map[string]string{"sh600519": "600519.SH", "sz000063": "000063.SZ", "bj920819": "920819.BJ", "sh512480": "512480.SH"}
	for in, want := range cases {
		got, err := FuyaoThscode(in)
		if err != nil || got != want {
			t.Errorf("%s: got %q %v, want %q", in, got, err, want)
		}
		if back := codeFromThscode(got); back != in {
			t.Errorf("round trip %s -> %s -> %s", in, got, back)
		}
	}
	if _, err := FuyaoThscode("hk00700"); err == nil {
		t.Error("hk code should be rejected")
	}
}

func TestFuyaoSendsKeyAndParsesExRight(t *testing.T) {
	c := newTestFuyao(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-api-key") != "test-key" {
			t.Errorf("missing api key header")
		}
		if r.URL.Query().Get("thscode") != "601991.SH" {
			t.Errorf("thscode = %q", r.URL.Query().Get("thscode"))
		}
		// 服务端按除权日降序返回（10-28 在前）
		w.Write([]byte(`{"code":0,"message":"success","data":{"item":[
			{"ex_date_ms":1793116800000,"dividend_per_share":0.068,"per_share_bonus":0},
			{"ex_date_ms":1787673600000,"dividend_per_share":0.093,"per_share_bonus":0}]}}`))
	})
	ev, err := c.AdjustmentFactors("sh601991", "2026-01-01", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(ev) != 2 || ev[0].ExDate != "2026-08-26" || ev[1].ExDate != "2026-10-28" || ev[0].DividendPerShare != 0.093 {
		t.Fatalf("events = %+v (want ascending 08-26 then 10-28, Asia/Shanghai dates)", ev)
	}
}

func TestFuyaoRetriesRateLimitThenSucceeds(t *testing.T) {
	calls := 0
	c := newTestFuyao(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Write([]byte(`{"code":4001,"message":"rate limited","data":null}`))
			return
		}
		w.Write([]byte(`{"code":0,"data":{"item":[{"date_ms":1781712000000}]}}`))
	})
	days, err := c.TradingDays("2026-06-18", "2026-06-18")
	if err != nil || len(days) != 1 || days[0] != "2026-06-18" || calls != 2 {
		t.Fatalf("days=%v err=%v calls=%d", days, err, calls)
	}
}

func TestFuyaoDoesNotRetryClientErrors(t *testing.T) {
	calls := 0
	c := newTestFuyao(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`{"code":1002,"message":"Unknown thscode","request_id":"rid","data":null}`))
	})
	_, err := c.AdjustmentFactors("sh600000", "", "")
	var fe *FuyaoError
	if !errors.As(err, &fe) || fe.Code != 1002 || calls != 1 {
		t.Fatalf("err=%v calls=%d, want FuyaoError 1002 with a single call", err, calls)
	}
}

func TestFuyaoGivesUpAfterBoundedRetries(t *testing.T) {
	calls := 0
	c := newTestFuyao(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`{"code":5001,"message":"upstream","data":null}`))
	})
	if _, err := c.TradingDays("2026-06-18", "2026-06-19"); err == nil || calls != fuyaoMaxAttempts {
		t.Fatalf("err=%v calls=%d, want failure after %d attempts", err, calls, fuyaoMaxAttempts)
	}
}

func TestFuyaoDailyBarsRoutesFundsAndIncludesEndDay(t *testing.T) {
	var gotPath, gotEnd string
	c := newTestFuyao(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotEnd = r.URL.Path, r.URL.Query().Get("end")
		w.Write([]byte(`{"code":0,"data":{"item":[{"date_ms":1795017600000,"open_price":0.765,"high_price":0.789,"low_price":0.755,"close_price":0.779}]}}`))
	})
	bars, err := c.DailyBars("sh512480", "2026-11-19", "2026-11-19")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/fund/market/historical" {
		t.Errorf("ETF should use fund endpoint, got %s", gotPath)
	}
	// end must be the day after 11-19 00:00 so the 11-19 bar is included
	if gotEnd != "1795104000000" {
		t.Errorf("end = %s", gotEnd)
	}
	if len(bars) != 1 || bars[0].Date != "2026-11-19" || bars[0].Open != 0.765 {
		t.Fatalf("bars = %+v", bars)
	}
	if _, err := c.DailyBars("sh600000", "2010-01-01", "2026-11-19"); err == nil || !strings.Contains(err.Error(), "10 年") {
		t.Errorf("span over 10y should be rejected locally, got %v", err)
	}
}

func TestFuyaoAnomalyBatchesBy50(t *testing.T) {
	var sizes []int
	c := newTestFuyao(t, func(w http.ResponseWriter, r *http.Request) {
		sizes = append(sizes, len(strings.Split(r.URL.Query().Get("thscodes"), ",")))
		w.Write([]byte(`{"code":0,"data":{"item":[{"thscode":"603496.SH","stock_name":"恒为科技","tag_name":"跌停","keyword_list":["算力租赁板块下跌"],"analysis_content":"x"}]}}`))
	})
	codes := make([]string, 0, 61)
	for i := 0; i < 60; i++ {
		codes = append(codes, "sh6000"+string(rune('0'+i/10))+string(rune('0'+i%10)))
	}
	codes = append(codes, "hk00700") // 不覆盖的代码应被略过
	got, err := c.AnomalyReasons(codes)
	if err != nil {
		t.Fatal(err)
	}
	if len(sizes) != 2 || sizes[0] != 50 || sizes[1] != 10 {
		t.Fatalf("batch sizes = %v", sizes)
	}
	if got[0].Code != "sh603496" || got[0].Tag != "跌停" {
		t.Fatalf("reason = %+v", got[0])
	}
}

func TestFuyaoExRightEmptyAndFundHandling(t *testing.T) {
	c := newTestFuyao(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":3002,"message":"No adjustment events for thscode=000063.SZ","data":null}`))
	})
	ev, err := c.AdjustmentFactors("sz000063", "2026-08-01", "")
	if err != nil || len(ev) != 0 {
		t.Fatalf("3002 must mean empty result, got %v %v", ev, err)
	}
	if _, err := c.AdjustmentFactors("sh512480", "", ""); !errors.Is(err, ErrFuyaoNotCovered) {
		t.Fatalf("fund should be ErrFuyaoNotCovered, got %v", err)
	}
}
