#pragma once
#include <cstdint>

namespace data_models {

// Represents a normalized market data update for internal use.
struct MarketUpdate {
    uint32_t instrument_token;
    double last_trade_price;
    uint64_t exchange_timestamp;
};
}