#include "decoder/socket_reader.hpp"
#include <iostream>
#include <vector>
#include <cstring>
#include <zmq.h>
#include <thread>
#include <sys/socket.h>
#include <netinet/in.h>
#include <arpa/inet.h>
#include <unistd.h>
#include <fcntl.h>

namespace decoder {

SocketReader::SocketReader(ThreadSafeQueue<Packet>& queue, std::atomic<bool>& running)
    : packet_queue_(queue), running_(running) {}

SocketReader::~SocketReader() {}

void SocketReader::start(const std::string& host, int port, const std::string& interface_ip) {
    std::cout << "[SocketReader] 🟢 PRIMARY: Connecting to UDP Multicast Feed on " << host << ":" << port << " (NIC: " << interface_ip << ")" << std::endl;
    std::cout << "[SocketReader] 🟡 FALLBACK: Connecting to Go XTS ZeroMQ Publisher on tcp://127.0.0.1:5555" << std::endl;
    
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
    
    // --- 2. Setup Fallback ZeroMQ Socket ---
    void* z_context = zmq_ctx_new();
    void* z_subscriber = zmq_socket(z_context, ZMQ_SUB);
    zmq_connect(z_subscriber, "tcp://127.0.0.1:5555");
    zmq_setsockopt(z_subscriber, ZMQ_SUBSCRIBE, "", 0);

    char buffer[65536];
    auto last_udp_time = std::chrono::steady_clock::now() - std::chrono::seconds(10);
    bool first_udp_received = false;
    bool first_zmq_received = false;
    while (running_) {
        bool received_data = false;

        // Check UDP (Primary LZO)
        if (udp_fd >= 0) {
            int bytes = recv(udp_fd, buffer, sizeof(buffer), 0);
            if (bytes > 0) {
                packet_queue_.push(Packet(reinterpret_cast<uint8_t*>(buffer), reinterpret_cast<uint8_t*>(buffer) + bytes));
                received_data = true;
                last_udp_time = std::chrono::steady_clock::now();
                
                static int udp_pkt_count = 0;
                if (++udp_pkt_count == 1) {
                    std::cout << "[SocketReader] 🟢 SUCCESS: First UDP Multicast packet received! (" << bytes << " bytes)" << std::endl;
                }
            }
        }

        // Check ZMQ (Fallback JSON)
        zmq_msg_t msg;
        zmq_msg_init(&msg);
        if (zmq_msg_recv(&msg, z_subscriber, ZMQ_DONTWAIT) != -1) {
            const uint8_t* data = static_cast<const uint8_t*>(zmq_msg_data(&msg));
            size_t size = zmq_msg_size(&msg);
            packet_queue_.push(Packet(data, data + size));
            received_data = true;
            if (!first_zmq_received) {
                std::cout << "[SocketReader] 🟡 SUCCESS: First Fallback ZMQ packet received!" << std::endl;
                first_zmq_received = true;
            }
        }
        zmq_msg_close(&msg);

        if (!received_data) {
            std::this_thread::sleep_for(std::chrono::microseconds(50)); // Prevent 100% CPU loops
        }
    }

    if (udp_fd >= 0) close(udp_fd);
    zmq_close(z_subscriber);
    zmq_ctx_destroy(z_context);
    std::cout << "[SocketReader] Stopped listening on ZMQ." << std::endl;
}

} // namespace decoder