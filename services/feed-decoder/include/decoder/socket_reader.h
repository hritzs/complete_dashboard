#pragma once

#include <functional>
#include <memory>
#include <string>

namespace decoder {

// Callback type for handling received packets.
// const char* -> pointer to the raw data buffer
// size_t -> length of the data in bytes
using PacketHandler = std::function<void(const char*, size_t)>;

class SocketReader {
public:
    /**
     * @brief Constructs a SocketReader.
     * @param multicast_ip The multicast group IP address to join.
     * @param port The port to listen on.
     */
    SocketReader(const std::string& multicast_ip, int port);
    ~SocketReader();

    /**
     * @brief Starts the blocking loop to read packets from the socket and calls the handler.
     */
    void run(const PacketHandler& handler);

private:
    class SocketReaderImpl;
    std::unique_ptr<SocketReaderImpl> impl_;
};

} // namespace decoder