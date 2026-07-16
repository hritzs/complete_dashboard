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

static inline bool bad(double x) {
    return std::isnan(x) || !std::isfinite(x);
}

static inline double intrinsic_call(double S, double K) {
    return std::fmax(S - K, 0.0);
}

static inline double intrinsic_put(double S, double K) {
    return std::fmax(K - S, 0.0);
}

double norm_pdf(double x) {
    return std::exp(-x * x / 2.0) / std::sqrt(2.0 * M_PI);
}

double norm_cdf(double x) {
    return 0.5 * (1.0 + std::erf(x / std::sqrt(2.0)));
}

static inline double d1(double S, double K, double r, double sigma, double t) {
    if (S <= 0.0 || K <= 0.0 || sigma <= 0.0 || t <= 0.0) return 0.0;
    return (std::log(S / K) + (r + 0.5 * sigma * sigma) * t) / (sigma * std::sqrt(t));
}

static inline double d2(double S, double K, double r, double sigma, double t) {
    return d1(S, K, r, sigma, t) - sigma * std::sqrt(t);
}

static inline double call_price(double S, double K, double r, double sigma, double t) {
    if (t <= 0.0) return std::fmax(0.0, S - K);
    return S * norm_cdf(d1(S, K, r, sigma, t)) - K * std::exp(-r * t) * norm_cdf(d2(S, K, r, sigma, t));
}

static inline double put_price(double S, double K, double r, double sigma, double t) {
    if (t <= 0.0) return std::fmax(0.0, K - S);
    return K * std::exp(-r * t) * norm_cdf(-d2(S, K, r, sigma, t)) - S * norm_cdf(-d1(S, K, r, sigma, t));
}

static inline double vega_internal(double S, double K, double r, double sigma, double t) {
    if (t <= 0.0) return 0.0;
    return (S * norm_pdf(d1(S, K, r, sigma, t)) * std::sqrt(t)) * 0.01;
}

static inline double call_delta(double S, double K, double r, double sigma, double t) {
    if (t <= 0.0) return (S > K) ? 1.0 : 0.0;
    return norm_cdf(d1(S, K, r, sigma, t));
}

static inline double put_delta(double S, double K, double r, double sigma, double t) {
    if (t <= 0.0) return (S < K) ? -1.0 : 0.0;
    return norm_cdf(d1(S, K, r, sigma, t)) - 1.0;
}

static inline double gamma_internal(double S, double K, double r, double sigma, double t) {
    if (S <= 0.0 || sigma <= 0.0 || t <= 0.0) return 0.0;
    return norm_pdf(d1(S, K, r, sigma, t)) / (S * sigma * std::sqrt(t));
}

static inline double call_theta(double S, double K, double r, double sigma, double t) {
    if (t <= 0.0) return 0.0;
    double D1 = d1(S, K, r, sigma, t);
    double D2 = d2(S, K, r, sigma, t);
    double term1 = -(S * norm_pdf(D1) * sigma) / (2.0 * std::sqrt(t));
    double term2 = -r * K * std::exp(-r * t) * norm_cdf(D2);
    return (term1 + term2) / 365.0;
}

static inline double put_theta(double S, double K, double r, double sigma, double t) {
    if (t <= 0.0) return 0.0;
    double D1 = d1(S, K, r, sigma, t);
    double D2 = d2(S, K, r, sigma, t);
    double term1 = -(S * norm_pdf(D1) * sigma) / (2.0 * std::sqrt(t));
    double term2 = r * K * std::exp(-r * t) * norm_cdf(-D2);
    return (term1 + term2) / 365.0;
}

double black_scholes(char calc_type, char option_type, double K, double S, double T, double sigma, double r) {
    if (bad(sigma) || sigma <= 0.0) return NAN;
    if (bad(K) || K <= 0.0) return NAN;
    if (bad(S) || S <= 0.0) return NAN;
    if (bad(T) || T <= 0.0) return NAN;

    char opt = std::tolower(option_type);
    char calc = std::tolower(calc_type);

    if (calc == 'p') { // PRICE
        return (opt == 'c') ? call_price(S, K, r, sigma, T) : put_price(S, K, r, sigma, T);
    } else if (calc == 'd') { // DELTA
        return (opt == 'c') ? call_delta(S, K, r, sigma, T) : put_delta(S, K, r, sigma, T);
    } else if (calc == 'g') { // GAMMA
        return gamma_internal(S, K, r, sigma, T);
    } else if (calc == 'v') { // VEGA
        return vega_internal(S, K, r, sigma, T);
    } else if (calc == 't') { // THETA
        return (opt == 'c') ? call_theta(S, K, r, sigma, T) : put_theta(S, K, r, sigma, T);
    } else if (calc == 'r') { // RHO
        double D2 = d2(S, K, r, sigma, T);
        if (opt == 'c') return K * T * std::exp(-r * T) * norm_cdf(D2) * 0.01;
        else return -K * T * std::exp(-r * T) * norm_cdf(-D2) * 0.01;
    }

    return NAN;
}

