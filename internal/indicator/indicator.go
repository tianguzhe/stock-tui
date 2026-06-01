package indicator

import "math"

type Candle struct {
	High   float64
	Low    float64
	Close  float64
	Volume float64
}

type Result struct {
	KDJ      KDJ
	MACD     MACD
	RSI      RSI
	WR       WR
	DMI      DMI
	CMI      float64
	BIAS     BIAS
	CHOP     float64
	ATR      ATR
	BOLL     BOLL
	Donchian Donchian
	MFI      float64
	SAR      SAR
	Keltner  Keltner
}

type KDJ struct {
	K float64
	D float64
	J float64
}

type MACD struct {
	DIF       float64
	DEA       float64
	Histogram float64
}

type RSI struct {
	RSI6  float64
	RSI12 float64
	RSI24 float64
}

type WR struct {
	WR10 float64
	WR14 float64
}

type DMI struct {
	PDI  float64
	MDI  float64
	ADX  float64
	ADXR float64
}

type BIAS struct {
	BIAS6  float64
	BIAS12 float64
	BIAS24 float64
}

type ATR struct {
	ATR14 float64
	Pct   float64
}

type BOLL struct {
	Mid       float64
	Upper     float64
	Lower     float64
	PercentB  float64
	Bandwidth float64
}

type Donchian struct {
	Upper20 float64
	Lower20 float64
	Upper55 float64
	Lower55 float64
}

// SAR is Wilder's Parabolic SAR: the stop-and-reverse level for the current bar,
// the trend stance it implies, and whether this bar flipped the stance.
type SAR struct {
	Value    float64 // SAR price: trailing stop in an uptrend, ceiling in a downtrend
	Long     bool    // true: rising stance (SAR below price); false: falling stance
	Reversed bool    // true only on the bar where price pierced SAR and flipped the stance
}

// Keltner is the John Carter TTM variant: EMA(20) midline with ATR(20)-based
// bands. Squeeze is true when the Bollinger band sits entirely inside the
// Keltner channel — volatility compression that typically precedes a breakout.
type Keltner struct {
	Mid     float64
	Upper   float64
	Lower   float64
	Squeeze bool
}

func Calculate(candles []Candle) []Result {
	results := make([]Result, len(candles))
	if len(candles) == 0 {
		return results
	}

	fillKDJ(candles, results)
	fillMACD(candles, results)
	fillRSI(candles, results)
	fillWR(candles, results)
	fillDMI(candles, results)
	fillCMI(candles, results)
	fillBIAS(candles, results)
	fillCHOP(candles, results)
	fillATR(candles, results)
	fillBOLL(candles, results)
	fillDonchian(candles, results)
	fillMFI(candles, results)
	fillSAR(candles, results)
	fillKeltner(candles, results) // reads results[i].BOLL, so must run after fillBOLL

	return results
}

func fillKDJ(candles []Candle, results []Result) {
	k, d := 50.0, 50.0
	for i := range candles {
		low, high := highLow(candles, i, 9)
		rsv := 50.0
		if high != low {
			rsv = (candles[i].Close - low) / (high - low) * 100
		}
		k = k*2/3 + rsv/3
		d = d*2/3 + k/3
		results[i].KDJ = KDJ{K: k, D: d, J: 3*k - 2*d}
	}
}

func fillMACD(candles []Candle, results []Result) {
	ema12, ema26 := candles[0].Close, candles[0].Close
	dea := 0.0
	for i, candle := range candles {
		if i > 0 {
			ema12 = ema(ema12, candle.Close, 12)
			ema26 = ema(ema26, candle.Close, 26)
		}
		dif := ema12 - ema26
		dea = ema(dea, dif, 9)
		results[i].MACD = MACD{DIF: dif, DEA: dea, Histogram: (dif - dea) * 2}
	}
}

func fillRSI(candles []Candle, results []Result) {
	// Wilder RSI: each period keeps an RMA of gains and losses, recursively
	// seeded from the first bar (SMA(X,N,1) convention) to match the project's
	// KDJ/MACD warmup style. The first bar has no prior close, so it is neutral.
	var g6, l6, g12, l12, g24, l24 float64
	for i := range candles {
		if i == 0 {
			results[i].RSI = RSI{RSI6: 50, RSI12: 50, RSI24: 50}
			continue
		}
		gain, loss := 0.0, 0.0
		if change := candles[i].Close - candles[i-1].Close; change > 0 {
			gain = change
		} else {
			loss = -change
		}
		g6, l6 = wilderRMA(g6, gain, 6), wilderRMA(l6, loss, 6)
		g12, l12 = wilderRMA(g12, gain, 12), wilderRMA(l12, loss, 12)
		g24, l24 = wilderRMA(g24, gain, 24), wilderRMA(l24, loss, 24)
		results[i].RSI = RSI{
			RSI6:  wilderRSI(g6, l6),
			RSI12: wilderRSI(g12, l12),
			RSI24: wilderRSI(g24, l24),
		}
	}
}

