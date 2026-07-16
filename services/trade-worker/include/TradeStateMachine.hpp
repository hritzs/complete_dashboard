#pragma once

#include "IntentBuilder.hpp"
#include "shm/market_state.hpp"
#include <string>
#include <unordered_map>
#include <vector>
#include <chrono>
#include <functional>

namespace trading {

enum class TradeState {
    INIT,
    BUILDING,
    CHASING,
    ACTIVE,
    SQUARING_OFF,
    CLOSED
};

// Minimal snapshot struct for HedgeMonitor
struct HedgeSnapshot {
    bool   valid;
    double pts_out;
    double points_allowed;
    double net_delta;
    int    atm_strike;
};

class TradeStateMachine {
public:
    // emit_func: function that sends serialized intents to Go gateway over ZMQ
    TradeStateMachine(const std::string& trade_uid,
                      const IntentBuilder& builder,
                      std::function<void(const std::string&)> emit_func);

    // External events from control layer / Go
    // target_lots: baseline lots; builder will apply delta-neutral if needed
    void on_command_deploy(int    target_lots,
                           double ce_ltp,
                           double pe_ltp,
                           double ce_delta,
                           double pe_delta,
                           int    ce_token,
                           int    pe_token);

    // Order updates coming back from gateway/broker
    void on_order_update(const std::string& intent_id,
                         const std::string& status,
                         int                filled_qty,
                         const std::string& exchange_order_id);

    // Called frequently (e.g., every 100ms) by the worker loop
    void on_timer_tick();

    // Reads directly from SHM (market_state.hpp) for live risk checks
    void perform_risk_checks(const shm::MarketStateBlock* market_data);

    // Getter
    TradeState get_state() const { return m_state; }

    // Helpers for HedgeMonitor
    HedgeSnapshot get_snapshot(const std::string& trade_uid) const;
    void save_hedge_params(const std::string& trade_uid,
                           double net_delta,
                           double target_delta_reduction,
                           int atm_strike);
    void emit_event(const std::string& event_type,
                    const std::string& trade_uid,
                    int priority);

private:
    void execute_current_chunk();
    void escalate_and_chase();
    void handle_fatal_rejection(const std::string& intent_id,
                                const std::string& status);
    void emit_intent_to_gateway(const OrderIntent& intent,
                                bool               is_modify,
                                const std::string& exchange_order_id = "");

    std::string m_trade_uid;
    TradeState  m_state;
    std::function<void(const std::string&)> m_emit_func;
    IntentBuilder m_builder;

    // Execution state
    std::vector<OrderChunk> m_chunks;
    std::size_t             m_current_chunk_idx;

    std::unordered_map<std::string, OrderIntent> m_pending_intents;
    std::unordered_map<std::string, std::string> m_exchange_order_ids;

    std::chrono::steady_clock::time_point m_chunk_start_time;
    int                                   m_chase_attempts;
};

} // namespace trading