#pragma once
#include <cstdint>
#include <cstddef>
#include "decoder/greeks_calculator.hpp"
#include "decoder/normalizer.hpp"

namespace decoder {

class MessageParser {
public:
    void parse(
        const uint8_t* message,
        size_t length,
        GreeksCalculator& greeks_calc,
        const Normalizer& normalizer
    );

    void parse_json(
        const uint8_t* buffer,
        size_t length,
        GreeksCalculator& greeks_calc,
        const Normalizer& normalizer
    );

    // ✅ NEW: direct BSE NFCAST parser
    void parse_bse(
        const uint8_t* buffer,
        size_t length,
        GreeksCalculator& greeks_calc,
        const Normalizer& normalizer
    );

private:
    void parse_7208(
        const uint8_t* b,
        size_t len,
        GreeksCalculator& greeks_calc,
        const Normalizer& normalizer
    );
};

} // namespace decoder