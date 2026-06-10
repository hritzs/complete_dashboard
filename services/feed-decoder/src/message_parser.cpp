#include "decoder/message_parser.hpp"
#include <iostream>
#include <string_view>
#include <charconv>
#include <cstdlib>
#include <string>

namespace decoder {

static inline uint16_t be16(const uint8_t* p) {
    return (p[0] << 8) | p[1];
}

static inline uint32_t be32(const uint8_t* p) {
    return (p[0] << 24) | (p[1] << 16) | (p[2] << 8) | p[3];
}

void MessageParser::parse(const uint8_t* message, size_t length, GreeksCalculator& greeks_calc, const Normalizer& normalizer) {
    // The message block must be long enough to contain the transaction code at offset 10
    if (length < 12) return;
    
    // As per NSE specs, the transaction code is at offset 10 of the message block
    uint16_t tx_code = be16(message + 10);

    if (tx_code == 7208) {
        parse_7208(message, length, greeks_calc, normalizer);
    }
}

void MessageParser::parse_7208(const uint8_t* b, size_t len, GreeksCalculator& greeks_calc, const Normalizer& normalizer) {
    constexpr size_t HEADER_SIZE = 40;
    constexpr size_t COUNT_SIZE  = 2;
    constexpr size_t START       = HEADER_SIZE + COUNT_SIZE;
    constexpr size_t RECORD_SIZE = 214;

    if (len < START) return;

    uint16_t recs = be16(b + 40);

    if (recs == 0 || recs > 50) return;

    size_t available = len - START;
    size_t max_recs  = available / RECORD_SIZE;
    if (recs > max_recs) recs = max_recs;

    for (uint16_t r = 0; r < recs; r++) {
        const size_t offset = START + r * RECORD_SIZE;
        if (offset + RECORD_SIZE > len) break;
        const uint8_t* rec = b + offset;

        uint32_t token = be32(rec + 0);
        uint16_t book = be16(rec + 4);

        double ltp = be32(rec + 12) / 100.0;
        double bid1 = be32(rec + 56 + 4) / 100.0;
        double ask1 = be32(rec + 56 + 64) / 100.0;
        
        
        ContractInfo info = normalizer.get_contract(token);
        if (info.is_valid) {
            static int udp_print_throttle = 0;
            if (++udp_print_throttle % 1000 == 0) { // Throttle logging to prevent flood
                std::cout << "[Decoder] 📡 UDP TICK | " << info.symbol 
                            << " | LTP: ₹" << ltp << std::endl;
            }
            // Pass 0 for volume and OI to save processing overhead
            greeks_calc.process_tick(token, ltp, bid1, ask1, 0, 0, info);
        }
    }
}

void MessageParser::parse_json(const uint8_t* buffer, size_t length, GreeksCalculator& greeks_calc, const Normalizer& normalizer) {
    std::string_view payload(reinterpret_cast<const char*>(buffer), length);
    
    // 1. Ultra-fast string scan for the Token
    size_t id_pos = payload.find("\"ExchangeInstrumentID\":");
    if (id_pos == std::string_view::npos) {
        std::cout << "[Decoder] ⚠️ Unrecognized JSON format on ZMQ: " << payload.substr(0, 100) << "...\n";
        return;
    }
    id_pos += 23; // length of string + formatting
    
    uint32_t token = 0;
    auto id_res = std::from_chars(payload.data() + id_pos, payload.data() + length, token);
    if (id_res.ec != std::errc()) return;

    // 2. Ultra-fast string scan for the Price
    size_t ltp_pos = payload.find("\"LastTradedPrice\":");
    if (ltp_pos != std::string_view::npos) {
        ltp_pos += 18; // length of string + formatting
    } else {
        // Fallback for Index prices (1105 / 1510 / 1502)
        ltp_pos = payload.find("\"IndexValue\":");
        if (ltp_pos == std::string_view::npos) return;
        ltp_pos += 13;
    }

    // Extract substring for price to ensure safe null-terminated parsing
    size_t end_pos = payload.find_first_of(",}", ltp_pos);
    if (end_pos == std::string_view::npos) end_pos = length;
    
    std::string price_str(payload.data() + ltp_pos, end_pos - ltp_pos);
    double price = std::strtod(price_str.c_str(), nullptr);
    
    if (token > 0 && price > 0.0) {
        ContractInfo info = normalizer.get_contract(token);
        if (info.is_valid) {
            std::cout << "[Decoder] ⚡ " << info.symbol << " | LTP: ₹" << price << std::endl;
            // Pass 0.0 for bid/ask since this XTS tick might not have them immediately
            greeks_calc.process_tick(token, price, 0.0, 0.0, 0, 0, info);
        }
    }
}

} // namespace decoder