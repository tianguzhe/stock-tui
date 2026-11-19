package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"stock-tui/internal/api"
)

func TestNewsEventTriggersBoundaries(t *testing.T) {
	if got := newsEventTriggers(7.99, 9.99, 5.01, 94.99); len(got) != 0 {
		t.Fatalf("just below every threshold should not trigger, got %v", got)
	}
	// 恒为 11-19：跌停 -10.00%，量比 1.46，range20 34%、range60 25% → 只命中涨跌幅
	if got := newsEventTriggers(-10.00, 1.46, 34, 25); !reflect.DeepEqual(got, []string{"单日-10.00%"}) {
		t.Fatalf("恒为 case = %v", got)
	}
	got := newsEventTriggers(8, 10, 5, 95)
	if len(got) != 4 {
		t.Fatalf("thresholds are inclusive, got %v", got)
	}
}

func TestPrintNewsAnomaly(t *testing.T) {
	hits := []string{"单日-10.00%"}
	fetch := func(codes []string) ([]api.AnomalyReason, error) {
		return []api.AnomalyReason{{Code: "sh603496", Tag: "跌停", Keywords: []string{"算力租赁板块下跌", "此前5日累计上涨"}, Content: "1、据同花顺iFinD数据，\n板块" + strings.Repeat("字", 400)}}, nil
	}
	out := captureStdout(t, func() { printNewsAnomaly("sh603496", "2026-11-19", "2026-11-19", hits, fetch, nil) })
	if !strings.Contains(out, "NEWS_ANOMALY tag=跌停 keywords=算力租赁板块下跌,此前5日累计上涨 | 1、据同花顺iFinD数据， 板块") || !strings.HasSuffix(strings.TrimSpace(out), "…") {
		t.Fatalf("output = %q (newlines must collapse, long content truncated)", out)
	}

	if out := captureStdout(t, func() { printNewsAnomaly("sh603496", "2026-11-19", "2026-11-19", nil, fetch, nil) }); out != "" {
		t.Errorf("no triggers must print nothing, got %q", out)
	}
	called := false
	stub := func([]string) ([]api.AnomalyReason, error) { called = true; return nil, nil }
	out = captureStdout(t, func() { printNewsAnomaly("sh603496", "2026-11-18", "2026-11-19", hits, stub, nil) })
	if called || !strings.Contains(out, "不是今天") {
		t.Errorf("stale bar must skip the lookup: called=%v out=%q", called, out)
	}
	out = captureStdout(t, func() { printNewsAnomaly("sh603496", "2026-11-19", "2026-11-19", hits, nil, api.ErrNoFuyaoKey) })
	if !strings.Contains(out, "未配置") {
		t.Errorf("no key: %q", out)
	}
	out = captureStdout(t, func() { printNewsAnomaly("sh603496", "2026-11-19", "2026-11-19", hits, stub, nil) })
	if !strings.Contains(out, "当日无异动解读记录") {
		t.Errorf("empty result: %q", out)
	}
	fail := func([]string) ([]api.AnomalyReason, error) { return nil, errors.New("down") }
	out = captureStdout(t, func() { printNewsAnomaly("sh603496", "2026-11-19", "2026-11-19", hits, fail, nil) })
	if !strings.Contains(out, "查询失败：down") {
		t.Errorf("error: %q", out)
	}
}
