#pragma once
#include <vector>
#include <cstdint>

namespace decoder {

class Decompressor {
public:
    Decompressor();
    bool decompress(const uint8_t* input, int input_len, std::vector<uint8_t>& output);
private:
    bool init_ = false;
};

} // namespace decoder