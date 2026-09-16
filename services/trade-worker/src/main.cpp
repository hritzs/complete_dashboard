#include "../include/worker_loop.hpp"
#include <iostream>
#include <cstdlib>
#include <csignal>

TradeWorker* global_worker = nullptr;

void signal_handler(int signal) {
    if (global_worker) {
        std::cout << "\n[Main] Gracefully shutting down Trade Worker..." << std::endl;
        global_worker->stop();
    }
}

int main(int argc, char** argv) {
    std::signal(SIGINT, signal_handler);
    std::signal(SIGTERM, signal_handler);

    std::cout << "========================================\n";
    std::cout << "  Starting C++ Trade Worker (Phase 3)  \n";
    std::cout << "========================================\n";

    // A trade must be specified explicitly via CLI args -- this used to
    // hardcode a live NIFTY 50-qty straddle ("TRD_NIFTY_001") that ran
    // unconditionally on every launch, meaning simply starting the
    // platform (e.g. via start_platform.sh) would attempt to place a
    // real trade with no user action. It also called init_shm() without
    // checking whether prices_shm/chain_shm actually mapped (nothing
    // currently writes them), so it ran on unmapped/garbage memory.
    // Refusing to run without explicit args removes both problems: no
    // implicit trade on startup, and no attempt to run the hot loop
    // against memory that was never actually attached.
    if (argc < 4) {
        std::cerr << "Usage: " << argv[0] << " <trade_id> <symbol> <quantity>\n";
        std::cerr << "No trade specified -- exiting without starting a worker.\n";
        return 1;
    }

    std::string trade_id = argv[1];
    std::string symbol = argv[2];
    int quantity = std::atoi(argv[3]);
    if (quantity <= 0) {
        std::cerr << "Invalid quantity: " << argv[3] << "\n";
        return 1;
    }

    TradeWorker worker(trade_id, symbol, quantity);
    global_worker = &worker;

    worker.init_shm();
    worker.init_zmq();
    worker.run(); // Blocks until stopped

    return 0;
}