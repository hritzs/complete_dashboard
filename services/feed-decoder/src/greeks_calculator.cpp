#include "decoder/greeks_calculator.hpp"
#include "decoder/chain_builder.hpp"
#include "decoder/normalizer.hpp"
#include <cmath>
#include <algorithm>
#include <cctype>

#ifndef M_PI
#define M_PI 3.14159265358979323846
#endif

namespace decoder {

void GreeksCalculator::process_tick(int token, double ltp, double bid, double ask, uint32_t volume, uint32_t oi, const ContractInfo& info) {
    if (chain_builder_) {
        if (info.symbol.find("FUT") != std::string::npos) {
            std::string base = info.symbol.substr(0, info.symbol.find("FUT"));
            chain_builder_->update_spot_price(base, ltp, volume, oi); 
        } else {
            chain_builder_->update_option_price(token, ltp, volume, oi);
        }
    }
}

namespace greeks {

double norm_pdf(double x) {
    return std::exp(-x * x / 2.0) / std::sqrt(2.0 * M_PI);
}

double norm_cdf(double x) {
    return 0.5 * (1.0 + std::erf(x / std::sqrt(2.0)));
}

double black_scholes(char calc_type, char option_type, double K, double S, double T, double sigma, double r) {
    // Convert days to years as per the Python implementation
    T = T / 365.0; 

    if (std::isnan(sigma) || sigma <= 0.0) return NAN;
    if (std::isnan(K) || K <= 0.0) return NAN;
    if (std::isnan(S) || S <= 0.0) return NAN;
    if (std::isnan(T) || T <= 0.0) return NAN;

    double denominator = sigma * std::sqrt(T);
    if (denominator == 0.0) return NAN;

    double d1 = (std::log(S / K) + (r + sigma * sigma / 2.0) * T) / denominator;
    double d2 = d1 - denominator;

    char opt = std::tolower(option_type);
    char calc = std::tolower(calc_type);

    if (calc == 'p') { // PRICE
        if (opt == 'c') {
            return S * norm_cdf(d1) - K * std::exp(-r * T) * norm_cdf(d2);
        } else if (opt == 'p') {
            return K * std::exp(-r * T) * norm_cdf(-d2) - S * norm_cdf(-d1);
        }
    } else if (calc == 'd') { // DELTA
        if (opt == 'c') {
            return norm_cdf(d1);
        } else if (opt == 'p') {
            return -norm_cdf(-d1);
        }
    } else if (calc == 'g') { // GAMMA
        return norm_pdf(d1) / (S * sigma * std::sqrt(T));
    } else if (calc == 'v') { // VEGA
        return S * norm_pdf(d1) * std::sqrt(T) * 0.01;
    } else if (calc == 't') { // THETA
        double theta = 0.0;
        if (opt == 'c') {
            theta = -S * norm_pdf(d1) * sigma / (2.0 * std::sqrt(T)) - r * K * std::exp(-r * T) * norm_cdf(d2);
        } else if (opt == 'p') {
            theta = -S * norm_pdf(d1) * sigma / (2.0 * std::sqrt(T)) + r * K * std::exp(-r * T) * norm_cdf(-d2);
        }
        return theta / 365.0;
    } else if (calc == 'r') { // RHO
        if (opt == 'c') {
            return K * T * std::exp(-r * T) * norm_cdf(d2) * 0.01;
        } else if (opt == 'p') {
            return -K * T * std::exp(-r * T) * norm_cdf(-d2) * 0.01;
        }
    }

    return NAN;
}

double implied_volatility(char option_type, double K, double S, double T, double option_price, double r, double tol, int max_iterations) {
    if (std::isnan(option_price) || option_price <= 0.0) return NAN;
    if (std::isnan(K) || K <= 0.0) return NAN;
    if (std::isnan(S) || S <= 0.0) return NAN;
    if (std::isnan(T) || T <= 0.0) return NAN;

    char opt = std::tolower(option_type);
    double intrinsic = (opt == 'c') ? std::max(0.0, S - K) : std::max(0.0, K - S);

    // If option price is at or below intrinsic, IV is theoretically 0
    if (option_price <= intrinsic) {
        return 0.0;
    }

    double T_in_years = T / 365.0;
    if (T_in_years <= 0.0) return NAN;

    double sqrt_T = std::sqrt(T_in_years);
    if (sqrt_T <= 0.0 || S <= 0.0) return NAN;

    // Initial guess for sigma 
    double sigma_guess = (std::sqrt(2.0 * M_PI) / sqrt_T) * (option_price / S);
    double sigma = 0.2;
    if (sigma_guess <= 0.03) {
        sigma = 0.2;
    } else if (sigma_guess >= 4.0) {
        sigma = 2.5;
    } else {
        sigma = sigma_guess;
    }

    // Newton-Raphson iteration
    for (int i = 0; i < max_iterations; ++i) {
        double price = black_scholes('p', opt, K, S, T, sigma, r);
        double vega = black_scholes('v', opt, K, S, T, sigma, r);

        if (std::isnan(price) || std::isnan(vega)) return NAN;

        // Nudge sigma up if price is too low to get a valid derivative
        if (price < 0.05) {
            int nudge_count = 0;
            double nudge_increment = (opt == 'c') ? 0.1 : 0.01;
            while (black_scholes('p', opt, K, S, T, sigma, r) < 0.05) {
                sigma += nudge_increment;
                nudge_count++;
                if (nudge_count > 50) return NAN; // Safety break
            }
            // Recalculate after nudging
            price = black_scholes('p', opt, K, S, T, sigma, r);
            vega = black_scholes('v', opt, K, S, T, sigma, r);
        }

        if (vega == 0.0) return NAN; // Cannot proceed if vega is zero

        double diff = price - option_price;
        
        // Check convergence
        if (std::abs(diff) < tol) return sigma;

        // Update sigma
        sigma = sigma - (diff / vega) / 100.0;

        // Bounds matching hoadley
        if (sigma > 4.0) sigma = 4.0;
        if (sigma <= 0.001) sigma = 0.001;
    }

    return NAN; // Failed to converge
}

Greeks calculate_all_greeks(char option_type, double K, double S, double T, double option_price, double r) {
    Greeks g{0.0, 0.0, 0.0, 0.0, 0.0};

    double iv = implied_volatility(option_type, K, S, T, option_price, r);
    
    if (std::isnan(iv) || iv <= 0.0) {
        return g; // Return all zeros if IV failed
    }

    double delta = black_scholes('d', option_type, K, S, T, iv, r);
    double gamma = black_scholes('g', option_type, K, S, T, iv, r);
    double vega  = black_scholes('v', option_type, K, S, T, iv, r);
    double theta = black_scholes('t', option_type, K, S, T, iv, r);

    // Formatting values identical to Python code
    g.iv = std::isnan(iv) ? 0.0 : std::round(iv * 10000.0) / 10000.0;
    g.delta = std::isnan(delta) ? 0.0 : std::round(delta * 10000.0) / 10000.0;
    g.gamma = std::isnan(gamma) ? 0.0 : std::round(gamma * 1000000.0) / 1000000.0;
    g.vega = std::isnan(vega) ? 0.0 : std::round(vega * 10000.0) / 10000.0;
    g.theta = std::isnan(theta) ? 0.0 : std::round(theta * 10000.0) / 10000.0;

    return g;
}

Greeks calculate_straddle_greeks(const Greeks& ce_greeks, const Greeks& pe_greeks, int ce_quantity, int pe_quantity) {
    Greeks g;
    // For a short straddle, the position's Greek is the negative of the long option's Greek.
    g.delta = (ce_greeks.delta * ce_quantity * -1.0) + (pe_greeks.delta * pe_quantity * -1.0);
    g.gamma = (ce_greeks.gamma * ce_quantity * -1.0) + (pe_greeks.gamma * pe_quantity * -1.0);
    g.vega  = (ce_greeks.vega  * ce_quantity * -1.0) + (pe_greeks.vega  * pe_quantity * -1.0);
    g.theta = (ce_greeks.theta * ce_quantity * -1.0) + (pe_greeks.theta * pe_quantity * -1.0);
    g.iv = 0.0; // Left at 0.0 for combined metric (can average downstream if necessary)
    return g;
}

} // namespace greeks
} // namespace decoder