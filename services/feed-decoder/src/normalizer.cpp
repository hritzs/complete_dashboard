#include "decoder/normalizer.hpp"
#include <fstream>
#include <sstream>
#include <iostream>
#include <stdexcept>
#include <vector>
#include <algorithm>
#include <map>

namespace decoder {

std::map<std::string, std::string> g_nearest_expiry;

static inline void clean_csv_token(std::string& token) {
    // Remove literal double quotes
    token.erase(std::remove(token.begin(), token.end(), '"'), token.end());
    // Remove carriage returns (Windows line endings)
    token.erase(std::remove(token.begin(), token.end(), '\r'), token.end());
    // Remove any stray spaces
    token.erase(std::remove(token.begin(), token.end(), ' '), token.end());
}

static double safe_stod(const std::string& str, double def = 0.0) {
    if (str.empty()) return def;
    try { return std::stod(str); } catch (...) { return def; }
}

static uint32_t safe_stoul(const std::string& str, uint32_t def = 0) {
    if (str.empty()) return def;
    try { return std::stoul(str); } catch (...) { return def; }
}

static int month_to_int(const std::string& m) {
    if (m=="JAN") return 1; if (m=="FEB") return 2; if (m=="MAR") return 3;
    if (m=="APR") return 4; if (m=="MAY") return 5; if (m=="JUN") return 6;
    if (m=="JUL") return 7; if (m=="AUG") return 8; if (m=="SEP") return 9;
    if (m=="OCT") return 10; if (m=="NOV") return 11; if (m=="DEC") return 12;
    return 99;
}

static long long expiry_to_yyyymmdd(std::string exp) {
    if (exp.empty() || exp == "NA") return 99999999LL;
    
    std::string expParts = exp; 
    std::replace(expParts.begin(), expParts.end(), '-', ' ');
    std::replace(expParts.begin(), expParts.end(), '/', ' ');
    
    std::istringstream iss(expParts);
    std::string d, m, y;
    iss >> d >> m >> y;
    
    if (d.empty() || m.empty() || y.empty()) {
        if (exp.length() >= 7) {
            size_t m_start = 0;
            while (m_start < exp.length() && std::isdigit(exp[m_start])) m_start++;
            size_t m_end = m_start;
            while (m_end < exp.length() && std::isalpha(exp[m_end])) m_end++;
            
            if (m_start > 0 && m_end > m_start && m_end < exp.length()) {
                d = exp.substr(0, m_start);
                m = exp.substr(m_start, m_end - m_start);
                y = exp.substr(m_end);
            } else return 99999999LL;
        } else return 99999999LL;
    }

    int day = 0, year = 0, month = 99;
    try { day = std::stoi(d); } catch(...) {}
    try { year = std::stoi(y); } catch(...) {}
    if (year > 0 && year < 100) year += 2000;
    
    for(auto& c: m) c = std::toupper(c);
    if(m.length() > 3) m = m.substr(0, 3);
    
    if (m == "JAN") month = 1; else if (m == "FEB") month = 2;
    else if (m == "MAR") month = 3; else if (m == "APR") month = 4;
    else if (m == "MAY") month = 5; else if (m == "JUN") month = 6;
    else if (m == "JUL") month = 7; else if (m == "AUG") month = 8;
    else if (m == "SEP") month = 9; else if (m == "OCT") month = 10;
    else if (m == "NOV") month = 11; else if (m == "DEC") month = 12;
    else { try { month = std::stoi(m); } catch(...) {} }
    
    if (year >= 2000 && month <= 12 && day > 0) {
        return year * 10000LL + month * 100 + day;
    }
    return 99999999LL;
}

bool Normalizer::load_contracts_csv(const std::string& filepath) {
    // Removed token_map_.clear() so multiple CSV files (NSE + BSE) can be loaded into the same memory pool
    std::ifstream file(filepath);
    if (!file.is_open()) {
        std::cerr << "[Normalizer] ❌ Failed to open contracts file: " << filepath << "\n";
        return false;
    }

    std::string line;
    std::getline(file, line); // Skip header row

    struct TempContract {
        ContractInfo info;
        std::string expiry;
    };

    std::vector<TempContract> temp_contracts;
    std::map<std::string, long long> min_expiry_val;
    std::map<std::string, std::string> nearest_expiry;

    while (std::getline(file, line)) {
        line.erase(std::remove(line.begin(), line.end(), '\r'), line.end());
        if (line.empty()) continue;

        std::stringstream ss(line);
        std::string cell;
        std::vector<std::string> cols;
        while(std::getline(ss, cell, ',')) {
            clean_csv_token(cell);
            cols.push_back(cell);
        }

        if (cols.size() < 8) continue; // Need at least 8 columns for the market token

        // Format: xx.Token(0),Exchange(1),Type(2),Symbol(3),Expiry(4),Strike(5),OpType(6),Token(7)
        const std::string& market_tok_str = cols[0]; // FIX: Use Column 0 for Unique Option Token
        const std::string& sym = cols[3];
        const std::string& expiry = cols[4];
        const std::string& strike_str = cols[5];
        const std::string& opt_type = cols[6];
        const std::string& instrument_type = cols[2];

        ContractInfo info;
        info.token = safe_stoul(market_tok_str, 0);
        if (info.token == 0) continue;

        info.base_symbol = sym;
        info.strike = safe_stod(strike_str, 0.0);
        info.time_to_expiry_years = 7.0 / 365.0; // Fail-safe DTE, will be recalculated live
        info.is_valid = true;

        // 1. Futures (Distinguish by Expiry so they don't overwrite Cash Index)
        if (instrument_type.find("FUT") != std::string::npos) {
            info.symbol = sym + expiry + "FUT"; 
            info.is_call = false;
            long long exp_val = expiry_to_yyyymmdd(expiry);
            if (min_expiry_val.find(sym) == min_expiry_val.end() || exp_val < min_expiry_val[sym]) {
                min_expiry_val[sym] = exp_val;
                g_nearest_expiry[sym] = expiry;
            }
        }
        // 2. Cash Index (EQ) - Strictly mapped to the Spot Price Slot
        else if (instrument_type.find("INDEX") != std::string::npos || instrument_type == "EQ" || opt_type == "XX") {
            info.symbol = sym + "FUT"; 
            info.is_call = false;
        } 
        // 3. Options
        else if (opt_type == "CE" || opt_type == "PE") {
            info.symbol = sym + opt_type + strike_str;
            info.is_call = (opt_type == "CE");
            long long exp_val = expiry_to_yyyymmdd(expiry);
            if (min_expiry_val.find(sym) == min_expiry_val.end() || exp_val < min_expiry_val[sym]) {
                min_expiry_val[sym] = exp_val;
                g_nearest_expiry[sym] = expiry;
            }
        } 
        else {
            continue; 
        }

        TempContract tc = {info, expiry};
        temp_contracts.push_back(tc);
    }

    int count = 0;
    for (const auto& tc : temp_contracts) {
        // Skip far expiries for BOTH Options and Futures!
        if (!tc.expiry.empty() && tc.expiry != g_nearest_expiry[tc.info.base_symbol]) {
            continue; 
        }
        token_map_[tc.info.token] = tc.info;
        count++;
    }

    std::cout << "[Normalizer] ✅ Loaded " << count << " contracts into lookup map.\n";
    return true;
}

ContractInfo Normalizer::get_contract(uint32_t token) const {
    auto it = token_map_.find(token);
    if (it != token_map_.end()) {
        return it->second;
    }
    return ContractInfo{}; // is_valid = false
}

} // namespace decoder