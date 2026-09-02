#pragma once

#include "exchange_protocol.h"
#include "internal_models.h"

namespace decoder {
class Normalizer {
public:
    bool normalize(const feed_protocol::ExchangeMessage& from, data_models::MarketUpdate& to);
};
}