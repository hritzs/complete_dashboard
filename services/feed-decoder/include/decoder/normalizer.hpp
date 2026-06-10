#pragma once
#include <string>
#include <unordered_map>
#include <cstdint>

namespace decoder {

struct ContractInfo {
    uint32_t token;
    std::string base_symbol; // Needed to identify the underlying (e.g. NIFTY)
    std::string symbol;
    double strike;
    double time_to_expiry_years;
    bool is_call;
    bool is_valid = false;
};

class Normalizer {
public:
    std::unordered_map<uint32_t, ContractInfo> token_map_;

    bool load_contracts_csv(const std::string& filepath);
    ContractInfo get_contract(uint32_t token) const;
};

} // namespace decoder