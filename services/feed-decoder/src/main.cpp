#include <iostream>
#include <string>
#include <thread>
#include <vector>
#include <atomic>
#include <chrono>
#include <fstream>
#include <map>
#include <algorithm>
#include <cmath>
#include <unistd.h>
#include <zmq.h>
#include <cstdlib>

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
    extern std::map<std::string, std::vector<std::string>> g_available_expiries;
}

namespace Config {
    constexpr const char* NSE_MULTICAST_IP = "233.1.2.5";
    constexpr int NSE_PORT = 34330;
    constexpr const char* NSE_LOCAL_IP = "0.0.0.0";

    constexpr const char* BSE_MULTICAST_IP = "233.1.2.4";
    constexpr int BSE_PORT = 2004;
    constexpr const char* BSE_LOCAL_IP = "0.0.0.0";

    constexpr int PUBLISH_INTERVAL_MS = 100;
}

// ============================================================
// Helpers
// ============================================================

static bool file_exists(const std::string& path) {
    return std::ifstream(path).good();
}

static std::string safe_num(double v) {
    if (std::isnan(v) || std::isinf(v)) return "0.0";
    return std::to_string(v);
}

static bool bootstrap_contracts(decoder::Normalizer& normalizer) {
    bool loaded_any = false;

    std::string base_dir;
    char exe_path[1024] = {0};

    ssize_t len = readlink("/proc/self/exe", exe_path, sizeof(exe_path) - 1);
    if (len != -1) {
        std::string full_path(exe_path);
        auto pos = full_path.find("/build/");
        if (pos != std::string::npos) {
            base_dir = full_path.substr(0, pos);
        }
    }

    if (base_dir.empty()) {
        base_dir = ".";
    }

    const std::string index_file = base_dir + "/IndexTokens.csv";
    const std::string bse_file   = base_dir + "/BSEIndexTokens.csv";

    std::cout << "[Feed Decoder] 📂 Base dir detected: " << base_dir << std::endl;

    if (file_exists(index_file)) {
        std::cout << "[Feed Decoder] 📘 Loading " << index_file << std::endl;
        if (normalizer.load_contracts_csv(index_file)) {
            loaded_any = true;
        }
    } else {
        std::cout << "[Feed Decoder] ❌ Missing " << index_file << std::endl;
    }

    if (file_exists(bse_file)) {
        std::cout << "[Feed Decoder] 📙 Loading " << bse_file << std::endl;
        if (normalizer.load_contracts_csv(bse_file)) {
            loaded_any = true;
        }
    } else {
        std::cout << "[Feed Decoder] ❌ Missing " << bse_file << std::endl;
    }

    if (!loaded_any) {
        std::cout << "[Feed Decoder] ❌ No contracts loaded -> option chain will not work" << std::endl;
    } else {
        std::cout << "[Feed Decoder] ✅ Contracts loaded successfully" << std::endl;
    }

    return loaded_any;
}

static double detect_strike_step(const decoder::OptionChain& chain) {
    if (chain.strikes.size() < 2) return 50.0;

    double min_step = 999999.0;
    for (size_t i = 1; i < chain.strikes.size(); ++i) {
        const double diff = std::abs(chain.strikes[i].strike - chain.strikes[i - 1].strike);
        if (diff > 0.0 && diff < min_step) {
            min_step = diff;
        }
    }

    return (min_step == 999999.0) ? 50.0 : min_step;
}

static double nearest_strike(const decoder::OptionChain& chain, double px) {
    if (chain.strikes.empty()) return 0.0;

    double best = chain.strikes.front().strike;
    double best_diff = std::abs(best - px);

    for (const auto& row : chain.strikes) {
        const double diff = std::abs(row.strike - px);
        if (diff < best_diff) {
            best_diff = diff;
            best = row.strike;
        }
    }

    return best;
}

static double round_to_gap_and_snap(const decoder::OptionChain& chain, double px) {
    if (chain.strikes.empty()) return 0.0;

    const double step = detect_strike_step(chain);
    if (step <= 0.0) return nearest_strike(chain, px);

    const double rounded = std::round(px / step) * step;
    return nearest_strike(chain, rounded);
}

static double compute_monthly_future_proxy(const decoder::OptionChain& chain, double anchor_px) {
    if (chain.strikes.empty()) return 0.0;

    struct Candidate {
        double strike;
        double f;
        double distance;
    };

    std::vector<Candidate> cands;
    cands.reserve(chain.strikes.size());

    for (const auto& row : chain.strikes) {
        if (row.ce_ltp > 0.0 && row.pe_ltp > 0.0) {
            const double synthetic_future = row.strike + (row.ce_ltp - row.pe_ltp);
            const double dist = (anchor_px > 0.0) ? std::abs(row.strike - anchor_px) : 0.0;
            cands.push_back({row.strike, synthetic_future, dist});
        }
    }

    if (cands.empty()) return 0.0;

    std::sort(
        cands.begin(),
        cands.end(),
        [](const Candidate& a, const Candidate& b) {
            return a.distance < b.distance;
        }
    );

    const size_t takeN = std::min<size_t>(9, cands.size());

    std::vector<double> vals;
    vals.reserve(takeN);

    for (size_t i = 0; i < takeN; ++i) {
        vals.push_back(cands[i].f);
    }

    std::sort(vals.begin(), vals.end());
    return vals[vals.size() / 2];
}

