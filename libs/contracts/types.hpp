#pragma once

#include <cstdint>

namespace trading::contracts {

    enum class Side : uint8_t {
        Buy,
        Sell
    };

    enum class OrderType : uint8_t {
        Market,
        Limit,
        StopLossMarket,
        StopLossLimit
    };

    enum class ProductType : uint8_t {
        NRML,
        MIS,
        CNC
    };

    enum class OrderStatus : uint8_t {
        SUBMITTED,
        ACKED,
        PARTIAL_FILL,
        FILLED,
        CANCELLED,
        RMS_REJECTED,
        EXCHANGE_REJECTED,
        EXPIRED
    };
}