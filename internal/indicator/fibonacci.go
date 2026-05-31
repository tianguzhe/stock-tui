package indicator

// fibRatios are the standard Fibonacci retracement ratios plus the 0%/100%
// endpoints. 50% is not a Fibonacci number but is included by convention.
var fibRatios = []float64{0, 0.236, 0.382, 0.5, 0.618, 0.786, 1.0}

// FibLevel is one retracement level: its ratio and the price it maps to.
type FibLevel struct {
	Ratio float64 // 0, 0.236, 0.382, 0.5, 0.618, 0.786, 1.0
	Price float64 // price at this ratio
}

// FibRetracement is the Fibonacci retracement of a swing within a lookback
// window: the swing high/low (with their candle indices), the inferred trend
// direction, and the price levels.
type FibRetracement struct {
	High      float64    // swing high price
	Low       float64    // swing low price
	HighIndex int        // index of the swing high in the input candles
	LowIndex  int        // index of the swing low in the input candles
	Uptrend   bool       // true: uptrend (levels are pullback supports); false: downtrend (levels are bounce resistances)
	Levels    []FibLevel // 7 levels ordered by ascending ratio
}

// FibRetracementOf computes the Fibonacci retracement over the most recent
// `lookback` candles. It takes the window's highest high and lowest low as the
// swing endpoints, then infers direction from which extreme is more recent:
// a more recent high means the price just rallied and we watch for a pullback
// (uptrend → 0% sits at the high, levels descend as supports); a more recent
// low means the price just fell and we watch for a bounce (downtrend → 0% sits
// at the low, levels ascend as resistances). 0% always anchors to the most
// recent extreme, matching common charting tools.
//
// lookback <= 0 or beyond the series uses the whole series. An empty series
// yields a zero value with nil Levels. A flat window (high == low) yields all
// levels at that single price.
func FibRetracementOf(candles []Candle, lookback int) FibRetracement {
	n := len(candles)
	if n == 0 {
		return FibRetracement{}
	}
	if lookback <= 0 || lookback > n {
		lookback = n
	}
	start := n - lookback

	// Use >= / <= so an equal high/low retest moves the index to the LATER bar:
	// direction below keys off which extreme is more recent, so a retested high
	// must be treated as the recent extreme (uptrend), not the earliest one.
	hiIdx, loIdx := start, start
	for i := start + 1; i < n; i++ {
		if candles[i].High >= candles[hiIdx].High {
			hiIdx = i
		}
		if candles[i].Low <= candles[loIdx].Low {
			loIdx = i
		}
	}

	high, low := candles[hiIdx].High, candles[loIdx].Low
	// The more recent extreme is the retracement anchor: a later high → uptrend
	// (awaiting pullback), a later low → downtrend (awaiting bounce). A degenerate
	// window (same bar holds both) is treated as an uptrend; levels collapse anyway.
	uptrend := hiIdx >= loIdx
	rng := high - low

	levels := make([]FibLevel, len(fibRatios))
	for i, ratio := range fibRatios {
		price := high - ratio*rng // uptrend: 0%→high, 100%→low
		if !uptrend {
			price = low + ratio*rng // downtrend: 0%→low, 100%→high
		}
		levels[i] = FibLevel{Ratio: ratio, Price: price}
	}

	return FibRetracement{
		High:      high,
		Low:       low,
		HighIndex: hiIdx,
		LowIndex:  loIdx,
		Uptrend:   uptrend,
		Levels:    levels,
	}
}
