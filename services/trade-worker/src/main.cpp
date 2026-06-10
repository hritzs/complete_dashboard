#include <iostream>
#include <string>
#include <thread>
#include <atomic>
#include <zmq.h>
#include <chrono>
#include <unordered_map>
#include <cmath>

std::atomic<bool> running{true};
std::unordered_map<std::string, std::string> latest_chains;

// Fast, allocation-free JSON string extractor
std::string extract_value(const std::string& json, const std::string& key, bool is_string) {
    std::string search = "\"" + key + "\":";
    if (is_string) search += "\"";
    size_t pos = json.find(search);
    if (pos == std::string::npos) return "";
    pos += search.length();
    size_t end = json.find(is_string ? "\"" : ",", pos);
    if (!is_string && end == std::string::npos) end = json.find_first_of("}]", pos);
    if (end == std::string::npos) return "";
    return json.substr(pos, end - pos);
}

double safe_stod(const std::string& s) {
    if (s.empty()) return 0.0;
    try { return std::stod(s); } catch(...) { return 0.0; }
}

int safe_stoi(const std::string& s) {
    if (s.empty()) return 0;
    try { return std::stoi(s); } catch(...) { return 0; }
}

void worker_loop() {
    void* context = zmq_ctx_new();

    // 1. Subscribe to Verified Fills from Go Reconciler
    void* fills_sub = zmq_socket(context, ZMQ_SUB);
    zmq_connect(fills_sub, "tcp://127.0.0.1:5564");
    zmq_setsockopt(fills_sub, ZMQ_SUBSCRIBE, "FILLS", 5);

    // 1.2 Subscribe to Go Control API Commands
    void* cmd_sub = zmq_socket(context, ZMQ_SUB);
    zmq_connect(cmd_sub, "tcp://127.0.0.1:5570");
    zmq_setsockopt(cmd_sub, ZMQ_SUBSCRIBE, "TRADE_CMD", 9);

    // 1.5 Subscribe to Live Option Chain & Greeks from C++ Feed Decoder
    void* greeks_sub = zmq_socket(context, ZMQ_SUB);
    zmq_connect(greeks_sub, "tcp://127.0.0.1:5556");
    zmq_setsockopt(greeks_sub, ZMQ_SUBSCRIBE, "", 0);

    // 2. Connect Publisher to Go Execution Gateway Hot-Path
    void* exec_pub = zmq_socket(context, ZMQ_PUB);
    zmq_connect(exec_pub, "ipc:///tmp/order_intents.ipc");

    std::cout << "[Trade Worker] 🚀 Started Ultra-Low Latency Trading Engine.\n";
    std::cout << "[Trade Worker] 🎧 Listening for Verified Fills on tcp://127.0.0.1:5564\n";
    std::cout << "[Trade Worker] 🎧 Listening for Trade Commands on tcp://127.0.0.1:5570\n";
    std::cout << "[Trade Worker] 🧠 Listening for C++ Greeks on tcp://127.0.0.1:5556\n";
    std::cout << "[Trade Worker] 🔫 Armed to fire OrderIntents on ipc:///tmp/order_intents.ipc\n";

    char buffer[2048];
    char greeks_buf[65536];
    while (running) {
        // Check for new fills from Reconciler (Non-blocking)
        int bytes = zmq_recv(fills_sub, buffer, sizeof(buffer) - 1, ZMQ_DONTWAIT);
        if (bytes > 0) {
            buffer[bytes] = '\0';
            std::string msg(buffer);
            std::cout << "[Trade Worker] 📥 Canonical Fill Received: " << msg << "\n";

            // TODO: In Phase 4.1, read C++ Greeks from SHM here.
            // TODO: Evaluate Stop-Loss & Delta-Neutral Hedge logic.
            
            // Example of firing an instant automated order:
            // std::string intent = R"({"trade_uid":"auto_hedge_1", "symbol":"NIFTY", "exchange_segment":"NSEFO", "quantity":25, "order_type":"MARKET"})";
            // zmq_send(exec_pub, intent.c_str(), intent.size(), 0);
        }

        // Check for new Commands from Go Control API (Non-blocking)
        int cmd_bytes = zmq_recv(cmd_sub, buffer, sizeof(buffer) - 1, ZMQ_DONTWAIT);
        if (cmd_bytes > 0) {
            // Go's ZMQ publisher sends Multipart messages (Topic + Payload)
            int more;
            size_t more_size = sizeof(more);
            zmq_getsockopt(cmd_sub, ZMQ_RCVMORE, &more, &more_size);
            
            if (more) {
                int payload_bytes = zmq_recv(cmd_sub, buffer, sizeof(buffer) - 1, 0);
                if (payload_bytes > 0) {
                    buffer[payload_bytes] = '\0';
                    std::string payload(buffer);
                    std::cout << "\n========================================================\n";
                    std::cout << "⚡ [Trade Worker] RECEIVED STRADDLE COMMAND:\n";
                    std::cout << payload << "\n";
                    std::cout << "========================================================\n";
                    std::cout << "   -> Reading Market State SHM for Greeks...\n";
                    std::cout << "   -> Calculating Delta-Neutral Quantities...\n";

                    std::string trade_id = extract_value(payload, "trade_id", true);
                    std::string symbol = extract_value(payload, "symbol", true);
                    int lots = safe_stoi(extract_value(payload, "lots", false));
                    bool delta_neutral = extract_value(payload, "delta_neutral", false) == "true";

                    std::string chain = latest_chains[symbol];
                    if (chain.empty()) {
                        std::cout << "❌ [Trade Worker] No Greeks cached for " << symbol << " yet! Ensure Feed Decoder is running.\n";
                    } else {
                        size_t atm_pos = chain.find("\"is_atm\":true");
                        if (atm_pos != std::string::npos) {
                            size_t row_start = chain.rfind("{", atm_pos);
                            size_t row_end = chain.find("}", atm_pos);
                            std::string atm_row = chain.substr(row_start, row_end - row_start + 1);

                            double ce_delta = safe_stod(extract_value(atm_row, "ce_delta", false));
                            double pe_delta = safe_stod(extract_value(atm_row, "pe_delta", false));
                            int strike = safe_stoi(extract_value(atm_row, "strike", false));
                            int lot_size = safe_stoi(extract_value(chain, "lot_size", false));

                            int ce_lots = lots, pe_lots = lots;
                            if (delta_neutral && lot_size > 0 && ce_delta != 0 && pe_delta != 0) {
                                double total_delta = std::abs(ce_delta) + std::abs(pe_delta);
                                double target_contracts = lots * lot_size;
                                ce_lots = std::round((target_contracts * (std::abs(pe_delta) / total_delta) * 2) / lot_size);
                                pe_lots = std::round((target_contracts * (std::abs(ce_delta) / total_delta) * 2) / lot_size);
                            }

                            std::string ce_token = extract_value(atm_row, "ce_token", false);
                            std::string pe_token = extract_value(atm_row, "pe_token", false);

                            std::string ce_intent = R"({"trade_uid":")" + trade_id + R"(", "symbol":")" + symbol + R"(", "action":"SELL", "token":)" + ce_token + R"(, "quantity":)" + std::to_string(ce_lots * lot_size) + R"(, "exchange_segment":"NSEFO"})";
                            std::string pe_intent = R"({"trade_uid":")" + trade_id + R"(", "symbol":")" + symbol + R"(", "action":"SELL", "token":)" + pe_token + R"(, "quantity":)" + std::to_string(pe_lots * lot_size) + R"(, "exchange_segment":"NSEFO"})";

                            zmq_send(exec_pub, ce_intent.c_str(), ce_intent.size(), 0);
                            zmq_send(exec_pub, pe_intent.c_str(), pe_intent.size(), 0);
                            std::cout << "🚀 [Trade Worker] Fired CE Intent: " << ce_intent << "\n";
                            std::cout << "🚀 [Trade Worker] Fired PE Intent: " << pe_intent << "\n";
                        }
                    }
                }
            }
        }

        // Check for Live Greeks from Feed Decoder and DRAIN THE QUEUE (Non-blocking)
        while (true) {
            int g_bytes = zmq_recv(greeks_sub, greeks_buf, sizeof(greeks_buf) - 1, ZMQ_DONTWAIT);
            if (g_bytes > 0) {
                greeks_buf[g_bytes] = '\0';
                std::string payload(greeks_buf);
                std::string sym = extract_value(payload, "symbol", true);
                if (!sym.empty()) {
                    latest_chains[sym] = payload;
                }
            } else {
                break; // Queue fully drained, we have the freshest tick!
            }
        }

        std::this_thread::sleep_for(std::chrono::microseconds(100)); // Prevent 100% CPU lock
    }

    zmq_close(fills_sub);
    zmq_close(greeks_sub);
    zmq_close(cmd_sub);
    zmq_close(exec_pub);
    zmq_ctx_destroy(context);
}

int main() {
    worker_loop();
    return 0;
}