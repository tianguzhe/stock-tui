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
		Low20:                    91.2,
		OBVUp3:                   true,
	}
	if err := s.SaveSnapshot(snap); err != nil {
		t.Fatalf("first save: %v", err)
	}

	read := func() (gotAvg sql.NullFloat64, squeeze, b20, b55, adj int, sarV, stV, low, high, amp, inVol, outVol, low20 float64, obvUp3 int) {
		t.Helper()
		err := s.db.QueryRow(
			`SELECT perf_trend_follow_bull_avg10, keltner_squeeze, donch_break20_bull, donch_break55_bull, score_adj, sar_value, supertrend_value, low, high, amplitude, inside_vol, outside_vol, low20, obv_up3
			 FROM snapshot WHERE code='sz002916' AND trade_date='2026-06-12'`,
		).Scan(&gotAvg, &squeeze, &b20, &b55, &adj, &sarV, &stV, &low, &high, &amp, &inVol, &outVol, &low20, &obvUp3)
		if err != nil {
			t.Fatalf("read new columns: %v", err)
		}
		return
	}

	gotAvg, squeeze, b20, b55, adj, sarV, stV, low, high, amp, inVol, outVol, low20, obvUp3 := read()
	if !gotAvg.Valid || gotAvg.Float64 != 6.5 || squeeze != 1 || b20 != 1 || b55 != 0 || adj != 66 || sarV != 95.5 || stV != 93.2 || low != 96.4 || high != 103.7 || amp != 7.3 || inVol != 1200 || outVol != 3400 || low20 != 91.2 || obvUp3 != 1 {
		t.Fatalf("round-trip mismatch: avg10=%+v squeeze=%d b20=%d b55=%d adj=%d sar=%v st=%v low=%v high=%v amp=%v in=%v out=%v low20=%v obvUp3=%d", gotAvg, squeeze, b20, b55, adj, sarV, stV, low, high, amp, inVol, outVol, low20, obvUp3)
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
	snap.Low20 = 85.5
	snap.OBVUp3 = false
	if err := s.SaveSnapshot(snap); err != nil {
		t.Fatalf("second save: %v", err)
	}
	gotAvg, squeeze, b20, b55, adj, sarV, stV, low, high, amp, inVol, outVol, low20, obvUp3 = read()
	if !gotAvg.Valid || gotAvg.Float64 != -1.2 || squeeze != 0 || b20 != 0 || b55 != 1 || adj != 48 || sarV != 97.1 || stV != 94.8 || low != 88.0 || high != 99.9 || amp != 4.1 || inVol != 800 || outVol != 2100 || low20 != 85.5 || obvUp3 != 0 {
		t.Fatalf("same-day overwrite mismatch: avg10=%+v squeeze=%d b20=%d b55=%d adj=%d sar=%v st=%v low=%v high=%v amp=%v in=%v out=%v low20=%v obvUp3=%d", gotAvg, squeeze, b20, b55, adj, sarV, stV, low, high, amp, inVol, outVol, low20, obvUp3)
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
		+COALESCE(amplitude,0)+COALESCE(inside_vol,0)+COALESCE(outside_vol,0)
		+COALESCE(low20,0)+COALESCE(obv_up3,0) FROM snapshot LIMIT 1`,
	).Scan(&dummy)
	// A "no rows" error is fine; "no such column" would be an error.
	if err != nil && err.Error() != "sql: no rows in result set" {
		t.Fatalf("new columns not accessible after migration: %v", err)
	}
}

// TestMigrationDropsInstrumentForeignKeys 迁移必须移除 snapshot/decision_log 指向
// instrument 的级联外键。instrument 是每日轮动的热榜池（ImportHotStocks 清理冷门
// 代码），而这两张表是回测与 PERF 的历史样本来源，级联删除会随热榜清理静默销毁历史。
// 迁移同时不得丢弃已成孤儿的历史行——它们正是过去被级联删除前的样本。
func TestMigrationDropsInstrumentForeignKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-fk.db")

	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy sqlite: %v", err)
	}
	// 裸连接默认 foreign_keys=OFF，故可以直接埋入孤儿行。
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
  code TEXT NOT NULL REFERENCES instrument(code) ON DELETE CASCADE,
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
INSERT INTO snapshot (code, trade_date, captured_at, close)
VALUES ('sz000001', '2026-06-01', 'now', 10.5),
       ('sz000001', '2026-06-02', 'now', 11.5),
       ('sz999999', '2026-06-01', 'now', 99.5);
INSERT INTO decision_log (code, log_date, action, tier, created_at)
VALUES ('sz000001', '2026-06-01', 'recommend', '⭐⭐', 'now'),
       ('sz999999', '2026-06-01', 'recommend', '⭐⭐', 'now');`)
	if err != nil {
		t.Fatalf("seed legacy schema: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy sqlite: %v", err)
	}

	migrated, err := Open(path)
	if err != nil {
		t.Fatalf("open migrated store: %v", err)
	}
	defer migrated.Close()

	for _, table := range []string{"snapshot", "decision_log"} {
		hasFK, err := migrated.hasInstrumentFK(table)
		if err != nil {
			t.Fatalf("inspect %s foreign keys: %v", table, err)
		}
		if hasFK {
			t.Errorf("%s 仍带 instrument 外键：热榜清理会继续级联删除历史", table)
		}
	}

	var snapRows int
	if err := migrated.db.QueryRow(`SELECT COUNT(*) FROM snapshot`).Scan(&snapRows); err != nil {
		t.Fatalf("count migrated snapshots: %v", err)
	}
	if snapRows != 3 {
		t.Errorf("snapshot 行数 = %d, want 3（含孤儿行）", snapRows)
	}

	var logRows int
	if err := migrated.db.QueryRow(`SELECT COUNT(*) FROM decision_log`).Scan(&logRows); err != nil {
		t.Fatalf("count migrated decisions: %v", err)
	}
	if logRows != 2 {
		t.Errorf("decision_log 行数 = %d, want 2（含孤儿行）", logRows)
	}

	// 重建走 SELECT *，列顺序错位会静默串值，故校验具体取值。
	var close2 float64
	if err := migrated.db.QueryRow(
		`SELECT close FROM snapshot WHERE code = 'sz000001' AND trade_date = '2026-06-02'`).Scan(&close2); err != nil {
		t.Fatalf("read migrated close: %v", err)
	}
	if close2 != 11.5 {
		t.Errorf("close = %v, want 11.5（列顺序在重建中错位）", close2)
	}
}

