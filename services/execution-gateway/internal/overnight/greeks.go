package overnight

// Option delta on the monthly future (Black-76, no discounting: the premium
// of a futures-style index option three weeks out moves by < 0.1% with it).
//
//	d1    = (ln(F/K) + s^2 T / 2) / (s sqrt(T))
//	call  = F N(d1) - K N(d1 - s sqrt(T))      delta_call = N(d1)
//	put   = K N(-d2) - F N(-d1)                delta_put  = N(d1) - 1
//
// F is the monthly future's own price, s is each strike's own implied
// volatility solved from its minute close, T is
// the calendar time to 15:30 of the expiry day in years.

import "math"

func normCDF(x float64) float64 { return 0.5 * math.Erfc(-x/math.Sqrt2) }

// normInv: inverse of normCDF by bisection (only used for a first guess).
func normInv(p float64) float64 {
	lo, hi := -8.0, 8.0
	for i := 0; i < 60; i++ {
		mid := (lo + hi) / 2
		if normCDF(mid) < p {
			lo = mid
		} else {
			hi = mid
		}
	}
	return (lo + hi) / 2
}

func black76(f, k, t, s float64, call bool) float64 {
	if t <= 0 || s <= 0 {
		if call {
			return math.Max(f-k, 0)
		}
		return math.Max(k-f, 0)
	}
	v := s * math.Sqrt(t)
	d1 := (math.Log(f/k) + v*v/2) / v
	d2 := d1 - v
	if call {
		return f*normCDF(d1) - k*normCDF(d2)
	}
	return k*normCDF(-d2) - f*normCDF(-d1)
}

// impliedVol solves black76 = price. ok=false when the price is below
// intrinsic value or otherwise has no solution.
func impliedVol(price, f, k, t float64, call bool) (float64, bool) {
	if price <= 0 || f <= 0 || k <= 0 || t <= 0 {
		return 0, false
	}
	lo, hi := 1e-4, 5.0
	if black76(f, k, t, lo, call) > price || black76(f, k, t, hi, call) < price {
		return 0, false
	}
	for i := 0; i < 70; i++ {
		mid := (lo + hi) / 2
		if black76(f, k, t, mid, call) < price {
			lo = mid
		} else {
			hi = mid
		}
	}
	return (lo + hi) / 2, true
}

func delta76(f, k, t, s float64, call bool) float64 {
	if t <= 0 || s <= 0 {
		return 0
	}
	v := s * math.Sqrt(t)
	d1 := (math.Log(f/k) + v*v/2) / v
	if call {
		return normCDF(d1)
	}
	return normCDF(d1) - 1
}

// guessStrike: where a strike of the wanted |delta| should sit for a flat
// volatility s -- the starting point of the search, nothing more.
func guessStrike(f, t, s, target float64, call bool) float64 {
	if s <= 0 || t <= 0 {
		return f
	}
	p := target
	if !call {
		p = 1 - target
	}
	v := s * math.Sqrt(t)
	return f * math.Exp(-normInv(p)*v+v*v/2)
}

func round4(x float64) float64 { return math.Round(x*1e4) / 1e4 }
func round2(x float64) float64 { return math.Round(x*100) / 100 }

// tick rounds a price to the 0.05 grid.
func tick(x float64) float64 { return math.Round(x*20) / 20 }
