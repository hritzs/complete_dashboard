#pragma once

#include <string>
#include <vector>
#include <unordered_map>
#include <shared_mutex>
#include "decoder/greeks_calculator.hpp"

namespace decoder {

// Represents a single strike row in the Option Chain
struct OptionStrike {
    double strike;
    int ce_token;
    int pe_token;
    double ce_ltp = 0.0;
    double pe_ltp = 0.0;
    greeks::Greeks ce_greeks;
    greeks::Greeks pe_greeks;
};

// Represents the full Option Chain for an underlying asset (e.g., NIFTY)
struct OptionChain {
    std::string symbol;
    double fut_ltp = 0.0;
    double synthetic_spot = 0.0;
    double dte = 0.0;
    std::vector<OptionStrike> strikes;
};

class ChainBuilder {
public:
    ChainBuilder() = default;
    ~ChainBuilder() = default;

    // Initialize or replace an entire chain
    void set_chain(const std::string& symbol, OptionChain chain);

    // Ultra-fast updates from the tick parser
    void update_spot_price(const std::string& symbol, double new_spot, uint32_t volume, uint32_t oi);
    void update_option_price(int token, double new_price, uint32_t volume, uint32_t oi);

    // Recalculate Greeks for the entire chain (called periodically or on spot change)
    void recalculate_greeks(const std::string& symbol);

    // Accessors
    bool get_chain(const std::string& symbol, OptionChain& out_chain);

private:
    std::unordered_map<std::string, OptionChain> chains_;
    std::unordered_map<int, std::pair<std::string, int>> token_to_strike_index_; 
    
    mutable std::shared_mutex rw_mutex_;
};

} // namespace decoder