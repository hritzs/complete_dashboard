#pragma once

#include <string>
#include <cstdint>

namespace trading {
namespace contracts {

struct OrderIntent {
    std::string trade_uid;
    uint32_t instrument_token;
    std::string side;          // "BUY" or "SELL"
    uint32_t quantity;
    double limit_price;
    std::string exchange_segment; // e.g., "NSEFO"

    // Fast minimal JSON serialization for sending over ZMQ to the Go gateway
    std::string to_json() const {
        return R"({"trade_uid":")" + trade_uid + 
               R"(","instrument_token":)" + std::to_string(instrument_token) + 
               R"(,"side":")" + side + 
               R"(","quantity":)" + std::to_string(quantity) + 
               R"(,"limit_price":)" + std::to_string(limit_price) + 
               R"(,"exchange_segment":")" + exchange_segment + R"("})";
    }
};

} // namespace contracts
} // namespace trading