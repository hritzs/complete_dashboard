#include "../include/worker_loop.hpp"
#include <iostream>
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

    // Example: Booting up a NIFTY Short Straddle with 50 quantity
    TradeWorker worker("TRD_NIFTY_001", "NIFTY", 50);
    global_worker = &worker;

    worker.init_shm();
    worker.init_zmq();
    worker.run(); // Blocks until stopped

    return 0;
}