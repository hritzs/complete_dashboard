// services/trade-worker/src/HedgeMonitor.cpp
#include "HedgeMonitor.hpp"
#include "TradeStateMachine.hpp"
#include <iostream>
#include <ctime>

namespace trading {

HedgeMonitor::HedgeMonitor(TradeStateMachine* parent,
                           const std::string& trade_uid,
                           const std::unordered_map<std::string, double>& cfg)
    : m_parent(parent),
      m_trade_uid(trade_uid),
      m_running(false),
      m_interval_sec(60.0),
      m_hedge_div(19.0),
      m_straddle_div(3.0),
      m_hedge_frac(1.0),
      m_last_check(std::chrono::steady_clock::now())
{
    auto get = [&](const std::string& k, double def) {
        auto it = cfg.find(k);
        return (it != cfg.end()) ? it->second : def;
    };
    m_interval_sec = get("hedge_monitor_interval", 60.0);
    m_hedge_div    = get("hedge_div", 19.0);
    m_straddle_div = get("straddle_div", 3.0);
    m_hedge_frac   = get("hedge_frac", 1.0);
    // hedge_start_time_str should come from a string map in practice
    std::cout << "✅ HedgeMonitor initialized: " << m_trade_uid
              << " Interval: " << m_interval_sec << "s" << std::endl;
}

void HedgeMonitor::start() {
    if (m_running) return;
    m_running = true;
    m_last_check = std::chrono::steady_clock::now();
    std::cout << "✅ HedgeMonitor enabled for " << m_trade_uid << std::endl;
}

void HedgeMonitor::stop() {
    if (!m_running) return;
    m_running = false;
    std::cout << "🛑 HedgeMonitor disabled for " << m_trade_uid << std::endl;
}

bool HedgeMonitor::start_time_gate_passed() const {
    // Simplified: assume you compare current IST time to m_hedge_start_time_str
    // For now, always true; add time parsing like in Python if needed.
    return true;
}

void HedgeMonitor::tick() {
    if (!m_running) return;
    if (!start_time_gate_passed()) return;

    auto now   = std::chrono::steady_clock::now();
    double elapsed = std::chrono::duration<double>(now - m_last_check).count();
    if (elapsed < m_interval_sec) return;

    // Keep schedule aligned
    m_last_check += std::chrono::duration_cast<std::chrono::steady_clock::duration>(
        std::chrono::duration<double>(m_interval_sec));

    // Get snapshot from parent (similar to state.trade_snapshots[trade_uid])
    auto snapshot = m_parent->get_snapshot(m_trade_uid);
    if (!snapshot.valid) {
        std::cout << "Hedge check: snapshot not available for "
                  << m_trade_uid << std::endl;
        return;
    }

    double pts_out        = snapshot.pts_out;
    double points_allowed = snapshot.points_allowed;
    double net_delta      = snapshot.net_delta;
    int    atm_strike     = snapshot.atm_strike;

    std::cout << "🛡️  Hedge Check for " << m_trade_uid
              << " pts_out=" << pts_out
              << " allowed=" << points_allowed << std::endl;

    if (pts_out > points_allowed) {
        std::cout << "HEDGE TRIGGERED for " << m_trade_uid
                  << " pts_out=" << pts_out
                  << " > allowed=" << points_allowed << std::endl;

        // Build hedge params equivalent
        double target_delta_reduction = -net_delta * m_hedge_frac;

        m_parent->save_hedge_params(m_trade_uid,
                                    net_delta,
                                    target_delta_reduction,
                                    atm_strike);

        // Emit event via event bus
        m_parent->emit_event("hedge_needed", m_trade_uid, /*priority*/ 1);
    }
}

} // namespace trading