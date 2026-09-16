#include "decoder/socket_reader.hpp"
#include <iostream>
#include <vector>
#include <cstring>
#include <zmq.h>
#include <thread>
#include <chrono>
#include <sys/socket.h>
#include <netinet/in.h>
#include <arpa/inet.h>
#include <unistd.h>
#include <fcntl.h>

namespace decoder {

std::atomic<int64_t> g_last_primary_tick_epoch_ms{0};

namespace {
int64_t now_epoch_ms() {
    return std::chrono::duration_cast<std::chrono::milliseconds>(
        std::chrono::system_clock::now().time_since_epoch()
    ).count();
}
}

SocketReader::SocketReader(ThreadSafeQueue<Packet>& queue, std::atomic<bool>& running)
    : packet_queue_(queue), running_(running) {}

SocketReader::~SocketReader() {}

void SocketReader::start(const std::string& host, int port, const std::string& interface_ip) {
    std::cout << "[SocketReader] 🟢 PRIMARY: Connecting to UDP Multicast Feed on " << host << ":" << port << " (NIC: " << interface_ip << ")" << std::endl;
    std::cout << "[SocketReader] 🟡 FALLBACK: Connecting to Go XTS ZeroMQ Publisher on tcp://127.0.0.1:5555" << std::endl;
    std::cout << "[SocketReader] 🔵 BACKUP: Connecting to GreekSoft Apollo backup feed on tcp://127.0.0.1:5560 (staleness-gated -- see greeksoft-feed-bridge)" << std::endl;
    
    // --- 1. Setup Primary UDP Multicast Socket ---
    int udp_fd = socket(AF_INET, SOCK_DGRAM, 0);
    if (udp_fd < 0) {
        std::cerr << "[SocketReader] CRITICAL: Failed to create UDP socket!" << std::endl;
    } else {
        int reuse = 1;
        setsockopt(udp_fd, SOL_SOCKET, SO_REUSEADDR, (char *)&reuse, sizeof(reuse));

        struct sockaddr_in localSock;
        memset((char *) &localSock, 0, sizeof(localSock));
        localSock.sin_family = AF_INET;
        localSock.sin_port = htons(port);
        localSock.sin_addr.s_addr = INADDR_ANY;
        bind(udp_fd, (struct sockaddr*)&localSock, sizeof(localSock));

        struct ip_mreq group;
        group.imr_multiaddr.s_addr = inet_addr(host.c_str());
        if (interface_ip == "0.0.0.0" || interface_ip.empty()) {
            group.imr_interface.s_addr = INADDR_ANY;
        } else {
            group.imr_interface.s_addr = inet_addr(interface_ip.c_str());
        }
        setsockopt(udp_fd, IPPROTO_IP, IP_ADD_MEMBERSHIP, (char *)&group, sizeof(group));

        // Make UDP socket non-blocking
        fcntl(udp_fd, F_SETFL, fcntl(udp_fd, F_GETFL, 0) | O_NONBLOCK);

        // --- UDP KERNEL OPTIMIZATIONS FOR ULTRA-LOW LATENCY ---
        // 1. Maximize Socket Receive Buffer (16MB) to prevent burst drops
        int rcvbuf = 16 * 1024 * 1024;
        setsockopt(udp_fd, SOL_SOCKET, SO_RCVBUF, &rcvbuf, sizeof(rcvbuf));

        // 2. Enable SO_BUSY_POLL (Linux Only) to poll NIC driver directly, saving ~10-20us context switch latency
        // Requires CAP_NET_ADMIN or root, but fails silently and safely if permissions are lacking.
        int busy_poll_us = 50; 
        setsockopt(udp_fd, SOL_SOCKET, SO_BUSY_POLL, &busy_poll_us, sizeof(busy_poll_us));
    }
    
    // --- 2. Setup Fallback ZeroMQ Socket (XTS, via Go market-data-gateway) ---
    void* z_context = zmq_ctx_new();
    void* z_subscriber = zmq_socket(z_context, ZMQ_SUB);
    zmq_connect(z_subscriber, "tcp://127.0.0.1:5555");
    zmq_setsockopt(z_subscriber, ZMQ_SUBSCRIBE, "", 0);

    // --- 2b. Setup Backup ZeroMQ Socket (GreekSoft Apollo, via Go
    // greeksoft-feed-bridge). The bridge itself only forwards ticks when
    // the primary/fallback feeds above have gone stale, so this socket
    // can be merged in exactly like the XTS fallback above with no
    // separate staleness logic needed here -- see
    // docs/greeksoft-integration-architecture.md section 3.
    void* z_backup_subscriber = zmq_socket(z_context, ZMQ_SUB);
    zmq_connect(z_backup_subscriber, "tcp://127.0.0.1:5560");
    zmq_setsockopt(z_backup_subscriber, ZMQ_SUBSCRIBE, "", 0);

    char buffer[65536];
    auto last_udp_time = std::chrono::steady_clock::now() - std::chrono::seconds(10);
    bool first_udp_received = false;
    bool first_zmq_received = false;
    bool first_backup_received = false;
    while (running_) {
        bool received_data = false;

        // Check UDP (Primary LZO)
        if (udp_fd >= 0) {
            int bytes = recv(udp_fd, buffer, sizeof(buffer), 0);
            if (bytes > 0) {
                packet_queue_.push(Packet(reinterpret_cast<uint8_t*>(buffer), reinterpret_cast<uint8_t*>(buffer) + bytes));
                received_data = true;
                last_udp_time = std::chrono::steady_clock::now();
                g_last_primary_tick_epoch_ms.store(now_epoch_ms(), std::memory_order_relaxed);

                static int udp_pkt_count = 0;
                if (++udp_pkt_count == 1) {
                    std::cout << "[SocketReader] 🟢 SUCCESS: First UDP Multicast packet received! (" << bytes << " bytes)" << std::endl;
                }
            }
        }

        // Check ZMQ (Fallback JSON, XTS)
        zmq_msg_t msg;
        zmq_msg_init(&msg);
        if (zmq_msg_recv(&msg, z_subscriber, ZMQ_DONTWAIT) != -1) {
            const uint8_t* data = static_cast<const uint8_t*>(zmq_msg_data(&msg));
            size_t size = zmq_msg_size(&msg);
            packet_queue_.push(Packet(data, data + size));
            received_data = true;
            g_last_primary_tick_epoch_ms.store(now_epoch_ms(), std::memory_order_relaxed);
            if (!first_zmq_received) {
                std::cout << "[SocketReader] 🟡 SUCCESS: First Fallback ZMQ packet received!" << std::endl;
                first_zmq_received = true;
            }
        }
        zmq_msg_close(&msg);

        // Check ZMQ (Backup JSON, GreekSoft Apollo -- staleness-gated by the bridge itself)
        zmq_msg_t backup_msg;
        zmq_msg_init(&backup_msg);
        if (zmq_msg_recv(&backup_msg, z_backup_subscriber, ZMQ_DONTWAIT) != -1) {
            const uint8_t* data = static_cast<const uint8_t*>(zmq_msg_data(&backup_msg));
            size_t size = zmq_msg_size(&backup_msg);
            packet_queue_.push(Packet(data, data + size));
            received_data = true;
            if (!first_backup_received) {
                std::cout << "[SocketReader] 🔵 SUCCESS: First Backup (GreekSoft Apollo) ZMQ packet received!" << std::endl;
                first_backup_received = true;
            }
        }
        zmq_msg_close(&backup_msg);

        if (!received_data) {
            std::this_thread::sleep_for(std::chrono::microseconds(50)); // Prevent 100% CPU loops
        }
    }

    if (udp_fd >= 0) close(udp_fd);
    zmq_close(z_subscriber);
    zmq_close(z_backup_subscriber);
    zmq_ctx_destroy(z_context);
    std::cout << "[SocketReader] Stopped listening on ZMQ." << std::endl;
}

} // namespace decoder