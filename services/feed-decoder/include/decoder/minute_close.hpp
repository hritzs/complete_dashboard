#pragma once
#include <cstdint>
#include <mutex>
#include <unordered_map>

namespace decoder {

// Per-token "candle close": the last trade before each minute boundary,
// keyed on the EXCHANGE trade time -- what a terminal's 1-minute candle
// shows, independent of when (or how conflated) the update reached us.
//
// Feed it every (token, price, trade time) seen, from any source (the NSE
// MBP broadcast's LTP+LTT today; tick-by-tick trades if wired in later).
// When the first trade of a later minute arrives, the previous last trade
// becomes final: `close_px` is the last price strictly before
// `close_next_s` (that new trade's time), and it is the close for every
// minute boundary B with close_ltt_s < B <= close_next_s.
struct MinuteClose {
    double  last_px = 0.0;      // latest trade price
    int64_t last_ltt_s = 0;     // its trade time (unix seconds)
    double  close_px = 0.0;     // last trade before the minute that followed it
    int64_t close_ltt_s = 0;    // that trade's time
    int64_t close_next_s = 0;   // time of the first trade after it (a later minute)
};

class MinuteCloseTracker {
public:
    void update(uint32_t token, double px, int64_t ltt_unix_s);
    bool get(uint32_t token, MinuteClose& out) const;

private:
    mutable std::mutex mu_;
    std::unordered_map<uint32_t, MinuteClose> by_token_;
};

// Pure state transition (unit-tested).
void minute_close_apply(MinuteClose& st, double px, int64_t ltt_unix_s);

// NSE broadcast times count seconds from 1980-01-01 IST.
constexpr int64_t kNseEpochToUnixS = 315532800LL - 19800LL;
inline int64_t nse_time_to_unix(uint32_t t) {
    return t == 0 ? 0 : static_cast<int64_t>(t) + kNseEpochToUnixS;
}

extern MinuteCloseTracker g_minute_close;

} // namespace decoder
