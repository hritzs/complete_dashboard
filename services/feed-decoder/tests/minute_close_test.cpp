// g++ -std=c++17 -Iinclude tests/minute_close_test.cpp src/minute_close.cpp -o /tmp/mc && /tmp/mc
#include "decoder/minute_close.hpp"
#include <cassert>
#include <cstdio>

using namespace decoder;

int main() {
    const int64_t B = 1791345420; // 09:17:00 IST, a minute boundary
    assert(B % 60 == 0);
    MinuteClose st;

    minute_close_apply(st, 157.45, B - 5);   // 09:16:55
    minute_close_apply(st, 155.75, B - 1);   // 09:16:59 -- the candle close
    assert(st.close_next_s == 0);            // not final yet: no later-minute trade
    minute_close_apply(st, 155.70, B - 2);   // late/older update: ignored
    assert(st.last_px == 155.75);

    minute_close_apply(st, 157.00, B);       // 09:17:00 first trade of the new minute
    assert(st.close_px == 155.75 && st.close_ltt_s == B - 1 && st.close_next_s == B);
    minute_close_apply(st, 158.00, B + 30);  // same minute: close for B unchanged
    assert(st.close_px == 155.75 && st.close_next_s == B);

    minute_close_apply(st, 159.00, B + 200); // trades skip minutes: close spans them
    assert(st.close_px == 158.00 && st.close_ltt_s == B + 30 && st.close_next_s == B + 200);

    assert(nse_time_to_unix(1475833565) == 1791346565); // live header sample 2026-10-07
    printf("OK: minute_close_test passed\n");
}
