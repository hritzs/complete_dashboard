#pragma once

#include <string>
#include <unordered_map>
#include <chrono>

namespace trading {

class TradeStateMachine;

class HedgeMonitor {
public:
    HedgeMonitor(TradeStateMachine* parent,
                 const std::string& trade_uid,
                 const std::unordered_map<std::string, double>& cfg);

    void start();
    void stop();
    void tick();

private:
    bool start_time_gate_passed() const;

    TradeStateMachine* m_parent;
    std::string        m_trade_uid;
    bool               m_running;

    double m_interval_sec;
    double m_hedge_div;
    double m_straddle_div;
    double m_hedge_frac;

    std::chrono::steady_clock::time_point m_last_check;
};

} // namespace trading