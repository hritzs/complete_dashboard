#include "decoder/decompressor.hpp"
#include <lzo/lzo1z.h>

namespace decoder {

Decompressor::Decompressor() {
    if (lzo_init() == LZO_E_OK) {
        init_ = true;
    }
}

bool Decompressor::decompress(const uint8_t* input, int input_len, std::vector<uint8_t>& output) {
    if (!init_) return false;
    
    output.resize(65536);
    lzo_uint out_len = 65536;
    
    int r = lzo1z_decompress(input, (lzo_uint)input_len, output.data(), &out_len, nullptr);
    if (r != LZO_E_OK) return false;
    
    output.resize((size_t)out_len);
    return true;
}

} // namespace decoder