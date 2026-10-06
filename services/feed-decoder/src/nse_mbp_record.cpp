#include "decoder/nse_mbp_record.hpp"

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

static inline uint64_t be64(const uint8_t* p) {
    return (static_cast<uint64_t>(be32(p)) << 32) | be32(p + 4);
}

static inline uint32_t qty32(uint64_t q) {
    return q > 0xFFFFFFFFull ? 0xFFFFFFFFu : static_cast<uint32_t>(q);
}

std::vector<Nse7208Record> parse_nse_7208_records(const uint8_t* b, size_t len) {
    std::vector<Nse7208Record> out;

    constexpr size_t HEADER_SIZE = 40;
    constexpr size_t COUNT_SIZE  = 2;
    constexpr size_t START       = HEADER_SIZE + COUNT_SIZE;
    constexpr size_t RECORD_SIZE = 258;
    constexpr size_t LADDER_START = 68;
    constexpr size_t LEVEL_STRIDE = 16;

    if (len < START) return out;

    uint16_t recs = be16(b + 40);
    if (recs == 0 || recs > 50) return out;

    size_t available = len - START;
    size_t max_recs  = available / RECORD_SIZE;
    if (recs > max_recs) recs = static_cast<uint16_t>(max_recs);

    out.reserve(recs);

    for (uint16_t r = 0; r < recs; ++r) {
        const size_t offset = START + static_cast<size_t>(r) * RECORD_SIZE;
        if (offset + RECORD_SIZE > len) break;

        const uint8_t* rec = b + offset;

        Nse7208Record parsed;
        parsed.token = be32(rec + 0);
        parsed.book_type = be16(rec + 4);
        parsed.volume = be32(rec + 8);
        parsed.ltp = be32(rec + 16) / 100.0;

        // Each 16-byte level is: qty (int64) | price (int32) | orders
        // (int16) | flag (int16), the first level's qty at offset 60 --
        // i.e. a level's qty sits in the 8 bytes BEFORE its price.
        // (Reading be32(price + 4) returned the order count << 16.)
        for (size_t i = 0; i < 5; ++i) {
            size_t lvl_off = LADDER_START + i * LEVEL_STRIDE;
            parsed.buy[i].price  = be32(rec + lvl_off) / 100.0;
            parsed.buy[i].qty    = qty32(be64(rec + lvl_off - 8));
            parsed.buy[i].orders = be16(rec + lvl_off + 4);
        }
        for (size_t i = 0; i < 5; ++i) {
            size_t lvl_off = LADDER_START + (5 + i) * LEVEL_STRIDE;
            parsed.sell[i].price  = be32(rec + lvl_off) / 100.0;
            parsed.sell[i].qty    = qty32(be64(rec + lvl_off - 8));
            parsed.sell[i].orders = be16(rec + lvl_off + 4);
        }

        out.push_back(parsed);
    }

    return out;
}

} // namespace decoder
