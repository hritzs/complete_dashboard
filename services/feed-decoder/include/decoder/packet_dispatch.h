#pragma once

#include <cstddef>
#include <memory>

#include "publisher.h"

namespace decoder {
class PacketDispatch {
public:
    explicit PacketDispatch(common_lib::ZmqPublisher& publisher);
    ~PacketDispatch();
    void process_packet(const char* buffer, size_t len);
private:
    class PacketDispatchImpl;
    std::unique_ptr<PacketDispatchImpl> impl_;
};
} // namespace decoder