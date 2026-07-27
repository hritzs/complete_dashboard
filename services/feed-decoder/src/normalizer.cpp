#include "decoder/normalizer.hpp"

#include <fstream>
#include <sstream>
#include <iostream>
#include <vector>
#include <algorithm>
#include <map>
#include <cctype>
#include <ctime>

namespace decoder {

std::map<std::string, std::string> g_nearest_expiry;
std::map<std::string, std::vector<std::string>> g_available_expiries;

namespace {

static inline void trim_inplace(std::string& s) {
    s.erase(std::remove(s.begin(), s.end(), '\r'), s.end());
    s.erase(std::remove(s.begin(), s.end(), '\n'), s.end());

    while (!s.empty() && std::isspace((unsigned char)s.front())) s.erase(s.begin());
    while (!s.empty() && std::isspace((unsigned char)s.back())) s.pop_back();

    if (!s.empty() && s.front() == '"' && s.back() == '"' && s.size() >= 2) {
        s = s.substr(1, s.size() - 2);
    }
}

// --------------------------------------------------
static inline std::string upper_copy(std::string s) {
    std::transform(
        s.begin(),
        s.end(),
        s.begin(),
        [](unsigned char c) { return (char)std::toupper(c); }
    );
    return s;
}

// --------------------------------------------------
static inline char detect_delimiter(const std::string& line) {
    if (line.find('\t') != std::string::npos) return '\t';
    return ',';
}

// --------------------------------------------------
static inline std::vector<std::string> split_line(const std::string& line, char delim) {
    std::vector<std::string> cols;
    std::string cell;
    bool in_quotes = false;

    for (char c : line) {
        if (c == '"') {
            in_quotes = !in_quotes;
        } else if (c == delim && !in_quotes) {
            trim_inplace(cell);
            cols.push_back(cell);
            cell.clear();
        } else {
            cell += c;
        }
    }

    trim_inplace(cell);
    cols.push_back(cell);
    return cols;
}

// --------------------------------------------------
static double safe_stod(const std::string& str, double def = 0.0) {
    if (str.empty()) return def;
    try { return std::stod(str); } catch (...) { return def; }
}

// --------------------------------------------------
static uint32_t safe_stoul(const std::string& str, uint32_t def = 0) {
    if (str.empty()) return def;
    try { return static_cast<uint32_t>(std::stoul(str)); } catch (...) { return def; }
}

// --------------------------------------------------
static long long expiry_to_yyyymmdd(std::string exp) {
    trim_inplace(exp);
    if (exp.empty() || upper_copy(exp) == "NA") return 99999999LL;

    std::replace(exp.begin(), exp.end(), '-', ' ');
    std::replace(exp.begin(), exp.end(), '/', ' ');

    std::istringstream iss(exp);
    std::string d, m, y;
    iss >> d >> m >> y;

    if (d.empty() || m.empty() || y.empty()) return 99999999LL;

    int day = 0, year = 0, month = 99;
    try { day = std::stoi(d); } catch (...) {}
    try { year = std::stoi(y); } catch (...) {}

    if (year > 0 && year < 100) year += 2000;

    m = upper_copy(m);

    if      (m == "JAN") month = 1;
    else if (m == "FEB") month = 2;
    else if (m == "MAR") month = 3;
    else if (m == "APR") month = 4;
    else if (m == "MAY") month = 5;
    else if (m == "JUN") month = 6;
    else if (m == "JUL") month = 7;
    else if (m == "AUG") month = 8;
    else if (m == "SEP") month = 9;
    else if (m == "OCT") month = 10;
    else if (m == "NOV") month = 11;
    else if (m == "DEC") month = 12;

    if (year >= 2000 && month <= 12 && day > 0) {
        return year * 10000LL + month * 100 + day;
    }

    return 99999999LL;
}

// --------------------------------------------------
// STRICT DTE NORMALIZATION (Institutional Fix)
// --------------------------------------------------
static std::unordered_map<std::string, int> MONTH_MAP = {
    {"JAN",0},{"FEB",1},{"MAR",2},{"APR",3},{"MAY",4},{"JUN",5},
    {"JUL",6},{"AUG",7},{"SEP",8},{"OCT",9},{"NOV",10},{"DEC",11}
};

static double to_epoch(const std::string& exp_in) {
    std::string exp = exp_in;
    exp.erase(std::remove(exp.begin(), exp.end(), '-'), exp.end());
    exp.erase(std::remove(exp.begin(), exp.end(), '/'), exp.end());

    if (exp.size() < 5) return 0;

    int day = 0;
    try { day = std::stoi(exp.substr(0,2)); } catch (...) { return 0; }
    std::string mon = exp.substr(2,3);
    for (char& c : mon) c = std::toupper(c);
    if (!MONTH_MAP.count(mon)) return 0;

    std::time_t t = std::time(nullptr);
    std::tm tm = *std::localtime(&t);
    tm.tm_mday = day;
    tm.tm_mon  = MONTH_MAP[mon];
    tm.tm_hour = 15; tm.tm_min = 30; tm.tm_sec = 0; tm.tm_isdst = -1;

    if (exp.size() >= 9) {
        int year_suffix = 0;
        try { year_suffix = std::stoi(exp.substr(5, 4)); } catch (...) { year_suffix = tm.tm_year + 1900; }
        tm.tm_year = year_suffix - 1900;
    } else if (exp.size() >= 7) {
        int year_suffix = 0;
        try { year_suffix = std::stoi(exp.substr(5, 2)); } catch(...) { year_suffix = tm.tm_year % 100; }
        if (year_suffix < 100) year_suffix += 2000;
        tm.tm_year = year_suffix - 1900;
    } else {
        int curr_year = tm.tm_year;
        tm.tm_year = curr_year;
        std::time_t cand_t = std::mktime(&tm);
        if (cand_t < t - 86400) { tm.tm_year = curr_year + 1; }
    }
    return static_cast<double>(std::mktime(&tm));
}

static double calculate_dte(const std::string& expiry) {
    double epoch = to_epoch(expiry);
    if (epoch <= 0.0) return 7.0 / 365.0; // Fail-safe
    double now = static_cast<double>(std::time(nullptr));
    double diff_days = (epoch - now) / 86400.0;
    return std::max(0.0001, diff_days) / 365.0; // Converted exactly to years for Black-Scholes
}

// --------------------------------------------------
// Today in YYYYMMDD (local time)
// --------------------------------------------------
static long long today_yyyymmdd() {
    std::time_t t = std::time(nullptr);
    std::tm tm_local = *std::localtime(&t);

    return (tm_local.tm_year + 1900) * 10000LL +
           (tm_local.tm_mon + 1) * 100LL +
           tm_local.tm_mday;
}

} // namespace

// ======================================================
// MAIN CSV LOADER
// ======================================================
bool Normalizer::load_contracts_csv(const std::string& filepath) {
    std::ifstream file(filepath);
    if (!file.is_open()) {
        std::cerr << "[Normalizer] ❌ Failed to open file: " << filepath << "\n";
        return false;
    }

    std::string headerLine;
    if (!std::getline(file, headerLine)) {
        std::cerr << "[Normalizer] ❌ Empty file: " << filepath << "\n";
        return false;
    }

    const char delim = detect_delimiter(headerLine);

    struct TempContract {
        ContractInfo info;
        std::string expiry;
        bool isDerivative = false;
        bool isFuture = false;
    };

    std::vector<TempContract> temp_contracts;
    std::map<std::string, long long> min_expiry_val;

    const long long todayVal = today_yyyymmdd();

    std::string line;
    int parsed = 0;
    int skipped = 0;

    while (std::getline(file, line)) {
        trim_inplace(line);
        if (line.empty()) continue;

        auto cols = split_line(line, delim);
        if (cols.size() < 8) {
            skipped++;
            continue;
        }

        const std::string xxTokenStr = cols[0];
        const std::string exchange   = upper_copy(cols[1]);
        const std::string type       = upper_copy(cols[2]);
        const std::string symbol     = upper_copy(cols[3]);
        const std::string expiry     = upper_copy(cols[4]);
        const std::string strikeStr  = cols[5];
        const std::string opType     = upper_copy(cols[6]);
        const std::string tokenStr   = upper_copy(cols[7]);

        if (symbol.empty()) {
            skipped++;
            continue;
        }

        ContractInfo info{};
        info.base_symbol = symbol;
        info.strike = safe_stod(strikeStr, 0.0);
        info.expiry = expiry;
        info.dte = calculate_dte(expiry);
        info.time_to_expiry_years = info.dte;
        info.is_valid = true;

        bool isDerivative = false;
        bool isFuture = false;

        // ---------------------------
        // OPTIONS
        // ---------------------------
        if (opType == "CE" || opType == "PE") {
            const long long expVal = expiry_to_yyyymmdd(expiry);

            // Keep only today or later
            if (expVal < todayVal) {
                skipped++;
                continue;
            }

            info.symbol = symbol + opType + strikeStr;
            info.is_call = (opType == "CE");
            isDerivative = true;

            if (min_expiry_val.find(symbol) == min_expiry_val.end() ||
                expVal < min_expiry_val[symbol]) {
                min_expiry_val[symbol] = expVal;
                g_nearest_expiry[symbol] = expiry;
            }
        }
        // ---------------------------
        // FUTURES
        // ---------------------------
        else if (opType == "XX" || opType == "FUT" || type.find("FUT") != std::string::npos) {
            const long long expVal = expiry_to_yyyymmdd(expiry);

            // Also reject stale futures
            if (expVal < todayVal) {
                skipped++;
                continue;
            }

            info.symbol = symbol + "FUT";
            info.is_call = false;
            isDerivative = true;
            isFuture = true;
        }
        else {
            skipped++;
            continue;
        }

        // ---------------------------
        // Token registration
        // ---------------------------
        std::vector<uint32_t> runtimeTokens;

        if (exchange == "NSE") {
            uint32_t t = safe_stoul(tokenStr, 0);
            if (t > 0) {
                runtimeTokens.push_back(t);
            }
        }
        else if (exchange == "BSE") {
            // IMPORTANT: register BOTH BSE token columns
            uint32_t t1 = safe_stoul(xxTokenStr, 0);
            uint32_t t2 = safe_stoul(tokenStr, 0);

            if (t1 > 0) runtimeTokens.push_back(t1);
            if (t2 > 0 && t2 != t1) runtimeTokens.push_back(t2);
        }
        else {
            skipped++;
            continue;
        }

        if (runtimeTokens.empty()) {
            skipped++;
            continue;
        }

        // Store one temp contract per runtime token
        for (uint32_t runtimeToken : runtimeTokens) {
            ContractInfo copied = info;
            copied.token = runtimeToken;

            temp_contracts.push_back({
                copied,
                expiry,
                isDerivative,
                isFuture
            });
            parsed++;
        }
    }

    // ======================================================
    // FINAL LOAD STEP
    // Retain all expiries concurrently and map out token relations
    // ======================================================
    int loaded = 0;

    for (const auto& tc : temp_contracts) {
        if (!tc.isFuture && tc.isDerivative) {
            auto& exps = g_available_expiries[tc.info.base_symbol];
            if (std::find(exps.begin(), exps.end(), tc.expiry) == exps.end()) {
                exps.push_back(tc.expiry);
            }
        }

        token_map_[tc.info.token] = tc.info;
        loaded++;
    }

    // Sort expiries cleanly from closest to furthest
    for (auto& pair : g_available_expiries) {
        std::sort(pair.second.begin(), pair.second.end(), [](const std::string& a, const std::string& b){
            return to_epoch(a) < to_epoch(b);
        });
    }

    std::cout << "[Normalizer] ✅ File: " << filepath
              << " | Parsed: " << parsed
              << " | Loaded: " << loaded
              << " | Skipped: " << skipped
              << std::endl;

    return loaded > 0;
}

// ======================================================
ContractInfo Normalizer::get_contract(uint32_t token) const {
    auto it = token_map_.find(token);
    if (it != token_map_.end()) return it->second;

    // Fallback masked lookup
    uint32_t masked = token & 0x00FFFFFF;
    it = token_map_.find(masked);
    if (it != token_map_.end()) return it->second;

    return ContractInfo{};
}

} // namespace decoder