package store

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

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

// TestSaveSnapshotNewColumnsRoundTripAndOverwrite verifies avg10/squeeze/
// donchian/score_adj/sar/st/low/high/amplitude/inside_vol/outside_vol columns
// persist and that a same-day re-save overwrites them (a missing ON CONFLICT
// DO UPDATE entry would silently keep stale values).
func TestSaveSnapshotNewColumnsRoundTripAndOverwrite(t *testing.T) {
	s := openTemp(t)
	if err := s.UpsertInstrument("sz002916", "深南电路", "sz", ""); err != nil {
		t.Fatalf("upsert instrument: %v", err)
	}

	avg10 := 6.5
	snap := Snapshot{
		Code: "sz002916", TradeDate: "2026-06-12", Close: 100,
		Low: 96.4, High: 103.7,
		ScoreTotal: 60, ScoreAdj: 66,
		PerfTrendFollowBullAvg10: &avg10,
		KeltnerSqueeze:           true,
		DonchBreak20Bull:         true,
		DonchBreak55Bull:         false,
		SARValue:                 95.5,
		SuperTrendValue:          93.2,
		Amplitude:                7.3,
		InsideVol:                1200,
		OutsideVol:               3400,
	}
	if err := s.SaveSnapshot(snap); err != nil {
		t.Fatalf("first save: %v", err)
	}

	read := func() (gotAvg sql.NullFloat64, squeeze, b20, b55, adj int, sarV, stV, low, high, amp, inVol, outVol float64) {
		t.Helper()
		err := s.db.QueryRow(
			`SELECT perf_trend_follow_bull_avg10, keltner_squeeze, donch_break20_bull, donch_break55_bull, score_adj, sar_value, supertrend_value, low, high, amplitude, inside_vol, outside_vol
			 FROM snapshot WHERE code='sz002916' AND trade_date='2026-06-12'`,
		).Scan(&gotAvg, &squeeze, &b20, &b55, &adj, &sarV, &stV, &low, &high, &amp, &inVol, &outVol)
		if err != nil {
			t.Fatalf("read new columns: %v", err)
		}
		return
	}

	gotAvg, squeeze, b20, b55, adj, sarV, stV, low, high, amp, inVol, outVol := read()
	if !gotAvg.Valid || gotAvg.Float64 != 6.5 || squeeze != 1 || b20 != 1 || b55 != 0 || adj != 66 || sarV != 95.5 || stV != 93.2 || low != 96.4 || high != 103.7 || amp != 7.3 || inVol != 1200 || outVol != 3400 {
		t.Fatalf("round-trip mismatch: avg10=%+v squeeze=%d b20=%d b55=%d adj=%d sar=%v st=%v low=%v high=%v amp=%v in=%v out=%v", gotAvg, squeeze, b20, b55, adj, sarV, stV, low, high, amp, inVol, outVol)
	}

	// Same-day re-save with changed values must overwrite all new columns.
	avg10b := -1.2
	snap.PerfTrendFollowBullAvg10 = &avg10b
	snap.KeltnerSqueeze = false
	snap.DonchBreak20Bull = false
	snap.DonchBreak55Bull = true
	snap.ScoreAdj = 48
	snap.SARValue = 97.1
	snap.SuperTrendValue = 94.8
	snap.Low = 88.0
	snap.High = 99.9
	snap.Amplitude = 4.1
	snap.InsideVol = 800
	snap.OutsideVol = 2100
	if err := s.SaveSnapshot(snap); err != nil {
		t.Fatalf("second save: %v", err)
	}
	gotAvg, squeeze, b20, b55, adj, sarV, stV, low, high, amp, inVol, outVol = read()
	if !gotAvg.Valid || gotAvg.Float64 != -1.2 || squeeze != 0 || b20 != 0 || b55 != 1 || adj != 48 || sarV != 97.1 || stV != 94.8 || low != 88.0 || high != 99.9 || amp != 4.1 || inVol != 800 || outVol != 2100 {
		t.Fatalf("same-day overwrite mismatch: avg10=%+v squeeze=%d b20=%d b55=%d adj=%d sar=%v st=%v low=%v high=%v amp=%v in=%v out=%v", gotAvg, squeeze, b20, b55, adj, sarV, stV, low, high, amp, inVol, outVol)
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
		`SELECT COALESCE(low,0)+COALESCE(high,0)+COALESCE(turnover_rate,0)+COALESCE(market_cap,0)+COALESCE(pe,0)+COALESCE(rs20,0)+COALESCE(rs60,0)+COALESCE(rs120,0)
		+COALESCE(ret20,0)+COALESCE(ret60,0)+COALESCE(ret120,0)
		+COALESCE(perf_trend_follow_bull_win10,0)+COALESCE(perf_overbought_bear_win10,0)+COALESCE(perf_div_bear_win10,0)
		+COALESCE(perf_trend_follow_bull_n,0)+COALESCE(perf_overbought_bear_n,0)+COALESCE(perf_div_bear_n,0)
		+COALESCE(perf_trend_follow_bull_avg10,0)+COALESCE(keltner_squeeze,0)+COALESCE(donch_break20_bull,0)+COALESCE(donch_break55_bull,0)+COALESCE(score_adj,0)
		+COALESCE(sar_value,0)+COALESCE(supertrend_value,0)
		+COALESCE(amplitude,0)+COALESCE(inside_vol,0)+COALESCE(outside_vol,0) FROM snapshot LIMIT 1`,
	).Scan(&dummy)
	// A "no rows" error is fine; "no such column" would be an error.
	if err != nil && err.Error() != "sql: no rows in result set" {
		t.Fatalf("new columns not accessible after migration: %v", err)
	}
}

