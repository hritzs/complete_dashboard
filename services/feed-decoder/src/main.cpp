#include <iostream>
#include <string>
#include <thread>
#include <vector>
#include <atomic>
#include <chrono>
#include <fstream>
#include <sstream>
#include <map>
#include <algorithm>
#include <cmath>
#include <zmq.h>
#include "decoder/socket_reader.hpp"
#include "decoder/thread_safe_queue.hpp"
#include "decoder/packet_dispatch.hpp"
#include "decoder/decompressor.hpp"
#include "decoder/message_parser.hpp"
#include "decoder/greeks_calculator.hpp"
#include "decoder/normalizer.hpp"
#include "decoder/chain_builder.hpp"

namespace decoder {
    extern std::map<std::string, std::string> g_nearest_expiry;
}

namespace Config
{
    // NSE Feed
    constexpr const char* NSE_MULTICAST_IP = "233.1.2.5";
    constexpr int NSE_PORT = 34330;
    constexpr const char* NSE_LOCAL_IP = "0.0.0.0"; // Replace with your NSE leased line NIC IP

    // BSE Feed (SENSEX)
    constexpr const char* BSE_MULTICAST_IP = "233.1.2.4";
    constexpr int BSE_PORT = 2004; 
    constexpr const char* BSE_LOCAL_IP = "172.16.1.9"; // From Manish IT config

    constexpr size_t MAX_PACKET = 4096;
}

