#pragma once

#include "IntentBuilder.hpp"
#include "shm/market_state.hpp"
#include <string>
#include <unordered_map>
#include <vector>
#include <chrono>

namespace trading {

enum class TradeState {
    INIT,
    BUILDING,
    CHASING,
    ACTIVE,
    SQUARING_OFF,
    CLOSED
};

class TradeStateMachine {
public:
    TradeStateMachine(std::string trade_uid, IntentBuilder builder);

    // External Events
    void on_command_deploy(int target_lots, double ce_ltp, double pe_ltp, double ce_delta, double pe_delta, int ce_token, int pe_token);
    void on_order_update(const std::string& intent_id, const std::string& status, int filled_qty, const std::string& exchange_order_id);
    void on_timer_tick(); // Called frequently (e.g., every 100ms) by the ZMQ poller
    void perform_risk_checks(const shm::MarketStateBlock* market_data); // Reads directly from SHM

    // Getters
    TradeState get_state() const { return m_state; }

private:
    void execute_current_chunk();
    void escalate_and_chase();
    void handle_fatal_rejection(const std::string& intent_id, const std::string& status);
    void emit_intent_to_gateway(const OrderIntent& intent, bool is_modify, const std::string& exchange_order_id = "");

    std::string m_trade_uid;
    TradeState m_state;
    IntentBuilder m_builder;

    // Execution State
    std::vector<OrderChunk> m_chunks;
    size_t m_current_chunk_idx;
    
    std::unordered_map<std::string, OrderIntent> m_pending_intents;
    std::unordered_map<std::string, std::string> m_exchange_order_ids;
    std::chrono::steady_clock::time_point m_chunk_start_time;
    int m_chase_attempts;
};

} // namespace trading