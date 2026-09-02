#pragma once

#include <atomic>
#include <string>
#include <zmq.hpp>
#include <memory>
#include "TradeStateMachine.hpp"

// Mock definitions for SHM structures based on typical Options Chain data
// Ensure this matches the exact memory layout written by feed-decoder/market-state
struct OptionNode {
    int strike;
    int ce_token;
    int pe_token;
    double ce_delta;
    double pe_delta;
};

struct ChainSHMData {
    double fut_ltp;
    int atm_strike;
    int num_strikes;
    OptionNode nodes[100]; // Fixed bounded size for SHM safety
};

class TradeWorker {
public:
    TradeWorker(const std::string& trade_id, const std::string& symbol, int lot_qty);
    ~TradeWorker();

    // Initialization routines
    void init_shm();
    void init_zmq();
    
    // Core Execution routines
    void run();
    void stop();
    void pin_thread_to_core(int core_id);

private:
    void send_order_intent(const std::string& intent_id, int instrument_id, const std::string& side, int qty, double limit_price);

    std::string trade_id_;
    std::string symbol_;
    int lot_qty_;
    std::atomic<bool> running_{false};

    // ZeroMQ
    zmq::context_t zmq_ctx_;
    zmq::socket_t zmq_push_sock_;
    
    std::unique_ptr<trading::TradeStateMachine> state_machine_;

    // Shared Memory Pointers
    int price_fd_{-1};
    int chain_fd_{-1};
    double* prices_{nullptr};
    ChainSHMData* chain_data_{nullptr};
};