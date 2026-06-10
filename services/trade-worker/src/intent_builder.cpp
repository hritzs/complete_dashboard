#include "IntentBuilder.hpp"
#include <chrono>

namespace trading {

IntentBuilder::IntentBuilder(int lot_size, int max_order_qty, int default_chunk_divisor)
    : m_lot_size(lot_size), m_max_order_qty(max_order_qty), m_chunk_divisor(default_chunk_divisor) {}

AllocationResult IntentBuilder::calculate_delta_neutral(double ce_delta, double pe_delta, int target_total_lots) {
    // Options deltas: CE is usually positive (e.g., 0.5), PE is negative (e.g., -0.5)
    double abs_ce = std::abs(ce_delta);
    double abs_pe = std::abs(pe_delta);
    double total_delta_mag = abs_ce + abs_pe;

    if (total_delta_mag == 0.0) {
        return {target_total_lots, target_total_lots, target_total_lots * m_lot_size, target_total_lots * m_lot_size, 0.0};
    }

    // Inverse weighting: higher delta means we need FEWER lots of that leg to balance
    double ce_weight = abs_pe / total_delta_mag;
    double pe_weight = abs_ce / total_delta_mag;

    // Total baseline lots * 2 (since target_total_lots is per leg in an equal allocation)
    double total_allocation = target_total_lots * 2.0;

    int ce_lots = std::round(total_allocation * ce_weight);
    int pe_lots = std::round(total_allocation * pe_weight);

    // Ensure we don't end up with 0 lots if target was > 0
    if (ce_lots == 0 && target_total_lots > 0) ce_lots = 1;
    if (pe_lots == 0 && target_total_lots > 0) pe_lots = 1;

    int ce_qty = ce_lots * m_lot_size;
    int pe_qty = pe_lots * m_lot_size;

    // For a short straddle (SELL), net delta = - (CE_qty * CE_delta + PE_qty * PE_delta)
    // Note: Assuming SELL action for the net delta calculation output
    double net_delta = -((ce_qty * ce_delta) + (pe_qty * pe_delta));

    return {ce_lots, pe_lots, ce_qty, pe_qty, net_delta};
}

std::vector<OrderChunk> IntentBuilder::generate_chunked_orders(
    const std::string& trade_uid, int ce_token, int ce_lots, double ce_ltp,
    int pe_token, int pe_lots, double pe_ltp, const std::string& action) 
{
    std::vector<OrderChunk> chunks(m_chunk_divisor);
    
    // Determine max lots per single order based on broker constraints
    int max_lots_per_order = std::max(1, m_max_order_qty / m_lot_size);
    
    // Calculate base chunks
    int ce_lots_per_chunk = ce_lots / m_chunk_divisor;
    int ce_remainder = ce_lots % m_chunk_divisor;

    int pe_lots_per_chunk = pe_lots / m_chunk_divisor;
    int pe_remainder = pe_lots % m_chunk_divisor;

    auto now = std::chrono::system_clock::now();
    auto ts_now = std::chrono::duration_cast<std::chrono::microseconds>(now.time_since_epoch()).count();
    int order_counter = 0;

    // Populate chunks interleaving CE and PE to minimize leg risk (execution gap)
    for (int i = 0; i < m_chunk_divisor; ++i) {
        int current_ce_lots = ce_lots_per_chunk + (i < ce_remainder ? 1 : 0);
        int current_pe_lots = pe_lots_per_chunk + (i < pe_remainder ? 1 : 0);

        // CE Order slicing
        while (current_ce_lots > 0) {
            int lots_to_place = std::min(current_ce_lots, max_lots_per_order);
            OrderIntent intent;
            intent.uid = trade_uid + "_" + std::to_string(ts_now + order_counter++);
            intent.token = ce_token;
            intent.option_type = "CE";
            intent.action = action;
            intent.quantity = lots_to_place * m_lot_size;
            intent.limit_price = ce_ltp; // Go gateway will apply the actual bid/ask buffer
            intent.limit_order_buffer_ticks = 2; // Default starting buffer
            
            chunks[i].push_back(intent);
            current_ce_lots -= lots_to_place;
        }

        // PE Order slicing
        while (current_pe_lots > 0) {
            int lots_to_place = std::min(current_pe_lots, max_lots_per_order);
            OrderIntent intent;
            intent.uid = trade_uid + "_" + std::to_string(ts_now + order_counter++);
            intent.token = pe_token;
            intent.option_type = "PE";
            intent.action = action;
            intent.quantity = lots_to_place * m_lot_size;
            intent.limit_price = pe_ltp; 
            intent.limit_order_buffer_ticks = 2;
            
            chunks[i].push_back(intent);
            current_pe_lots -= lots_to_place;
        }
    }

    // Remove empty chunks if quantities were very small
    std::vector<OrderChunk> valid_chunks;
    for (const auto& chunk : chunks) {
        if (!chunk.empty()) {
            valid_chunks.push_back(chunk);
        }
    }

    return valid_chunks;
}

} // namespace trading