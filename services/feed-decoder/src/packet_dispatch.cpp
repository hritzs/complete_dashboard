#include "decoder/packet_dispatch.hpp"
#include <iostream>

namespace decoder {

static inline uint16_t be16(const uint8_t* p) {
    return (p[0] << 8) | p[1];
}

void PacketDispatch::dispatch(const uint8_t* buffer, size_t length, Decompressor& decompressor, MessageParser& parser, GreeksCalculator& greeks_calc, const Normalizer& normalizer) {
    // Path 1: JSON from ZMQ Fallback
    if (length > 0 && (buffer[0] == '{' || buffer[0] == '[')) {
        parser.parse_json(buffer, length, greeks_calc, normalizer);
        return;
    }

    // Path 2: Direct Uncompressed NSE Interactive Feed (e.g., 512-byte packets)
    // The message block starts immediately. The transaction code is at offset 10.
    if (length > 42 && be16(buffer + 10) == 7208) {
        parser.parse(buffer, length, greeks_calc, normalizer);
        return;
    }

    // Path 3: LZO Compressed NSE Broadcast Feed (logic from reference project)
    if (length < 4) {
        return;
    }

    // NSE LZO chunk format: first two bytes (unused), then number of packets
    uint16_t packets = be16(buffer + 2);

    if (packets == 0 || packets > 50) {
        // This is not a valid LZO packet, and it wasn't a direct 7208 packet.
        // It's likely an unknown packet type or heartbeat.
        return;
    }

    size_t pos = 4;
    for (uint16_t i = 0; i < packets; i++) {
        if (pos + 2 > length) break;

        uint16_t comp_len = be16(buffer + pos);
        pos += 2;

        if (comp_len == 0 || comp_len > 2048 || pos + comp_len > length) break;

        std::vector<uint8_t> decompressed;
        if (decompressor.decompress(buffer + pos, comp_len, decompressed)) {
            // After decompression, there's an 8-byte header before the message block
            if (decompressed.size() > 18) {
                const uint8_t* msg_buf = decompressed.data() + 8;
                size_t msg_len = decompressed.size() - 8;
                // The parse function will check the tx_code at offset 10 of the message block
                parser.parse(msg_buf, msg_len, greeks_calc, normalizer);
            }
        }
        
        pos += comp_len;
    }
}

} // namespace decoder