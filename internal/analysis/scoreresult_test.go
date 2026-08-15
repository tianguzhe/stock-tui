package analysis

import (
	"testing"

	"stock-tui/internal/indicator"
)

// syntheticTrend builds a candle series that drifts steadily (up when
// dailyMove>1, down when dailyMove<1) so indicator.Calculate produces a
// well-formed, non-degenerate Result series for ScoreResult to score.
func syntheticTrend(n int, dailyMove float64) []indicator.Candle {
	candles := make([]indicator.Candle, n)
	price := 10.0
	for i := 0; i < n; i++ {
		price *= dailyMove
		candles[i] = indicator.Candle{
			Open: price / dailyMove, High: price * 1.01, Low: price * 0.99,
			Close: price, Volume: 1_000_000 + float64(i%9)*10_000,
		}
	}
	return candles
}

// TestScoreResultDeltaTotalLabelWiring locks the assembly-layer invariants
// that ApplyPerfAdaptive and every caller rely on: Total is exactly the sum
// of the published sub-scores clamped into [0,100], and Label always tracks
// Total through the already-tested ScoreLabel. This is the wiring a future
// edit (new sub-score, forgotten in the Delta sum) would silently break.
func TestScoreResultDeltaTotalLabelWiring(t *testing.T) {
	for _, tc := range []struct {
		name      string
		dailyMove float64
	}{
		{"平稳上行", 1.006},
		{"平稳下行", 0.994},
		{"横盘震荡", 1.0005},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candles := syntheticTrend(200, tc.dailyMove)
			results := indicator.Calculate(candles)
			obv := OBVSeries(candles)
			_, upAvgVol, _, downAvgVol := RecentVolumeHealth(candles, 5)
			volRatio := VolRatio(candles, len(candles)-1)

			score := ScoreResult(candles, results, obv, upAvgVol, downAvgVol, volRatio)

			wantDelta := score.DMI + score.MA + score.MACD + score.KdjWr + score.RSI +
				score.BIAS + score.CHOPCMI + score.Volume + score.Divergence
			if score.Delta != wantDelta {
				t.Fatalf("Delta = %d, want sum of sub-scores %d (DMI=%d MA=%d MACD=%d KdjWr=%d RSI=%d BIAS=%d CHOPCMI=%d Volume=%d Divergence=%d)",
					score.Delta, wantDelta, score.DMI, score.MA, score.MACD, score.KdjWr, score.RSI, score.BIAS, score.CHOPCMI, score.Volume, score.Divergence)
			}
			wantTotal := ClampInt(50+score.Delta, 0, 100)
			if score.Total != wantTotal {
				t.Fatalf("Total = %d, want clamp(50+Delta)=%d", score.Total, wantTotal)
			}
			if score.Label != ScoreLabel(score.Total) {
				t.Fatalf("Label = %q, want ScoreLabel(Total)=%q", score.Label, ScoreLabel(score.Total))
			}
			if score.Volume < -5 || score.Volume > 5 {
				t.Fatalf("Volume = %d, want clamped to [-5,5]", score.Volume)
			}
		})
	}
}

// TestScoreResultDMIBranches locks the DMI sub-score's directional branches
// in isolation (hand-crafted results, no need for a realistic full series).
func TestScoreResultDMIBranches(t *testing.T) {
	candles := syntheticTrend(70, 1.001)
	base := indicator.Calculate(candles)
	obv := OBVSeries(candles)

	set := func(pdi, mdi, adx float64) []indicator.Result {
		out := make([]indicator.Result, len(base))
		copy(out, base)
		last := out[len(out)-1]
		last.DMI.PDI, last.DMI.MDI, last.DMI.ADX = pdi, mdi, adx
		out[len(out)-1] = last
		return out
	}

	cases := []struct {
		name          string
		pdi, mdi, adx float64
		wantDMI       int
	}{
		{"强多头_diff>15_ADX>25", 40, 20, 30, 12},
		{"中多头_diff>8_ADX>20", 30, 20, 22, 8},
		{"弱多头_diff>0", 21, 20, 15, 3},
		{"强空头_diff<-15_ADX>25", 20, 40, 30, -12},
		{"中空头_diff<-8_ADX>20", 20, 30, 22, -8},
		{"弱空头_diff<0", 20, 21, 15, -3},
		{"持平_diff=0", 20, 20, 30, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			results := set(tc.pdi, tc.mdi, tc.adx)
			score := ScoreResult(candles, results, obv, 1, 1, 1.0)
			if score.DMI != tc.wantDMI {
				t.Errorf("DMI = %d, want %d (pdi=%.0f mdi=%.0f adx=%.0f)", score.DMI, tc.wantDMI, tc.pdi, tc.mdi, tc.adx)
			}
		})
	}
}

// --- LateStagePenalty ---

// withCloseStreak returns a candle series whose final `days` bars move
// monotonically (up when up=true), preceded by a flat pair that terminates
// StreakValue's backward walk cleanly at exactly `days`.
func withCloseStreak(days int, up bool) []indicator.Candle {
	price := 10.0
	candles := []indicator.Candle{{Close: price}, {Close: price}}
	for i := 0; i < days; i++ {
		if up {
			price *= 1.02
		} else {
			price *= 0.98
		}
		candles = append(candles, indicator.Candle{Close: price})
	}
	return candles
}

func resultsWithLast(n int, atrPct, bias24 float64) []indicator.Result {
	results := make([]indicator.Result, n)
	last := results[n-1]
	last.ATR.Pct = atrPct
	last.BIAS.BIAS24 = bias24
	results[n-1] = last
	return results
}

func TestLateStagePenalty(t *testing.T) {
	cases := []struct {
		name        string
		days        int
		up          bool
		atrPct      float64
		bias24      float64
		wantPenalty int
	}{
		{"无触发", 2, true, 10, 20, 0},           // biasAtr=2, streak=2
		{"乖离超4档_减2", 2, true, 10, 45, -2},     // biasAtr=4.5
		{"乖离超6档_再减1_共3", 2, true, 10, 65, -3}, // biasAtr=6.5
		{"连涨5档_减2", 5, true, 10, 20, -2},
		{"连涨7档_再减1_共3", 7, true, 10, 20, -3},
		{"连跌不触发streak惩罚", 7, false, 10, 20, 0}, // StreakValue 为负, streak>=5 条件不成立
		{"双触发叠加后夹到-5", 7, true, 10, 65, -5},    // 单独 -3 + -3 = -6, clamp 到 -5
		{"ATR为0不除零_不触发乖离惩罚", 2, true, 0, 999, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			candles := withCloseStreak(tc.days, tc.up)
			results := resultsWithLast(len(candles), tc.atrPct, tc.bias24)

			penalty, streak, biasAtr := LateStagePenalty(candles, results)
			if penalty != tc.wantPenalty {
				t.Errorf("penalty = %d, want %d (streak=%d biasAtr=%.2f)", penalty, tc.wantPenalty, streak, biasAtr)
			}
			wantStreak := tc.days
			if !tc.up {
				wantStreak = -tc.days
			}
			if streak != wantStreak {
				t.Errorf("streak = %d, want %d", streak, wantStreak)
			}
			if penalty < -5 {
				t.Errorf("penalty = %d, must stay >= -5 (documented floor)", penalty)
			}
		})
	}
}
