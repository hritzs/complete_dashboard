#include <iostream>
#include <atomic>
#include <thread>
#include <string>

// Use Unity Build approach (include cpp directly) for aggressive compiler inlining
#include "intent_builder.cpp"

class WorkerLoop {
private:
    std::atomic<bool> running{false};
    std::string trade_uid;
    std::string instrument_token;
    double trigger_price;
    int quantity;
    IntentBuilder& publisher; 

public:
    WorkerLoop(const std::string& uid, const std::string& token, double trigger, int qty, IntentBuilder& pub)
        : trade_uid(uid), instrument_token(token), trigger_price(trigger), quantity(qty), publisher(pub) {}

    void start() {
        running = true;
        std::cout << "[WorkerLoop] Starting ultra-low latency spin-wait loop for " << trade_uid << "\n";
        std::cout << "[WorkerLoop] Waiting for " << instrument_token << " to cross price: " << trigger_price << "\n";

        int mock_ticks = 0;

        while(running.load(std::memory_order_relaxed)) {
            // 1. In production: Read from Shared Memory Array (Lock-Free)
            // double current_price = shm->get_price(instrument_token);
            
            // Mocking a price jump after ~1 million loops
            mock_ticks++;
            double current_price = (mock_ticks > 1000000) ? (trigger_price + 1.0) : (trigger_price - 10.0);

            // 2. Strategy Logic: If price crosses threshold
            if (current_price >= trigger_price) {
                std::cout << "[WorkerLoop] Threshold crossed! Fast Path Execution Triggered.\n";
                
                // 3. Fire OrderIntent via ZMQ immediately
                publisher.emit_intent(trade_uid, instrument_token, "BUY", quantity, current_price);
                
                running = false;
                break;
            }
        }
    }

    void stop() { running = false; }
};