#include "decoder/message_parser.hpp"
#include "decoder/minute_close.hpp"
#include "decoder/nse_mbp_record.hpp"
#include <iostream>
#include <string_view>
#include <charconv>
#include <cstdlib>
#include <string>
#include <algorithm>
#include <cstring>
#include <chrono>
#include <unordered_map>
#include <vector>
#include <utility>
#include <fstream>
#include <mutex>
#include <unistd.h>

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
    size_t pos() const { return pos_; }

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

    // Compressed field: a signed 16-bit difference from base, or the
    // escape 32767 followed by the full value -- 4 bytes for a rate/count,
    // 8 bytes for a quantity (quantities are 64-bit in this feed).
    int64_t read_compressed(int64_t base, bool wide = false) {
        if (pos_ + 2 > len_) return base;
        int16_t diff = read_s16();
        if (diff == 32767) {
            return wide ? static_cast<int64_t>(read_u64())
                        : static_cast<int64_t>(read_u32());
        }
        return base + diff;
    }
};

// ======================================================
// BSE raw-sample capture (diagnostics)
// ======================================================
// Writes up to kMaxBseSamples raw 2020/2021 messages (hex) that contained a
// record whose token did not map, plus where parsing was when it failed, to
// <repo>/logs/bse_samples.hex -- so the real record layout can be checked
// against wire bytes instead of guessed. Truncated once per process start.
static void dump_bse_sample(const uint8_t* buf, size_t len, uint32_t msg_type,
                            int num_records, int bad_index, size_t rec_start,
                            size_t bad_pos, uint32_t bad_token,
                            const std::vector<size_t>& rec_starts) {
    constexpr int kMaxBseSamples = 40;
    static std::mutex mu;
    static int written = 0;
    static std::string path;
    std::lock_guard<std::mutex> lock(mu);
    if (written >= kMaxBseSamples) return;
    if (path.empty()) {
        std::string base = ".";
        char exe[1024] = {0};
        ssize_t n = readlink("/proc/self/exe", exe, sizeof(exe) - 1);
        if (n > 0) {
            std::string full(exe);
            auto at = full.find("/build/");
            if (at != std::string::npos) base = full.substr(0, at);
        }
        path = base + "/logs/bse_samples.hex";
        std::ofstream(path, std::ios::trunc).close();
        std::cout << "[BSE] writing up to " << kMaxBseSamples << " raw misaligned-message samples to " << path << std::endl;
    }
    std::ofstream out(path, std::ios::app);
    if (!out) return;
    out << "# sample " << (written + 1) << " msg_type=" << msg_type << " len=" << len
        << " num_records=" << num_records << " bad_record_index=" << bad_index
        << " bad_record_start=" << rec_start << " token_read_at=" << bad_pos
        << " bad_token=" << bad_token << " record_starts=";
    for (size_t i = 0; i < rec_starts.size(); ++i) out << (i ? "," : "") << rec_starts[i];
    out << "\n";
    static const char* hx = "0123456789abcdef";
    for (size_t i = 0; i < len; ++i) {
        out << hx[buf[i] >> 4] << hx[buf[i] & 0xF];
        if (i % 32 == 31) out << "\n"; else if (i % 2 == 1) out << ' ';
    }
    out << "\n";
    ++written;
}

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

    if (tx_code == 7208 || tx_code == 17208) {
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
    // Record decode (size, field offsets, depth-ladder layout) lives in
    // nse_mbp_record.cpp, verified against live-captured wire samples --
    // the previous inline offsets here (RECORD_SIZE=214, bid1/ask1 read
    // from inside what is actually the depth ladder) were wrong: real
    // records are 258 bytes, so any message with 2+ records misread every
    // record after the first at the wrong offset.
    for (const auto& rec : parse_nse_7208_records(b, len)) {
        if (rec.book_type != 1 && rec.book_type != 2) continue;

        ContractInfo info = normalizer.get_contract(rec.token);
        if (info.is_valid) {
            static int udp_print_throttle = 0;
            if (++udp_print_throttle % 1000 == 0) {
                std::cout << "[Decoder] 📡 UDP TICK | "
                          << info.symbol
                          << " | LTP: ₹" << rec.ltp
                          << std::endl;
            }

            // OI's real offset within the record is not yet confirmed (it
            // sits somewhere past the depth ladder); pass 0 rather than
            // the old, now-confirmed-wrong offset 174, which fell inside
            // the ladder itself. Nothing downstream currently reads oi.
            greeks_calc.process_tick(rec.token, rec.ltp, rec.buy[0].price, rec.sell[0].price, rec.volume, 0, info);
            g_minute_close.update(rec.token, rec.ltp, nse_time_to_unix(rec.last_trade_time));

            // Full L1-L5 ladder for depth-aware execution.
            double bpx[5], apx[5];
            uint32_t bq[5], aq[5];
            for (int i = 0; i < 5; ++i) {
                bpx[i] = rec.buy[i].price;
                bq[i] = rec.buy[i].qty;
                apx[i] = rec.sell[i].price;
                aq[i] = rec.sell[i].qty;
            }
            greeks_calc.process_depth(rec.token, bpx, bq, apx, aq, info);
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
    // 2021 is the spread/combination market picture (complex instruments,
    // small instrument IDs like 2590/3096): nothing here trades spreads, and
    // its record layout is not verified, so it is skipped quietly.
    if (msg_type != 2020) {
        return;
    }

    p.skip(22);
    uint16_t num_records = p.read_u16();

    // Records are decoded first and only published once the whole message
    // has parsed to its exact length (see the integrity check below).
    struct PendingBseTick {
        uint32_t token;
        double ltp;
        double best_bid;
        double best_ask;
        uint32_t oi;
    };
    std::vector<PendingBseTick> pending;
    pending.reserve(num_records);
    std::vector<size_t> rec_starts;
    for (int i = 0; i < num_records; ++i) {
        if (p.eof()) break;

        const size_t rec_start = p.pos();
        rec_starts.push_back(rec_start);
        const size_t token_pos = (msg_type == 2020) ? rec_start : rec_start + 4;
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

        // 21 compressed stats fields precede the depth ladders. Verified
        // against 40 live-captured 2020 messages (logs/bse_samples.hex,
        // 2026-09-30): with the old 12, the remaining 9 were read as the bid
        // ladder, so every record after the first in a message started at
        // the wrong offset (the "unmapped BSE token" flood: 32767,
        // 2147418112, ...) and best bid/ask were wrong even for record #1.
        // Fields 7-9 (indicative eq qty, total bid qty, total offer qty) are
        // quantities: 8-byte escape, based on LTQ. The rest are rates or
        // counts: 4-byte escape, based on LTP.
        for (int k = 0; k < 21; ++k) {
            const bool qty = (k >= 6 && k <= 8);
            p.read_compressed(qty ? ltq : ltp_raw, qty);
        }
        // Open interest's position among these fields isn't confirmed; nothing
        // downstream reads it (the NSE path passes 0 too). The old code
        // passed field 10, which is actually the lower circuit limit.
        const uint32_t current_oi = 0;

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

            int64_t qty = p.read_compressed(bid_qty_base, true); // total qty
            p.read_compressed(bid_qty_base);                      // no. of orders
            p.read_compressed(bid_qty_base, true);                // implied qty
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

            int64_t qty = p.read_compressed(ask_qty_base, true); // total qty
            p.read_compressed(ask_qty_base);                      // no. of orders
            p.read_compressed(ask_qty_base, true);                // implied qty
            p.read_compressed(ask_qty_base);

            ask_price_base = price_raw;
            ask_qty_base = qty;
        }

        pending.push_back(PendingBseTick{token, ltp, best_bid, best_ask, current_oi});
        (void)token_pos;
    }

    // Integrity check: with the verified layout every 2020 message is used
    // up EXACTLY by its records (all 40 live samples, 2026-09-30). If not,
    // this message is a variant we don't understand, and any of its records
    // could be read from the wrong bytes -- drop the whole message rather
    // than risk a garbage price landing on a real token.
    if (p.pos() != length || static_cast<int>(pending.size()) != num_records) {
        static uint64_t misaligned = 0;
        static auto last_report = std::chrono::steady_clock::now();
        ++misaligned;
        dump_bse_sample(buffer, length, msg_type, num_records, -1,
                        rec_starts.empty() ? 0 : rec_starts.back(), p.pos(), 0, rec_starts);
        auto now = std::chrono::steady_clock::now();
        if (now - last_report >= std::chrono::seconds(30)) {
            std::cout << "[BSE] ⚠️ " << misaligned
                      << " BSE message(s) in the last 30s did not parse to their exact length -- dropped whole"
                      << " (samples in logs/bse_samples.hex)" << std::endl;
            misaligned = 0;
            last_report = now;
        }
        return;
    }

    for (const auto& t : pending) {
        ContractInfo info = normalizer.get_contract(t.token);
        if (!info.is_valid) {
            // A real, correctly-parsed record for a contract we don't load
            // (seen live: untraded contracts with 0 trades/LTP that are not
            // in BSEIndexTokens.csv). Harmless -- summarised every 5 minutes.
            static std::unordered_map<uint32_t, uint64_t> unlisted;
            static uint64_t unlisted_total = 0;
            static auto last_summary = std::chrono::steady_clock::now();
            ++unlisted_total;
            if (unlisted.size() < 64 || unlisted.count(t.token)) ++unlisted[t.token];
            auto now = std::chrono::steady_clock::now();
            if (now - last_summary >= std::chrono::minutes(5)) {
                std::vector<std::pair<uint32_t, uint64_t>> top(unlisted.begin(), unlisted.end());
                std::partial_sort(top.begin(), top.begin() + std::min<size_t>(5, top.size()), top.end(),
                    [](const auto& a, const auto& b) { return a.second > b.second; });
                std::cout << "[BSE] ℹ️ " << unlisted_total << " ticks in the last 5m for "
                          << unlisted.size() << " BSE contract(s) not in BSEIndexTokens.csv (ignored), e.g.:";
                for (size_t i = 0; i < std::min<size_t>(5, top.size()); ++i)
                    std::cout << " " << top[i].first << "(x" << top[i].second << ")";
                std::cout << std::endl;
                unlisted.clear();
                unlisted_total = 0;
                last_summary = now;
            }
            continue;
        }

        static int bse_tick_log_throttle = 0;
        if (++bse_tick_log_throttle % 1000 == 0) {
            std::cout << "[Decoder] 📡 BSE TICK | "
                      << info.symbol
                      << " | LTP: ₹" << t.ltp << std::endl;
        }

        greeks_calc.process_tick(t.token, t.ltp, t.best_bid, t.best_ask, 0, t.oi, info);
    }
}

} // namespace decoder