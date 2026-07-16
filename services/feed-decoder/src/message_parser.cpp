#include "decoder/message_parser.hpp"
#include <iostream>
#include <string_view>
#include <charconv>
#include <cstdlib>
#include <string>
#include <algorithm>
#include <cstring>

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

// ======================================================
// BSE NFCAST helper parser
// ======================================================
class BseParser {
    const uint8_t* buf_;
    size_t len_;
    size_t pos_;

public:
    BseParser(const uint8_t* b, size_t l) : buf_(b), len_(l), pos_(0) {}

    bool eof() const { return pos_ >= len_; }

    uint8_t read_u8() {
        if (pos_ + 1 > len_) return 0;
        return buf_[pos_++];
    }

    int16_t read_s16() {
        if (pos_ + 2 > len_) return 0;
        int16_t v = static_cast<int16_t>((buf_[pos_] << 8) | buf_[pos_ + 1]);
        pos_ += 2;
        return v;
    }

    uint16_t read_u16() {
        if (pos_ + 2 > len_) return 0;
        uint16_t v = static_cast<uint16_t>((buf_[pos_] << 8) | buf_[pos_ + 1]);
        pos_ += 2;
        return v;
    }

    uint32_t read_u32() {
        if (pos_ + 4 > len_) return 0;
        uint32_t v = (static_cast<uint32_t>(buf_[pos_]) << 24) |
                     (static_cast<uint32_t>(buf_[pos_ + 1]) << 16) |
                     (static_cast<uint32_t>(buf_[pos_ + 2]) << 8) |
                     (static_cast<uint32_t>(buf_[pos_ + 3]));
        pos_ += 4;
        return v;
    }

    uint64_t read_u64() {
        if (pos_ + 8 > len_) return 0;
        uint64_t hi = read_u32();
        uint64_t lo = read_u32();
        return (hi << 32) | lo;
    }

    void skip(size_t n) {
        if (pos_ + n <= len_) pos_ += n;
        else pos_ = len_;
    }

    int64_t read_compressed(int64_t base) {
        if (pos_ + 2 > len_) return base;
        int16_t diff = read_s16();
        if (diff == 32767) {
            return static_cast<int64_t>(read_u32());
        }
        return base + diff;
    }
};

// ======================================================
// MAIN NSE PARSER ENTRY
// ======================================================
void MessageParser::parse(
    const uint8_t* message,
    size_t length,
    GreeksCalculator& greeks_calc,
    const Normalizer& normalizer
) {
    if (length < 12) return;

    // NSE transaction code is at offset 10
    uint16_t tx_code = be16(message + 10);

    if (tx_code == 7208) {
        parse_7208(message, length, greeks_calc, normalizer);
    }
}

// ======================================================
// NSE 7208 PARSER
// ======================================================
void MessageParser::parse_7208(
    const uint8_t* b,
    size_t len,
    GreeksCalculator& greeks_calc,
    const Normalizer& normalizer
) {
    constexpr size_t HEADER_SIZE = 40;
    constexpr size_t COUNT_SIZE  = 2;
    constexpr size_t START       = HEADER_SIZE + COUNT_SIZE;
    constexpr size_t RECORD_SIZE = 214;

    if (len < START) return;

    uint16_t recs = be16(b + 40);
    if (recs == 0 || recs > 50) return;

    size_t available = len - START;
    size_t max_recs  = available / RECORD_SIZE;
    if (recs > max_recs) recs = static_cast<uint16_t>(max_recs);

    for (uint16_t r = 0; r < recs; ++r) {
        const size_t offset = START + static_cast<size_t>(r) * RECORD_SIZE;
        if (offset + RECORD_SIZE > len) break;

        const uint8_t* rec = b + offset;

        uint32_t token = be32(rec + 0);
        uint16_t book  = be16(rec + 4);

        if (book != 1 && book != 2) continue;

        double ltp  = be32(rec + 12) / 100.0;
        double bid1 = be32(rec + 56 + 4) / 100.0;
        double ask1 = be32(rec + 56 + 64) / 100.0;
        uint32_t volume = be32(rec + 8);
        uint32_t oi     = be32(rec + 174);

        ContractInfo info = normalizer.get_contract(token);
        if (info.is_valid) {
            static int udp_print_throttle = 0;
            if (++udp_print_throttle % 1000 == 0) {
                std::cout << "[Decoder] 📡 UDP TICK | "
                          << info.symbol
                          << " | LTP: ₹" << ltp
                          << std::endl;
            }

            greeks_calc.process_tick(token, ltp, bid1, ask1, volume, oi, info);
        }
    }
}

