#pragma once
#include <array>
#include <cstdint>
#include <cstddef>
#include <vector>

namespace decoder {

// One price/quantity level of the NSE BCAST_ONLY_MBP (7208/17208) depth
// ladder. Price is already divided by 100 (exchange sends paise).
struct Nse7208Level {
    double price = 0.0;
    uint32_t qty = 0;      // contracts (wire: int64 in the 8 bytes before the price)
    uint16_t orders = 0;   // number of orders at this level
};

// One decoded instrument record from an NSE BCAST_ONLY_MBP message.
// Layout confirmed against live-captured wire samples on 2026-09-23 --
// it differs from the older NNF protocol doc (record size 258 bytes, not
// the documented 213/214; ladder starts at record-offset 68, not 55, with
// a 16-byte stride per level, not 12).
struct Nse7208Record {
    uint32_t token = 0;
    uint16_t book_type = 0;
    uint32_t volume = 0;
    double ltp = 0.0;
    // Exchange last-trade time, whole seconds since 1980-01-01 IST
    // (record offset 30, confirmed live 2026-10-07 against the header
    // LogTime and GreekSoft's own ltt). 0 = no trade yet.
    uint32_t last_trade_time = 0;
    std::array<Nse7208Level, 5> buy{};   // best bid first, descending price
    std::array<Nse7208Level, 5> sell{};  // best ask first, ascending price
};

// Decodes every instrument record out of a decompressed NSE 7208/17208
// message body (the same buffer parse_7208 receives: header at offset 0,
// tx_code at offset 10, record count at offset 40, records starting at
// offset 42). Pure function, no I/O, safe to unit test directly.
std::vector<Nse7208Record> parse_nse_7208_records(const uint8_t* b, size_t len);

} // namespace decoder
