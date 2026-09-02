#pragma once

#include <cstddef>
#include <functional>

#include "exchange_protocol.h"

namespace decoder {

using MessageHandler = std::function<void(const feed_protocol::ExchangeMessage &)>;

class MessageParser {
public:
  void parse_messages(const char *buffer, size_t len, const MessageHandler &handler);
};

} // namespace decoder