// ======================================================
// JSON FALLBACK PARSER
// ======================================================
void MessageParser::parse_json(
    const uint8_t* buffer,
    size_t length,
    GreeksCalculator& greeks_calc,
    const Normalizer& normalizer
) {
    std::string_view payload(reinterpret_cast<const char*>(buffer), length);

    // 1) Standard XTS instrument tick with token
    size_t id_pos = payload.find("\"ExchangeInstrumentID\":");
    if (id_pos != std::string_view::npos) {
        id_pos += 23;

        uint32_t token = 0;
        auto id_res = std::from_chars(payload.data() + id_pos, payload.data() + length, token);
        if (id_res.ec != std::errc()) return;

        size_t ltp_pos = payload.find("\"LastTradedPrice\":");
        if (ltp_pos != std::string_view::npos) {
            ltp_pos += 18;
        } else {
            ltp_pos = payload.find("\"IndexValue\":");
            if (ltp_pos == std::string_view::npos) return;
            ltp_pos += 13;
        }

        size_t end_pos = payload.find_first_of(",}", ltp_pos);
        if (end_pos == std::string_view::npos) end_pos = length;

        std::string price_str(payload.data() + ltp_pos, end_pos - ltp_pos);
        double price = std::strtod(price_str.c_str(), nullptr);

        if (token > 0 && price > 0.0) {
            ContractInfo info = normalizer.get_contract(token);
            if (info.is_valid) {
                std::cout << "[Decoder] ⚡ " << info.symbol << " | LTP: ₹" << price << std::endl;
                greeks_calc.process_tick(token, price, 0.0, 0.0, 0, 0, info);
            }
        }
        return;
    }

    // 2) Index-style JSON without token
    size_t name_pos = payload.find("\"IndexName\":");
    size_t val_pos  = payload.find("\"IndexValue\":");

    if (name_pos != std::string_view::npos && val_pos != std::string_view::npos) {
        name_pos += 12;
        if (name_pos < payload.size() && payload[name_pos] == '"') {
            name_pos++;
        }

        size_t name_end = payload.find('"', name_pos);
        if (name_end == std::string_view::npos) return;

        std::string symbol(payload.data() + name_pos, name_end - name_pos);

        val_pos += 13;
        size_t end_pos = payload.find_first_of(",}", val_pos);
        if (end_pos == std::string_view::npos) end_pos = length;

        std::string price_str(payload.data() + val_pos, end_pos - val_pos);
        double price = std::strtod(price_str.c_str(), nullptr);

        if (!symbol.empty() && price > 0.0) {
            ContractInfo fake{};
            fake.token = 0;
            fake.base_symbol = symbol;
            fake.symbol = symbol + "FUT";
            fake.strike = 0.0;
            fake.time_to_expiry_years = 7.0 / 365.0;
            fake.is_call = false;
            fake.is_valid = true;

            std::cout << "[Decoder] ⚡ " << fake.symbol << " | LTP: ₹" << price << std::endl;
            greeks_calc.process_tick(0, price, 0.0, 0.0, 0, 0, fake);
        }
        return;
    }

    std::cout << "[Decoder] ⚠️ Unrecognized JSON format on ZMQ: "
              << payload.substr(0, 100) << "...\n";
}

