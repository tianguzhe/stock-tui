package store

import (
	"fmt"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestUpsertInstrumentIdempotentAndNamePreserved(t *testing.T) {
	s := openTemp(t)

	if err := s.UpsertInstrument("sz002916", "深南电路", "sz", ""); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	// A later tag-only call passes an empty name; it must not wipe the stored name.
	if err := s.UpsertInstrument("sz002916", "", "sz", ""); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	if err := s.AddTag("sz002916", "PCB链"); err != nil {
		t.Fatalf("add tag: %v", err)
	}
	got, err := s.ListByTag("PCB链")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].Name != "深南电路" {
		t.Fatalf("expected name preserved as 深南电路, got %+v", got)
	}
}

func TestTagManyToManyAndRemove(t *testing.T) {
	s := openTemp(t)

	for _, c := range []string{"sz002916", "sz002463"} {
		if err := s.AddTag(c, "PCB链"); err != nil {
			t.Fatalf("add tag %s: %v", c, err)
		}
	}
	// One instrument carries two tags.
	if err := s.AddTag("sz002916", "观察名单"); err != nil {
		t.Fatalf("add second tag: %v", err)
	}
	// Idempotent: re-adding the same link must not error or duplicate.
	if err := s.AddTag("sz002916", "PCB链"); err != nil {
		t.Fatalf("re-add tag: %v", err)
	}

	pcb, _ := s.ListByTag("PCB链")
	if len(pcb) != 2 {
		t.Fatalf("PCB链 expected 2 instruments, got %d", len(pcb))
	}
	watch, _ := s.ListByTag("观察名单")
	if len(watch) != 1 || watch[0].Code != "sz002916" {
		t.Fatalf("观察名单 expected only sz002916, got %+v", watch)
	}

	if err := s.RemoveTag("sz002916", "PCB链"); err != nil {
		t.Fatalf("remove tag: %v", err)
	}
	pcb, _ = s.ListByTag("PCB链")
	if len(pcb) != 1 || pcb[0].Code != "sz002463" {
		t.Fatalf("after remove, PCB链 expected only sz002463, got %+v", pcb)
	}
}

func TestSaveSnapshotUpsertSameDay(t *testing.T) {
	s := openTemp(t)
	if err := s.UpsertInstrument("sz002916", "深南电路", "sz", ""); err != nil {
		t.Fatalf("upsert instrument: %v", err)
	}

	snap := Snapshot{Code: "sz002916", TradeDate: "2026-06-03", Close: 382.0, ADX: 53.4, KDJ_J: 38.0, ScoreTotal: 65}
	if err := s.SaveSnapshot(snap); err != nil {
		t.Fatalf("first save: %v", err)
	}
	// Same trading day, updated values: must overwrite, not duplicate.
	snap.Close = 390.0
	snap.ScoreTotal = 70
	if err := s.SaveSnapshot(snap); err != nil {
		t.Fatalf("second save: %v", err)
	}

	hist, err := s.History("sz002916", 10)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(hist) != 1 {
		t.Fatalf("expected 1 snapshot after same-day upsert, got %d", len(hist))
	}
	if hist[0].Close != 390.0 || hist[0].ScoreTotal != 70 {
		t.Fatalf("expected overwritten values close=390 score=70, got close=%v score=%d", hist[0].Close, hist[0].ScoreTotal)
	}

	// A different day accrues a second row.
	snap.TradeDate = "2026-06-04"
	if err := s.SaveSnapshot(snap); err != nil {
		t.Fatalf("save next day: %v", err)
	}
	hist, _ = s.History("sz002916", 10)
	if len(hist) != 2 {
		t.Fatalf("expected 2 snapshots across two days, got %d", len(hist))
	}
	// Newest first.
	if hist[0].TradeDate != "2026-06-04" {
		t.Fatalf("expected newest first, got %s", hist[0].TradeDate)
	}
}

func TestMigrationAddsNewColumns(t *testing.T) {
	// Simulate an existing DB created before the new columns existed: create the
	// snapshot table without the new columns, then call Open which should add them.
	path := filepath.Join(t.TempDir(), "legacy.db")

	legacy, err := Open(path)
	if err != nil {
		t.Fatalf("initial open: %v", err)
	}
	// Drop the new columns by recreating the table without them.  This mimics a
	// pre-migration database.
	_, err = legacy.db.Exec(`
CREATE TABLE IF NOT EXISTS snapshot_legacy AS SELECT code, trade_date, captured_at, close FROM snapshot LIMIT 0;
DROP TABLE snapshot;
ALTER TABLE snapshot_legacy RENAME TO snapshot;
`)
	if err != nil {
		t.Fatalf("simulate legacy schema: %v", err)
	}
	legacy.Close()

	// Re-open: migrate() should add the missing columns without error.
	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("re-open after migration: %v", err)
	}
	defer reopened.Close()

	// Verify the new columns exist by querying them.
	var dummy float64
	err = reopened.db.QueryRow(
		`SELECT COALESCE(turnover_rate,0)+COALESCE(market_cap,0)+COALESCE(pe,0)+COALESCE(rs20,0)+COALESCE(rs60,0)+COALESCE(rs120,0) FROM snapshot LIMIT 1`,
	).Scan(&dummy)
	// A "no rows" error is fine; "no such column" would be an error.
	if err != nil && err.Error() != "sql: no rows in result set" {
		t.Fatalf("new columns not accessible after migration: %v", err)
	}
}

