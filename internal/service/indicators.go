package service

import "math"

// Plain float64 indicator maths. Kept separate from the service so the strategy
// is testable without a database or a feed.

// EMA returns the exponential moving average series for period p.
// The first value seeds from a simple average of the first p samples.
func EMA(values []float64, p int) []float64 {
	if len(values) < p || p <= 0 {
		return nil
	}
	out := make([]float64, 0, len(values)-p+1)

	var seed float64
	for i := 0; i < p; i++ {
		seed += values[i]
	}
	prev := seed / float64(p)
	out = append(out, prev)

	k := 2.0 / (float64(p) + 1.0)
	for i := p; i < len(values); i++ {
		prev = values[i]*k + prev*(1-k)
		out = append(out, prev)
	}
	return out
}

// RSI returns Wilder's relative strength index over period p, as a single
// value for the most recent sample.
func RSI(values []float64, p int) float64 {
	if len(values) < p+1 {
		return 50 // not enough data: treat as neutral
	}

	var gain, loss float64
	for i := 1; i <= p; i++ {
		d := values[i] - values[i-1]
		if d > 0 {
			gain += d
		} else {
			loss -= d
		}
	}
	avgGain := gain / float64(p)
	avgLoss := loss / float64(p)

	for i := p + 1; i < len(values); i++ {
		d := values[i] - values[i-1]
		g, l := 0.0, 0.0
		if d > 0 {
			g = d
		} else {
			l = -d
		}
		avgGain = (avgGain*float64(p-1) + g) / float64(p)
		avgLoss = (avgLoss*float64(p-1) + l) / float64(p)
	}

	if avgLoss == 0 {
		if avgGain == 0 {
			return 50
		}
		return 100
	}
	rs := avgGain / avgLoss
	return 100 - (100 / (1 + rs))
}

// StdDev is used as a cheap volatility proxy for sizing stop loss and take
// profit distances, since tick data has no true high/low for a real ATR.
func StdDev(values []float64) float64 {
	n := len(values)
	if n < 2 {
		return 0
	}
	var mean float64
	for _, v := range values {
		mean += v
	}
	mean /= float64(n)

	var sum float64
	for _, v := range values {
		d := v - mean
		sum += d * d
	}
	return math.Sqrt(sum / float64(n-1))
}