// ======================================================
// BSE DIRECT NFCAST PARSER
// ======================================================
void MessageParser::parse_bse(
    const uint8_t* buffer,
    size_t length,
    GreeksCalculator& greeks_calc,
    const Normalizer& normalizer
) {
    BseParser p(buffer, length);
    if (length < 4) return;

    uint32_t msg_type = p.read_u32();
    if (length < 28) return;

    // ----------------------------------
    // BSE spot index broadcast: 2011 / 2012
    // ----------------------------------
    if (msg_type == 2011 || msg_type == 2012) {
        p.skip(22);
        uint16_t num_records = p.read_u16();

        for (int i = 0; i < num_records; ++i) {
            if (p.eof()) break;

            p.skip(4);   // Index Code
            p.skip(16);  // High, Low, Open, PrevClose

            uint32_t index_val_raw = p.read_u32();
            double index_value = index_val_raw / 100.0;

            char index_id[8] = {0};
            for (int j = 0; j < 7; ++j) {
                index_id[j] = static_cast<char>(p.read_u8());
            }

            p.skip(9);

            std::string symbol(index_id);
            symbol.erase(std::find(symbol.begin(), symbol.end(), '\0'), symbol.end());

            if (!symbol.empty() && index_value > 0.0) {
                ContractInfo fake{};
                fake.token = 0;
                fake.base_symbol = symbol;
                fake.symbol = symbol + "FUT";
                fake.strike = 0.0;
                fake.time_to_expiry_years = 7.0 / 365.0;
                fake.is_call = false;
                fake.is_valid = true;

                static int bse_index_log_throttle = 0;
                if (++bse_index_log_throttle % 200 == 0) {
                    std::cout << "[Decoder] 📡 BSE INDEX | "
                              << fake.symbol
                              << " | LTP: ₹" << index_value << std::endl;
                }

                greeks_calc.process_tick(0, index_value, 0.0, 0.0, 0, 0, fake);
            }
        }

        return;
    }

    // ----------------------------------
    // BSE option / derivative messages: 2020 / 2021
    // ----------------------------------
    if (msg_type != 2020 && msg_type != 2021) {
        return;
    }

    p.skip(22);
    uint16_t num_records = p.read_u16();

    for (int i = 0; i < num_records; ++i) {
        if (p.eof()) break;

        uint32_t token = 0;
        if (msg_type == 2020) {
            token = p.read_u32();
        } else {
            p.skip(4); // upper half of complex token
            token = p.read_u32();
        }

        // Skip common fields
        p.skip(46);

        uint16_t num_price_points = p.read_u16();
        p.skip(12);

        int64_t ltq = static_cast<int64_t>(p.read_u64());
        int32_t ltp_raw = static_cast<int32_t>(p.read_u32());
        double ltp = ltp_raw / 100.0;

        uint32_t current_oi = 0;

        // Parse 12 compressed stats fields
        for (int k = 0; k < 12; ++k) {
            int64_t stat_val = p.read_compressed((k >= 6 && k <= 8) ? ltq : ltp_raw);
            if (k == 9) {
                current_oi = static_cast<uint32_t>(stat_val);
            }
        }

        // Best bid ladder
        double best_bid = 0.0;
        int32_t bid_price_base = ltp_raw;
        int64_t bid_qty_base = ltq;

        for (int lvl = 0; lvl < num_price_points; ++lvl) {
            if (p.eof()) break;

            int16_t diff = p.read_s16();
            if (diff == 32766) break;

            int32_t price_raw = (diff == 32767)
                ? static_cast<int32_t>(p.read_u32())
                : (bid_price_base + diff);

            if (lvl == 0) best_bid = price_raw / 100.0;

            int64_t qty = p.read_compressed(bid_qty_base);
            p.read_compressed(bid_qty_base);
            p.read_compressed(bid_qty_base);
            p.read_compressed(bid_qty_base);

            bid_price_base = price_raw;
            bid_qty_base = qty;
        }

        // Best ask ladder
        double best_ask = 0.0;
        int32_t ask_price_base = ltp_raw;
        int64_t ask_qty_base = ltq;

        for (int lvl = 0; lvl < num_price_points; ++lvl) {
            if (p.eof()) break;

            int16_t diff = p.read_s16();
            if (diff == -32766) break;

            int32_t price_raw = (diff == 32767)
                ? static_cast<int32_t>(p.read_u32())
                : (ask_price_base + diff);

            if (lvl == 0) best_ask = price_raw / 100.0;

            int64_t qty = p.read_compressed(ask_qty_base);
            p.read_compressed(ask_qty_base);
            p.read_compressed(ask_qty_base);
            p.read_compressed(ask_qty_base);

            ask_price_base = price_raw;
            ask_qty_base = qty;
        }

        ContractInfo info = normalizer.get_contract(token);

        if (!info.is_valid) {
            static int miss_count = 0;
            if (++miss_count % 5000 == 0) {
                std::cout << "[BSE] ❌ Unknown token: " << token << std::endl;
            }
            continue;
        }

        static int bse_tick_log_throttle = 0;
        if (++bse_tick_log_throttle % 1000 == 0) {
            std::cout << "[Decoder] 📡 BSE TICK | "
                      << info.symbol
                      << " | LTP: ₹" << ltp << std::endl;
        }

        greeks_calc.process_tick(token, ltp, best_bid, best_ask, 0, current_oi, info);
    }
}

} // namespace decoder