static double compute_synthetic_from_strike(const decoder::OptionChain& chain, double strike) {
    for (const auto& row : chain.strikes) {
        if (row.strike == strike && row.ce_ltp > 0.0 && row.pe_ltp > 0.0) {
            return row.strike + (row.ce_ltp - row.pe_ltp);
        }
    }
    return 0.0;
}

// ============================================================
// MAIN
// ============================================================

int main() {
    setenv("TZ", "Asia/Kolkata", 1);
    tzset();

    std::cout << "[Feed Decoder] 🚀 Starting Ultra-Low Latency Feed Decoder..." << std::endl;

    decoder::Normalizer normalizer;
    decoder::ThreadSafeQueue<decoder::Packet> packet_queue;
    std::atomic<bool> running{true};

    decoder::SocketReader nse_reader(packet_queue, running);
    decoder::SocketReader bse_reader(packet_queue, running);

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

    void* zmq_ctx = zmq_ctx_new();
    void* zmq_pub = zmq_socket(zmq_ctx, ZMQ_PUB);

    if (zmq_bind(zmq_pub, "tcp://*:5556") != 0) {
        std::cerr << "[Feed Decoder] ❌ Failed to bind ZMQ Publisher on port 5556" << std::endl;
    } else {
        std::cout << "[Feed Decoder] 📡 Option Chain ZMQ Publisher bound on tcp://*:5556" << std::endl;
    }

    bool tokens_loaded = bootstrap_contracts(normalizer);
    if (!tokens_loaded) {
        std::cout << "[Feed Decoder] ⚠️ Proceeding without contracts; live mapping will fail." << std::endl;
    }

    std::map<std::string, decoder::OptionChain> active_chains;

    // Seed chains from token map
    for (const auto& kv : normalizer.token_map_) {
        const uint32_t token = kv.first;
        const auto& info = kv.second;

        if (!info.is_valid) continue;

        // Seed only option rows into the chain
        if (info.symbol.find("CE") == std::string::npos &&
            info.symbol.find("PE") == std::string::npos) {
            continue;
        }

        std::string chain_key = info.base_symbol + "|" + info.expiry;
        auto& chain = active_chains[chain_key];
        chain.symbol = info.base_symbol;
        chain.expiry = info.expiry;
        chain.dte = info.dte;

        auto it = std::find_if(
            chain.strikes.begin(),
            chain.strikes.end(),
            [&](const auto& r) { return r.strike == info.strike; }
        );

        if (it == chain.strikes.end()) {
            chain.strikes.push_back({});
            it = chain.strikes.end() - 1;
            it->strike = info.strike;
        }

        if (info.is_call) {
            it->ce_token = token;
        } else {
            it->pe_token = token;
        }
    }

    for (auto& pair : active_chains) {
        std::sort(
            pair.second.strikes.begin(),
            pair.second.strikes.end(),
            [](const auto& a, const auto& b) {
                return a.strike < b.strike;
            }
        );

        chain_builder.set_chain(pair.first, pair.second);

        std::cout << "[Feed Decoder] 🔗 Seeded "
                  << pair.second.strikes.size()
                  << " strikes for "
                  << pair.first
                  << std::endl;
    }

    if (active_chains.empty()) {
        std::cout << "[Feed Decoder] ⚠️ No option chains seeded. Check CSV parsing and token mapping." << std::endl;
    }

    // ========================================================
    // Publisher thread
    //
    // Per symbol / per publish:
    // 1) monthly_future anchor
    // 2) preliminary_atm from monthly_future
    // 3) synthetic_future from CE/PE of preliminary_atm
    // 4) actual_atm from synthetic_future
    // ========================================================
    std::thread publisher_thread([&]() {
        int print_throttle = 0;

        std::vector<std::string> symbols_to_publish = {
            "NIFTY",
            "BANKNIFTY",
            "FINNIFTY",
            "MIDCPNIFTY",
            "SENSEX",
            "BANKEX"
        };

        while (running.load()) {
            std::this_thread::sleep_for(std::chrono::milliseconds(Config::PUBLISH_INTERVAL_MS));
            print_throttle++;

            for (const auto& sym : symbols_to_publish) {
                const auto& expiries = decoder::g_available_expiries[sym];
                if (expiries.empty()) continue;

                std::string avail_exp_json = "[";
                for (size_t i = 0; i < expiries.size(); ++i) {
                    if (i > 0) avail_exp_json += ",";
                    avail_exp_json += "\"" + expiries[i] + "\"";
                }
                avail_exp_json += "]";

                for (const auto& exp : expiries) {
                    std::string chain_key = sym + "|" + exp;
                    decoder::OptionChain chain;

                    if (!chain_builder.get_chain(chain_key, chain)) continue;
                    if (chain.strikes.empty()) continue;

                    // 1) Monthly future anchor
                    double monthlyFutureRef = 0.0;

                    if (chain.fut_ltp > 0.0) {
                        monthlyFutureRef = chain.fut_ltp;
                    } else {
                        const double anchor =
                            (chain.synthetic_future > 0.0) ? chain.synthetic_future : 0.0;
                        monthlyFutureRef = compute_monthly_future_proxy(chain, anchor);
                    }

                    if (monthlyFutureRef <= 0.0 && !chain.strikes.empty()) {
                        // bootstrap only for strike selection
                        monthlyFutureRef = chain.strikes[chain.strikes.size() / 2].strike;
                    }

                    // 2) Preliminary ATM from monthly future
                    const double preliminaryAtm =
                        round_to_gap_and_snap(chain, monthlyFutureRef);

                    // 3) Real synthetic_future from CE/PE of that strike
                    double syntheticFuture = compute_synthetic_from_strike(chain, preliminaryAtm);

                    // If unavailable at preliminary ATM, keep stored value
                    if (syntheticFuture <= 0.0) {
                        syntheticFuture = chain.synthetic_future;
                    }

                    // Final safety fallback so UI does not stay zero forever
                    if (syntheticFuture <= 0.0 && chain.fut_ltp > 0.0) {
                        syntheticFuture = chain.fut_ltp;
                    }

                    // 4) Actual ATM from synthetic_future
                    double actualAtm = 0.0;
                    if (syntheticFuture > 0.0) {
                        actualAtm = round_to_gap_and_snap(chain, syntheticFuture);
                    } else if (!chain.strikes.empty()) {
                        actualAtm = chain.strikes[chain.strikes.size() / 2].strike;
                    }

                    std::string json =
                        "{\"type\":\"option_chain\","
                        "\"symbol\":\"" + sym + "\","
                        "\"synthetic_future\":" + safe_num(syntheticFuture) + ","
                        "\"future_ltp\":" + safe_num(chain.fut_ltp > 0.0 ? chain.fut_ltp : 0.0) + ","
                        "\"atm\":" + safe_num(actualAtm) + ","
                        "\"expiry\":\"" + exp + "\","
                        "\"available_expiries\":" + avail_exp_json + ","
                        "\"chain\":[";

                    bool first = true;
                    for (const auto& row : chain.strikes) {
                        if (!first) json += ",";

                        const bool is_atm = (row.strike == actualAtm);

                        json += "{";
                        json += "\"strike\":" + safe_num(row.strike) + ",";
                        json += "\"ce_token\":" + std::to_string(row.ce_token) + ",";
                        json += "\"pe_token\":" + std::to_string(row.pe_token) + ",";
                        json += "\"ce_ltp\":" + safe_num(row.ce_ltp) + ",";
                        json += "\"pe_ltp\":" + safe_num(row.pe_ltp) + ",";
                        json += "\"ce_iv\":" + safe_num(row.ce_greeks.iv * 100.0) + ",";
                        json += "\"pe_iv\":" + safe_num(row.pe_greeks.iv * 100.0) + ",";
                        json += "\"ce_delta\":" + safe_num(row.ce_greeks.delta) + ",";
                        json += "\"pe_delta\":" + safe_num(row.pe_greeks.delta) + ",";
                        json += "\"ce_gamma\":" + safe_num(row.ce_greeks.gamma) + ",";
                        json += "\"pe_gamma\":" + safe_num(row.pe_greeks.gamma) + ",";
                        json += "\"ce_vega\":" + safe_num(row.ce_greeks.vega) + ",";
                        json += "\"pe_vega\":" + safe_num(row.pe_greeks.vega) + ",";
                        json += "\"ce_theta\":" + safe_num(row.ce_greeks.theta) + ",";
                        json += "\"pe_theta\":" + safe_num(row.pe_greeks.theta) + ",";
                        json += "\"is_atm\":" + std::string(is_atm ? "true" : "false");
                        json += "}";

                        first = false;
                    }

                    json += "]}";

                    zmq_send(zmq_pub, json.c_str(), json.length(), 0);

                    if (print_throttle % 20 == 0 && exp == expiries.front()) {
                        std::cout << "[Feed Decoder] 📤 Published Chain | Symbol: "
                                  << sym
                                  << " | Expiry: " << exp
                                  << " | synthetic_future: ₹" << syntheticFuture
                                  << " | actual_atm: " << actualAtm
                                  << " | strikes: " << chain.strikes.size()
                                  << std::endl;
                    }
                }
            }
        }
    });

    std::cout << "[Feed Decoder] 📡 Listening and processing packets..." << std::endl;

    while (running.load()) {
        decoder::Packet packet;
        if (packet_queue.pop(packet)) {
            dispatcher.dispatch(
                packet.data(),
                packet.size(),
                decompressor,
                parser,
                greeks_calc,
                normalizer
            );
        }
    }

    running = false;

    if (nse_thread.joinable()) nse_thread.join();
    if (bse_thread.joinable()) bse_thread.join();
    if (publisher_thread.joinable()) publisher_thread.join();

    zmq_close(zmq_pub);
    zmq_ctx_destroy(zmq_ctx);

    return 0;
}