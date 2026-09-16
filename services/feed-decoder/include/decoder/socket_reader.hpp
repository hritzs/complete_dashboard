#pragma once

#include "decoder/thread_safe_queue.hpp"
#include <string>
#include <atomic>
#include <cstdint>

namespace decoder {

class SocketReader {
public:
    SocketReader(ThreadSafeQueue<Packet>& queue, std::atomic<bool>& running);
    ~SocketReader();
    void start(const std::string& multicast_ip, int port, const std::string& interface_ip);

private:
    ThreadSafeQueue<Packet>& packet_queue_;
    std::atomic<bool>& running_;
};

// Epoch-ms timestamp of the last packet received on EITHER the primary UDP
// multicast socket or the existing XTS ZMQ fallback (tcp://127.0.0.1:5555)
// -- i.e. "primary" in the sense of "not the GreekSoft Apollo backup feed".
// Updated by every SocketReader instance (one per exchange -- NSE/BSE).
// Read by main.cpp's publisher thread to report feed liveness externally,
// so a separate process (services/greeksoft-feed-bridge) can make a
// correct staleness decision without needing to join the multicast group
// itself. See docs/greeksoft-integration-architecture.md section 3.
extern std::atomic<int64_t> g_last_primary_tick_epoch_ms;

} // namespace decoder