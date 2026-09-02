#include "shm/market_state_shm.hpp"
#include <iostream>
#include <vector>
#include <chrono>
#include <thread>
#include <csignal>
#include <iomanip>

bool running = true;

void signal_handler(int signum) {
    std::cout << "\nInterrupt signal (" << signum << ") received. Shutting down trade-worker...\n";
    running = false;
}

int main() {
    signal(SIGINT, signal_handler);

    trading::shm::MarketStateReader reader;

    // Wait a moment for the writer to start
    std::this_thread::sleep_for(std::chrono::seconds(1));

    if (!reader.open()) {
        return 1;
    }

    std::vector<uint32_t> tokens_to_read = {26000, 26001, 3045};

    std::cout << "Starting trade-worker. Press Ctrl+C to exit." << std::endl;
    std::cout << "Reading updates from shared memory..." << std::endl;

    while (running) {
        std::cout << "\r" << std::fixed << std::setprecision(2);

        for (const auto& token : tokens_to_read) {
            const trading::shm::InstrumentState* state = reader.get_instrument_state(token);
            if (state) {
                double ltp = state->last_traded_price.load(std::memory_order_acquire);
                std::cout << "Token " << token << ": " << ltp << "   ";
            } else {
                std::cout << "Token " << token << ": Not Found   ";
            }
        }
        std::cout << std::flush;

        std::this_thread::sleep_for(std::chrono::milliseconds(250));
    }

    reader.close();
    std::cout << "\nTrade-worker shut down." << std::endl;
    return 0;
}
