package main

import (
	"errors"
	"math"
	"reflect"
	"sync"
	"testing"

	"stock-tui/internal/api"
)

func TestClassifyExRightsWindowsAndReceivable(t *testing.T) {
	events := map[string][]api.ExRightEvent{
		// 大唐 2026-08：漏记红利 46.50 的真实案例（500 股 × 0.093）
		"sh601991": {{ExDate: "2026-08-26", DividendPerShare: 0.093}, {ExDate: "2026-10-28", DividendPerShare: 0.068}},
		"sz001256": {{ExDate: "2026-11-23", DividendPerShare: 0.15}},
		"sh600000": {{ExDate: "2026-11-19", DividendPerShare: 0.2}}, // 恰在今天：已除权，不算「即将」
	}
	w := exWindow{Today: "2026-11-19", Until: "2026-11-26", From: "2026-08-01", To: "2026-11-19"}
	upcoming, past := classifyExRights(events, w, map[string]int{"sh601991": 500})

	if len(upcoming) != 1 || upcoming[0].Code != "sz001256" || upcoming[0].Shares != 0 {
		t.Fatalf("upcoming = %+v", upcoming)
	}
	var dates []string
	for _, r := range past {
		dates = append(dates, r.Code+"@"+r.Event.ExDate)
	}
	want := []string{"sh601991@2026-08-26", "sh601991@2026-10-28", "sh600000@2026-11-19"}
	if !reflect.DeepEqual(dates, want) {
		t.Fatalf("past order = %v, want %v", dates, want)
	}
	if math.Abs(past[0].Receivable-46.50) > 1e-9 || math.Abs(sumReceivable(past)-80.50) > 1e-9 {
		t.Fatalf("receivable = %.4f total %.4f, want 46.50 / 80.50 (non-holding rows add 0)", past[0].Receivable, sumReceivable(past))
	}
}

func TestBaselineBreaksOnlyInsideSnapshotRange(t *testing.T) {
	events := map[string][]api.ExRightEvent{
		"sh600863": {{ExDate: "2026-07-28"}},                         // 在区间内
		"sh601166": {{ExDate: "2026-02-06"}, {ExDate: "2026-06-12"}}, // 仅 06-12 在区间内
		"sz000001": {{ExDate: "2026-06-03"}},                         // 与 snapshot 首日相同：首日之前没有旧基准的行，不算断点
	}
	got := baselineBreaks(events, "2026-06-03", "2026-11-19")
	want := []baselineBreak{{Code: "sh600863", Dates: []string{"2026-07-28"}}, {Code: "sh601166", Dates: []string{"2026-06-12"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("breaks = %+v", got)
	}
}

type fakeExFetcher struct {
	mu    sync.Mutex
	calls int
}

func (f *fakeExFetcher) AdjustmentFactors(code, from, to string) ([]api.ExRightEvent, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if code == "bad" {
		return nil, errors.New("boom")
	}
	return []api.ExRightEvent{{ExDate: "2026-11-20"}}, nil
}

func TestFetchExRightsReportsFailuresWithoutDroppingOthers(t *testing.T) {
	f := &fakeExFetcher{}
	ev, failed := fetchExRights(f, []string{"a", "bad", "b", "c"}, "", "", 2)
	if f.calls != 4 || len(ev) != 3 || len(failed) != 1 || failed["bad"] == nil {
		t.Fatalf("calls=%d ev=%d failed=%v", f.calls, len(ev), failed)
	}
}

func TestDedupeAndAddDays(t *testing.T) {
	if got := dedupe([]string{"a", "", "b", "a"}); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("dedupe = %v", got)
	}
	if got := addDays("2026-11-28", 7); got != "2026-12-05" {
		t.Errorf("addDays = %s", got)
	}
}

func TestAnalyzeCalendar(t *testing.T) {
	// 06-19 端午不是交易日；07-20 全池缺失；07-13 残缺（218 vs ~600）。
	cal := []string{"2026-06-18", "2026-06-22", "2026-07-09", "2026-07-10", "2026-07-13", "2026-07-14", "2026-07-15", "2026-07-16", "2026-07-17", "2026-07-20", "2026-07-21"}
	counts := map[string]int{
		"2026-06-18": 354, "2026-06-22": 400,
		"2026-07-09": 590, "2026-07-10": 600, "2026-07-13": 218, "2026-07-14": 610,
		"2026-07-15": 620, "2026-07-16": 600, "2026-07-17": 605, "2026-07-21": 630,
		"2026-06-20": 10, // 周六：非交易日却有快照
	}
	rep := analyzeCalendar(cal, counts)
	if !reflect.DeepEqual(rep.Missing, []string{"2026-07-20"}) {
		t.Errorf("missing = %v (06-19 holiday must not be reported)", rep.Missing)
	}
	if !reflect.DeepEqual(rep.Extra, []string{"2026-06-20"}) {
		t.Errorf("extra = %v", rep.Extra)
	}
	if len(rep.Thin) != 1 || rep.Thin[0].Date != "2026-07-13" {
		t.Errorf("thin = %+v", rep.Thin)
	}
}

func TestAnalyzeCalendarIgnoresDatesBeforeCoverage(t *testing.T) {
	// 日历只覆盖 07-01 起：06 月的日期既不算缺失也不算非交易日。
	rep := analyzeCalendar([]string{"2026-07-01", "2026-07-02"}, map[string]int{"2026-06-15": 300, "2026-07-01": 300, "2026-07-02": 310})
	if rep.CoverFrom != "2026-07-01" || len(rep.Missing)+len(rep.Extra)+len(rep.Thin) != 0 {
		t.Fatalf("rep = %+v", rep)
	}
}