double implied_volatility(char option_type, double K, double S, double T, double option_price, double r, double tol, int max_iterations) {
    if (bad(option_price) || option_price <= 0.0) return NAN;
    if (bad(K) || K <= 0.0) return NAN;
    if (bad(S) || S <= 0.0) return NAN;
    if (bad(T) || T <= 0.0) return NAN;

    char opt = std::tolower(option_type);
    bool is_call = (opt == 'c');

    double intrinsic = is_call ? intrinsic_call(S, K) : intrinsic_put(S, K);
    if (option_price <= intrinsic) return 0.0;

    double sigma = 0.50;

    // Newton-Raphson iteration
    for (int i = 0; i < max_iterations; ++i) {
        double theo = is_call ? call_price(S, K, r, sigma, T) : put_price(S, K, r, sigma, T);
        if (bad(theo)) return NAN;

        double diff = theo - option_price;
        if (std::fabs(diff) < tol) return sigma;

        double vega = vega_internal(S, K, r, sigma, T);

        if (bad(vega) || vega < 1e-8) {
            sigma *= 1.5;
            if (sigma > 4.0) return NAN;
            continue;
        }

        double step = (diff / vega) / 100.0;

        if (step > sigma * 0.5) step = sigma * 0.5;
        else if (step < -2.0) step = -2.0;

        sigma -= step;

        if (sigma > 4.0) sigma = 4.0;
        if (sigma < 0.0001) return NAN;
    }

    return NAN; // Failed to converge
}

Greeks calculate_all_greeks(char option_type, double K, double S, double T, double option_price, double r) {
    Greeks g{0.0, 0.0, 0.0, 0.0, 0.0};

    double iv = implied_volatility(option_type, K, S, T, option_price, r);
    
    if (bad(iv) || iv <= 0.0) {
        return g; // Return all zeros if IV failed
    }

    char opt = std::tolower(option_type);
    double delta = (opt == 'c') ? call_delta(S, K, r, iv, T) : put_delta(S, K, r, iv, T);
    double gamma = gamma_internal(S, K, r, iv, T);
    double vega  = vega_internal(S, K, r, iv, T);
    double theta = (opt == 'c') ? call_theta(S, K, r, iv, T) : put_theta(S, K, r, iv, T);

    g.iv = std::round(iv * 10000.0) / 10000.0;
    g.delta = bad(delta) ? 0.0 : std::round(delta * 10000.0) / 10000.0;
    g.gamma = bad(gamma) ? 0.0 : std::round(gamma * 1000000.0) / 1000000.0;
    g.vega = bad(vega) ? 0.0 : std::round(vega * 10000.0) / 10000.0;
    g.theta = bad(theta) ? 0.0 : std::round(theta * 10000.0) / 10000.0;

    return g;
}

Greeks calculate_greeks_from_iv(char option_type, double K, double S, double T, double iv, double r) {
    Greeks g{0.0, 0.0, 0.0, 0.0, 0.0};

    if (bad(iv) || iv <= 0.0) {
        return g; 
    }

    char opt = std::tolower(option_type);
    double delta = (opt == 'c') ? call_delta(S, K, r, iv, T) : put_delta(S, K, r, iv, T);
    double gamma = gamma_internal(S, K, r, iv, T);
    double vega  = vega_internal(S, K, r, iv, T);
    double theta = (opt == 'c') ? call_theta(S, K, r, iv, T) : put_theta(S, K, r, iv, T);

    g.iv = std::round(iv * 10000.0) / 10000.0;
    g.delta = bad(delta) ? 0.0 : std::round(delta * 10000.0) / 10000.0;
    g.gamma = bad(gamma) ? 0.0 : std::round(gamma * 1000000.0) / 1000000.0;
    g.vega = bad(vega) ? 0.0 : std::round(vega * 10000.0) / 10000.0;
    g.theta = bad(theta) ? 0.0 : std::round(theta * 10000.0) / 10000.0;

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