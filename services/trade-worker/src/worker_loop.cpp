#include "../include/worker_loop.hpp"
#include <iostream>
#include <sys/mman.h>
#include <fcntl.h>
#include <unistd.h>
#include <chrono>
#include <thread>
#include <sstream>
#include <pthread.h>
#include <sched.h>

TradeWorker::TradeWorker(const std::string& trade_id, const std::string& symbol, int lot_qty)
    : trade_id_(trade_id), symbol_(symbol), lot_qty_(lot_qty), 
      zmq_ctx_(1), zmq_push_sock_(zmq_ctx_, zmq::socket_type::push) {
    
    // Initialize State Machine with IntentBuilder and ZMQ emitter callback
    trading::IntentBuilder builder(lot_qty_, 500, 7); // lot_size, max_qty, chunks
    state_machine_ = std::make_unique<trading::TradeStateMachine>(
        trade_id_, std::move(builder), [this](const std::string& payload) {
            zmq::message_t msg(payload.data(), payload.size());
            zmq_push_sock_.send(msg, zmq::send_flags::none);
        });
}

TradeWorker::~TradeWorker() {
    stop();
    
    // Cleanup SHM mappings
    if (prices_ != nullptr && prices_ != MAP_FAILED) {
        munmap(prices_, 50000 * sizeof(double));
    }
    if (chain_data_ != nullptr && chain_data_ != MAP_FAILED) {
        munmap(chain_data_, sizeof(ChainSHMData));
    }
    if (price_fd_ != -1) close(price_fd_);
    if (chain_fd_ != -1) close(chain_fd_);
}

void TradeWorker::init_shm() {
    // 1. Attach to PriceSHM (50000 8-byte floats as mapped by Feed Decoder)
    price_fd_ = shm_open("prices_shm", O_RDONLY, 0666);
    if (price_fd_ == -1) {
        std::cerr << "[TradeWorker] Error: Failed to open prices_shm" << std::endl;
    } else {
        prices_ = static_cast<double*>(mmap(0, 50000 * sizeof(double), PROT_READ, MAP_SHARED, price_fd_, 0));
    }

    // 2. Attach to ChainSHM
    chain_fd_ = shm_open("chain_shm", O_RDONLY, 0666);
    if (chain_fd_ == -1) {
        std::cerr << "[TradeWorker] Error: Failed to open chain_shm" << std::endl;
    } else {
        chain_data_ = static_cast<ChainSHMData*>(mmap(0, sizeof(ChainSHMData), PROT_READ, MAP_SHARED, chain_fd_, 0));
    }
    std::cout << "[TradeWorker] SHM Segments Attached successfully." << std::endl;
}

void TradeWorker::init_zmq() {
    // Connect ZMQ PUSH socket to the Go Execution Gateway
    zmq_push_sock_.connect("tcp://127.0.0.1:5557");
    std::cout << "[TradeWorker] ZMQ PUSH socket connected to Go Gateway at tcp://127.0.0.1:5557" << std::endl;
}

void TradeWorker::send_order_intent(const std::string& intent_id, int instrument_id, const std::string& side, int qty, double limit_price) {
    // Construct OrderIntent JSON directly for minimal latency overhead
    std::ostringstream oss;
    oss << "{"
        << "\"intent_id\": \"" << intent_id << "\", "
        << "\"trade_id\": \"" << trade_id_ << "\", "
        << "\"worker_id\": \"cpp_worker_01\", "
        << "\"instrument_id\": " << instrument_id << ", "
        << "\"side\": \"" << side << "\", "
        << "\"quantity\": " << qty << ", "
        << "\"order_type\": \"LIMIT\", "
        << "\"limit_price\": " << limit_price << ", "
        << "\"product_type\": \"MIS\""
        << "}";

    std::string payload = oss.str();
    zmq::message_t msg(payload.data(), payload.size());
    
    zmq_push_sock_.send(msg, zmq::send_flags::none);
    std::cout << "[TradeWorker] -> Fired OrderIntent: " << payload << std::endl;
}

void TradeWorker::pin_thread_to_core(int core_id) {
    cpu_set_t cpuset;
    CPU_ZERO(&cpuset);
    CPU_SET(core_id, &cpuset);
    pthread_t current_thread = pthread_self();
    int result = pthread_setaffinity_np(current_thread, sizeof(cpu_set_t), &cpuset);
    if (result != 0) {
        std::cerr << "[TradeWorker] ⚠️ Warning: Failed to pin thread to CPU core " << core_id << "\n";
    } else {
        std::cout << "[TradeWorker] 🚀 Thread successfully pinned to isolated CPU Core " << core_id << "\n";
    }
}

void TradeWorker::run() {
    running_ = true;
    
    // Pin to an isolated core to prevent OS context switching latency
    pin_thread_to_core(2);
    
    std::cout << "[TradeWorker] Spin-wait hot loop started for trade: " << trade_id_ << std::endl;

    while (running_) {
        // If actively managing a trade, poll state machine and tick checks
        if (state_machine_->get_state() != trading::TradeState::INIT) {
            state_machine_->on_timer_tick();
            // state_machine_->perform_risk_checks(market_data); // Enable when SHM Market block is linked
            std::this_thread::sleep_for(std::chrono::microseconds(50));
            continue;
        }

        if (!chain_data_ || chain_data_ == MAP_FAILED || !prices_ || prices_ == MAP_FAILED) {
            std::this_thread::sleep_for(std::chrono::milliseconds(10));
            continue;
        }

        int current_atm = chain_data_->atm_strike;
        if (current_atm <= 0) continue;

        // Rapidly scan SHM for the ATM node
        OptionNode atm_node;
        bool found = false;
        for (int i = 0; i < chain_data_->num_strikes; ++i) {
            if (chain_data_->nodes[i].strike == current_atm) {
                atm_node = chain_data_->nodes[i];
                found = true;
                break;
            }
        }

        // Execute Delta-Neutral Short Straddle
        if (found && atm_node.ce_token > 0 && atm_node.pe_token > 0) {
            double ce_price = prices_[atm_node.ce_token];
            double pe_price = prices_[atm_node.pe_token];

            if (ce_price > 0 && pe_price > 0) {
                // Trigger State Machine Deployment (calculates delta-neutral quantities & generates chunks)
                state_machine_->on_command_deploy(
                    1, // Target total lots
                    ce_price, pe_price, 
                    atm_node.ce_delta, atm_node.pe_delta, 
                    atm_node.ce_token, atm_node.pe_token
                );
            }
        }
    }
}

void TradeWorker::stop() {
    running_ = false;
}