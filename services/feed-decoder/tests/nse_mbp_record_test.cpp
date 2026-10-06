// Standalone test (no gtest dependency -- none is installed) for the NSE
// 7208/17208 record decoder. Verifies against two real messages captured
// live from the production multicast feed on 2026-09-23, each containing
// two instrument records back to back, which is exactly the case the old
// RECORD_SIZE=214 bug misread (only record 0 ever decoded correctly).
#include "decoder/nse_mbp_record.hpp"
#include <cassert>
#include <cstdio>
#include <cstdint>
#include <string>
#include <vector>

static std::vector<uint8_t> from_hex(const std::string& hex) {
    std::vector<uint8_t> out;
    out.reserve(hex.size() / 2);
    for (size_t i = 0; i + 1 < hex.size(); i += 2) {
        out.push_back(static_cast<uint8_t>(std::stoul(hex.substr(i, 2), nullptr, 16)));
    }
    return out;
}

// Live capture: recs=2, msg_len=558, tokens 73927 (NIFTY 23400 CE) then
// 73928 (PE), same underlying expiry, captured back to back in one
// decompressed NSE multicast message.
static const std::string kLiveMsgHex =
    "0000000057e504460000433800000005bdf50e000000000057e50446ad720820202020202020"
    "022e0002000120c700010002000000000471e40a000033452b0000002f850000004157e50446"
    "0000300200000000000000000000000000000000000000000000000000000000014500003327"
    "000200000000000000000492000033220008000000000000000004920000331d000800000000"
    "00000000086100003318000c0000000000000000030c00003313000300000000000000000861"
    "00003345000c00000000000000000a280000334a0010000000000000000008e30000334f000d"
    "0000000000000000092400003354000e0000000000000000059600003359000a000000000000"
    "00000000002976ce000000000015dd9e800000002f8500002ecc000037e60000270b000120c8"
    "0001000200000000045df382000028d22d00000033630000010457e5044600002e2000000000"
    "000000000000000000000000000000000000000000000000079e000028b9000b000000000000"
    "00000b6d000028b4001000000000000000000a69000028af000e00000000000000000c710000"
    "28aa00110000000000000000071c000028a5000c000000000000000003cf000028d200060000"
    "000000000000075d000028d7000d00000000000000000d75000028dc00120000000000000000"
    "075d000028e1000c00000000000000000514000028e60007000000000000000000000024c1c4"
    "000000000013cbb78000000033630000339000003561000027d8";

int main() {
    auto msg = from_hex(kLiveMsgHex);
    printf("decoded message hex to %zu bytes\n", msg.size());
    assert(msg.size() == 558);

    auto recs = decoder::parse_nse_7208_records(msg.data(), msg.size());
    assert(recs.size() == 2);

    // Record 0: token 73927, ltp 131.25 -- previously the ONLY record this
    // parser could ever get right, since it always starts at offset 42.
    assert(recs[0].token == 73927);
    assert(recs[0].ltp > 131.24 && recs[0].ltp < 131.26);
    // Best bid/ask from the confirmed depth-ladder layout (offset 68,
    // 16-byte stride): buy0=130.95, sell0=131.25.
    assert(recs[0].buy[0].price > 130.94 && recs[0].buy[0].price < 130.96);
    assert(recs[0].sell[0].price > 131.24 && recs[0].sell[0].price < 131.26);
    // Full descending buy / ascending sell ladder.
    double expected_buy[5]  = {130.95, 130.90, 130.85, 130.80, 130.75};
    double expected_sell[5] = {131.25, 131.30, 131.35, 131.40, 131.45};
    for (int i = 0; i < 5; ++i) {
        assert(recs[0].buy[i].price > expected_buy[i] - 0.01 && recs[0].buy[i].price < expected_buy[i] + 0.01);
        assert(recs[0].sell[i].price > expected_sell[i] - 0.01 && recs[0].sell[i].price < expected_sell[i] + 0.01);
    }

    // Quantities (int64 before each price) -- every one a whole NIFTY lot
    // of 65; the old read returned order_count << 16 (e.g. 2 -> 131072).
    unsigned expected_bq[5] = {325, 1170, 1170, 2145, 780};
    unsigned expected_sq[5] = {2145, 2600, 2275, 2340, 1430};
    unsigned expected_bo[5] = {2, 8, 8, 12, 3};
    for (int i = 0; i < 5; ++i) {
        assert(recs[0].buy[i].qty == expected_bq[i]);
        assert(recs[0].sell[i].qty == expected_sq[i]);
        assert(recs[0].buy[i].orders == expected_bo[i]);
        assert(recs[0].buy[i].qty % 65 == 0 && recs[0].sell[i].qty % 65 == 0);
    }

    // Record 1: token 73928 -- this is the record the RECORD_SIZE=214 bug
    // always misread, since it starts at offset 42+214=256 under the old
    // (wrong) stride instead of the real 42+258=300.
    assert(recs[1].token == 73928);
    assert(recs[1].ltp > 0.0);
    assert(recs[1].buy[0].qty == 1950 && recs[1].sell[4].qty == 1300);

    printf("OK: nse_mbp_record_test passed (record0.token=%u ltp=%.2f, record1.token=%u ltp=%.2f)\n",
           recs[0].token, recs[0].ltp, recs[1].token, recs[1].ltp);
    return 0;
}
