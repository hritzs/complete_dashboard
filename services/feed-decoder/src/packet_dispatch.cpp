#include "decoder/packet_dispatch.hpp"
#include <iostream>
#include <vector>

namespace decoder {

static inline uint16_t be16(const uint8_t* p) {
    return (static_cast<uint16_t>(p[0]) << 8) | p[1];
}

static inline uint32_t be32(const uint8_t* p) {
    return (static_cast<uint32_t>(p[0]) << 24) |
           (static_cast<uint32_t>(p[1]) << 16) |
           (static_cast<uint32_t>(p[2]) << 8)  |
            static_cast<uint32_t>(p[3]);
}

void PacketDispatch::dispatch(
    const uint8_t* buffer,
    size_t length,
    Decompressor& decompressor,
    MessageParser& parser,
    GreeksCalculator& greeks_calc,
    const Normalizer& normalizer
) {
    if (!buffer || length == 0) return;

    // =============================================
    // 1) BSE NFCAST DETECT (CRITICAL FIX)
    // =============================================
    // BSE raw packets begin directly with message type:
    // 2011 / 2012 / 2020 / 2021
    if (length >= 4) {
        uint32_t msg_type = be32(buffer);

        if (msg_type == 2011 ||
            msg_type == 2012 ||
            msg_type == 2020 ||
            msg_type == 2021) {

            parser.parse_bse(buffer, length, greeks_calc, normalizer);
            return;
        }
    }

    // =============================================
    // 2) JSON ZMQ fallback
    // =============================================
    if (buffer[0] == '{' || buffer[0] == '[') {
        parser.parse_json(buffer, length, greeks_calc, normalizer);
        return;
    }

    // =============================================
    // 3) Direct uncompressed NSE 7208 packet
    // =============================================
    if (length > 42 && be16(buffer + 10) == 7208) {
        parser.parse(buffer, length, greeks_calc, normalizer);
        return;
    }

    // =============================================
    // 4) NSE LZO compressed packets
    // =============================================
    if (length < 4) return;

    uint16_t packets = be16(buffer + 2);
    if (packets == 0 || packets > 50) {
        return;
    }

    size_t pos = 4;

    for (uint16_t i = 0; i < packets; ++i) {
        if (pos + 2 > length) break;

        uint16_t comp_len = be16(buffer + pos);
        pos += 2;

        if (comp_len == 0 || comp_len > 2048 || pos + comp_len > length) {
            break;
        }

        std::vector<uint8_t> decompressed;
        if (decompressor.decompress(buffer + pos, static_cast<int>(comp_len), decompressed)) {
            if (decompressed.size() > 18) {
                const uint8_t* msg_buf = decompressed.data() + 8;
                size_t msg_len = decompressed.size() - 8;

                parser.parse(msg_buf, msg_len, greeks_calc, normalizer);
            }
        }

        pos += comp_len;
    }
}

} // namespace decoder