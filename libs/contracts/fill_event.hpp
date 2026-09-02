#pragma once

#include <chrono>
#include <cstdint>
#include "contracts/common/types.hpp"

namespace trading::contracts {

    struct FillEvent {
        uint64_t fill_id;
        uint64_t intent_id;
        char broker_order_id[32]{};
        uint64_t trade_id;
        uint32_t instrument_id;

        Side side;
        uint32_t fill_qty;
        double fill_price;

        std::chrono::nanoseconds fill_time; // epoch nanoseconds
    };

}