// TestHotPruneKeepsHistoricalRows 是上面迁移的端到端对应：热榜清理冷门标的后，
// 该标的的 snapshot/decision_log 历史必须留下。回测与 PERF 直接读 snapshot 而不
// join instrument，级联删除会让样本静默缩水；重新入榜的标的还会以"新股"身份从零
// 开始累积，全程不报错。
func TestHotPruneKeepsHistoricalRows(t *testing.T) {
	s := openTemp(t)

	if err := s.UpsertInstrument("sh600001", "冷门股", "sh", ""); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := s.SaveSnapshot(Snapshot{
		Code: "sh600001", TradeDate: "2026-06-01", Close: 12.5, ScoreTotal: 60,
	}); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}
	if _, err := s.db.Exec(`
INSERT INTO decision_log (code, log_date, action, tier, created_at)
VALUES ('sh600001', '2026-06-01', 'recommend', '⭐⭐', 'now')`); err != nil {
		t.Fatalf("seed decision_log: %v", err)
	}
	if _, err := s.db.Exec(`UPDATE instrument SET hot_score = 0`); err != nil {
		t.Fatalf("set hot_score=0: %v", err)
	}

	// 今日尚未衰减过，故这次导入会执行 decay+prune。
	if _, err := s.ImportHotStocks([]HotStockEntry{
		{Code: "sh600003", Name: "今日热门", Market: "sh"},
	}); err != nil {
		t.Fatalf("ImportHotStocks: %v", err)
	}

	var stillPooled int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM instrument WHERE code = 'sh600001'`).Scan(&stillPooled); err != nil {
		t.Fatal(err)
	}
	if stillPooled != 0 {
		t.Fatal("冷门标的应被移出 instrument 池（拉取范围需要收敛）")
	}

	var snapKept, logKept int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM snapshot WHERE code = 'sh600001'`).Scan(&snapKept); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM decision_log WHERE code = 'sh600001'`).Scan(&logKept); err != nil {
		t.Fatal(err)
	}
	if snapKept != 1 {
		t.Error("热榜清理连带删除了 snapshot 历史——回测与 PERF 样本会静默缩水")
	}
	if logKept != 1 {
		t.Error("热榜清理连带删除了 decision_log——已结算的胜率样本会丢失")
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

// backfill-date 补出的历史行 rs 列为 0，需要单独补算；补算必须只作用于目标日，
// 不能扰动已排好的最新日。
func TestUpdateRSRankingsForDateOnlyTouchesThatDate(t *testing.T) {
	s := openTemp(t)

	const older, newer = "2026-07-20", "2026-07-21"
	codes := []string{"sz000001", "sz000002", "sz000003"}
	for i, c := range codes {
		if err := s.UpsertInstrument(c, c, "sz", ""); err != nil {
			t.Fatalf("seed instrument: %v", err)
		}
		for _, d := range []string{older, newer} {
			if err := s.SaveSnapshot(Snapshot{
				Code: c, TradeDate: d, Close: 10, Ret20: float64(i) * 5,
			}); err != nil {
				t.Fatalf("seed snapshot: %v", err)
			}
		}
	}

	// rs20 不在 SaveSnapshot 的写入列内，未排名的行为 NULL，故用 COALESCE 归零。
	rsOn := func(code, date string) float64 {
		t.Helper()
		var rs float64
		if err := s.DB().QueryRow(
			`SELECT COALESCE(rs20, 0) FROM snapshot WHERE code=? AND trade_date=?`,
			code, date).Scan(&rs); err != nil {
			t.Fatalf("read rs20 %s@%s: %v", code, date, err)
		}
		return rs
	}

	// 默认只排最新日，历史日应保持未排名。
	if _, err := s.UpdateRSRankings(); err != nil {
		t.Fatalf("UpdateRSRankings: %v", err)
	}
	if got := rsOn("sz000003", older); got != 0 {
		t.Errorf("older rs20 = %v after ranking newest only, want 0", got)
	}
	newestBefore := rsOn("sz000003", newer)
	if newestBefore == 0 {
		t.Fatal("newest date was not ranked")
	}

	// 补算历史日：该日被排名，最新日不受影响。
	n, err := s.UpdateRSRankingsForDate(older)
	if err != nil {
		t.Fatalf("UpdateRSRankingsForDate: %v", err)
	}
	if n != len(codes) {
		t.Fatalf("updated %d, want %d", n, len(codes))
	}
	if got := rsOn("sz000003", older); got == 0 {
		t.Error("older rs20 still 0 after backfilling that date")
	}
	if got := rsOn("sz000003", newer); got != newestBefore {
		t.Errorf("newest rs20 changed to %v, want unchanged %v", got, newestBefore)
	}
}

func TestUpdateRSRankingsForDateRejectsEmptyDate(t *testing.T) {
	s := openTemp(t)
	if _, err := s.UpdateRSRankingsForDate(""); err == nil {
		t.Error("expected an error for an empty date")
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

// TestHotPruneExemptsHoldings 持仓豁免热榜清理：hot_score 衰减到 0 的普通标的
// 会被删除，但 note='holdings' 的必须留下——否则 batch-save 停止更新它，
// 选股表的持仓行会静默读到越来越旧的快照。
func TestHotPruneExemptsHoldings(t *testing.T) {
	s := openTemp(t)

	// 两只都冷到 hot_score=0，其中一只是持仓。
	for _, c := range []struct{ code, name string }{
		{"sh600001", "冷门股"},
		{"sh600002", "持仓股"},
	} {
		if err := s.UpsertInstrument(c.code, c.name, "sh", ""); err != nil {
			t.Fatalf("upsert %s: %v", c.code, err)
		}
	}
	if _, err := s.db.Exec(`UPDATE instrument SET hot_score = 0`); err != nil {
		t.Fatalf("set hot_score=0: %v", err)
	}
	n, err := s.MarkHoldings([]string{"sh600002"})
	if err != nil {
		t.Fatalf("MarkHoldings: %v", err)
	}
	if n != 1 {
		t.Fatalf("MarkHoldings updated %d rows, want 1", n)
	}

	// 触发一次热榜导入（今日尚未衰减过，故 decay+prune 会执行）。
	if _, err := s.ImportHotStocks([]HotStockEntry{
		{Code: "sh600003", Name: "今日热门", Market: "sh"},
	}); err != nil {
		t.Fatalf("ImportHotStocks: %v", err)
	}

	var coldGone, holdingKept int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM instrument WHERE code='sh600001'`).Scan(&coldGone); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM instrument WHERE code='sh600002'`).Scan(&holdingKept); err != nil {
		t.Fatal(err)
	}
	if coldGone != 0 {
		t.Error("非持仓的冷门标的应被清理，但仍存在")
	}
	if holdingKept != 1 {
		t.Error("持仓标的被热榜清理了——batch-save 将停止更新它，持仓数据会静默陈旧")
	}
}

// TestMarkHoldingsIdempotentAndSkipsMissing 重复标记不重复计数；
// instrument 中不存在的代码被忽略而非报错。
func TestMarkHoldingsIdempotentAndSkipsMissing(t *testing.T) {
	s := openTemp(t)
	if err := s.UpsertInstrument("sh600519", "贵州茅台", "sh", ""); err != nil {
		t.Fatal(err)
	}

	n, err := s.MarkHoldings([]string{"sh600519", "sh999999"})
	if err != nil {
		t.Fatalf("MarkHoldings: %v", err)
	}
	if n != 1 {
		t.Errorf("首次标记 updated=%d, want 1 (不存在的代码应被忽略)", n)
	}

	n, err = s.MarkHoldings([]string{"sh600519"})
	if err != nil {
		t.Fatalf("MarkHoldings 二次: %v", err)
	}
	if n != 0 {
		t.Errorf("重复标记 updated=%d, want 0 (已是 holdings)", n)
	}

	if _, err := s.MarkHoldings(nil); err != nil {
		t.Errorf("MarkHoldings(nil) 应安全返回, got %v", err)
	}
}