func TestUpdateRSRankings(t *testing.T) {
	s := openTemp(t)

	codes := []string{"sz000001", "sz000002", "sz000003"}
	for _, c := range codes {
		if err := s.UpsertInstrument(c, c, "sz", ""); err != nil {
			t.Fatalf("seed instrument: %v", err)
		}
	}

	// Insert 25 snapshots per code at different prices (newest first in time but
	// inserted oldest first so trade_date order is ascending).
	for day := 1; day <= 25; day++ {
		date := fmt.Sprintf("2026-%02d-%02d", day/30+1, day%30+1)
		for i, c := range codes {
			close := 10.0 + float64(i)*2 + float64(day)*0.1 // prices all rising, at different rates
			if err := s.SaveSnapshot(Snapshot{Code: c, TradeDate: date, Close: close}); err != nil {
				t.Fatalf("seed snapshot: %v", err)
			}
		}
	}

	n, err := s.UpdateRSRankings()
	if err != nil {
		t.Fatalf("UpdateRSRankings: %v", err)
	}
	if n != len(codes) {
		t.Fatalf("expected %d updated, got %d", len(codes), n)
	}

	// All three codes should now have rs20 in [0, 100].
	for _, c := range codes {
		snaps, err := s.History(c, 1)
		if err != nil || len(snaps) == 0 {
			t.Fatalf("history for %s: %v", c, err)
		}
		rs := snaps[0].RS20
		if rs < 0 || rs > 100 {
			t.Errorf("%s RS20=%v out of range", c, rs)
		}
	}
}

func TestScreenFilters(t *testing.T) {
	s := openTemp(t)

	// Three instruments with distinct latest snapshots.
	seed := []struct {
		code, name string
		adx, j     float64
		score      int
		tag        string
	}{
		{"sz002916", "深南电路", 53.4, 38.0, 65, "PCB链"},   // pass adx>25 & j<80
		{"sz002463", "沪电股份", 39.5, 72.8, 68, "PCB链"},   // pass
		{"sz300285", "国瓷材料", 39.7, 90.5, 73, "电子材料"}, // fail j<80
	}
	for _, x := range seed {
		if err := s.UpsertInstrument(x.code, x.name, x.code[:2], ""); err != nil {
			t.Fatalf("seed instrument: %v", err)
		}
		if err := s.AddTag(x.code, x.tag); err != nil {
			t.Fatalf("seed tag: %v", err)
		}
		if err := s.SaveSnapshot(Snapshot{Code: x.code, TradeDate: "2026-06-03", ADX: x.adx, KDJ_J: x.j, ScoreTotal: x.score}); err != nil {
			t.Fatalf("seed snapshot: %v", err)
		}
	}

	rows, err := s.Screen(Filter{MinADX: 25, MaxJ: 80, UseMaxJ: true})
	if err != nil {
		t.Fatalf("screen: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows (ADX>25 & J<80), got %d: %+v", len(rows), rows)
	}
	// Ordered by score desc: 沪电(68) before 深南(65).
	if rows[0].Instrument.Code != "sz002463" || rows[1].Instrument.Code != "sz002916" {
		t.Fatalf("expected score-desc order, got %s then %s", rows[0].Instrument.Code, rows[1].Instrument.Code)
	}

	// Tag constraint narrows to 电子材料, but J filter excludes it -> empty.
	rows, _ = s.Screen(Filter{Tag: "电子材料", MaxJ: 80, UseMaxJ: true})
	if len(rows) != 0 {
		t.Fatalf("expected 0 rows for 电子材料 with J<80, got %d", len(rows))
	}
	// Same tag without J filter -> the one instrument.
	rows, _ = s.Screen(Filter{Tag: "电子材料"})
	if len(rows) != 1 || rows[0].Instrument.Code != "sz300285" {
		t.Fatalf("expected sz300285 for 电子材料, got %+v", rows)
	}
}
