package indicator

import "math"

// TDSignal labels the direction of a TD Sequential setup or countdown.
//
// TD Sequential reads exhaustion from the price action itself: a long run of
// declining closes (TDBuy) signals a potential bottom, while a long run of
// advancing closes (TDSell) signals a potential top. The names follow Tom
// DeMark's "buy/sell setup" convention — a buy setup is built from falling
// closes (you would buy the exhaustion), a sell setup from rising closes.
type TDSignal int

const (
	TDNeutral TDSignal = iota
	TDBuy              // exhausted decline, potential bottom
	TDSell             // exhausted advance, potential top
)

// TD holds the TD Sequential state for a single candle. SetupCount reaching 9
// marks a completed setup; CountdownCount reaching 13 marks a completed
// countdown (the strongest reversal cue). Both counts stay populated while
// their phase is active so callers can render the running progress; only bars
// that satisfy the condition increment.
type TD struct {
	SetupCount      int      // 0..9, current setup progress
	SetupSignal     TDSignal // direction of the active setup
	SetupPerfected  bool     // meaningful only when SetupCount == 9
	CountdownCount  int      // 0..13, current countdown progress
	CountdownSignal TDSignal // direction of the active countdown
}

// TDSequential computes the TD Sequential setup/countdown for each candle.
//
// Implemented variant (agreed scope):
//   - Setup requires a TD price flip to start, then counts 9 bars where each
//     close compares against the close 4 bars earlier; an interrupted run
//     resets and the same bar may flip into the opposite direction.
//   - Setup perfection is evaluated at bar 9 (lows for buy, highs for sell).
//   - Countdown starts when a setup completes and tallies (not necessarily
//     consecutive) bars up to 13; close <= low[i-2] for buy, close >= high[i-2]
//     for sell.
//   - An opposite completed setup cancels and flips the running countdown; a
//     same-direction completed setup keeps the running count (no recycling).
//
// Not implemented (explicitly out of scope): TDST lines, the 13-vs-8 countdown
// check, and countdown recycling.
func TDSequential(candles []Candle) []TD {
	out := make([]TD, len(candles))
	if len(candles) == 0 {
		return out
	}

	setupSignal := TDNeutral
	setupCount := 0
	cdSignal := TDNeutral
	cdCount := 0

	for i := range candles {
		// --- Setup ---
		// completedSetup carries the direction of a setup that hits 9 on this
		// bar, so the countdown stage below can react to it after the local
		// setup state has been reset.
		completedSetup := TDNeutral
		if i >= 4 {
			buyBar := candles[i].Close < candles[i-4].Close
			sellBar := candles[i].Close > candles[i-4].Close

			if setupCount > 0 {
				if (setupSignal == TDBuy && buyBar) || (setupSignal == TDSell && sellBar) {
					setupCount++
				} else {
					setupCount, setupSignal = 0, TDNeutral
				}
			}
			// A reset (or never-started) setup needs a price flip to begin: the
			// prior bar must have closed on the opposite side of its own 4-bars-
			// earlier close. Needs i>=5 for close[i-5] to exist.
			if setupCount == 0 && i >= 5 {
				switch {
				case buyBar && candles[i-1].Close > candles[i-5].Close:
					setupSignal, setupCount = TDBuy, 1
				case sellBar && candles[i-1].Close < candles[i-5].Close:
					setupSignal, setupCount = TDSell, 1
				}
			}
		}

		out[i].SetupCount = setupCount
		out[i].SetupSignal = setupSignal
		if setupCount == 9 {
			out[i].SetupPerfected = tdSetupPerfected(candles, i, setupSignal)
			completedSetup = setupSignal
			// A setup tops out at 9; a fresh price flip is required to start the
			// next one, so reset here (same-direction continuation won't re-count).
			setupCount, setupSignal = 0, TDNeutral
		}

		// --- Countdown ---
		// A completed setup starts the countdown; an opposite one cancels and
		// flips it. A same-direction completion is ignored (no recycling).
		if completedSetup != TDNeutral && completedSetup != cdSignal {
			cdSignal, cdCount = completedSetup, 0
		}
		if cdSignal != TDNeutral && i >= 2 {
			hit := (cdSignal == TDBuy && candles[i].Close <= candles[i-2].Low) ||
				(cdSignal == TDSell && candles[i].Close >= candles[i-2].High)
			if hit {
				cdCount++
			}
		}
		out[i].CountdownCount = cdCount
		out[i].CountdownSignal = cdSignal
		if cdCount >= 13 {
			cdCount, cdSignal = 0, TDNeutral
		}
	}

	return out
}

// tdSetupPerfected checks DeMark setup perfection at bar 9 (end). The setup's
// bars 6/7/8/9 are end-3/end-2/end-1/end. A buy setup is perfected when bar 8
// or 9 makes a low at/below both bars 6 and 7; a sell setup when bar 8 or 9
// makes a high at/above both bars 6 and 7.
func tdSetupPerfected(candles []Candle, end int, signal TDSignal) bool {
	if end < 8 {
		return false
	}
	bar6, bar7, bar8, bar9 := end-3, end-2, end-1, end
	switch signal {
	case TDBuy:
		ref := math.Min(candles[bar6].Low, candles[bar7].Low)
		return candles[bar8].Low <= ref || candles[bar9].Low <= ref
	case TDSell:
		ref := math.Max(candles[bar6].High, candles[bar7].High)
		return candles[bar8].High >= ref || candles[bar9].High >= ref
	}
	return false
}
