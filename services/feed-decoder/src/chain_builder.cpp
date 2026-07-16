#include "decoder/chain_builder.hpp"

#include <cmath>
#include <iostream>
#include <vector>
#include <algorithm>
#include <mutex>
#include <shared_mutex>
#include <ctime>
#include <unordered_map>
#include <cctype>

namespace decoder {

namespace {

// ------------------------------------------------------------
// Strict DTE Normalization (Institutional Trading Day Fraction)
// ------------------------------------------------------------
static std::unordered_map<std::string, int> MONTH_MAP = {
    {"JAN", 0}, {"FEB", 1}, {"MAR", 2}, {"APR", 3}, {"MAY", 4}, {"JUN", 5},
    {"JUL", 6}, {"AUG", 7}, {"SEP", 8}, {"OCT", 9}, {"NOV", 10}, {"DEC", 11}
};

static double to_epoch(const std::string& exp_in) {
    std::string exp = exp_in;

    exp.erase(std::remove(exp.begin(), exp.end(), '-'), exp.end());
    exp.erase(std::remove(exp.begin(), exp.end(), '/'), exp.end());

    if (exp.size() < 5) {
        return 0.0;
    }

    int day = 0;
    try {
        day = std::stoi(exp.substr(0, 2));
    } catch (...) {
        return 0.0;
    }

    std::string mon = exp.substr(2, 3);
    for (char& c : mon) {
        c = static_cast<char>(std::toupper(static_cast<unsigned char>(c)));
    }

    if (!MONTH_MAP.count(mon)) {
        return 0.0;
    }

    std::time_t now_t = std::time(nullptr);
    std::tm tm_exp = *std::localtime(&now_t);

    tm_exp.tm_mday = day;
    tm_exp.tm_mon = MONTH_MAP[mon];
    tm_exp.tm_hour = 15;
    tm_exp.tm_min = 30;
    tm_exp.tm_sec = 0;
    tm_exp.tm_isdst = -1;

    if (exp.size() >= 9) {
        // e.g. 26JUN2026
        int year_full = 0;
        try {
            year_full = std::stoi(exp.substr(5, 4));
        } catch (...) {
            year_full = tm_exp.tm_year + 1900;
        }
        tm_exp.tm_year = year_full - 1900;
    } else if (exp.size() >= 7) {
        // e.g. 26JUN26
        int year_short = 0;
        try {
            year_short = std::stoi(exp.substr(5, 2));
        } catch (...) {
            year_short = (tm_exp.tm_year + 1900) % 100;
        }

        if (year_short < 100) {
            year_short += 2000;
        }

        tm_exp.tm_year = year_short - 1900;
    } else {
        // e.g. 26JUN -> infer current year or next year
        int current_year = tm_exp.tm_year;
        tm_exp.tm_year = current_year;

        std::time_t candidate = std::mktime(&tm_exp);
        if (candidate < now_t - 86400) {
            tm_exp.tm_year = current_year + 1;
        }
    }

    return static_cast<double>(std::mktime(&tm_exp));
}

static double calculate_dte(const std::string& expiry) {
    double epoch = to_epoch(expiry);
    if (epoch <= 0.0) {
        return 7.0 / 365.0;
    }

    std::time_t now_t = std::time(nullptr);
    std::tm tm_now;
    localtime_r(&now_t, &tm_now);

    std::time_t expiry_t = static_cast<std::time_t>(epoch);
    std::tm tm_expiry;
    localtime_r(&expiry_t, &tm_expiry);

    std::tm tm_today = tm_now;
    tm_today.tm_hour = 0;
    tm_today.tm_min = 0;
    tm_today.tm_sec = 0;
    std::time_t today_start = std::mktime(&tm_today);

    std::tm tm_expiry_day = tm_expiry;
    tm_expiry_day.tm_hour = 0;
    tm_expiry_day.tm_min = 0;
    tm_expiry_day.tm_sec = 0;
    std::time_t expiry_day_start = std::mktime(&tm_expiry_day);

    double days_diff = std::round(std::difftime(expiry_day_start, today_start) / 86400.0);

    double current_minutes =
        tm_now.tm_hour * 60.0 +
        tm_now.tm_min +
        tm_now.tm_sec / 60.0;

    const double start_minutes = 9.0 * 60.0 + 15.0;   // 09:15
    const double end_minutes   = 15.0 * 60.0 + 30.0;  // 15:30
    const double total_trading_minutes = end_minutes - start_minutes;

    double fraction = 0.0;
    if (current_minutes < start_minutes) {
        fraction = 1.0;
    } else if (current_minutes >= end_minutes) {
        fraction = 0.0;
    } else {
        fraction = (end_minutes - current_minutes) / total_trading_minutes;
    }

    double dte_days = std::max(0.000001, days_diff + fraction);
    return dte_days / 365.0;
}

// ------------------------------------------------------------
// Detect strike step dynamically from adjacent strikes
// ------------------------------------------------------------
static double detect_strike_step(const OptionChain& chain) {
    if (chain.strikes.size() < 2) return 50.0;

    double min_step = 999999.0;
    for (size_t i = 1; i < chain.strikes.size(); ++i) {
        const double diff = std::abs(chain.strikes[i].strike - chain.strikes[i - 1].strike);
        if (diff > 0.0 && diff < min_step) {
            min_step = diff;
        }
    }

    return (min_step == 999999.0) ? 50.0 : min_step;
}

// ------------------------------------------------------------
// Find nearest actual listed strike to a target price
// ------------------------------------------------------------
static double nearest_strike(const OptionChain& chain, double px) {
    if (chain.strikes.empty()) return 0.0;

    double best = chain.strikes.front().strike;
    double best_diff = std::abs(best - px);

    for (const auto& row : chain.strikes) {
        const double diff = std::abs(row.strike - px);
        if (diff < best_diff) {
            best_diff = diff;
            best = row.strike;
        }
    }

    return best;
}

// ------------------------------------------------------------
// Round to strike gap and then snap to nearest listed strike
// ------------------------------------------------------------
static double round_to_gap_and_snap(const OptionChain& chain, double px) {
    if (chain.strikes.empty()) return 0.0;

    const double step = detect_strike_step(chain);
    if (step <= 0.0) return nearest_strike(chain, px);

    const double rounded = std::round(px / step) * step;
    return nearest_strike(chain, rounded);
}

// ------------------------------------------------------------
// Monthly future proxy from put-call parity.
// Used only if raw monthly future tick is missing.
// Median of nearest usable rows.
// ------------------------------------------------------------
static double compute_monthly_future_proxy(const OptionChain& chain, double anchor_px) {
    struct Candidate {
        double strike;
        double proxy;
        double distance;
    };

    std::vector<Candidate> candidates;
    candidates.reserve(chain.strikes.size());

    for (const auto& row : chain.strikes) {
        if (row.ce_ltp > 0.0 && row.pe_ltp > 0.0) {
            const double proxy = row.strike + row.ce_ltp - row.pe_ltp;
            const double distance = (anchor_px > 0.0)
                ? std::abs(row.strike - anchor_px)
                : 0.0;

            candidates.push_back({row.strike, proxy, distance});
        }
    }

    if (candidates.empty()) return 0.0;

    std::sort(
        candidates.begin(),
        candidates.end(),
        [](const Candidate& a, const Candidate& b) {
            return a.distance < b.distance;
        }
    );

    const size_t takeN = std::min<size_t>(9, candidates.size());

    std::vector<double> values;
    values.reserve(takeN);

    for (size_t i = 0; i < takeN; ++i) {
        values.push_back(candidates[i].proxy);
    }

    std::sort(values.begin(), values.end());
    return values[values.size() / 2];
}

// ------------------------------------------------------------
// Real synthetic_future from ONE strike:
// synthetic_future = strike + (CE - PE)
// ------------------------------------------------------------
static double compute_synthetic_from_strike(const OptionChain& chain, double strike) {
    for (const auto& row : chain.strikes) {
        if (row.strike == strike && row.ce_ltp > 0.0 && row.pe_ltp > 0.0) {
            return row.strike + (row.ce_ltp - row.pe_ltp);
        }
    }
    return 0.0;
}

// ------------------------------------------------------------
// Per-tick recomputation of canonical synthetic_future
//
// FLOW:
// 1) monthly_future = raw future if available, else parity proxy
// 2) preliminary_atm = round_to_gap_and_snap(monthly_future)
// 3) synthetic_future = strike + (CE - PE) at preliminary_atm
// 4) if CE/PE missing at preliminary_atm, keep last stored synthetic_future
//
// IMPORTANT:
// - NO ongoing fallback from synthetic_future to raw future
// - BUT if synthetic_future has never been initialized and
//   parity is unavailable, initialize it ONCE from monthly_future
//   so it doesn't stay zero forever.
// ------------------------------------------------------------
static double recompute_underlying_from_options(OptionChain& chain) {
    double monthly_future = 0.0;

    // 1) Monthly future anchor
    if (chain.fut_ltp > 0.0) {
        monthly_future = chain.fut_ltp;
    } else {
        const double anchor = (chain.synthetic_future > 0.0) ? chain.synthetic_future : 0.0;
        monthly_future = compute_monthly_future_proxy(chain, anchor);
    }

    if (monthly_future <= 0.0) {
        // No usable anchor -> keep last stored synthetic_future
        return chain.synthetic_future;
    }

    // 2) Preliminary ATM from rounded monthly future
    const double preliminary_atm = round_to_gap_and_snap(chain, monthly_future);
    if (preliminary_atm <= 0.0) {
        return chain.synthetic_future;
    }

    // 3) Real synthetic_future from THAT strike
    const double synthetic = compute_synthetic_from_strike(chain, preliminary_atm);
    if (synthetic > 0.0) {
        chain.synthetic_future = synthetic;
        return chain.synthetic_future;
    }

    // 4) If parity unavailable and synthetic was never initialized,
    // initialize once from monthly_future so it does not stay zero forever.
    if (chain.synthetic_future <= 0.0) {
        chain.synthetic_future = monthly_future;
    }

    return chain.synthetic_future;
}

} // namespace

// ============================================================
// Seed / set option chain
// ============================================================
void ChainBuilder::set_chain(const std::string& symbol, OptionChain chain) {
    std::unique_lock<std::shared_mutex> lock(rw_mutex_);

    chains_[symbol] = std::move(chain);
    auto& stored_chain = chains_[symbol];

    for (size_t i = 0; i < stored_chain.strikes.size(); ++i) {
        if (stored_chain.strikes[i].ce_token != 0) {
            token_to_strike_index_[static_cast<int>(stored_chain.strikes[i].ce_token)] = {
                symbol,
                static_cast<int>(i)
            };
        }

        if (stored_chain.strikes[i].pe_token != 0) {
            token_to_strike_index_[static_cast<int>(stored_chain.strikes[i].pe_token)] = {
                symbol,
                static_cast<int>(i)
            };
        }
    }

    // Optional warm-up recalc
    recalculate_greeks_internal(stored_chain);
}

// ============================================================
// Update raw monthly future tick
// ============================================================
void ChainBuilder::update_spot_price(const std::string& base_symbol, double new_spot, uint32_t volume, uint32_t oi) {
    (void)volume;
    (void)oi;

    std::unique_lock<std::shared_mutex> lock(rw_mutex_);

    // Iterate over all expiries matching this base symbol
    for (auto& pair : chains_) {
        if (pair.second.symbol == base_symbol) {
            if (new_spot > 0.0) {
                pair.second.fut_ltp = new_spot;
            }
            recalculate_greeks_internal(pair.second);
        }
    }
}

// ============================================================
// Update CE / PE tick
// ============================================================
void ChainBuilder::update_option_price(int token, double new_price, uint32_t volume, uint32_t oi) {
    (void)volume;
    (void)oi;

    std::unique_lock<std::shared_mutex> lock(rw_mutex_);

    auto it = token_to_strike_index_.find(token);

    if (it == token_to_strike_index_.end()) {
        static int miss_count = 0;
        if (++miss_count % 5000 == 0) {
            std::cout << "[ChainBuilder] ⚠️ Unknown token received: " << token << std::endl;
        }
        return;
    }

    const std::string& chain_key = it->second.first;
    const int strike_index = it->second.second;

    auto chain_it = chains_.find(chain_key);
    if (chain_it == chains_.end()) return;

    auto& chain = chain_it->second;
    if (strike_index < 0 || static_cast<size_t>(strike_index) >= chain.strikes.size()) return;

    auto& strike_row = chain.strikes[static_cast<size_t>(strike_index)];

    if (strike_row.ce_token == static_cast<uint32_t>(token)) {
        strike_row.ce_ltp = new_price;
    } else if (strike_row.pe_token == static_cast<uint32_t>(token)) {
        strike_row.pe_ltp = new_price;
    } else {
        return;
    }

    recalculate_greeks_internal(chain);
}

// ============================================================
// Recalculate Greeks using synthetic_future as underlying
// ============================================================
void ChainBuilder::recalculate_greeks(const std::string& chain_key) {
    auto it = chains_.find(chain_key);
    if (it == chains_.end()) return;

    recalculate_greeks_internal(it->second);
}

void ChainBuilder::recalculate_greeks_internal(OptionChain& chain) {
    // Recompute DTE every tick using normalized market-hours fraction
    chain.dte = calculate_dte(chain.expiry);
    if (chain.dte <= 0.0) return;

    // primary underlying from synthetic logic
    double effective_underlying = recompute_underlying_from_options(chain);

    // --------------------------------------------------------
    // CRITICAL SAFETY FALLBACK
    // If synthetic is still not ready, DO NOT skip Greeks.
    // Use monthly future if present, otherwise middle strike
    // as a bootstrap underlying.
    // --------------------------------------------------------
    if (effective_underlying <= 0.0) {
        if (chain.fut_ltp > 0.0) {
            effective_underlying = chain.fut_ltp;
        } else if (!chain.strikes.empty()) {
            effective_underlying = chain.strikes[chain.strikes.size() / 2].strike;
        }
    }

    if (effective_underlying <= 0.0) return;

    const double risk_free_rate = 0.0;

    for (auto& row : chain.strikes) {
        greeks::Greeks ce_greeks{0.0, 0.0, 0.0, 0.0, 0.0};
        greeks::Greeks pe_greeks{0.0, 0.0, 0.0, 0.0, 0.0};

        if (row.ce_ltp > 0.0) {
            ce_greeks = greeks::calculate_all_greeks(
                'c',
                row.strike,
                effective_underlying,
                chain.dte,
                row.ce_ltp,
                risk_free_rate
            );
        }

        if (row.pe_ltp > 0.0) {
            pe_greeks = greeks::calculate_all_greeks(
                'p',
                row.strike,
                effective_underlying,
                chain.dte,
                row.pe_ltp,
                risk_free_rate
            );
        }

        // Apply OTM IV to ITM options to prevent unstable greeks
        const bool is_ce_itm = row.strike < effective_underlying;
        const bool is_pe_itm = row.strike > effective_underlying;

        if (is_ce_itm && pe_greeks.iv > 0.0) {
            ce_greeks = greeks::calculate_greeks_from_iv(
                'c',
                row.strike,
                effective_underlying,
                chain.dte,
                pe_greeks.iv,
                risk_free_rate
            );
        } else if (is_pe_itm && ce_greeks.iv > 0.0) {
            pe_greeks = greeks::calculate_greeks_from_iv(
                'p',
                row.strike,
                effective_underlying,
                chain.dte,
                ce_greeks.iv,
                risk_free_rate
            );
        }

        row.ce_greeks = ce_greeks;
        row.pe_greeks = pe_greeks;
    }
}

// ============================================================
// Read-only snapshot getter
// ============================================================
bool ChainBuilder::get_chain(const std::string& symbol, OptionChain& out_chain) {
    std::shared_lock<std::shared_mutex> lock(rw_mutex_);

    auto it = chains_.find(symbol);
    if (it != chains_.end()) {
        out_chain = it->second;
        return true;
    }

    return false;
}

} // namespace decoder
