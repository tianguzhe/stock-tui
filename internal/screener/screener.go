// Package screener implements multi-factor stock screening logic.
package screener

import (
	"database/sql"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

const (
	MinRSCoverage = 90.0 // RS20 覆盖率阈值（%）
)

// Compiled once; used by CdwnTopN, tdSafe, tdTopCount.
var (
	reCdwnTopN = regexp.MustCompile(`顶/(\d+)`)
	reTopSetup = regexp.MustCompile(`见顶/(\d+)`)
)

// Tier represents stock rating levels.
type Tier string

const (
	TierStar3 Tier = "⭐⭐⭐"
	TierStar2 Tier = "⭐⭐"
	TierWatch Tier = "👁️观察"
	TierNone  Tier = ""
)

// Holding represents a user's stock position.
type Holding struct {
	Code   string
	Cost   float64
	Shares int
}

// Candidate represents a screened stock candidate.
type Candidate struct {
	Code              string
	Name              string
	HotScore          int
	ScoreTotal        int
	ADX               float64
	ChangePct         float64
	Close             float64
	RS20, RS60, RS120 sql.NullFloat64
	Bias24            float64
	ATRPct            float64
	Streak            int
	MA20              float64
	Low20             float64 // 近 20 日最低价,作 SAR 失效(空头)时的止损回退
	SARLong           bool
	SuperTrendLong    bool
	OBVUp             bool
	OBVUp3Day         bool
	MACDHist          float64
	DivBear           bool
	SigOverbought     bool
	TDSetup           string
	TDCountdown       string
	VolRatio          float64
	TurnoverRate      float64
	MarketCap         float64
	PE                float64

	// PERF historical win rates
	PerfTrendFollowBullWin10 sql.NullFloat64
	PerfOverboughtBearWin10  sql.NullFloat64
	PerfDivBearWin10         sql.NullFloat64
	PerfTrendFollowBullN     sql.NullInt64
	PerfOverboughtBearN      sql.NullInt64
	PerfDivBearN             sql.NullInt64
	PerfTrendFollowBullAvg10 sql.NullFloat64

	// New indicators
	KeltnerSqueeze   bool
	DonchBreak20Bull bool
	DonchBreak55Bull bool
	SARValue         sql.NullFloat64
	SuperTrendValue  sql.NullFloat64

	// Computed fields
	Tier    Tier
	SortKey float64
}

// WilsonBounds computes Wilson 95% confidence interval for win rate.
// Returns (lower_bound%, upper_bound%).
//
// Small sample water: N=10, win=40% → lower≈17%, upper≈69% — statistically meaningless.
// Rule: exclusion requires lower>50 (signal significantly better than coin flip);
// "historically bad" requires upper<50 (significantly worse than coin flip).
func WilsonBounds(winPct float64, n int) (lower, upper float64) {
	const z = 1.96 // 95% confidence
	if n == 0 {
		return 0.0, 100.0
	}
	// Clamp to [0,1] to avoid NaN from sqrt of negative value.
	p := math.Max(0, math.Min(1, winPct/100.0))
	denom := 1 + z*z/float64(n)
	centre := p + z*z/(2*float64(n))
	margin := z * math.Sqrt(p*(1-p)/float64(n)+z*z/(4*float64(n)*float64(n)))
	lower = (centre - margin) / denom * 100
	upper = (centre + margin) / denom * 100
	return
}

// MarketBreadth computes % of stocks above MA20.
// Momentum crash protection (Daniel & Moskowitz 2016): when breadth collapses,
// chasing momentum stocks concentrates drawdown risk — capping recommendation count
// is more effective than any individual stock filter.
func MarketBreadth(candidates []Candidate) float64 {
	var total, above int
	for _, c := range candidates {
		if c.Close > 0 && c.MA20 > 0 {
			total++
			if c.Close > c.MA20 {
				above++
			}
		}
	}
	if total == 0 {
		return 100.0
	}
	return float64(above) / float64(total) * 100
}

// fundOK checks fundamental hard thresholds:
// - Valid market cap & turnover rate
// - Market cap ≥ 20B CNY
// - Turnover rate 0.3% – 20%
func fundOK(c *Candidate) bool {
	if c.MarketCap <= 0 || c.TurnoverRate <= 0 {
		return false
	}
	if c.MarketCap < 20 {
		return false
	}
	if c.TurnoverRate < 0.3 || c.TurnoverRate > 20 {
		return false
	}
	return true
}

// perfOK checks PERF historical win rate filters (Wilson 95% CI):
// - Trend follow "historically bad": Wilson upper < 50% → exclude
// - Trend follow avg ≤ 0 with N≥10 → exclude (not profitable on average)
// - Overbought signal "historically effective": triggered + Wilson lower > 50% → exclude (wait for pullback)
func perfOK(c *Candidate) bool {
	// Trend follow bad
	if c.PerfTrendFollowBullWin10.Valid && c.PerfTrendFollowBullN.Valid {
		_, hi := WilsonBounds(c.PerfTrendFollowBullWin10.Float64, int(c.PerfTrendFollowBullN.Int64))
		if hi < 50 {
			return false
		}
	}
	// Trend follow not profitable
	if c.PerfTrendFollowBullAvg10.Valid && c.PerfTrendFollowBullN.Valid &&
		c.PerfTrendFollowBullN.Int64 >= 10 && c.PerfTrendFollowBullAvg10.Float64 <= 0 {
		return false
	}
	// Overbought historically effective
	if c.SigOverbought && c.PerfOverboughtBearWin10.Valid && c.PerfOverboughtBearN.Valid {
		lo, _ := WilsonBounds(c.PerfOverboughtBearWin10.Float64, int(c.PerfOverboughtBearN.Int64))
		if lo > 50 {
			return false
		}
	}
	return true
}

// CdwnTopN extracts countdown top sequence count from "见顶/N" format.
func CdwnTopN(tdCountdown string) int {
	matches := reCdwnTopN.FindStringSubmatch(tdCountdown)
	if len(matches) > 1 {
		n, _ := strconv.Atoi(matches[1])
		return n
	}
	return 0
}

// tdSafe checks TD safety: setup 见顶/8-9 or countdown 见顶/7-13 are high-risk zones.
func tdSafe(c *Candidate) bool {
	if strings.Contains(c.TDSetup, "见顶") {
		matches := reTopSetup.FindStringSubmatch(c.TDSetup)
		if len(matches) > 1 {
			n, _ := strconv.Atoi(matches[1])
			if n >= 8 {
				return false
			}
		}
	}
	n := CdwnTopN(c.TDCountdown)
	if n > 0 {
		return n <= 6
	}
	return true
}

// tdTopCount returns the highest TD top count (setup or countdown).
func tdTopCount(c *Candidate) int {
	if matches := reTopSetup.FindStringSubmatch(c.TDSetup); len(matches) > 1 {
		n, _ := strconv.Atoi(matches[1])
		return n
	}
	return CdwnTopN(c.TDCountdown)
}

// divBearState returns divergence bearish state: "ok" / "watch" / "exclude".
//
// - Historically effective (Wilson lower > 50%) → exclude
// - No sample = uncertain → watch (old version excluded; now symmetric with perfOK)
// - Not significant + strong trend (ADX≥38 + SAR/ST both long) → ok; else → watch
func divBearState(c *Candidate) string {
	if !c.DivBear {
		return "ok"
	}
	if !c.PerfDivBearWin10.Valid || !c.PerfDivBearN.Valid || c.PerfDivBearN.Int64 == 0 {
		return "watch"
	}
	lo, _ := WilsonBounds(c.PerfDivBearWin10.Float64, int(c.PerfDivBearN.Int64))
	if lo > 50 {
		return "exclude"
	}
	strongTrend := c.ADX >= 38 && c.SARLong && c.SuperTrendLong
	if strongTrend {
		return "ok"
	}
	return "watch"
}

// lateStageRisk checks late-stage chase risk (demotes to watch, not exclude):
//
// - Bias normalized by volatility: bias24/atr_pct > 4 (deviates >4 daily ATRs from MA24)
// - Consecutive up days ≥ 5 (A-share short-term reversal effect)
// - Turnover 15-20% (forms gradient with >20% exclusion in fundOK)
// - Large gain + volume spike, or gain + divergence
func lateStageRisk(c *Candidate) bool {
	chg := c.ChangePct
	vr := c.VolRatio
	tdTop := tdTopCount(c)
	divBear := c.DivBear
	bias := c.Bias24
	atr := c.ATRPct
	stretched := false
	if atr > 0 {
		stretched = bias/atr > 4
	} else {
		stretched = bias > 25
	}
	streak := c.Streak
	tr := c.TurnoverRate

	return (chg >= 5 && vr >= 1.5) ||
		(chg >= 3 && divBear) ||
		(tdTop >= 5 && divBear) ||
		stretched ||
		streak >= 5 ||
		tr >= 15
}

// ComputeTier assigns rating tier to a candidate.
func ComputeTier(c *Candidate) Tier {
	if !fundOK(c) {
		return TierNone
	}
	if !perfOK(c) {
		return TierNone
	}

	// Momentum gates
	if !c.RS20.Valid || c.RS20.Float64 < 60 {
		return TierNone
	}
	// Medium-term momentum insurance: strong 20-day but weak 60-day = pure reversal candidate
	if c.RS60.Valid && c.RS60.Float64 < 45 {
		return TierNone
	}

	chg := c.ChangePct
	vr := c.VolRatio

	// ±9.5 gate implies 10% limit assumption for main board
	if chg <= -9.5 {
		return TierNone
	}
	if chg <= -5.0 && vr > 1.5 {
		return TierNone
	}
	if chg >= 9.5 {
		return TierNone
	}

	divState := divBearState(c)
	if divState == "exclude" {
		return TierNone
	}

	// Core technical requirements
	coreTech := c.SARLong && c.SuperTrendLong && c.OBVUp && c.MACDHist > 0 && tdSafe(c)
	if !coreTech {
		return TierNone
	}

	// OBV 3-day sustained inflow required for star tiers; 1-day-only demotes to watch.
	// Backtest 2026-06: 1-day-only group 70.2% win / +8.87% vs 3-day group 82.6% / +21.02%
	// — keep them visible as watch, don't exclude.
	obv3Day := c.OBVUp3Day

	// Red/flat days
	if chg >= 0 {
		if c.ScoreTotal >= 70 && c.ADX >= 38 {
			if divState == "watch" || lateStageRisk(c) || !obv3Day {
				return TierWatch
			}
			return TierStar3
		}
		if c.ScoreTotal >= 65 && c.ADX >= 35 {
			if divState == "watch" || lateStageRisk(c) || !obv3Day {
				return TierWatch
			}
			return TierStar2
		}
	}

	// Pullback with strong fundamentals
	if divState == "ok" {
		rs20 := 0.0
		if c.RS20.Valid {
			rs20 = c.RS20.Float64
		}
		isStrong := rs20 >= 80 && c.ScoreTotal >= 65
		minChg := -5.0
		if !isStrong {
			minChg = -2.0
		}
		maxVR := 1.5
		if !isStrong {
			maxVR = 1.0
		}
		if chg >= minChg && vr < maxVR && c.ScoreTotal >= 65 && c.ADX >= 35 {
			return TierWatch
		}
	}

	return TierNone
}

// SortKey computes sorting key: tier → RS composite → score → ADX.
// RS composite: 0.3*rs20 + 0.5*rs60 + 0.2*rs120
func SortKey(c *Candidate) float64 {
	tierWeight := map[Tier]float64{
		TierStar3: 1000.0,
		TierStar2: 900.0,
		TierWatch: 800.0,
	}
	base := tierWeight[c.Tier]

	rs20 := 0.0
	if c.RS20.Valid {
		rs20 = c.RS20.Float64
	}
	rs60 := 0.0
	if c.RS60.Valid {
		rs60 = c.RS60.Float64
	}
	rs120 := 0.0
	if c.RS120.Valid {
		rs120 = c.RS120.Float64
	}
	rsComposite := 0.3*rs20 + 0.5*rs60 + 0.2*rs120

	return base + rsComposite + float64(c.ScoreTotal)*0.1 + c.ADX*0.01
}

// StopText formats stop-loss price with distance%.
// StopText formats the stop-loss line and its distance from current price.
//
// SAR is a trailing stop only while long (SAR below price). Once SAR flips
// bearish it sits ABOVE price — that line is a short-cover / 反手 line, not a
// sell stop, so showing it as "+28%" for a held long is misleading. When SAR is
// above close (bearish) we fall back to the 20-day low (跌破前低就跑, price-
// action stop independent of any lagging indicator). No Low20 → no honest stop
// to display, return "—" rather than fabricate one.
func StopText(c *Candidate) string {
	if c.Close == 0 {
		return "—"
	}
	stop, hasStop := effectiveStop(c)
	if !hasStop {
		return "—"
	}
	dist := (stop/c.Close - 1) * 100
	return fmt.Sprintf("%.2f(%+.1f%%)", stop, dist)
}

// effectiveStop returns the stop price to surface for a held long: SAR while
// it sits below price (bullish trailing stop), otherwise the 20-day low.
// ok is false when neither yields an in-below-price stop.
func effectiveStop(c *Candidate) (stop float64, ok bool) {
	if c.SARValue.Valid && c.SARValue.Float64 < c.Close {
		return c.SARValue.Float64, true
	}
	if c.Low20 > 0 && c.Low20 < c.Close {
		return c.Low20, true
	}
	return 0, false
}

// PositionHint computes suggested position size based on 1% risk per trade.
//
// risk per share = Close - effectiveStop. When no in-below-price stop exists
// (SAR bearish AND no 20-day low below close), we cannot bound the risk, so
// advise waiting instead of fabricating a position size.
func PositionHint(c *Candidate, capital float64) string {
	if capital == 0 || c.Close == 0 {
		return ""
	}
	stop, ok := effectiveStop(c)
	if !ok {
		return "止损距离过宽，建议观望"
	}
	riskPerShare := c.Close - stop
	if riskPerShare <= 0 {
		return "止损距离过宽，建议观望"
	}
	shares := int(capital*0.01/riskPerShare/100) * 100
	if shares <= 0 {
		return "止损距离过宽，建议观望"
	}
	return fmt.Sprintf("建议≤%d股", shares)
}

// LoadSnapshots loads latest snapshot data from database.
func LoadSnapshots(dbPath string) (date string, candidates []Candidate, rsCoverage float64, err error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return "", nil, 0, err
	}
	defer db.Close()

	// Get latest date
	err = db.QueryRow("SELECT MAX(trade_date) FROM snapshot").Scan(&date)
	if err != nil {
		return "", nil, 0, fmt.Errorf("query max trade_date: %w", err)
	}

	// Check RS coverage
	var total, covered sql.NullInt64
	err = db.QueryRow(`
		SELECT
			COUNT(*) as total,
			SUM(CASE WHEN rs20 IS NOT NULL THEN 1 ELSE 0 END) as covered
		FROM snapshot WHERE trade_date = ?
	`, date).Scan(&total, &covered)
	if err != nil {
		return "", nil, 0, fmt.Errorf("query RS coverage: %w", err)
	}
	if total.Valid && total.Int64 > 0 && covered.Valid {
		rsCoverage = float64(covered.Int64) / float64(total.Int64) * 100
	}

	// Load candidates
	rows, err := db.Query(`
		SELECT i.code, i.name, COALESCE(i.hot_score, 0),
		       COALESCE(s.score_adj, s.score_total) AS score_total,
		       s.adx, s.change_pct, s.close,
		       s.sar_long, s.supertrend_long, s.obv_up,
		       (SELECT COALESCE(SUM(obv_up), 0) FROM snapshot s2
		        WHERE s2.code = s.code
		          AND s2.trade_date IN (
		            SELECT s3.trade_date FROM snapshot s3
		            WHERE s3.code = s.code
		              AND s3.trade_date <= s.trade_date
		            ORDER BY s3.trade_date DESC LIMIT 3
		          )) AS obv_3day_sum,
		       s.macd_hist, s.vol_ratio,
		       s.td_setup, s.td_countdown,
		       s.div_bear, s.sig_overbought,
		       COALESCE(s.turnover_rate, 0) AS turnover_rate,
		       COALESCE(s.market_cap, 0) AS market_cap,
		       COALESCE(s.pe, 0) AS pe,
		       s.rs20, s.rs60, s.rs120,
		       s.bias24, s.atr_pct, s.streak, s.ma20,
		       (SELECT COALESCE(MIN(s3.low), 0) FROM snapshot s3
		        WHERE s3.code = s.code
		          AND s3.trade_date IN (
		            SELECT trade_date FROM snapshot
		            WHERE trade_date <= s.trade_date
		            GROUP BY trade_date
		            ORDER BY trade_date DESC LIMIT 20
		          )) AS low20,
		       s.perf_trend_follow_bull_win10,
		       s.perf_overbought_bear_win10,
		       s.perf_div_bear_win10,
		       s.perf_trend_follow_bull_n,
		       s.perf_overbought_bear_n,
		       s.perf_div_bear_n,
		       s.perf_trend_follow_bull_avg10,
		       COALESCE(s.keltner_squeeze, 0),
		       COALESCE(s.donch_break20_bull, 0),
		       COALESCE(s.donch_break55_bull, 0),
		       s.sar_value, s.supertrend_value
		FROM snapshot s
		JOIN instrument i ON s.code = i.code
		WHERE s.trade_date = ?
	`, date)
	if err != nil {
		return "", nil, 0, fmt.Errorf("query snapshots: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var c Candidate
		var sarLongInt, stLongInt, obvUpInt, obv3daySumInt, divBearInt, sigOBInt, keltSqInt, d20Int, d55Int int
		err = rows.Scan(
			&c.Code, &c.Name, &c.HotScore,
			&c.ScoreTotal, &c.ADX, &c.ChangePct, &c.Close,
			&sarLongInt, &stLongInt, &obvUpInt, &obv3daySumInt,
			&c.MACDHist, &c.VolRatio,
			&c.TDSetup, &c.TDCountdown,
			&divBearInt, &sigOBInt,
			&c.TurnoverRate, &c.MarketCap, &c.PE,
			&c.RS20, &c.RS60, &c.RS120,
			&c.Bias24, &c.ATRPct, &c.Streak, &c.MA20, &c.Low20,
			&c.PerfTrendFollowBullWin10,
			&c.PerfOverboughtBearWin10,
			&c.PerfDivBearWin10,
			&c.PerfTrendFollowBullN,
			&c.PerfOverboughtBearN,
			&c.PerfDivBearN,
			&c.PerfTrendFollowBullAvg10,
			&keltSqInt, &d20Int, &d55Int,
			&c.SARValue, &c.SuperTrendValue,
		)
		if err != nil {
			return "", nil, 0, fmt.Errorf("scan candidate: %w", err)
		}

		c.SARLong = sarLongInt == 1
		c.SuperTrendLong = stLongInt == 1
		c.OBVUp = obvUpInt == 1
		c.OBVUp3Day = obv3daySumInt >= 3
		c.DivBear = divBearInt == 1
		c.SigOverbought = sigOBInt == 1
		c.KeltnerSqueeze = keltSqInt == 1
		c.DonchBreak20Bull = d20Int == 1
		c.DonchBreak55Bull = d55Int == 1

		candidates = append(candidates, c)
	}

	return date, candidates, rsCoverage, rows.Err()
}
