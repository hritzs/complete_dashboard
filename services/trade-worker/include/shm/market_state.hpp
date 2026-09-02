#pragma once

#include <atomic>

namespace shm {

struct TickData {
    std::atomic<double> ltp{0.0};
};

struct MarketStateBlock {
    TickData ticks[100000]; // Sized to comfortably hold active token space
};

} // namespace shm