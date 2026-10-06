#pragma once

#include <string>
#include <vector>
#include <unordered_map>
#include <shared_mutex>
#include <cstdint>
#include <array>

#include "decoder/greeks_calculator.hpp"

namespace decoder {

// One price/quantity level of an exchange depth ladder.
struct DepthLevel {
    double price = 0.0;
    uint32_t qty = 0;
};

// Represents a single strike row in the option chain
struct OptionStrike {
    double strike = 0.0;

    uint32_t ce_token = 0;
    uint32_t pe_token = 0;

    double ce_ltp = 0.0;
    double pe_ltp = 0.0;

    // Best bid/ask from the exchange depth ladder, used for live order
    // pricing (execution-gateway prices SELL legs off bid, BUY off ask).
    double ce_bid = 0.0;
    double ce_ask = 0.0;
    double pe_bid = 0.0;
    double pe_ask = 0.0;

    greeks::Greeks ce_greeks{};
    greeks::Greeks pe_greeks{};

    // Full 5-level exchange depth (NSE 7208 ladder), best level first.
    // *_depth_ms is the wall-clock time (epoch ms) of the last ladder update;
    // 0 means no depth received yet for that leg.
    std::array<DepthLevel, 5> ce_bids{};
    std::array<DepthLevel, 5> ce_asks{};
    std::array<DepthLevel, 5> pe_bids{};
    std::array<DepthLevel, 5> pe_asks{};
    int64_t ce_depth_ms = 0;
    int64_t pe_depth_ms = 0;
};

// Represents the full option chain for one underlying
struct OptionChain {
    std::string symbol;
    std::string expiry;

    // Raw monthly future tick if available
    double fut_ltp = 0.0;

    // Canonical derived underlying everywhere
    double synthetic_future = 0.0;

    // Time to expiry in years
    double dte = 0.0;

    std::vector<OptionStrike> strikes;
};

class ChainBuilder {
public:
    ChainBuilder() = default;
    ~ChainBuilder() = default;

    void set_chain(const std::string& symbol, OptionChain chain);

    void update_spot_price(const std::string& symbol, double new_spot, uint32_t volume, uint32_t oi);
    void update_option_price(int token, double new_price, double bid, double ask, uint32_t volume, uint32_t oi);
    // Stores the full 5-level ladder for an option token (no greeks recalc).
    void update_option_depth(int token, const std::array<DepthLevel, 5>& bids, const std::array<DepthLevel, 5>& asks, int64_t now_ms);

    void recalculate_greeks(const std::string& chain_key);

    bool get_chain(const std::string& symbol, OptionChain& out_chain);

private:
    void recalculate_greeks_internal(OptionChain& chain);
    std::unordered_map<std::string, OptionChain> chains_;
    std::unordered_map<int, std::pair<std::string, int>> token_to_strike_index_;

    mutable std::shared_mutex rw_mutex_;
};

} // namespace decoder