func TestMigrationRepairsLegacyDecisionLogSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-decision.db")

	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy sqlite: %v", err)
	}
	_, err = legacy.Exec(`
CREATE TABLE instrument (
  code       TEXT PRIMARY KEY,
  name       TEXT NOT NULL,
  market     TEXT NOT NULL,
  note       TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE TABLE snapshot (
  code        TEXT NOT NULL REFERENCES instrument(code) ON DELETE CASCADE,
  trade_date  TEXT NOT NULL,
  captured_at TEXT NOT NULL,
  close REAL,
  PRIMARY KEY (code, trade_date)
);
CREATE TABLE decision_log (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  code TEXT NOT NULL,
  log_date TEXT NOT NULL,
  action TEXT NOT NULL,
  tier TEXT NOT NULL,
  score_total INTEGER,
  adx REAL,
  sar_long INTEGER,
  st_long INTEGER,
  obv_up INTEGER,
  macd_hist REAL,
  td_countdown TEXT,
  signals TEXT,
  created_at TEXT NOT NULL,
  outcome_pct REAL,
  outcome_date TEXT,
  correct INTEGER,
  UNIQUE(code, log_date, action)
);
INSERT INTO instrument (code, name, market, created_at) VALUES ('sz000001', '平安银行', 'sz', 'now');
INSERT INTO decision_log (code, log_date, action, tier, created_at)
VALUES ('sz000001', '2026-06-01', 'recommend', '⭐⭐', 'now'),
       ('sz999999', '2026-06-01', 'recommend', '⭐⭐', 'now');`)
	if err != nil {
		t.Fatalf("seed legacy schema: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy sqlite: %v", err)
	}

	repaired, err := Open(path)
	if err != nil {
		t.Fatalf("open repaired store: %v", err)
	}
	defer repaired.Close()

	var fkCount int
	if err := repaired.db.QueryRow(`
SELECT COUNT(*)
FROM pragma_foreign_key_list('decision_log')
WHERE "table" = 'instrument' AND "from" = 'code' AND "to" = 'code' AND on_delete = 'CASCADE'`).Scan(&fkCount); err != nil {
		t.Fatalf("inspect repaired foreign keys: %v", err)
	}
	if fkCount != 1 {
		t.Fatalf("expected repaired decision_log foreign key, got %d", fkCount)
	}

	var rows int
	if err := repaired.db.QueryRow(`SELECT COUNT(*) FROM decision_log`).Scan(&rows); err != nil {
		t.Fatalf("count repaired decisions: %v", err)
	}
	if rows != 1 {
		t.Fatalf("expected only valid legacy decision row copied, got %d", rows)
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

func TestCloseAfterUsesGlobalTradingDayAndRequiresExactCodeSnapshot(t *testing.T) {
	s := openTemp(t)

	for _, c := range []string{"sz000001", "sz000002"} {
		if err := s.UpsertInstrument(c, c, "sz", ""); err != nil {
			t.Fatalf("seed instrument %s: %v", c, err)
		}
	}
	for day := 1; day <= 5; day++ {
		date := fmt.Sprintf("2026-06-%02d", day)
		if err := s.SaveSnapshot(Snapshot{Code: "sz000001", TradeDate: date, Close: float64(10 + day)}); err != nil {
			t.Fatalf("seed sz000001 %s: %v", date, err)
		}
		if day != 4 {
			if err := s.SaveSnapshot(Snapshot{Code: "sz000002", TradeDate: date, Close: float64(20 + day)}); err != nil {
				t.Fatalf("seed sz000002 %s: %v", date, err)
			}
		}
	}

	close, date, err := s.CloseAfter("sz000002", "2026-06-01", 3)
	if err != nil {
		t.Fatalf("CloseAfter missing exact date: %v", err)
	}
	if close != 0 || date != "" {
		t.Fatalf("expected missing exact global date to skip, got close=%v date=%q", close, date)
	}

	close, date, err = s.CloseAfter("sz000002", "2026-06-01", 4)
	if err != nil {
		t.Fatalf("CloseAfter next global date: %v", err)
	}
	if close != 25 || date != "2026-06-05" {
		t.Fatalf("expected exact fourth global day close=25 date=2026-06-05, got close=%v date=%q", close, date)
	}
}

// TestDecisionLogLifecycle covers the decision-log round trip: SaveDecision dedup
// via UNIQUE(code,log_date,action), PendingDecisions surfaces un-backfilled rows
// older than the T+10 natural-day cutoff, BackfillDecision writes outcome/correct,
// and StatsByTier only aggregates rows whose outcome_pct has been set.
func TestDecisionLogLifecycle(t *testing.T) {
	s := openTemp(t)

	if err := s.UpsertInstrument("sz000001", "平安银行", "sz", ""); err != nil {
		t.Fatalf("seed instrument: %v", err)
	}

	d := Decision{
		Code: "sz000001", LogDate: "2026-01-01", Action: "recommend", Tier: "⭐⭐⭐",
		ScoreTotal: 70, ADX: 25, SARLong: true, STLong: true, OBVUp: true,
		MACDHist: 0.1, TDCountdown: "见顶/3", Signals: "trend_bull",
	}
	if err := s.SaveDecision(d); err != nil {
		t.Fatalf("SaveDecision: %v", err)
	}
	// Duplicate (code, log_date, action) must be ignored, not error — SaveDecision
	// uses INSERT OR IGNORE on the UNIQUE constraint.
	if err := s.SaveDecision(d); err != nil {
		t.Fatalf("SaveDecision duplicate should be ignored: %v", err)
	}

	pending, err := s.PendingDecisions()
	if err != nil {
		t.Fatalf("PendingDecisions: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending decision (log_date 2026-01-01 is older than the -10 days cutoff), got %d", len(pending))
	}
	if pending[0].Code != "sz000001" || pending[0].Action != "recommend" {
		t.Fatalf("unexpected pending decision: %+v", pending[0])
	}
	id := pending[0].ID

	// A fresh (within the cutoff) decision must NOT be returned as pending.
	fresh := Decision{Code: "sz000001", LogDate: todayISO(), Action: "hold", Tier: "持仓"}
	if err := s.SaveDecision(fresh); err != nil {
		t.Fatalf("SaveDecision fresh: %v", err)
	}
	pending, err = s.PendingDecisions()
	if err != nil {
		t.Fatalf("PendingDecisions after fresh: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("fresh decision (within -10 days) should not surface as pending, got %d", len(pending))
	}

	// Backfill the original pending decision.
	if err := s.BackfillDecision(id, 3.5, "2026-01-15", true); err != nil {
		t.Fatalf("BackfillDecision: %v", err)
	}

	// Once backfilled, it must leave the pending set.
	pending, err = s.PendingDecisions()
	if err != nil {
		t.Fatalf("PendingDecisions after backfill: %v", err)
	}
	// Only the fresh un-backfilled hold remains; the backfilled recommend is gone.
	stillPending := false
	for _, p := range pending {
		if p.ID == id {
			stillPending = true
		}
	}
	if stillPending {
		t.Fatalf("backfilled decision %d must not remain pending: %+v", id, pending)
	}

	// StatsByTier aggregates only outcome_pct IS NOT NULL — the backfilled row
	// counts, the un-backfilled hold does not.
	stats, err := s.StatsByTier()
	if err != nil {
		t.Fatalf("StatsByTier: %v", err)
	}
	var found bool
	for _, st := range stats {
		if st.Tier == "⭐⭐⭐" {
			found = true
			if st.Count != 1 || st.Wins != 1 {
				t.Fatalf("tier ⭐⭐⭐ expected count=1 wins=1, got count=%d wins=%d", st.Count, st.Wins)
			}
		}
	}
	if !found {
		t.Fatalf("expected tier ⭐⭐⭐ in stats, got %+v", stats)
	}
}

// todayISO returns the current date as YYYY-MM-DD, used to seed a within-cutoff
// decision that PendingDecisions must skip.
func todayISO() string {
	return time.Now().Format("2006-01-02")
}

func TestImportHotStocksInsertIgnore(t *testing.T) {
	s := openTemp(t)

	entries := []HotStockEntry{
		{Code: "sh600519", Name: "贵州茅台", Market: "sh"},
		{Code: "sz000001", Name: "平安银行", Market: "sz"},
	}
	res, err := s.ImportHotStocks(entries)
	if err != nil {
		t.Fatalf("ImportHotStocks: %v", err)
	}
	if res.Imported != 2 {
		t.Errorf("Imported = %d, want 2", res.Imported)
	}

	// Verify the new stock was created with hot_score=9 (today's hot list).
	var name string
	var hot int
	if err := s.db.QueryRow(`SELECT name, hot_score FROM instrument WHERE code = 'sz000001'`).Scan(&name, &hot); err != nil {
		t.Fatalf("query new: %v", err)
	}
	if name != "平安银行" {
		t.Errorf("sz000001 name = %q, want 平安银行", name)
	}
	if hot != 9 {
		t.Errorf("sz000001 hot_score = %d, want 9 (today's hot list)", hot)
	}
}

func TestImportHotStocksIgnoresExisting(t *testing.T) {
	s := openTemp(t)

	// Seed one pre-existing instrument with a custom name. Set hot_score=9 so it
	// looks like yesterday's hot-list entry rather than a fully-decayed cold row
	// (which the decay step would prune before the INSERT OR IGNORE runs).
	if err := s.UpsertInstrument("sh600519", "茅台", "sh", "custom"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := s.db.Exec(`UPDATE instrument SET hot_score = 9 WHERE code = 'sh600519'`); err != nil {
		t.Fatalf("seed hot_score: %v", err)
	}

	// Import the same code with a different name — INSERT OR IGNORE must not overwrite.
	res, err := s.ImportHotStocks([]HotStockEntry{
		{Code: "sh600519", Name: "贵州茅台", Market: "sh"},
		{Code: "sz000001", Name: "平安银行", Market: "sz"},
	})
	if err != nil {
		t.Fatalf("ImportHotStocks: %v", err)
	}
	if res.Imported != 1 {
		t.Errorf("Imported = %d, want 1 (only sz000001 is new)", res.Imported)
	}
	if res.Refreshed != 1 {
		t.Errorf("Refreshed = %d, want 1 (sh600519 reset to 9)", res.Refreshed)
	}

	// Verify the existing stock's name/note were NOT overwritten.
	var name, note string
	if err := s.db.QueryRow(`SELECT name, note FROM instrument WHERE code = 'sh600519'`).Scan(&name, &note); err != nil {
		t.Fatalf("query existing: %v", err)
	}
	if name != "茅台" {
		t.Errorf("sh600519 name = %q, want 茅台 (should be untouched)", name)
	}
	if note != "custom" {
		t.Errorf("sh600519 note = %q, want custom (should be untouched)", note)
	}
}

func TestImportHotStocksDecay(t *testing.T) {
	s := openTemp(t)

	// Seed instruments mimicking various decay stages. None are on today's list,
	// so they should all decrement by 1 and the score=0 row should be pruned.
	seed := []struct {
		code string
		hot  int
	}{
		{"sh600001", 9},
		{"sh600002", 5},
		{"sh600003", 1},
		{"sh600004", 0}, // pruned by decay
	}
	for _, r := range seed {
		if err := s.UpsertInstrument(r.code, r.code, "sh", ""); err != nil {
			t.Fatalf("seed %s: %v", r.code, err)
		}
		if _, err := s.db.Exec(`UPDATE instrument SET hot_score = ? WHERE code = ?`, r.hot, r.code); err != nil {
			t.Fatalf("seed hot_score %s: %v", r.code, err)
		}
	}

	// Import an empty list so no stocks get inserted/reset — only the decay step runs.
	res, err := s.ImportHotStocks(nil)
	if err != nil {
		t.Fatalf("ImportHotStocks: %v", err)
	}
	if !res.Decayed {
		t.Error("Decayed = false, want true (first call of the day)")
	}
	if res.Pruned != 1 {
		t.Errorf("Pruned = %d, want 1 (the hot_score=0 row)", res.Pruned)
	}
	if res.Imported != 0 {
		t.Errorf("Imported = %d, want 0", res.Imported)
	}

	// Remaining rows should all have decremented by 1.
	for _, r := range seed {
		if r.hot == 0 {
			continue
		}
		var got int
		if err := s.db.QueryRow(`SELECT hot_score FROM instrument WHERE code = ?`, r.code).Scan(&got); err != nil {
			t.Fatalf("query %s: %v", r.code, err)
		}
		want := r.hot - 1
		if got != want {
			t.Errorf("%s hot_score = %d, want %d", r.code, got, want)
		}
	}
	var remaining int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM instrument`).Scan(&remaining); err != nil {
		t.Fatalf("count: %v", err)
	}
	if remaining != len(seed)-1 {
		t.Errorf("remaining rows = %d, want %d", remaining, len(seed)-1)
	}
}

func TestImportHotStocksIdempotentSameDay(t *testing.T) {
	s := openTemp(t)

	// Seed one off-list row at hot_score=3.
	if err := s.UpsertInstrument("sh600001", "x", "sh", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := s.db.Exec(`UPDATE instrument SET hot_score = 3 WHERE code = 'sh600001'`); err != nil {
		t.Fatalf("seed hot_score: %v", err)
	}

	// First call: decay runs -> hot_score becomes 2.
	if _, err := s.ImportHotStocks(nil); err != nil {
		t.Fatalf("first import: %v", err)
	}
	// Second call same day: decay must NOT run again.
	res, err := s.ImportHotStocks(nil)
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if res.Decayed {
		t.Error("Decayed = true on second same-day call, want false")
	}
	var got int
	if err := s.db.QueryRow(`SELECT hot_score FROM instrument WHERE code = 'sh600001'`).Scan(&got); err != nil {
		t.Fatalf("query: %v", err)
	}
	if got != 2 {
		t.Errorf("hot_score = %d after second call, want 2 (no double decay)", got)
	}
}