int main() {
    std::cout << "[Feed Decoder] Starting Ultra-Low Latency Feed Decoder..." << std::endl;
    
    decoder::Normalizer normalizer;
    decoder::ThreadSafeQueue<decoder::Packet> packet_queue;
    std::atomic<bool> running{true};

    decoder::SocketReader nse_reader(packet_queue, running);
    decoder::SocketReader bse_reader(packet_queue, running);
    
    // Run socket readers in background threads for both exchanges
    std::thread nse_thread([&]() {
        nse_reader.start(Config::NSE_MULTICAST_IP, Config::NSE_PORT, Config::NSE_LOCAL_IP);
    });

    std::thread bse_thread([&]() {
        bse_reader.start(Config::BSE_MULTICAST_IP, Config::BSE_PORT, Config::BSE_LOCAL_IP);
    });

    decoder::Decompressor decompressor;
    decoder::MessageParser parser;
    decoder::ChainBuilder chain_builder;
    decoder::GreeksCalculator greeks_calc;
    decoder::PacketDispatch dispatcher;

    greeks_calc.set_chain_builder(&chain_builder);

    // 1. Setup ZMQ Publisher on Port 5556
    void* zmq_ctx = zmq_ctx_new();
    void* zmq_pub = zmq_socket(zmq_ctx, ZMQ_PUB);
    if (zmq_bind(zmq_pub, "tcp://*:5556") != 0) {
        std::cerr << "[Feed Decoder] ❌ Failed to bind ZMQ Publisher on port 5556" << std::endl;
    } else {
        std::cout << "[Feed Decoder] 📡 Option Chain ZMQ Publisher bound on tcp://*:5556" << std::endl;
    }

    // 1b. Setup Secondary ZMQ Publisher for Legacy Python Services (Port 5558)
    void* zmq_pub_py = zmq_socket(zmq_ctx, ZMQ_PUB);
    if (zmq_bind(zmq_pub_py, "tcp://*:5558") != 0) {
        std::cerr << "[Feed Decoder] ❌ Failed to bind Python ZMQ Publisher on port 5558" << std::endl;
    } else {
        std::cout << "[Feed Decoder] 🐍 Python Analytics ZMQ Publisher bound on tcp://*:5558" << std::endl;
    }

    // 2. Background Thread to push Option Chain to Go Snapshot Service at 10Hz
    std::thread publisher_thread([&]() {
        int print_throttle = 0;
        std::vector<std::string> symbols_to_publish = {"NIFTY", "BANKNIFTY", "FINNIFTY", "MIDCPNIFTY", "SENSEX", "BANKEX"};
        while (running) {
            std::this_thread::sleep_for(std::chrono::milliseconds(100));
            
            print_throttle++;
            for (const auto& sym : symbols_to_publish) {
                decoder::OptionChain chain;
                if (chain_builder.get_chain(sym, chain) && !chain.strikes.empty()) {
                    // Synthetic spot is continuously calculated by ChainBuilder on every tick.
                    // If we have absolutely no equity spot, fall back to synthetic for the ATM reference.
                    double final_spot = chain.fut_ltp > 0.0 ? chain.fut_ltp : chain.synthetic_spot;

                    // Find ATM strike (closest to Futures LTP)
                    double atm_strike = 0.0;
                    if (final_spot > 0.0) {
                        double min_diff = 999999.0;
                        for (const auto& row : chain.strikes) {
                            double diff = std::abs(row.strike - final_spot);
                            if (diff < min_diff) {
                                min_diff = diff;
                                atm_strike = row.strike;
                            }
                        }
                    }

                    // Safe stringifier to prevent "nan" and "inf" from breaking JSON parsers in Go/JS
                    auto safe_num = [](double v) -> std::string {
                        if (std::isnan(v) || std::isinf(v)) return "0.0";
                        return std::to_string(v);
                    };

                    std::string exp_str = decoder::g_nearest_expiry[sym];
                    // Manually build ultra-fast JSON payload
                    std::string json = "{\"type\":\"option_chain\",\"symbol\":\"" + sym + "\",\"spot\":" + safe_num(chain.fut_ltp) + ",\"synthetic_spot\":" + safe_num(chain.synthetic_spot) + ",\"atm\":" + safe_num(atm_strike) + ",\"expiry\":\"" + exp_str + "\",\"chain\":[";
                    bool first = true;
                    for (const auto& row : chain.strikes) {
                        if (!first) {
                            json += ",";
                        }
                        json += "{";
                        json += "\"strike\":" + safe_num(row.strike) + ",";
                        json += "\"ce_ltp\":" + safe_num(row.ce_ltp) + ",";
                        json += "\"pe_ltp\":" + safe_num(row.pe_ltp) + ",";
                        json += "\"ce_iv\":" + safe_num(row.ce_greeks.iv) + ",";
                        json += "\"pe_iv\":" + safe_num(row.pe_greeks.iv) + ",";
                        json += "\"ce_delta\":" + safe_num(row.ce_greeks.delta) + ",";
                        json += "\"pe_delta\":" + safe_num(row.pe_greeks.delta) + ",";
                        json += "\"ce_gamma\":" + safe_num(row.ce_greeks.gamma) + ",";
                        json += "\"pe_gamma\":" + safe_num(row.pe_greeks.gamma) + ",";
                        json += "\"ce_vega\":" + safe_num(row.ce_greeks.vega) + ",";
                        json += "\"pe_vega\":" + safe_num(row.pe_greeks.vega) + ",";
                        json += "\"ce_theta\":" + safe_num(row.ce_greeks.theta) + ",";
                        json += "\"pe_theta\":" + safe_num(row.pe_greeks.theta) + ",";
                        json += "\"is_atm\":" + std::string((row.strike == atm_strike) ? "true" : "false");
                        json += "}";
                        first = false;
                    }
                    json += "]}";

                    zmq_send(zmq_pub, json.c_str(), json.length(), 0);

                    // Push the identical JSON stream to the Python downstream services
                    zmq_send(zmq_pub_py, json.c_str(), json.length(), 0);

                    // Print to console every 2 seconds to verify output
                    if (print_throttle % 20 == 0) {
                        std::cout << "[Feed Decoder] 📤 Published Chain | Symbol: " << sym 
                                  << " | Spot: ₹" << chain.fut_ltp << " | SynSpot: ₹" << chain.synthetic_spot
                                  << " | ATM: " << atm_strike 
                                  << " | Strikes: " << chain.strikes.size() << std::endl;
                    }
                }
            }
        }
    });

    std::vector<std::string> contract_files = {
        "/mnt/shared_tokens/IndexTokens.csv",
        "/mnt/shared_tokens/BSEIndexTokens.csv"
    };
    
    for (const auto& file : contract_files) {
        std::ifstream check_file(file);
        if (check_file.is_open()) {
            normalizer.load_contracts_csv(file);
        } else {
            std::cout << "[Feed Decoder] ⚠️ " << file << " not found immediately. Skipping to ensure 1-second startup." << std::endl;
        }
    }

    // Seed Chain Builder directly from Normalizer memory map!
    std::map<std::string, decoder::OptionChain> active_chains;
    
    for (const auto& [token, info] : normalizer.token_map_) {
        // Skip spots for the strike array
        if (info.symbol.find("FUT") != std::string::npos) {
            continue;
        }

        auto& chain = active_chains[info.base_symbol];
        chain.symbol = info.base_symbol;
        
        auto it = std::find_if(chain.strikes.begin(), chain.strikes.end(), 
            [&info](const auto& r){ return r.strike == info.strike; });
            
        if (it == chain.strikes.end()) {
            chain.strikes.push_back({}); 
            it = chain.strikes.end() - 1;
            it->strike = info.strike;
        }
        
        if (info.is_call) it->ce_token = token;
        else it->pe_token = token;
    }

    for (auto& pair : active_chains) {
        std::sort(pair.second.strikes.begin(), pair.second.strikes.end(), 
            [](const auto& a, const auto& b){ return a.strike < b.strike; });
        chain_builder.set_chain(pair.first, pair.second);
        std::cout << "[Feed Decoder] 🔗 Seeded C++ Greeks engine with " << pair.second.strikes.size() << " strikes for " << pair.first << std::endl;
    }

    std::cout << "[Feed Decoder] Listening and processing packets..." << std::endl;

    while (running) {
        decoder::Packet packet;
        if (packet_queue.pop(packet)) {
            // Dispatch to decompressor -> parser -> greeks -> SHM
            dispatcher.dispatch(packet.data(), packet.size(), decompressor, parser, greeks_calc, normalizer);
        }
    }
    
    running = false;
    nse_thread.join();
    bse_thread.join();
    publisher_thread.join();
    
    zmq_close(zmq_pub);
    zmq_close(zmq_pub_py);
    zmq_ctx_destroy(zmq_ctx);
    
    return 0;
}