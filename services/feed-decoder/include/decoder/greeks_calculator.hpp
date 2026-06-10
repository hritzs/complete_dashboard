#pragma once

#include <cmath>
#include <string>
#include <cstdint>

namespace decoder {
namespace greeks {

struct Greeks {
    double iv;
    double delta;
    double gamma;
    double vega;
    double theta;
};

// Low-level Math Utilities
double norm_pdf(double x);
double norm_cdf(double x);

// Core Black-Scholes Pricing and Greeks Calculation
// calculation_type: 'p' (price), 'd' (delta), 'g' (gamma), 'v' (vega), 't' (theta), 'r' (rho)
// option_type: 'c' (call) or 'p' (put)
double black_scholes(char calc_type, char option_type, double K, double S, double T, double sigma, double r = 0.0);

// Implied Volatility calculation (Newton-Raphson approach matching hoadley.py exactly)
double implied_volatility(char option_type, double K, double S, double T, double option_price, double r = 0.0, double tol = 0.0001, int max_iterations = 100);

// Calculate all Greeks in a single pass returning a structured Greeks object
Greeks calculate_all_greeks(char option_type, double K, double S, double T, double option_price, double r = 0.0);

// Calculate combined Greeks for a short straddle position
Greeks calculate_straddle_greeks(const Greeks& ce_greeks, const Greeks& pe_greeks, int ce_quantity, int pe_quantity);

} // namespace greeks

// Forward declare ChainBuilder
class ChainBuilder;

// Forward declare ContractInfo
struct ContractInfo;

// Adapter to bridge raw ticks into the ChainBuilder
class GreeksCalculator {
public:
    GreeksCalculator() = default;
    ~GreeksCalculator() = default;

    void set_chain_builder(ChainBuilder* cb) { chain_builder_ = cb; }

    void process_tick(int token, double ltp, double bid, double ask, uint32_t volume, uint32_t oi, const ContractInfo& info);

private:
    ChainBuilder* chain_builder_ = nullptr;
};

} // namespace decoder