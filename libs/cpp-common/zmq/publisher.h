#pragma once
#include <string>
#include <memory>

namespace common_lib {

class ZmqPublisherImpl; // Forward declaration for the PIMPL idiom

class ZmqPublisher final {
public:
    explicit ZmqPublisher(const char* endpoint);
    ~ZmqPublisher();
    void publish(const std::string& topic, const std::string& payload);
private:
    std::unique_ptr<ZmqPublisherImpl> impl_; // Pointer to implementation
};
} // namespace common_lib