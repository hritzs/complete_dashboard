#pragma once

#include "decoder/thread_safe_queue.hpp"
#include <string>
#include <atomic>

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

} // namespace decoder