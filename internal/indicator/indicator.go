package indicator

import "math"

type Candle struct {
	High   float64
	Low    float64
	Close  float64
	Volume float64
}

type Result struct {
	KDJ  KDJ
	MACD MACD
	RSI  RSI
	WR   WR
	DMI  DMI
	CMI  float64
	BIAS BIAS
	CHOP float64
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
				continue // first bar has no previous close, so no true range
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

func maxFloat(values ...float64) float64 {
	maxValue := values[0]
	for _, value := range values[1:] {
		if value > maxValue {
			maxValue = value
		}
	}
	return maxValue
}
