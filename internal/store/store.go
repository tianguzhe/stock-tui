// Package store persists technical-analysis snapshots and a tagged instrument
// list in a local SQLite database (pure-Go modernc.org/sqlite driver).
//
// Two concerns are served:
//   - classification: instruments carry many-to-many tags (sectors/groups);
//   - recording: each analysis run upserts one snapshot keyed by (code, trade_date),
//     so the same trading day keeps only its latest result while history accrues.
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"stock-tui/internal/market"

	_ "modernc.org/sqlite"
)

// DefaultPath returns the database location: $STOCK_DB if set, else data/stock.db.
func DefaultPath() string {
	if p := os.Getenv("STOCK_DB"); p != "" {
		return p
	}
	return filepath.Join("data", "stock.db")
}

// Store wraps the SQLite handle. Construct it with Open and release it with Close.
type Store struct {
	db *sql.DB
}

// Instrument is a tracked symbol in the watchlist.
type Instrument struct {
	Code   string
	Name   string
	Market string
	Note   string
}

// Snapshot is one analysis result for a symbol on a trading day. Fields mirror
// the snapshot table columns one-to-one; bool indicators are stored as 0/1.
type Snapshot struct {
	Code      string
	TradeDate string

	Close     float64
	ChangePct float64

	MA5, MA10, MA20, MA60 float64

	KDJ_J                          float64
	MACD_DIF, MACD_DEA, MACD_Hist  float64
	RSI6, WR14, BIAS6, BIAS24      float64
	PDI, MDI, ADX, ADXR, CMI, CHOP float64
	ATRPct, BollPB, BollBW, MFI    float64
	SARLong, SuperTrendLong        bool
	VolRatio                       float64
	OBVUp                          bool
	ScoreTotal, ScoreDelta         int
	ScoreLabel                     string
	SigTrendBull, SigOverbought    bool
	SigOversold                    bool
	DivBull, DivBear, DivBearToday bool
	TDSetup, TDCountdown           string
	Streak                         int // positive: consecutive up days, negative: down days

	// Fundamental data (populated by indicator-analyze -save via Tencent real-time API).
	TurnoverRate float64 // 换手率 %
	MarketCap    float64 // 总市值 亿元
	PE           float64 // 市盈率动态

	// Raw N-day price returns (%) computed from K-line data during -save.
	Ret20, Ret60, Ret120 float64

	// RS percentile rankings (0–100) computed by stockdb rs-rank after batch saves.
	RS20, RS60, RS120 float64
}

