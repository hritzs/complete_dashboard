#pragma once
#include <cstdint>
#include <cstddef>
#include "decoder/decompressor.hpp"
#include "decoder/message_parser.hpp"
#include "decoder/greeks_calculator.hpp"
#include "decoder/normalizer.hpp"

namespace decoder {

class PacketDispatch {
public:
    void dispatch(const uint8_t* buffer, size_t length, Decompressor& decompressor, MessageParser& parser, GreeksCalculator& greeks_calc, const Normalizer& normalizer);
};

} // namespace decoder