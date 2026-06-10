#pragma once

#include <chrono>
#include <cstdint>
#include "contracts/common/types.hpp"

namespace trading::contracts {

    struct OrderUpdate {
        uint64_t intent_id;
        char broker_order_id[32]{};
        char exchange_order_id[32]{};
        uint64_t trade_id;

        OrderStatus status;
        uint32_t filled_qty;
        uint32_t pending_qty;
        double avg_fill_price;

        char reason_code[16]{};
        char reason_text[128]{};

        std::chrono::nanoseconds broker_timestamp; // epoch nanoseconds
    };

}