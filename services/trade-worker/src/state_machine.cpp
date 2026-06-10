#include "TradeStateMachine.hpp"
#include <iostream>

namespace trading {

TradeStateMachine::TradeStateMachine(std::string trade_uid, IntentBuilder builder)
    : m_trade_uid(std::move(trade_uid)), 
      m_state(TradeState::INIT), 
      m_builder(std::move(builder)), 
      m_current_chunk_idx(0), 
      m_chase_attempts(0) {}

void TradeStateMachine::on_command_deploy(int target_lots, double ce_ltp, double pe_ltp, double ce_delta, double pe_delta, int ce_token, int pe_token) {
    if (m_state != TradeState::INIT) return;

    std::cout << "[TradeStateMachine] Deploying Delta-Neutral Straddle: " << m_trade_uid << "\n";
    m_state = TradeState::BUILDING;

    // 1. Calculate Delta Neutral Quantities
    auto allocation = m_builder.calculate_delta_neutral(ce_delta, pe_delta, target_lots);
    
    // 2. Slice into chunks
    m_chunks = m_builder.generate_chunked_orders(
        m_trade_uid, 
        ce_token, allocation.ce_lots, ce_ltp,
        pe_token, allocation.pe_lots, pe_ltp, 
        "SELL"
    );

    m_current_chunk_idx = 0;
    execute_current_chunk();
}

void TradeStateMachine::execute_current_chunk() {
    if (m_current_chunk_idx >= m_chunks.size()) {
        std::cout << "[TradeStateMachine] All chunks executed. Trade is ACTIVE.\n";
        m_state = TradeState::ACTIVE;
        return;
    }

    m_state = TradeState::CHASING;
    m_chase_attempts = 0;
    m_pending_intents.clear();
    m_chunk_start_time = std::chrono::steady_clock::now();

    std::cout << "[TradeStateMachine] Executing Chunk " << (m_current_chunk_idx + 1) << "/" << m_chunks.size() << "\n";
    
    const auto& current_chunk = m_chunks[m_current_chunk_idx];
    for (const auto& intent : current_chunk) {
        m_pending_intents[intent.uid] = intent;
        emit_intent_to_gateway(intent, false); // place order
    }
}

void TradeStateMachine::on_order_update(const std::string& intent_id, const std::string& status, int filled_qty, const std::string& exchange_order_id) {
    if (m_pending_intents.find(intent_id) == m_pending_intents.end()) return;

    // Track the exchange order ID so we can modify it if it gets stuck
    if (!exchange_order_id.empty()) {
        m_exchange_order_ids[intent_id] = exchange_order_id;
    }

    if (status == "FILLED" || status == "COMPLETE") {
        std::cout << "[TradeStateMachine] Intent " << intent_id << " filled.\n";
        m_pending_intents.erase(intent_id);
    } 
    else if (status == "REJECTED" || status == "CANCELLED") {
        std::cout << "[TradeStateMachine] Intent " << intent_id << " failed (" << status << "). Triggering abort/recovery.\n";
        handle_fatal_rejection(intent_id, status);
    }

    // If all intents in the current chunk are filled, move to the next chunk
    if (m_pending_intents.empty() && m_state == TradeState::CHASING) {
        m_current_chunk_idx++;
        execute_current_chunk();
    }
}

void TradeStateMachine::on_timer_tick() {
    if (m_state != TradeState::CHASING) return;

    auto now = std::chrono::steady_clock::now();
    auto elapsed_ms = std::chrono::duration_cast<std::chrono::milliseconds>(now - m_chunk_start_time).count();

    // Timeout logic: If 1500ms have passed and chunk is not filled, escalate limit buffer
    if (elapsed_ms > 1500) {
        escalate_and_chase();
    }
}

void TradeStateMachine::escalate_and_chase() {
    m_chase_attempts++;
    if (m_chase_attempts > 3) {
        std::cout << "[TradeStateMachine] Max chase attempts reached. Fallback to sweeping (market orders).\n";
        // Implement Sweeping logic here
        return;
    }

    std::cout << "[TradeStateMachine] Chasing pending orders (Attempt " << m_chase_attempts << "). Escalating buffer.\n";

    for (auto& pair : m_pending_intents) {
        auto& intent = pair.second;
        
        // Escalate the tick buffer (e.g., 2 -> 4 -> 6)
        intent.limit_order_buffer_ticks += 2; 
        
        // Request modification from Go Gateway
        std::string exch_id = m_exchange_order_ids[intent.uid];
        if (!exch_id.empty()) {
            emit_intent_to_gateway(intent, true, exch_id); // true = modify
        }
    }
    
    // Reset the timer for the next chase cycle
    m_chunk_start_time = std::chrono::steady_clock::now();
}

void TradeStateMachine::handle_fatal_rejection(const std::string& intent_id, const std::string& status) {
    if (m_state == TradeState::SQUARING_OFF) {
        std::cout << "[TradeStateMachine] CRITICAL: Rejection during SQUARING_OFF for intent " << intent_id << ". Entering panic sweep.\n";
        // In SQUARING_OFF, we must close. Escalate immediately to market order or max limit.
        auto intent = m_pending_intents[intent_id];
        // Generate a new intent ID to prevent exchange rejection of duplicate IDs
        intent.uid = intent.uid + "_RTRY"; 
        intent.limit_order_buffer_ticks += 10; // Massive buffer to simulate market order
        
        m_pending_intents.erase(intent_id);
        m_pending_intents[intent.uid] = intent;
        emit_intent_to_gateway(intent, false); // Resend as new order
    } else {
        std::cout << "[TradeStateMachine] Aborting trade build due to rejection on intent " << intent_id << "\n";
        m_state = TradeState::CLOSED; // Simplification: move to closed or recovery state
        m_pending_intents.clear();
    }
}

void TradeStateMachine::perform_risk_checks(const shm::MarketStateBlock* market_data) {
    if (m_state != TradeState::ACTIVE) return;
    if (!market_data) return;

    // Example: Assuming we saved the tokens we are trading during the BUILDING phase
    // int ce_token = ...; 
    // int pe_token = ...;
    
    // Instant O(1) lock-free read from Shared Memory! No JSON, no ZMQ parsing, no Python GIL.
    // double current_ce_price = market_data->ticks[ce_token].ltp.load(std::memory_order_relaxed);
    // double current_pe_price = market_data->ticks[pe_token].ltp.load(std::memory_order_relaxed);

    // Calculate live PnL here...
    // if (live_pnl <= m_sl_threshold) {
    //     std::cout << "[TradeStateMachine] SL HIT! Moving to SQUARING_OFF.\n";
    //     m_state = TradeState::SQUARING_OFF;
    // }
}

void TradeStateMachine::emit_intent_to_gateway(const OrderIntent& intent, bool is_modify, const std::string& exchange_order_id) {
    // In production, build a fast JSON string or Protobuf
    std::string payload = "{"
        "\"trade_uid\":\"" + m_trade_uid + "\","
        "\"intent_id\":\"" + intent.uid + "\","
        "\"token\":" + std::to_string(intent.token) + ","
        "\"action\":\"" + intent.action + "\","
        "\"option_type\":\"" + intent.option_type + "\","
        "\"quantity\":" + std::to_string(intent.quantity) + ","
        "\"limit_price\":" + std::to_string(intent.limit_price) + ","
        "\"limit_order_buffer_ticks\":" + std::to_string(intent.limit_order_buffer_ticks) + ","
        "\"is_modify\":" + (is_modify ? "true" : "false") + ","
        "\"exchange_order_id\":\"" + exchange_order_id + "\""
        "}";

    // Emit via ZMQ to Go Execution Gateway
    std::cout << "[ZMQ -> Gateway] " << payload << "\n";
}

} // namespace trading