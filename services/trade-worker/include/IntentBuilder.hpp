#pragma once

#include <string>
#include <vector>
#include <cmath>
#include <iostream>

namespace trading {

// Represents a single order to be sent to the Go Execution Gateway
struct OrderIntent {
    std::string uid;
    int token;
    std::string option_type; // "CE" or "PE"
    std::string action;      // "BUY" or "SELL"
    int quantity;
    double limit_price;
    int limit_order_buffer_ticks;
};

// Represents a chunk of orders to be executed simultaneously
using OrderChunk = std::vector<OrderIntent>;

// Result of the Delta Neutral calculation
struct AllocationResult {
    int ce_lots;
    int pe_lots;
    int ce_quantity;
    int pe_quantity;
    double net_delta;
};

class IntentBuilder {
public:
    IntentBuilder(int lot_size, int max_order_qty, int default_chunk_divisor = 7);

    // Calculates the number of lots required to achieve Delta Neutrality
    AllocationResult calculate_delta_neutral(
        double ce_delta, 
        double pe_delta, 
        int target_total_lots
    );

    // Generates chunked limits orders interleaved (PE and CE together) to minimize leg risk
    std::vector<OrderChunk> generate_chunked_orders(
        const std::string& trade_uid,
        int ce_token,
        int ce_lots,
        double ce_ltp,
        int pe_token,
        int pe_lots,
        double pe_ltp,
        const std::string& action = "SELL"
    );

private:
    int m_lot_size;
    int m_max_order_qty;
    int m_chunk_divisor;
};

} // namespace trading