// Open opens (creating if needed) the SQLite database at path, enables foreign
// keys, and runs the idempotent schema migration.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create db dir %s: %w", dir, err)
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable foreign_keys: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the underlying database handle.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	const schema = `
CREATE TABLE IF NOT EXISTS instrument (
  code       TEXT PRIMARY KEY,
  name       TEXT NOT NULL,
  market     TEXT NOT NULL,
  note       TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS tag (
  id   INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL UNIQUE
);
CREATE TABLE IF NOT EXISTS instrument_tag (
  code   TEXT NOT NULL REFERENCES instrument(code) ON DELETE CASCADE,
  tag_id INTEGER NOT NULL REFERENCES tag(id) ON DELETE CASCADE,
  PRIMARY KEY (code, tag_id)
);
CREATE TABLE IF NOT EXISTS snapshot (
  code        TEXT NOT NULL REFERENCES instrument(code) ON DELETE CASCADE,
  trade_date  TEXT NOT NULL,
  captured_at TEXT NOT NULL,
  close REAL, change_pct REAL,
  ma5 REAL, ma10 REAL, ma20 REAL, ma60 REAL,
  kdj_j REAL, macd_dif REAL, macd_dea REAL, macd_hist REAL,
  rsi6 REAL, wr14 REAL, bias6 REAL, bias24 REAL,
  pdi REAL, mdi REAL, adx REAL, adxr REAL, cmi REAL, chop REAL,
  atr_pct REAL, boll_pb REAL, boll_bw REAL, mfi REAL,
  sar_long INTEGER, supertrend_long INTEGER,
  vol_ratio REAL, obv_up INTEGER,
  score_total INTEGER, score_delta INTEGER, score_label TEXT,
  sig_trend_bull INTEGER, sig_overbought INTEGER, sig_oversold INTEGER,
  div_bull INTEGER, div_bear INTEGER, div_bear_today INTEGER,
  td_setup TEXT, td_countdown TEXT,
  streak INTEGER,
  turnover_rate REAL DEFAULT 0,
  market_cap REAL DEFAULT 0,
  pe REAL DEFAULT 0,
  ret20 REAL,
  ret60 REAL,
  ret120 REAL,
  rs20 REAL DEFAULT 0,
  rs60 REAL DEFAULT 0,
  rs120 REAL DEFAULT 0,
  PRIMARY KEY (code, trade_date)
);
CREATE TABLE IF NOT EXISTS decision_log (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  code         TEXT NOT NULL REFERENCES instrument(code) ON DELETE CASCADE,
  log_date     TEXT NOT NULL,
  action       TEXT NOT NULL,
  tier         TEXT NOT NULL,
  score_total  INTEGER,
  adx          REAL,
  sar_long     INTEGER,
  st_long      INTEGER,
  obv_up       INTEGER,
  macd_hist    REAL,
  td_countdown TEXT,
  signals      TEXT,
  created_at   TEXT NOT NULL,
  outcome_pct  REAL,
  outcome_date TEXT,
  correct      INTEGER,
  UNIQUE(code, log_date, action)
);
CREATE INDEX IF NOT EXISTS idx_decision_log_date ON decision_log(log_date);
CREATE INDEX IF NOT EXISTS idx_decision_log_pending ON decision_log(outcome_pct) WHERE outcome_pct IS NULL;`
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("migrate schema: %w", err)
	}
	if err := s.ensureDecisionLogForeignKey(); err != nil {
		return err
	}
	// Add new columns to existing databases; SQLite does not support IF NOT EXISTS
	// in ALTER TABLE, so we ignore the "duplicate column name" error.
	for _, col := range []string{
		"turnover_rate REAL DEFAULT 0",
		"market_cap REAL DEFAULT 0",
		"pe REAL DEFAULT 0",
		"ret20 REAL",
		"ret60 REAL",
		"ret120 REAL",
		"rs20 REAL DEFAULT 0",
		"rs60 REAL DEFAULT 0",
		"rs120 REAL DEFAULT 0",
	} {
		s.db.Exec("ALTER TABLE snapshot ADD COLUMN " + col) //nolint:errcheck
	}
	// Clear ret values that are all-zero but were written as Go zero-values before
	// the nDayReturn computation existed. All three being exactly 0 is impossible
	// for real market data across 20/60/120 days.
	s.db.Exec(`UPDATE snapshot SET ret20=NULL, ret60=NULL, ret120=NULL
		WHERE ret20=0 AND ret60=0 AND ret120=0 AND COALESCE(ret20,0)=0`) //nolint:errcheck
	return nil
}

