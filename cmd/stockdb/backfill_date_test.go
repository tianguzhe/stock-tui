package main

import (
	"path/filepath"
	"testing"

	"stock-tui/internal/store"
)

// newTestStore 建一个临时库并写入若干快照行。
func newTestStore(t *testing.T, snaps []store.Snapshot) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	for _, s := range snaps {
		if err := st.SaveSnapshot(s); err != nil {
			t.Fatalf("save snapshot %s@%s: %v", s.Code, s.TradeDate, err)
		}
	}
	return st
}

// 补历史日的股票池应取目标日前后最近交易日的并集，而不是 instrument 表——
// 后者反映的是"现在"的池子，会漏掉当时在池、之后被热榜清理的标的。
func TestCodesAroundDateUsesUnionOfAdjacentDays(t *testing.T) {
	st := newTestStore(t, []store.Snapshot{
		{Code: "sh600000", TradeDate: "2026-07-17", Close: 10},
		{Code: "sh600001", TradeDate: "2026-07-17", Close: 10},
		{Code: "sh600001", TradeDate: "2026-07-21", Close: 11},
		{Code: "sh600002", TradeDate: "2026-07-21", Close: 12},
		{Code: "sh600003", TradeDate: "2026-07-24", Close: 13}, // 更远的日子不应纳入
	})

	codes, prev, next, err := codesAroundDate(st.DB(), "2026-07-20")
	if err != nil {
		t.Fatalf("codesAroundDate: %v", err)
	}
	if prev != "2026-07-17" || next != "2026-07-21" {
		t.Fatalf("adjacent days = %s/%s, want 2026-07-17/2026-07-21", prev, next)
	}
	want := []string{"sh600000", "sh600001", "sh600002"}
	if len(codes) != len(want) {
		t.Fatalf("codes = %v, want %v", codes, want)
	}
	for i, c := range want {
		if codes[i] != c {
			t.Fatalf("codes = %v, want %v", codes, want)
		}
	}
}

// 目标日已有行时，必须读出其实时行情字段供 applyPreserved 回填。
// buildSnapshot 不产出这些值，漏掉这一步会在补"部分缺失"的交易日时把它们清零。
func TestExistingPreservedReadsRealtimeFields(t *testing.T) {
	st := newTestStore(t, []store.Snapshot{{
		Code: "sh600000", TradeDate: "2026-07-15", Close: 10,
		TurnoverRate: 3.5, MarketCap: 120.5, PE: 18.25,
		InsideVol: 1000, OutsideVol: 2000,
	}})

	keep, found, err := existingPreserved(st.DB(), "sh600000", "2026-07-15")
	if err != nil {
		t.Fatalf("existingPreserved: %v", err)
	}
	if !found {
		t.Fatal("found = false, want true for an existing row")
	}

	// 模拟 buildSnapshot 的产出：实时字段全为零值。
	snap := store.Snapshot{Code: "sh600000", TradeDate: "2026-07-15", Close: 10}
	applyPreserved(&snap, keep)

	if snap.TurnoverRate != 3.5 || snap.MarketCap != 120.5 || snap.PE != 18.25 {
		t.Errorf("turnover/cap/pe = %v/%v/%v, want 3.5/120.5/18.25",
			snap.TurnoverRate, snap.MarketCap, snap.PE)
	}
	if snap.InsideVol != 1000 || snap.OutsideVol != 2000 {
		t.Errorf("inside/outside = %v/%v, want 1000/2000", snap.InsideVol, snap.OutsideVol)
	}
}

// 目标日无该标的行时返回 found=false，调用方据此跳过回填（保持零值）。
func TestExistingPreservedMissingRow(t *testing.T) {
	st := newTestStore(t, []store.Snapshot{
		{Code: "sh600000", TradeDate: "2026-07-15", Close: 10},
	})

	if _, found, err := existingPreserved(st.DB(), "sh600000", "2026-07-20"); err != nil {
		t.Fatalf("existingPreserved: %v", err)
	} else if found {
		t.Error("found = true for a missing row, want false")
	}
}
