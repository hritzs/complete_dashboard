#include "TradeStateMachine.hpp"
#include <iostream>
#include <sstream>

namespace trading {

TradeStateMachine::TradeStateMachine(const std::string& trade_uid,
                                     const IntentBuilder& builder,
                                     std::function<void(const std::string&)> emit_func)
    : m_trade_uid(trade_uid),
      m_state(TradeState::INIT),
      m_emit_func(std::move(emit_func)),
      m_builder(builder),
      m_current_chunk_idx(0),
      m_chase_attempts(0) {}

// Called when control layer wants to deploy a straddle
void TradeStateMachine::on_command_deploy(int    target_lots,
                                          double ce_ltp,
                                          double pe_ltp,
                                          double ce_delta,
                                          double pe_delta,
                                          int    ce_token,
                                          int    pe_token) {
    std::cout << "[TradeStateMachine] on_command_deploy for " << m_trade_uid
              << " lots=" << target_lots << std::endl;

    // Delta-neutral allocation using IntentBuilder
    AllocationResult alloc = m_builder.calculate_delta_neutral(
        ce_delta,
        pe_delta,
        target_lots
    );

    std::cout << "[TradeStateMachine] Delta-neutral allocation: "
              << "CE lots=" << alloc.ce_lots
              << " PE lots=" << alloc.pe_lots
              << " CE qty=" << alloc.ce_quantity
              << " PE qty=" << alloc.pe_quantity
              << " net_delta=" << alloc.net_delta
              << std::endl;

    // Build chunked orders
    m_chunks = m_builder.generate_chunked_orders(
        m_trade_uid,
        ce_token,
        alloc.ce_lots,
        ce_ltp,
        pe_token,
        alloc.pe_lots,
        pe_ltp,
        "SELL"
    );

    m_current_chunk_idx = 0;
    m_chase_attempts    = 0;

    if (m_chunks.empty()) {
        std::cerr << "[TradeStateMachine] No chunks generated for "
                  << m_trade_uid << std::endl;
        m_state = TradeState::CLOSED;
        return;
    }

    m_state = TradeState::BUILDING;
    m_chunk_start_time = std::chrono::steady_clock::now();

    execute_current_chunk();
}

// Called when an order update arrives from gateway/broker
void TradeStateMachine::on_order_update(const std::string& intent_id,
                                        const std::string& status,
                                        int                filled_qty,
                                        const std::string& exchange_order_id) {
    auto it = m_pending_intents.find(intent_id);
    if (it == m_pending_intents.end()) {
        std::cout << "[TradeStateMachine] Unknown intent_id update: "
                  << intent_id << std::endl;
        return;
    }

    std::cout << "[TradeStateMachine] Order update for " << intent_id
              << " status=" << status
              << " filled=" << filled_qty << std::endl;

    if (!exchange_order_id.empty()) {
        m_exchange_order_ids[intent_id] = exchange_order_id;
    }

    if (status == "FILLED" || status == "COMPLETE") {
        m_pending_intents.erase(it);
        if (m_pending_intents.empty()) {
            // move to next chunk
            m_current_chunk_idx++;
            if (m_current_chunk_idx >= m_chunks.size()) {
                m_state = TradeState::ACTIVE;
                std::cout << "[TradeStateMachine] All chunks filled. "
                          << "Trade ACTIVE: " << m_trade_uid << std::endl;
            } else {
                m_state = TradeState::BUILDING;
                m_chunk_start_time = std::chrono::steady_clock::now();
                execute_current_chunk();
            }
        }
    } else if (status == "REJECTED" || status == "CANCELED") {
        handle_fatal_rejection(intent_id, status);
    } else {
        // other statuses: keep pending
    }
}

// Called frequently by worker loop
void TradeStateMachine::on_timer_tick() {
    if (m_state != TradeState::BUILDING &&
        m_state != TradeState::CHASING) {
        return;
    }

    auto now   = std::chrono::steady_clock::now();
    double elapsed = std::chrono::duration<double>(now - m_chunk_start_time).count();

    const double chase_timeout_sec = 3.0;
    if (elapsed > chase_timeout_sec && !m_pending_intents.empty()) {
        std::cout << "[TradeStateMachine] Chunk timeout for " << m_trade_uid
                  << ", escalating and chasing.\n";
        escalate_and_chase();
    }
}

// Risk checks reading SHM (placeholder)
void TradeStateMachine::perform_risk_checks(const shm::MarketStateBlock* market_data) {
    if (!market_data) return;
    // TODO: implement when you integrate market_state.hpp
}

// Send all intents in current chunk
void TradeStateMachine::execute_current_chunk() {
    if (m_current_chunk_idx >= m_chunks.size()) {
        return;
    }

    const auto& chunk = m_chunks[m_current_chunk_idx];
    m_pending_intents.clear();

    std::cout << "[TradeStateMachine] Executing chunk "
              << m_current_chunk_idx + 1 << "/" << m_chunks.size()
              << " for " << m_trade_uid
              << " (" << chunk.size() << " intents)\n";

    for (const auto& intent : chunk) {
        m_pending_intents[intent.uid] = intent;
        emit_intent_to_gateway(intent, /*is_modify*/ false);
    }

    m_chunk_start_time = std::chrono::steady_clock::now();
}

// Adjust prices/buffer and re-emit pending intents
void TradeStateMachine::escalate_and_chase() {
    m_state = TradeState::CHASING;
    m_chase_attempts++;

    double buffer_multiplier = 1.0 + 0.2 * m_chase_attempts;

    std::cout << "[TradeStateMachine] Chase attempt " << m_chase_attempts
              << " for " << m_trade_uid
              << " buffer_multiplier=" << buffer_multiplier << std::endl;

    for (auto& kv : m_pending_intents) {
        OrderIntent& intent = kv.second;
        intent.limit_order_buffer_ticks =
            static_cast<int>(intent.limit_order_buffer_ticks * buffer_multiplier);

        auto ex_it = m_exchange_order_ids.find(intent.uid);
        std::string ex_order_id = (ex_it != m_exchange_order_ids.end())
                                  ? ex_it->second
                                  : "";

        emit_intent_to_gateway(intent,
                               /*is_modify*/ !ex_order_id.empty(),
                               ex_order_id);
    }

    m_chunk_start_time = std::chrono::steady_clock::now();
}

void TradeStateMachine::handle_fatal_rejection(const std::string& intent_id,
                                               const std::string& status) {
    std::cerr << "[TradeStateMachine] Fatal rejection for " << m_trade_uid
              << " intent=" << intent_id
              << " status=" << status << std::endl;
    m_state = TradeState::CLOSED;
    m_pending_intents.clear();
}

// Serialize intent as a simple string and send to Go gateway via emit_func
void TradeStateMachine::emit_intent_to_gateway(const OrderIntent& intent,
                                               bool               is_modify,
                                               const std::string& exchange_order_id) {
    std::ostringstream oss;
    oss << m_trade_uid << "|"
        << intent.uid << "|"
        << intent.token << "|"
        << intent.option_type << "|"
        << intent.action << "|"
        << intent.quantity << "|"
        << intent.limit_price << "|"
        << intent.limit_order_buffer_ticks << "|"
        << (is_modify ? "1" : "0") << "|"
        << exchange_order_id;

    m_emit_func(oss.str());
}

// Hedge helpers (stubs for now)

HedgeSnapshot TradeStateMachine::get_snapshot(const std::string& trade_uid) const {
    HedgeSnapshot s{};
    s.valid          = false;
    s.pts_out        = 0.0;
    s.points_allowed = 0.0;
    s.net_delta      = 0.0;
    s.atm_strike     = 0;
    std::cout << "[TradeStateMachine] get_snapshot stub for "
              << trade_uid << std::endl;
    return s;
}

void TradeStateMachine::save_hedge_params(const std::string& trade_uid,
                                          double net_delta,
                                          double target_delta_reduction,
                                          int atm_strike) {
    std::cout << "[TradeStateMachine] save_hedge_params for " << trade_uid
              << " net_delta=" << net_delta
              << " target_delta_reduction=" << target_delta_reduction
              << " atm_strike=" << atm_strike
              << std::endl;
}

void TradeStateMachine::emit_event(const std::string& event_type,
                                   const std::string& trade_uid,
                                   int priority) {
    std::cout << "[TradeStateMachine] emit_event type=" << event_type
              << " trade_uid=" << trade_uid
              << " priority=" << priority << std::endl;
}

} // namespace trading