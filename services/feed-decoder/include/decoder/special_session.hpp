#pragma once

// Special trading sessions (NSE Diwali Muhurat), shared with the execution
// gateway (internal/trading/special_session.go): a JSON list of
//   {"date":"YYYY-MM-DD","name":"...","pre_open":"HH:MM","open":"HH:MM","close":"HH:MM"}
// at $SPECIAL_SESSIONS_FILE or ~/.trading-platform/special_sessions.json.
// On a listed date the DTE fraction counts only that session's remaining
// trading minutes; on every other date nothing changes. Re-read at most
// once a minute.

#include <cstdlib>
#include <ctime>
#include <fstream>
#include <mutex>
#include <regex>
#include <sstream>
#include <string>
#include <unordered_map>

namespace decoder {

struct SpecialSessionWindow {
    double open_min = 0;   // minutes since midnight
    double close_min = 0;
};

inline std::string special_sessions_path() {
    if (const char* p = std::getenv("SPECIAL_SESSIONS_FILE"); p && *p) return p;
    const char* home = std::getenv("HOME");
    return std::string(home ? home : ".") + "/.trading-platform/special_sessions.json";
}

// special_session_today: the session listed for `ymd` ("YYYY-MM-DD"), if any.
inline bool special_session_for(const std::string& ymd, SpecialSessionWindow& out) {
    static std::mutex mu;
    static std::time_t loaded_at = 0;
    static std::unordered_map<std::string, SpecialSessionWindow> by_day;

    std::lock_guard<std::mutex> lock(mu);
    std::time_t now = std::time(nullptr);
    if (loaded_at == 0 || now - loaded_at >= 60) {
        loaded_at = now;
        by_day.clear();
        std::ifstream f(special_sessions_path());
        if (f.is_open()) {
            std::stringstream ss;
            ss << f.rdbuf();
            const std::string body = ss.str();
            // One object per session; the fields may come in any order.
            static const std::regex obj(R"(\{[^{}]*\})");
            static const std::regex date_re(R"re("date"\s*:\s*"(\d{4}-\d{2}-\d{2})")re");
            static const std::regex open_re(R"re("open"\s*:\s*"(\d{1,2}):(\d{2})")re");
            static const std::regex close_re(R"re("close"\s*:\s*"(\d{1,2}):(\d{2})")re");
            for (auto it = std::sregex_iterator(body.begin(), body.end(), obj); it != std::sregex_iterator(); ++it) {
                const std::string o = it->str();
                std::smatch d, op, cl;
                if (std::regex_search(o, d, date_re) && std::regex_search(o, op, open_re) &&
                    std::regex_search(o, cl, close_re)) {
                    SpecialSessionWindow w;
                    w.open_min = std::stoi(op[1]) * 60.0 + std::stoi(op[2]);
                    w.close_min = std::stoi(cl[1]) * 60.0 + std::stoi(cl[2]);
                    if (w.close_min > w.open_min) by_day[d[1]] = w;
                }
            }
        }
    }
    auto it = by_day.find(ymd);
    if (it == by_day.end()) return false;
    out = it->second;
    return true;
}

}  // namespace decoder