func fillWR(candles []Candle, results []Result) {
	for i := range candles {
		results[i].WR = WR{
			WR10: wr(candles, i, 10),
			WR14: wr(candles, i, 14),
		}
	}
}

func fillDMI(candles []Candle, results []Result) {
	// Wilder DMI (original RMA smoothing): +DM/-DM/TR are smoothed by a
	// 14-period RMA, ADX = RMA(DX,14), and ADXR averages ADX with its value 14
	// bars ago. The RMA is recursively seeded from the first bar (SMA(X,N,1)
	// convention), consistent with the other indicators. The first bar has no
	// prior candle, so its directional movement is undefined and left zero.
	const period = 14
	var trRMA, pdmRMA, mdmRMA, adx float64
	for i := range candles {
		if i == 0 {
			results[i].DMI = DMI{}
			continue
		}
		upMove := candles[i].High - candles[i-1].High
		downMove := candles[i-1].Low - candles[i].Low
		pdm, mdm := 0.0, 0.0
		if upMove > downMove && upMove > 0 {
			pdm = upMove
		}
		if downMove > upMove && downMove > 0 {
			mdm = downMove
		}
		tr := trueRange(candles[i], candles[i-1].Close)

		trRMA = wilderRMA(trRMA, tr, period)
		pdmRMA = wilderRMA(pdmRMA, pdm, period)
		mdmRMA = wilderRMA(mdmRMA, mdm, period)

		pdi, mdi := 0.0, 0.0
		if trRMA > 0 {
			pdi = pdmRMA / trRMA * 100
			mdi = mdmRMA / trRMA * 100
		}
		dx := 0.0
		if pdi+mdi > 0 {
			dx = math.Abs(pdi-mdi) / (pdi + mdi) * 100
		}
		adx = wilderRMA(adx, dx, period)

		adxr := adx
		if i >= period {
			adxr = (adx + results[i-period].DMI.ADX) / 2
		}
		results[i].DMI = DMI{PDI: pdi, MDI: mdi, ADX: adx, ADXR: adxr}
	}
}

func fillCMI(candles []Candle, results []Result) {
	for i := range candles {
		low, high := highLow(candles, i, 20)
		if high == low {
			results[i].CMI = 0
			continue
		}
		start := i - 19
		if start < 0 {
			start = 0
		}
		results[i].CMI = math.Abs(candles[i].Close-candles[start].Close) / (high - low) * 100
	}
}

func fillBIAS(candles []Candle, results []Result) {
	for i := range candles {
		results[i].BIAS = BIAS{
			BIAS6:  bias(candles, i, 6),
			BIAS12: bias(candles, i, 12),
			BIAS24: bias(candles, i, 24),
		}
	}
}

func fillCHOP(candles []Candle, results []Result) {
	// Choppiness Index (period 14): 100 * log10(sum(TR,n) / (HHV(n)-LLV(n))) / log10(n).
	// High (~61.8+) means choppy/range-bound; low (~38.2-) means trending. The
	// window grows from the first bar (nEff) so every bar yields a value, matching
	// the other indicators. A flat window (no high-low range) is undefined, so it
	// is reported as the neutral 50.
	const period = 14
	for i := range candles {
		low, high := highLow(candles, i, period)
		start := i - period + 1
		if start < 0 {
			start = 0
		}
		nEff := i - start + 1
		rangeHL := high - low
		if nEff < 2 || rangeHL <= 0 {
			results[i].CHOP = 50
			continue
		}
		sumTR := 0.0
		for j := start; j <= i; j++ {
			if j == 0 {
				// First bar has no previous close; its true range degenerates
				// to High-Low. It must still be counted: its high/low already
				// feed rangeHL, so dropping it would let sumTR < rangeHL and
				// push CHOP negative during the warmup window.
				sumTR += candles[j].High - candles[j].Low
				continue
			}
			sumTR += trueRange(candles[j], candles[j-1].Close)
		}
		if sumTR <= 0 {
			results[i].CHOP = 50
			continue
		}
		results[i].CHOP = 100 * math.Log10(sumTR/rangeHL) / math.Log10(float64(nEff))
	}
}

