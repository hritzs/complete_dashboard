package greeks

import (
	"math"
)

const (
	sqrt2Pi = 2.50662827463 // sqrt(2 * PI)
)

// normCDF calculates the cumulative distribution function for a standard normal distribution.
func normCDF(x float64) float64 {
	return 0.5 * (1 + math.Erf(x/math.Sqrt2))
}

// normPDF calculates the probability density function for a standard normal distribution.
func normPDF(x float64) float64 {
	return math.Exp(-x*x/2.0) / sqrt2Pi
}

// BlackScholes calculates the price or a specific Greek for an option.
func BlackScholes(calcType string, isCall bool, strike, spot, tte, sigma, riskFreeRate float64) float64 {
	if sigma <= 0 || tte <= 0 || spot <= 0 || strike <= 0 {
		return 0.0
	}

	d1 := (math.Log(spot/strike) + (riskFreeRate+sigma*sigma/2.0)*tte) / (sigma * math.Sqrt(tte))
	d2 := d1 - sigma*math.Sqrt(tte)

	switch calcType {
	case "price":
		if isCall {
			return spot*normCDF(d1) - strike*math.Exp(-riskFreeRate*tte)*normCDF(d2)
		}
		return strike*math.Exp(-riskFreeRate*tte)*normCDF(-d2) - spot*normCDF(-d1)
	case "delta":
		if isCall {
			return normCDF(d1)
		}
		return normCDF(d1) - 1.0 // Put delta
	case "gamma":
		return normPDF(d1) / (spot * sigma * math.Sqrt(tte))
	case "vega":
		return spot * normPDF(d1) * math.Sqrt(tte) * 0.01 // Vega per 1% change
	case "theta":
		if isCall {
			theta := -spot*normPDF(d1)*sigma/(2*math.Sqrt(tte)) - riskFreeRate*strike*math.Exp(-riskFreeRate*tte)*normCDF(d2)
			return theta / 365.0
		}
		theta := -spot*normPDF(d1)*sigma/(2*math.Sqrt(tte)) + riskFreeRate*strike*math.Exp(-riskFreeRate*tte)*normCDF(-d2)
		return theta / 365.0
	}
	return 0.0
}

// ImpliedVolatility calculates the implied volatility for an option using the Newton-Raphson method.
func ImpliedVolatility(isCall bool, strike, spot, tte, optionPrice, riskFreeRate float64) float64 {
	if optionPrice <= 0 || tte <= 0 || spot <= 0 || strike <= 0 {
		return 0.0
	}

	maxIterations := 100
	tolerance := 1e-5
	sigma := 0.5 // Initial guess

	for i := 0; i < maxIterations; i++ {
		price := BlackScholes("price", isCall, strike, spot, tte, sigma, riskFreeRate)
		vega := BlackScholes("vega", isCall, strike, spot, tte, sigma, riskFreeRate) * 100 // Vega is per 1%, we need raw vega

		if vega < 1e-6 {
			break // Avoid division by zero
		}

		diff := price - optionPrice
		if math.Abs(diff) < tolerance {
			return sigma
		}

		sigma = sigma - diff/vega
	}
	return sigma
}

// Result holds all calculated greeks for an option.
type Result struct {
	IV    float64
	Delta float64
	Gamma float64
	Vega  float64
	Theta float64
}

// CalculateAllGreeks computes IV and all associated greeks for an option.
func CalculateAllGreeks(isCall bool, strike, spot, dte, optionPrice, riskFreeRate float64) Result {
	if dte <= 0 {
		return Result{}
	}
	tte := dte / 365.0

	iv := ImpliedVolatility(isCall, strike, spot, tte, optionPrice, riskFreeRate)
	if iv <= 0 || math.IsNaN(iv) {
		return Result{}
	}

	return CalculateGreeksFromIV(isCall, strike, spot, dte, iv, riskFreeRate)
}

// CalculateGreeksFromIV computes all greeks from a given IV.
func CalculateGreeksFromIV(isCall bool, strike, spot, dte, iv, riskFreeRate float64) Result {
	if dte <= 0 || iv <= 0 {
		return Result{}
	}
	tte := dte / 365.0

	delta := BlackScholes("delta", isCall, strike, spot, tte, iv, riskFreeRate)
	gamma := BlackScholes("gamma", isCall, strike, spot, tte, iv, riskFreeRate)
	vega := BlackScholes("vega", isCall, strike, spot, tte, iv, riskFreeRate)
	theta := BlackScholes("theta", isCall, strike, spot, tte, iv, riskFreeRate)

	return Result{
		IV:    iv,
		Delta: delta,
		Gamma: gamma,
		Vega:  vega,
		Theta: theta,
	}
}