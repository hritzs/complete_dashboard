// services/trade-worker/src/NetDeltaLogger.cpp
#include "NetDeltaLogger.hpp"
#include "TradeStateMachine.hpp" // your existing state class
#include <iostream>

namespace trading {

NetDeltaLogger::NetDeltaLogger(TradeStateMachine* parent,
                               const std::string& trade_uid,
                               double interval_seconds)
    : m_parent(parent),
      m_trade_uid(trade_uid),
      m_running(false),
      m_interval_sec(interval_seconds),
      m_last_check(std::chrono::steady_clock::now()) {}

void NetDeltaLogger::start() {
    m_running = true;
    m_last_check = std::chrono::steady_clock::now();
}

void NetDeltaLogger::stop() {
    m_running = false;
}

void NetDeltaLogger::tick() {
    if (!m_running) return;

    auto now = std::chrono::steady_clock::now();
    double elapsed = std::chrono::duration<double>(now - m_last_check).count();
    if (elapsed < m_interval_sec) return;

    m_last_check = now;

    // Ask TradeStateMachine to compute net delta from SHM
    double net_delta = m_parent->compute_net_delta(m_trade_uid);
    std::cout << "[NetDeltaLogger] " << m_trade_uid
              << " net_delta=" << net_delta << std::endl;
}

} // namespace trading