func fillATR(candles []Candle, results []Result) {
	const period = 14
	var atr float64
	for i, candle := range candles {
		tr := candle.High - candle.Low
		if i > 0 {
			tr = trueRange(candle, candles[i-1].Close)
		}
		atr = wilderRMA(atr, tr, period)
		pct := 0.0
		if candle.Close != 0 {
			pct = atr / candle.Close * 100
		}
		results[i].ATR = ATR{ATR14: atr, Pct: pct}
	}
}

func fillBOLL(candles []Candle, results []Result) {
	const period = 20
	for i := range candles {
		start := i - period + 1
		if start < 0 {
			start = 0
		}
		count := float64(i - start + 1)
		sum := 0.0
		for j := start; j <= i; j++ {
			sum += candles[j].Close
		}
		mid := sum / count

		variance := 0.0
		for j := start; j <= i; j++ {
			diff := candles[j].Close - mid
			variance += diff * diff
		}
		std := math.Sqrt(variance / count)
		upper := mid + 2*std
		lower := mid - 2*std

		percentB := 50.0
		if upper != lower {
			percentB = (candles[i].Close - lower) / (upper - lower) * 100
		}
		bandwidth := 0.0
		if mid != 0 {
			bandwidth = (upper - lower) / mid * 100
		}
		results[i].BOLL = BOLL{Mid: mid, Upper: upper, Lower: lower, PercentB: percentB, Bandwidth: bandwidth}
	}
}

func fillDonchian(candles []Candle, results []Result) {
	for i := range candles {
		low20, high20 := highLow(candles, i, 20)
		low55, high55 := highLow(candles, i, 55)
		results[i].Donchian = Donchian{
			Upper20: high20,
			Lower20: low20,
			Upper55: high55,
			Lower55: low55,
		}
	}
}

func fillMFI(candles []Candle, results []Result) {
	const period = 14
	results[0].MFI = 50
	posFlow := make([]float64, len(candles))
	negFlow := make([]float64, len(candles))
	prevTP := typicalPrice(candles[0])
	for i := 1; i < len(candles); i++ {
		tp := typicalPrice(candles[i])
		flow := tp * candles[i].Volume
		switch {
		case tp > prevTP:
			posFlow[i] = flow
		case tp < prevTP:
			negFlow[i] = flow
		}
		start := i - period + 1
		if start < 1 {
			start = 1
		}
		pos, neg := 0.0, 0.0
		for j := start; j <= i; j++ {
			pos += posFlow[j]
			neg += negFlow[j]
		}
		switch {
		case pos == 0 && neg == 0:
			results[i].MFI = 50
		case neg == 0:
			results[i].MFI = 100
		default:
			results[i].MFI = 100 - 100/(1+pos/neg)
		}
		prevTP = tp
	}
}

// fillSAR computes Wilder's Parabolic SAR. The acceleration factor (AF) starts
// at 0.02, steps up by 0.02 each time the extreme point (EP) makes a new
// favorable extreme, and caps at 0.20. The SAR is constrained so it never moves
// into the prior two bars' price range; when price pierces it, the stance flips,
// the SAR jumps to the old EP, EP resets to the new extreme, and AF resets.
//
// The first bar has no prior trend to seed from: direction is taken from the
// first close-to-close change (defaulting to long on a single bar / flat open),
// matching the "every bar yields a value" warmup style of the other indicators.
func fillSAR(candles []Candle, results []Result) {
	const step, maxAF = 0.02, 0.20
	n := len(candles)

	long := true
	if n >= 2 {
		long = candles[1].Close >= candles[0].Close
	}
	var sar, ep float64
	af := step
	if long {
		sar, ep = candles[0].Low, candles[0].High
	} else {
		sar, ep = candles[0].High, candles[0].Low
	}
	results[0].SAR = SAR{Value: sar, Long: long}

	for i := 1; i < n; i++ {
		sar += af * (ep - sar)
		reversed := false
		if long {
			// SAR may not penetrate the prior one/two bars' lows.
			sar = math.Min(sar, candles[i-1].Low)
			if i >= 2 {
				sar = math.Min(sar, candles[i-2].Low)
			}
			if candles[i].Low < sar {
				long, reversed = false, true
				sar, ep, af = ep, candles[i].Low, step
			} else if candles[i].High > ep {
				ep, af = candles[i].High, math.Min(af+step, maxAF)
			}
		} else {
			sar = math.Max(sar, candles[i-1].High)
			if i >= 2 {
				sar = math.Max(sar, candles[i-2].High)
			}
			if candles[i].High > sar {
				long, reversed = true, true
				sar, ep, af = ep, candles[i].High, step
			} else if candles[i].Low < ep {
				ep, af = candles[i].Low, math.Min(af+step, maxAF)
			}
		}
		results[i].SAR = SAR{Value: sar, Long: long, Reversed: reversed}
	}
}

