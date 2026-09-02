#pragma once

#include <cstdint>
#include <atomic>

namespace trading {
namespace shm {

// 200,000 items * 48 bytes = ~9.6 MB of RAM (Extremely cache friendly)
constexpr size_t MAX_TOKENS = 200000;

struct MarketTick {
    std::atomic<double> ltp;
    std::atomic<double> bid_price;
    std::atomic<double> ask_price;
    std::atomic<int32_t> bid_qty;
    std::atomic<int32_t> ask_qty;
    std::atomic<int64_t> timestamp; // microsecond epoch of last update
};

struct MarketStateBlock {
    std::atomic<int64_t> last_global_update; // updated every time any tick arrives
    
    // Flat array allows O(1) lookup: ticks[token_id]
    MarketTick ticks[MAX_TOKENS];
};

} // namespace shm
} // namespace trading