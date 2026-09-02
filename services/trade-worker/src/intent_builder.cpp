#include "IntentBuilder.hpp"
#include <chrono>
#include <algorithm> // for std::max
#include <iostream>  // for debug output

namespace trading {

IntentBuilder::IntentBuilder(int lot_size, int max_order_qty, int default_chunk_divisor)
    : m_lot_size(lot_size),
      m_max_order_qty(max_order_qty),
      m_chunk_divisor(default_chunk_divisor) {}

// Delta-neutral allocation, inverse weighting (similar spirit to Python)
AllocationResult IntentBuilder::calculate_delta_neutral(
    double ce_delta,
    double pe_delta,
    int    target_total_lots
) {
    AllocationResult result{0, 0, 0, 0, 0.0};

    if (target_total_lots <= 0 || m_lot_size <= 0) {
        return result;
    }

    double abs_ce = std::abs(ce_delta);
    double abs_pe = std::abs(pe_delta);
    double total_delta_mag = abs_ce + abs_pe;

    // No usable delta → equal allocation
    if (total_delta_mag == 0.0) {
        int qty = target_total_lots * m_lot_size;
        result.ce_lots     = target_total_lots;
        result.pe_lots     = target_total_lots;
        result.ce_quantity = qty;
        result.pe_quantity = qty;
        result.net_delta   = 0.0;

        // Debug log
        std::cout << "[DeltaNeutral] CE lots: " << result.ce_lots
                  << ", PE lots: " << result.pe_lots
                  << ", CE qty: " << result.ce_quantity
                  << ", PE qty: " << result.pe_quantity
                  << ", net_delta: " << result.net_delta
                  << std::endl;

        return result;
    }

    // Inverse weighting: higher delta → fewer lots for that leg
    double ce_weight = abs_pe / total_delta_mag;
    double pe_weight = abs_ce / total_delta_mag;

    // Baseline lots per leg, total lots across both legs
    double total_allocation = target_total_lots * 2.0;

    int ce_lots = static_cast<int>(std::round(total_allocation * ce_weight));
    int pe_lots = static_cast<int>(std::round(total_allocation * pe_weight));

    // Avoid zero lots if target > 0
    if (ce_lots == 0 && target_total_lots > 0) ce_lots = 1;
    if (pe_lots == 0 && target_total_lots > 0) pe_lots = 1;

    int ce_qty = ce_lots * m_lot_size;
    int pe_qty = pe_lots * m_lot_size;

    // For a short straddle: net delta = - (CE_qty * CE_delta + PE_qty * PE_delta)
    double net_delta = -((static_cast<double>(ce_qty) * ce_delta) +
                         (static_cast<double>(pe_qty) * pe_delta));

    result.ce_lots     = ce_lots;
    result.pe_lots     = pe_lots;
    result.ce_quantity = ce_qty;
    result.pe_quantity = pe_qty;
    result.net_delta   = net_delta;

    // Debug log inside the function (correct location)
    std::cout << "[DeltaNeutral] CE lots: " << result.ce_lots
              << ", PE lots: " << result.pe_lots
              << ", CE qty: " << result.ce_quantity
              << ", PE qty: " << result.pe_quantity
              << ", net_delta: " << result.net_delta
              << std::endl;

    return result;
}

// Chunked order generation (interleaved CE/PE)
std::vector<OrderChunk> IntentBuilder::generate_chunked_orders(
    const std::string& trade_uid,
    int                ce_token,
    int                ce_lots,
    double             ce_ltp,
    int                pe_token,
    int                pe_lots,
    double             pe_ltp,
    const std::string& action
) {
    std::vector<OrderChunk> chunks(m_chunk_divisor);

    // Max lots per order from broker constraint
    int max_lots_per_order = std::max(1, m_max_order_qty / m_lot_size);

    // Base chunks
    int ce_lots_per_chunk = (m_chunk_divisor > 0) ? ce_lots / m_chunk_divisor : ce_lots;
    int ce_remainder      = (m_chunk_divisor > 0) ? ce_lots % m_chunk_divisor : 0;

    int pe_lots_per_chunk = (m_chunk_divisor > 0) ? pe_lots / m_chunk_divisor : pe_lots;
    int pe_remainder      = (m_chunk_divisor > 0) ? pe_lots % m_chunk_divisor : 0;

    auto now   = std::chrono::system_clock::now();
    auto ts_now = std::chrono::duration_cast<std::chrono::microseconds>(
                      now.time_since_epoch())
                      .count();
    int order_counter = 0;

    // Populate chunks interleaving CE and PE to minimize leg risk
    for (int i = 0; i < m_chunk_divisor; ++i) {
        int current_ce_lots = ce_lots_per_chunk + (i < ce_remainder ? 1 : 0);
        int current_pe_lots = pe_lots_per_chunk + (i < pe_remainder ? 1 : 0);

        // CE order slicing
        while (current_ce_lots > 0) {
            int lots_to_place = std::min(current_ce_lots, max_lots_per_order);

            OrderIntent intent;
            intent.uid   = trade_uid + "_" + std::to_string(ts_now + order_counter++);
            intent.token = ce_token;
            intent.option_type = "CE";
            intent.action      = action;
            intent.quantity    = lots_to_place * m_lot_size;
            intent.limit_price = ce_ltp;
            intent.limit_order_buffer_ticks = 2; // Gateway adjusts buffer

            chunks[i].push_back(intent);
            current_ce_lots -= lots_to_place;
        }

        // PE order slicing
        while (current_pe_lots > 0) {
            int lots_to_place = std::min(current_pe_lots, max_lots_per_order);

            OrderIntent intent;
            intent.uid   = trade_uid + "_" + std::to_string(ts_now + order_counter++);
            intent.token = pe_token;
            intent.option_type = "PE";
            intent.action      = action;
            intent.quantity    = lots_to_place * m_lot_size;
            intent.limit_price = pe_ltp;
            intent.limit_order_buffer_ticks = 2;

            chunks[i].push_back(intent);
            current_pe_lots -= lots_to_place;
        }
    }

    // Remove empty chunks (if lots were small)
    std::vector<OrderChunk> valid_chunks;
    valid_chunks.reserve(chunks.size());
    for (const auto& chunk : chunks) {
        if (!chunk.empty()) {
            valid_chunks.push_back(chunk);
        }
    }

    return valid_chunks;
}

} // namespace trading