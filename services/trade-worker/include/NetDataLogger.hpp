// services/trade-worker/include/NetDeltaLogger.hpp
#pragma once

#include <chrono>
#include <string>

namespace trading {

class TradeStateMachine; // forward

class NetDeltaLogger {
public:
    NetDeltaLogger(TradeStateMachine* parent,
                   const std::string& trade_uid,
                   double interval_seconds = 1.0);

    void start();
    void stop();
    void tick(); // call this periodically from the worker loop

private:
    TradeStateMachine* m_parent;
    std::string        m_trade_uid;
    bool               m_running;
    double             m_interval_sec;
    std::chrono::steady_clock::time_point m_last_check;
};

} // namespace trading