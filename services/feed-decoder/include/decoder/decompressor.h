#pragma once
#include <cstddef>

namespace decoder {
class Decompressor {
public:
    size_t decompress(const char* source, size_t source_len, char* dest, size_t dest_len);
};
}