func (s *Store) ensureDecisionLogForeignKey() error {
	rows, err := s.db.Query(`PRAGMA foreign_key_list(decision_log)`)
	if err != nil {
		return fmt.Errorf("inspect decision_log foreign keys: %w", err)
	}
	hasFK := false
	for rows.Next() {
		var id, seq int
		var table, from, to, onUpdate, onDelete, match string
		if err := rows.Scan(&id, &seq, &table, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			rows.Close()
			return err
		}
		if table == "instrument" && from == "code" && to == "code" && onDelete == "CASCADE" {
			hasFK = true
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if hasFK {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`ALTER TABLE decision_log RENAME TO decision_log_legacy`); err != nil {
		return fmt.Errorf("rename legacy decision_log: %w", err)
	}
	if _, err := tx.Exec(`DROP INDEX IF EXISTS idx_decision_log_date`); err != nil {
		return fmt.Errorf("drop legacy decision_log date index: %w", err)
	}
	if _, err := tx.Exec(`DROP INDEX IF EXISTS idx_decision_log_pending`); err != nil {
		return fmt.Errorf("drop legacy decision_log pending index: %w", err)
	}
	if _, err := tx.Exec(`
CREATE TABLE decision_log (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  code         TEXT NOT NULL REFERENCES instrument(code) ON DELETE CASCADE,
  log_date     TEXT NOT NULL,
  action       TEXT NOT NULL,
  tier         TEXT NOT NULL,
  score_total  INTEGER,
  adx          REAL,
  sar_long     INTEGER,
  st_long      INTEGER,
  obv_up       INTEGER,
  macd_hist    REAL,
  td_countdown TEXT,
  signals      TEXT,
  created_at   TEXT NOT NULL,
  outcome_pct  REAL,
  outcome_date TEXT,
  correct      INTEGER,
  UNIQUE(code, log_date, action)
)`); err != nil {
		return fmt.Errorf("create repaired decision_log: %w", err)
	}
	if _, err := tx.Exec(`
INSERT OR IGNORE INTO decision_log
  (id, code, log_date, action, tier, score_total, adx, sar_long, st_long,
   obv_up, macd_hist, td_countdown, signals, created_at, outcome_pct,
   outcome_date, correct)
SELECT id, code, log_date, action, tier, score_total, adx, sar_long, st_long,
       obv_up, macd_hist, td_countdown, signals, created_at, outcome_pct,
       outcome_date, correct
FROM decision_log_legacy
WHERE EXISTS (SELECT 1 FROM instrument WHERE instrument.code = decision_log_legacy.code)`); err != nil {
		return fmt.Errorf("copy legacy decision_log: %w", err)
	}
	if _, err := tx.Exec(`DROP TABLE decision_log_legacy`); err != nil {
		return fmt.Errorf("drop legacy decision_log: %w", err)
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_decision_log_date ON decision_log(log_date)`); err != nil {
		return fmt.Errorf("create decision_log date index: %w", err)
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_decision_log_pending ON decision_log(outcome_pct) WHERE outcome_pct IS NULL`); err != nil {
		return fmt.Errorf("create decision_log pending index: %w", err)
	}
	return tx.Commit()
}

// UpsertInstrument inserts the instrument or updates its name/market/note when
// the new value is non-empty, preserving existing fields on blank input (so a
// tag-only call that lacks the name does not wipe a previously recorded name).
func (s *Store) UpsertInstrument(code, name, market, note string) error {
	_, err := s.db.Exec(`
INSERT INTO instrument (code, name, market, note, created_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(code) DO UPDATE SET
  name   = CASE WHEN excluded.name   <> '' THEN excluded.name   ELSE instrument.name   END,
  market = CASE WHEN excluded.market <> '' THEN excluded.market ELSE instrument.market END,
  note   = CASE WHEN excluded.note   <> '' THEN excluded.note   ELSE instrument.note   END`,
		code, name, market, note, time.Now().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("upsert instrument %s: %w", code, err)
	}
	return nil
}

// AddTag attaches tag to code, creating the tag and a placeholder instrument row
// if needed. It is idempotent.
func (s *Store) AddTag(code, tag string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
INSERT INTO instrument (code, name, market, created_at)
VALUES (?, '', ?, ?)
ON CONFLICT(code) DO NOTHING`, code, market.Prefix(code), time.Now().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("ensure instrument %s: %w", code, err)
	}
	if _, err := tx.Exec(`INSERT INTO tag (name) VALUES (?) ON CONFLICT(name) DO NOTHING`, tag); err != nil {
		return fmt.Errorf("ensure tag %s: %w", tag, err)
	}
	if _, err := tx.Exec(`
INSERT INTO instrument_tag (code, tag_id)
SELECT ?, id FROM tag WHERE name = ?
ON CONFLICT(code, tag_id) DO NOTHING`, code, tag); err != nil {
		return fmt.Errorf("link %s<->%s: %w", code, tag, err)
	}
	return tx.Commit()
}

// RemoveTag detaches tag from code. Removing a missing link is a no-op.
func (s *Store) RemoveTag(code, tag string) error {
	_, err := s.db.Exec(`
DELETE FROM instrument_tag
WHERE code = ? AND tag_id = (SELECT id FROM tag WHERE name = ?)`, code, tag)
	if err != nil {
		return fmt.Errorf("remove tag %s from %s: %w", tag, code, err)
	}
	return nil
}

// SaveSnapshot upserts s, keyed by (code, trade_date): re-analyzing the same
// trading day overwrites the prior row instead of duplicating it.
func (s *Store) SaveSnapshot(snap Snapshot) error {
	_, err := s.db.Exec(`
INSERT INTO snapshot (
  code, trade_date, captured_at,
  close, change_pct, ma5, ma10, ma20, ma60,
  kdj_j, macd_dif, macd_dea, macd_hist,
  rsi6, wr14, bias6, bias24,
  pdi, mdi, adx, adxr, cmi, chop,
  atr_pct, boll_pb, boll_bw, mfi,
  sar_long, supertrend_long, vol_ratio, obv_up,
  score_total, score_delta, score_label,
  sig_trend_bull, sig_overbought, sig_oversold,
  div_bull, div_bear, div_bear_today,
  td_setup, td_countdown, streak,
  turnover_rate, market_cap, pe,
  ret20, ret60, ret120
) VALUES (
  ?, ?, ?,
  ?, ?, ?, ?, ?, ?,
  ?, ?, ?, ?,
  ?, ?, ?, ?,
  ?, ?, ?, ?, ?, ?,
  ?, ?, ?, ?,
  ?, ?, ?, ?,
  ?, ?, ?,
  ?, ?, ?,
  ?, ?, ?,
  ?, ?, ?,
  ?, ?, ?,
  ?, ?, ?
)
ON CONFLICT(code, trade_date) DO UPDATE SET
  captured_at=excluded.captured_at,
  close=excluded.close, change_pct=excluded.change_pct,
  ma5=excluded.ma5, ma10=excluded.ma10, ma20=excluded.ma20, ma60=excluded.ma60,
  kdj_j=excluded.kdj_j, macd_dif=excluded.macd_dif, macd_dea=excluded.macd_dea, macd_hist=excluded.macd_hist,
  rsi6=excluded.rsi6, wr14=excluded.wr14, bias6=excluded.bias6, bias24=excluded.bias24,
  pdi=excluded.pdi, mdi=excluded.mdi, adx=excluded.adx, adxr=excluded.adxr, cmi=excluded.cmi, chop=excluded.chop,
  atr_pct=excluded.atr_pct, boll_pb=excluded.boll_pb, boll_bw=excluded.boll_bw, mfi=excluded.mfi,
  sar_long=excluded.sar_long, supertrend_long=excluded.supertrend_long, vol_ratio=excluded.vol_ratio, obv_up=excluded.obv_up,
  score_total=excluded.score_total, score_delta=excluded.score_delta, score_label=excluded.score_label,
  sig_trend_bull=excluded.sig_trend_bull, sig_overbought=excluded.sig_overbought, sig_oversold=excluded.sig_oversold,
  div_bull=excluded.div_bull, div_bear=excluded.div_bear, div_bear_today=excluded.div_bear_today,
  td_setup=excluded.td_setup, td_countdown=excluded.td_countdown, streak=excluded.streak,
  turnover_rate=excluded.turnover_rate, market_cap=excluded.market_cap, pe=excluded.pe,
  ret20=excluded.ret20, ret60=excluded.ret60, ret120=excluded.ret120`,
		snap.Code, snap.TradeDate, time.Now().Format(time.RFC3339),
		snap.Close, snap.ChangePct, snap.MA5, snap.MA10, snap.MA20, snap.MA60,
		snap.KDJ_J, snap.MACD_DIF, snap.MACD_DEA, snap.MACD_Hist,
		snap.RSI6, snap.WR14, snap.BIAS6, snap.BIAS24,
		snap.PDI, snap.MDI, snap.ADX, snap.ADXR, snap.CMI, snap.CHOP,
		snap.ATRPct, snap.BollPB, snap.BollBW, snap.MFI,
		boolToInt(snap.SARLong), boolToInt(snap.SuperTrendLong), snap.VolRatio, boolToInt(snap.OBVUp),
		snap.ScoreTotal, snap.ScoreDelta, snap.ScoreLabel,
		boolToInt(snap.SigTrendBull), boolToInt(snap.SigOverbought), boolToInt(snap.SigOversold),
		boolToInt(snap.DivBull), boolToInt(snap.DivBear), boolToInt(snap.DivBearToday),
		snap.TDSetup, snap.TDCountdown, snap.Streak,
		snap.TurnoverRate, snap.MarketCap, snap.PE,
		snap.Ret20, snap.Ret60, snap.Ret120)
	if err != nil {
		return fmt.Errorf("save snapshot %s@%s: %w", snap.Code, snap.TradeDate, err)
	}
	return nil
}

// ListByTag returns instruments carrying tag, ordered by code.
func (s *Store) ListByTag(tag string) ([]Instrument, error) {
	rows, err := s.db.Query(`
SELECT i.code, i.name, i.market, i.note
FROM instrument i
JOIN instrument_tag it ON it.code = i.code
JOIN tag t ON t.id = it.tag_id
WHERE t.name = ?
ORDER BY i.code`, tag)
	if err != nil {
		return nil, fmt.Errorf("list by tag %s: %w", tag, err)
	}
	defer rows.Close()

	var out []Instrument
	for rows.Next() {
		var in Instrument
		if err := rows.Scan(&in.Code, &in.Name, &in.Market, &in.Note); err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

// History returns up to limit most-recent snapshots for code, newest first.
func (s *Store) History(code string, limit int) ([]Snapshot, error) {
	rows, err := s.db.Query(`
SELECT trade_date, close, change_pct, ma5, ma10, ma20, ma60, kdj_j, adx, adxr,
       rsi6, score_total, score_delta, score_label, td_setup, td_countdown, streak,
       COALESCE(turnover_rate,0), COALESCE(market_cap,0), COALESCE(pe,0),
       COALESCE(ret20,0)  AS ret20,
       COALESCE(ret60,0)  AS ret60,
       COALESCE(ret120,0) AS ret120,
       COALESCE(rs20,0), COALESCE(rs60,0), COALESCE(rs120,0)
FROM snapshot WHERE code = ?
ORDER BY trade_date DESC
LIMIT ?`, code, limit)
	if err != nil {
		return nil, fmt.Errorf("history %s: %w", code, err)
	}
	defer rows.Close()

	var out []Snapshot
	for rows.Next() {
		var snap Snapshot
		snap.Code = code
		if err := rows.Scan(
			&snap.TradeDate, &snap.Close, &snap.ChangePct,
			&snap.MA5, &snap.MA10, &snap.MA20, &snap.MA60,
			&snap.KDJ_J, &snap.ADX, &snap.ADXR, &snap.RSI6,
			&snap.ScoreTotal, &snap.ScoreDelta, &snap.ScoreLabel,
			&snap.TDSetup, &snap.TDCountdown, &snap.Streak,
			&snap.TurnoverRate, &snap.MarketCap, &snap.PE,
			&snap.Ret20, &snap.Ret60, &snap.Ret120,
			&snap.RS20, &snap.RS60, &snap.RS120,
		); err != nil {
			return nil, err
		}
		out = append(out, snap)
	}
	return out, rows.Err()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// UpdateRSRankings reads the ret20/ret60/ret120 stored in each code's latest
// snapshot (populated by indicator-analyze -save from K-line history), computes
// cross-sectional percentile ranks (0–100), and writes them back as rs20/rs60/rs120.
// Returns the number of codes successfully updated.
func (s *Store) UpdateRSRankings() (int, error) {
	// Read latest ret20/ret60/ret120 for every code where ret20 has been computed.
	// Rows where ret20 IS NULL are from old snapshots and are excluded from ranking.
	rows, err := s.db.Query(`
SELECT code, trade_date,
       ret20  AS r20,
       COALESCE(ret60,0)  AS r60,
       COALESCE(ret120,0) AS r120
FROM snapshot
WHERE trade_date = (SELECT MAX(trade_date) FROM snapshot s2 WHERE s2.code = snapshot.code)
  AND ret20 IS NOT NULL`)
	if err != nil {
		return 0, fmt.Errorf("read returns: %w", err)
	}
	defer rows.Close()

	// entry keeps only what the write-back needs; the ret values stream straight
	// into per-horizon slices for ranking.
	type entry struct {
		code      string
		tradeDate string
	}
	var (
		entries           []entry
		r20s, r60s, r120s []float64
	)
	for rows.Next() {
		var (
			e              entry
			r20, r60, r120 float64
		)
		if err := rows.Scan(&e.code, &e.tradeDate, &r20, &r60, &r120); err != nil {
			return 0, err
		}
		entries = append(entries, e)
		r20s = append(r20s, r20)
		r60s = append(r60s, r60)
		r120s = append(r120s, r120)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	rank20 := percentileRanks(r20s)
	rank60 := percentileRanks(r60s)
	rank120 := percentileRanks(r120s)

	// Reuse the trade_date selected above instead of recomputing MAX(trade_date)
	// per row: the SELECT already pinned each code to its latest snapshot.
	updated := 0
	for i, e := range entries {
		_, err := s.db.Exec(
			`UPDATE snapshot SET rs20=?, rs60=?, rs120=? WHERE code=? AND trade_date=?`,
			rank20[i], rank60[i], rank120[i], e.code, e.tradeDate)
		if err == nil {
			updated++
		}
	}
	return updated, nil
}

// Decision is one recommendation or hold record written by screen-stocks.
type Decision struct {
	ID          int
	Code        string
	LogDate     string
	Action      string // "recommend" or "hold"
	Tier        string // "⭐⭐⭐", "⭐⭐", "📌持仓"
	ScoreTotal  int
	ADX         float64
	SARLong     bool
	STLong      bool
	OBVUp       bool
	MACDHist    float64
	TDCountdown string
	Signals     string
	OutcomePct  *float64 // nil when not yet backfilled
	OutcomeDate *string
	Correct     *int
}

// SaveDecision inserts a decision log entry, ignoring duplicates on (code, log_date, action).
func (s *Store) SaveDecision(d Decision) error {
	_, err := s.db.Exec(`
INSERT OR IGNORE INTO decision_log
  (code, log_date, action, tier, score_total, adx, sar_long, st_long, obv_up,
   macd_hist, td_countdown, signals, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.Code, d.LogDate, d.Action, d.Tier, d.ScoreTotal, d.ADX,
		boolToInt(d.SARLong), boolToInt(d.STLong), boolToInt(d.OBVUp),
		d.MACDHist, d.TDCountdown, d.Signals, time.Now().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("save decision %s@%s: %w", d.Code, d.LogDate, err)
	}
	return nil
}

// PendingDecisions returns decisions older than 10 days that have no outcome yet.
func (s *Store) PendingDecisions() ([]Decision, error) {
	rows, err := s.db.Query(`
SELECT id, code, log_date, action, tier, score_total, adx,
       sar_long, st_long, obv_up, macd_hist, td_countdown, signals
FROM decision_log
WHERE outcome_pct IS NULL
  AND log_date <= date('now', '-10 days')
ORDER BY log_date, code`)
	if err != nil {
		return nil, fmt.Errorf("pending decisions: %w", err)
	}
	defer rows.Close()

	var out []Decision
	for rows.Next() {
		var d Decision
		if err := rows.Scan(&d.ID, &d.Code, &d.LogDate, &d.Action, &d.Tier,
			&d.ScoreTotal, &d.ADX, &d.SARLong, &d.STLong, &d.OBVUp,
			&d.MACDHist, &d.TDCountdown, &d.Signals); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// BackfillDecision writes the outcome for a previously recorded decision.
func (s *Store) BackfillDecision(id int, outcomePct float64, outcomeDate string, correct bool) error {
	c := 0
	if correct {
		c = 1
	}
	_, err := s.db.Exec(
		`UPDATE decision_log SET outcome_pct=?, outcome_date=?, correct=? WHERE id=?`,
		outcomePct, outcomeDate, c, id)
	if err != nil {
		return fmt.Errorf("backfill decision %d: %w", id, err)
	}
	return nil
}

// DecisionStats aggregates win rate and average return by tier.
type DecisionStats struct {
	Tier      string
	Count     int
	Wins      int
	AvgReturn float64
	WinRate   float64
}

// CloseOnDate returns the close price for code on the exact tradeDate.
// Returns 0 if no snapshot exists for that date.
func (s *Store) CloseOnDate(code, tradeDate string) (float64, error) {
	var close float64
	err := s.db.QueryRow(
		`SELECT close FROM snapshot WHERE code = ? AND trade_date = ?`,
		code, tradeDate).Scan(&close)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("close on %s for %s: %w", tradeDate, code, err)
	}
	return close, nil
}

// CloseAfter returns the close price and trade_date on the Nth global trading day
// after startDate. Returns 0 and "" when the global date is missing, or when the
// code has no snapshot on that exact target date.
func (s *Store) CloseAfter(code, startDate string, n int) (float64, string, error) {
	var close float64
	var tradeDate string
	err := s.db.QueryRow(`
WITH target AS (
  SELECT trade_date
  FROM (
    SELECT DISTINCT trade_date
    FROM snapshot
    WHERE trade_date > ?
    ORDER BY trade_date ASC
    LIMIT 1 OFFSET ?
  )
)
SELECT close, trade_date FROM snapshot
WHERE code = ? AND trade_date = (SELECT trade_date FROM target)`, startDate, n-1, code).Scan(&close, &tradeDate)
	if err == sql.ErrNoRows {
		return 0, "", nil
	}
	if err != nil {
		return 0, "", fmt.Errorf("close after %s+%d for %s: %w", startDate, n, code, err)
	}
	return close, tradeDate, nil
}

// StatsByTier returns aggregated decision statistics grouped by tier.
func (s *Store) StatsByTier() ([]DecisionStats, error) {
	rows, err := s.db.Query(`
SELECT tier, COUNT(*), SUM(correct), AVG(outcome_pct)
FROM decision_log
WHERE outcome_pct IS NOT NULL
GROUP BY tier
ORDER BY tier`)
	if err != nil {
		return nil, fmt.Errorf("decision stats: %w", err)
	}
	defer rows.Close()

	var out []DecisionStats
	for rows.Next() {
		var st DecisionStats
		var sumCorrect int
		if err := rows.Scan(&st.Tier, &st.Count, &sumCorrect, &st.AvgReturn); err != nil {
			return nil, err
		}
		st.Wins = sumCorrect
		if st.Count > 0 {
			st.WinRate = float64(st.Wins) / float64(st.Count) * 100
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// percentileRanks assigns each value its 0–100 cross-sectional percentile rank
// (lowest → 0, highest → (n-1)/n*100). An empty input yields an empty result.
func percentileRanks(values []float64) []float64 {
	ranks := make([]float64, len(values))
	if len(values) == 0 {
		return ranks
	}
	order := make([]int, len(values))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool { return values[order[a]] < values[order[b]] })
	total := float64(len(values))
	for rank, idx := range order {
		ranks[idx] = float64(rank) / total * 100
	}
	return ranks
}
