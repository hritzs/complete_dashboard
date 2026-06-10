#pragma once

#include <atomic>
#include <cstdint>
#include <chrono>
#include <sys/mman.h>
#include <sys/stat.h>
#include <fcntl.h>
#include <unistd.h>
#include <iostream>

namespace shm {

constexpr size_t MAX_INSTRUMENTS = 100000;
constexpr const char* SHM_NAME = "/trading_platform_market_state";

struct TickData {
    std::atomic<double> ltp{0.0};
    std::atomic<double> bid1{0.0};
    std::atomic<double> ask1{0.0};
    std::atomic<double> iv{0.0};
    std::atomic<uint64_t> timestamp{0};
};

struct MarketState {
    TickData ticks[MAX_INSTRUMENTS];
};

inline uint64_t now_micros() {
    auto now = std::chrono::system_clock::now().time_since_epoch();
    return std::chrono::duration_cast<std::chrono::microseconds>(now).count();
}

inline MarketState* get_shm(bool create = false) {
    int fd = shm_open(SHM_NAME, (create ? O_CREAT : 0) | O_RDWR, 0666);
    if (fd < 0) {
        if (!create) return nullptr;
        std::cerr << "[SHM] Failed to create shared memory block.\n";
        return nullptr;
    }
    if (create) {
        ftruncate(fd, sizeof(MarketState));
    }
    void* ptr = mmap(0, sizeof(MarketState), PROT_READ | PROT_WRITE, MAP_SHARED, fd, 0);
    if (ptr == MAP_FAILED) return nullptr;
    
    return static_cast<MarketState*>(ptr);
}

} // namespace shm