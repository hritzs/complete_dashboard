#pragma once
#include <cstdint>

namespace feed_protocol {

// Represents a single message from the raw exchange feed.
struct ExchangeMessage {
    uint32_t token;
    uint64_t timestamp;
};
}