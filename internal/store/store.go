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
	"time"

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
}

// ScreenRow is a Screen result: the matched instrument joined with its latest snapshot.
type ScreenRow struct {
	Instrument
	Snapshot
}

// Filter selects instruments by their latest snapshot. Zero-value fields are
// ignored (no constraint); UseMaxJ guards MaxJ because 0 is a meaningful bound.
type Filter struct {
	Tag      string
	MinADX   float64
	MinScore int
	MaxJ     float64
	UseMaxJ  bool
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
  PRIMARY KEY (code, trade_date)
);`
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("migrate schema: %w", err)
	}
	return nil
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
ON CONFLICT(code) DO NOTHING`, code, marketOf(code), time.Now().Format(time.RFC3339)); err != nil {
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
  td_setup, td_countdown, streak
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
  td_setup=excluded.td_setup, td_countdown=excluded.td_countdown, streak=excluded.streak`,
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
		snap.TDSetup, snap.TDCountdown, snap.Streak)
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
       rsi6, score_total, score_delta, score_label, td_setup, td_countdown, streak
FROM snapshot WHERE code = ?
ORDER BY trade_date DESC
LIMIT ?`, code, limit)
	if err != nil {
		return nil, fmt.Errorf("history %s: %w", code, err)
	}
	defer rows.Close()

	var out []Snapshot
	for rows.Next() {
		var s Snapshot
		s.Code = code
		if err := rows.Scan(&s.TradeDate, &s.Close, &s.ChangePct, &s.MA5, &s.MA10, &s.MA20, &s.MA60,
			&s.KDJ_J, &s.ADX, &s.ADXR, &s.RSI6, &s.ScoreTotal, &s.ScoreDelta, &s.ScoreLabel,
			&s.TDSetup, &s.TDCountdown, &s.Streak); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Screen returns instruments whose latest snapshot satisfies f. The latest
// snapshot per code is taken by max(trade_date).
func (s *Store) Screen(f Filter) ([]ScreenRow, error) {
	query := `
SELECT i.code, i.name, i.market, i.note,
       s.trade_date, s.close, s.change_pct, s.ma5, s.ma10, s.ma20, s.ma60,
       s.kdj_j, s.adx, s.adxr, s.score_total, s.score_delta, s.score_label,
       s.td_setup, s.td_countdown, s.streak
FROM instrument i
JOIN snapshot s ON s.code = i.code
WHERE s.trade_date = (SELECT MAX(trade_date) FROM snapshot s2 WHERE s2.code = i.code)`
	var args []any
	if f.Tag != "" {
		query += `
  AND i.code IN (SELECT it.code FROM instrument_tag it JOIN tag t ON t.id = it.tag_id WHERE t.name = ?)`
		args = append(args, f.Tag)
	}
	if f.MinADX > 0 {
		query += ` AND s.adx >= ?`
		args = append(args, f.MinADX)
	}
	if f.UseMaxJ {
		query += ` AND s.kdj_j <= ?`
		args = append(args, f.MaxJ)
	}
	if f.MinScore > 0 {
		query += ` AND s.score_total >= ?`
		args = append(args, f.MinScore)
	}
	query += ` ORDER BY s.score_total DESC, i.code`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("screen: %w", err)
	}
	defer rows.Close()

	var out []ScreenRow
	for rows.Next() {
		var r ScreenRow
		r.Snapshot.Code = ""
		if err := rows.Scan(&r.Instrument.Code, &r.Name, &r.Market, &r.Note,
			&r.TradeDate, &r.Close, &r.ChangePct, &r.MA5, &r.MA10, &r.MA20, &r.MA60,
			&r.KDJ_J, &r.ADX, &r.ADXR, &r.ScoreTotal, &r.ScoreDelta, &r.ScoreLabel,
			&r.TDSetup, &r.TDCountdown, &r.Streak); err != nil {
			return nil, err
		}
		r.Snapshot.Code = r.Instrument.Code
		out = append(out, r)
	}
	return out, rows.Err()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// marketOf extracts the sh/sz/bj/hk prefix from a normalized code; empty if absent.
func marketOf(code string) string {
	if len(code) >= 2 {
		return code[:2]
	}
	return ""
}
