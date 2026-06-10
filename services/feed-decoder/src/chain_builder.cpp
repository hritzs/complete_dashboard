#include "decoder/chain_builder.hpp"
#include <cmath>
#include <iostream>
#include <mutex>

namespace decoder {

void ChainBuilder::set_chain(const std::string& symbol, OptionChain chain) {
    std::unique_lock lock(rw_mutex_);
    
    chains_[symbol] = std::move(chain);
    auto& stored_chain = chains_[symbol];

    // Build fast O(1) lookup map from token to the strike index
    for (size_t i = 0; i < stored_chain.strikes.size(); ++i) {
        if (stored_chain.strikes[i].ce_token != 0) {
            token_to_strike_index_[stored_chain.strikes[i].ce_token] = {symbol, i};
        }
        if (stored_chain.strikes[i].pe_token != 0) {
            token_to_strike_index_[stored_chain.strikes[i].pe_token] = {symbol, i};
        }
    }
}

void ChainBuilder::update_spot_price(const std::string& symbol, double new_spot, uint32_t volume, uint32_t oi) {
    std::unique_lock lock(rw_mutex_);
    auto it = chains_.find(symbol);
    if (it != chains_.end()) {
        it->second.fut_ltp = new_spot;
        // Immediately recalculate greeks when spot changes
        recalculate_greeks(symbol);
    }
}

void ChainBuilder::update_option_price(int token, double new_price, uint32_t volume, uint32_t oi) {
    std::unique_lock lock(rw_mutex_);
    
    auto it = token_to_strike_index_.find(token);
    if (it == token_to_strike_index_.end()) return; // Token not in any active chain

    const std::string& symbol = it->second.first;
    int strike_index = it->second.second;

    auto& chain = chains_[symbol];
    auto& strike_row = chain.strikes[strike_index];

    // Update the specific CE or PE price
    if (strike_row.ce_token == token) {
        strike_row.ce_ltp = new_price;
        // Note: For ultra-low latency, we don't recalculate Greeks on EVERY option tick,
        // we let a dedicated thread sweep it, OR we only recalculate this specific strike.
    } else if (strike_row.pe_token == token) {
        strike_row.pe_ltp = new_price;
    }

    // 🚀 ULTRA-ROBUST SYNTHETIC SPOT: Update continuously on every option tick
    if (strike_row.ce_ltp > 0.0 && strike_row.pe_ltp > 0.0) {
        chain.synthetic_spot = strike_row.strike + strike_row.ce_ltp - strike_row.pe_ltp;
    }
    
    if (chain.dte <= 0.0) chain.dte = 7.0 / 365.0; // Fail-safe DTE
    double effective_spot = chain.fut_ltp > 0.0 ? chain.fut_ltp : chain.synthetic_spot;

    if (effective_spot > 0.0) {
        if (strike_row.ce_ltp > 0.0) {
            strike_row.ce_greeks = greeks::calculate_all_greeks('c', strike_row.strike, effective_spot, chain.dte, strike_row.ce_ltp, 0.0);
        }
        if (strike_row.pe_ltp > 0.0) {
            strike_row.pe_greeks = greeks::calculate_all_greeks('p', strike_row.strike, effective_spot, chain.dte, strike_row.pe_ltp, 0.0);
        }
    }
}

void ChainBuilder::recalculate_greeks(const std::string& symbol) {
    // Note: Mutex should already be held by caller (unique_lock)
    auto it = chains_.find(symbol);
    if (it == chains_.end()) return;

    auto& chain = it->second;
    if (chain.fut_ltp <= 0.0 || chain.dte <= 0.0) return;

    double effective_spot = chain.synthetic_spot > 0 ? chain.synthetic_spot : chain.fut_ltp;
    double risk_free_rate = 0.0;

    for (auto& row : chain.strikes) {
        if (row.ce_ltp > 0.0) {
            row.ce_greeks = greeks::calculate_all_greeks('c', row.strike, effective_spot, chain.dte, row.ce_ltp, risk_free_rate);
        }
        if (row.pe_ltp > 0.0) {
            row.pe_greeks = greeks::calculate_all_greeks('p', row.strike, effective_spot, chain.dte, row.pe_ltp, risk_free_rate);
        }
    }
}

bool ChainBuilder::get_chain(const std::string& symbol, OptionChain& out_chain) {
    std::shared_lock lock(rw_mutex_);
    auto it = chains_.find(symbol);
    if (it != chains_.end()) {
        // Because OptionChain vectors are relatively small (~20-40 strikes), 
        // copying here is acceptable for a snapshot, but passing by ref/pointer 
        // to a SHM writer is faster.
        out_chain = it->second;
        return true;
    }
    return false;
}

} // namespace decoder