// fillKeltner computes the John Carter TTM Keltner channel: an EMA(20) midline
// with bands at ±1.5*ATR(20) (ATR via Wilder RMA, seeded from the first bar like
// fillATR). Squeeze is true when the Bollinger band lies fully inside the Keltner
// channel — a volatility squeeze that often precedes a breakout. It reads
// results[i].BOLL, so Calculate must run fillBOLL first.
func fillKeltner(candles []Candle, results []Result) {
	const period = 20
	const mult = 1.5
	emaClose := candles[0].Close
	var atr float64
	for i, candle := range candles {
		if i > 0 {
			emaClose = ema(emaClose, candle.Close, period)
		}
		tr := candle.High - candle.Low
		if i > 0 {
			tr = trueRange(candle, candles[i-1].Close)
		}
		atr = wilderRMA(atr, tr, period)
		upper := emaClose + mult*atr
		lower := emaClose - mult*atr
		// Squeeze: Bollinger band strictly inside Keltner. A degenerate window
		// (both bands collapse to the mid) is not a squeeze, so use strict <.
		squeeze := results[i].BOLL.Upper < upper && results[i].BOLL.Lower > lower
		results[i].Keltner = Keltner{Mid: emaClose, Upper: upper, Lower: lower, Squeeze: squeeze}
	}
}

func ema(prev, value float64, period int) float64 {
	return prev*(float64(period)-1)/(float64(period)+1) + value*2/(float64(period)+1)
}

// wilderRMA applies Wilder's recursive moving average (RMA), i.e. the
// SMA(X,N,1) convention: rma = (value + (period-1)*prev) / period. It is
// seeded from the first bar with prev == 0, matching the warmup behavior of
// the project's other indicators (KDJ/MACD also recurse from the first value).
func wilderRMA(prev, value float64, period int) float64 {
	p := float64(period)
	return (value + (p-1)*prev) / p
}

// wilderRSI maps Wilder-smoothed average gain/loss onto the 0..100 RSI scale.
// No movement at all (both zero) is neutral 50; a pure-gain window yields 100
// and a pure-loss window yields 0 without any special casing.
func wilderRSI(avgGain, avgLoss float64) float64 {
	denom := avgGain + avgLoss
	if denom == 0 {
		return 50
	}
	return avgGain / denom * 100
}

func wr(candles []Candle, end, period int) float64 {
	low, high := highLow(candles, end, period)
	if high == low {
		return 50
	}
	return (high - candles[end].Close) / (high - low) * 100
}

func bias(candles []Candle, end, period int) float64 {
	ma := averageClose(candles, end, period)
	if ma == 0 {
		return 0
	}
	return (candles[end].Close - ma) / ma * 100
}

func highLow(candles []Candle, end, period int) (float64, float64) {
	start := end - period + 1
	if start < 0 {
		start = 0
	}
	low, high := candles[start].Low, candles[start].High
	for i := start + 1; i <= end; i++ {
		if candles[i].Low < low {
			low = candles[i].Low
		}
		if candles[i].High > high {
			high = candles[i].High
		}
	}
	return low, high
}

func averageClose(candles []Candle, end, period int) float64 {
	start := end - period + 1
	if start < 0 {
		start = 0
	}
	total := 0.0
	for i := start; i <= end; i++ {
		total += candles[i].Close
	}
	return total / float64(end-start+1)
}

func trueRange(candle Candle, previousClose float64) float64 {
	return maxFloat(
		candle.High-candle.Low,
		math.Abs(candle.High-previousClose),
		math.Abs(candle.Low-previousClose),
	)
}

func typicalPrice(candle Candle) float64 {
	return (candle.High + candle.Low + candle.Close) / 3
}

func maxFloat(values ...float64) float64 {
	maxValue := values[0]
	for _, value := range values[1:] {
		if value > maxValue {
			maxValue = value
		}
	}
	return maxValue
}
