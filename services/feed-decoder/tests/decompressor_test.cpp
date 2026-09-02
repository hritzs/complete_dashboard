#include <gtest/gtest.h>
#include "decoder/decompressor.h"
#include <zlib.h>
#include <string>
#include <vector>

// Test fixture for the Decompressor class
class DecompressorTest : public ::testing::Test {};

TEST_F(DecompressorTest, DecompressesDataCorrectly) {
    // 1. Define the original, uncompressed data.
    const std::string original_data = "This is a test string for the decompressor. "
                                      "It needs to be reasonably long to ensure that "
                                      "compression actually occurs and we can test the logic.";

    // 2. Compress this data in-memory using zlib's `compress` function.
    // This gives us a valid, compressed input for our test.
    uLong source_len = original_data.length() + 1; // +1 for the null terminator
    uLong compressed_len = compressBound(source_len);
    std::vector<char> compressed_buffer(compressed_len);

    int res = compress(
        reinterpret_cast<Bytef*>(compressed_buffer.data()),
        &compressed_len,
        reinterpret_cast<const Bytef*>(original_data.c_str()),
        source_len
    );
    ASSERT_EQ(res, Z_OK) << "zlib compression failed during test setup.";

    // 3. Instantiate our Decompressor and decompress the data.
    decoder::Decompressor decompressor;
    std::vector<char> decompressed_buffer(source_len);

    size_t decompressed_size = decompressor.decompress(
        compressed_buffer.data(),
        compressed_len,
        decompressed_buffer.data(),
        decompressed_buffer.size()
    );

    // 4. Assert that the output matches the original input.
    // Check that the returned size is correct.
    ASSERT_EQ(decompressed_size, source_len);

    // Check that the content of the decompressed buffer is identical to the original string.
    ASSERT_STREQ(decompressed_buffer.data(), original_data.c_str());
}