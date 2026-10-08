#include "decoder/minute_close.hpp"

namespace decoder {

MinuteCloseTracker g_minute_close;

void minute_close_apply(MinuteClose& st, double px, int64_t ltt) {
    if (px <= 0.0 || ltt <= 0) return;
    if (st.last_ltt_s == 0) {
        st.last_px = px;
        st.last_ltt_s = ltt;
        return;
    }
    if (ltt < st.last_ltt_s) return; // older than what we have: ignore
    if (ltt / 60 > st.last_ltt_s / 60) {
        // First trade of a later minute: the previous last trade is final.
        st.close_px = st.last_px;
        st.close_ltt_s = st.last_ltt_s;
        st.close_next_s = ltt;
    }
    st.last_px = px;
    st.last_ltt_s = ltt;
}

void MinuteCloseTracker::update(uint32_t token, double px, int64_t ltt) {
    std::lock_guard<std::mutex> lk(mu_);
    minute_close_apply(by_token_[token], px, ltt);
}

bool MinuteCloseTracker::get(uint32_t token, MinuteClose& out) const {
    std::lock_guard<std::mutex> lk(mu_);
    auto it = by_token_.find(token);
    if (it == by_token_.end()) return false;
    out = it->second;
    return true;
}

} // namespace decoder
