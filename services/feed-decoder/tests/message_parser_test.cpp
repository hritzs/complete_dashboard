#include <gtest/gtest.h>
#include "decoder/message_parser.h"
#include "protocol/exchange_protocol.h"
#include <vector>
#include <string>

// Test fixture for the MessageParser class
class MessageParserTest : public ::testing::Test {};

TEST_F(MessageParserTest, ParsesMultipleMessagesCorrectly) {
    // 1. Create a buffer containing three mock messages.
    std::vector<protocol::ExchangeMessage> original_messages = {
        {100, 25001, 1234567890},
        {101, 25002, 1234567891},
        {102, 25003, 1234567892}
    };

    // Copy the raw bytes into a character buffer.
    const size_t buffer_size = sizeof(protocol::ExchangeMessage) * original_messages.size();
    std::vector<char> buffer(buffer_size);
    memcpy(buffer.data(), original_messages.data(), buffer_size);

    // 2. Set up variables to capture the output of the parser.
    int message_count = 0;
    std::vector<uint32_t> received_tokens;

    // 3. Instantiate the parser and process the buffer.
    decoder::MessageParser parser;
    parser.parse_messages(buffer.data(), buffer.size(),
        [&](const protocol::ExchangeMessage& msg) {
            message_count++;
            received_tokens.push_back(msg.token);
        }
    );

    // 4. Assert that the correct number of messages were parsed.
    ASSERT_EQ(message_count, 3);

    // 5. Assert that the content of the parsed messages is correct.
    ASSERT_EQ(received_tokens.size(), 3);
    EXPECT_EQ(received_tokens[0], 25001);
    EXPECT_EQ(received_tokens[1], 25002);
    EXPECT_EQ(received_tokens[2], 25003);
}

TEST_F(MessageParserTest, HandlesBufferWithTrailingBytes) {
    // Create a buffer with one full message and some extra bytes.
    protocol::ExchangeMessage msg = {100, 25001, 1234567890};
    const size_t buffer_size = sizeof(protocol::ExchangeMessage) + 5;
    std::vector<char> buffer(buffer_size);
    memcpy(buffer.data(), &msg, sizeof(protocol::ExchangeMessage));

    int message_count = 0;
    decoder::MessageParser parser;
    parser.parse_messages(buffer.data(), buffer.size(),
        [&](const protocol::ExchangeMessage& msg) {
            message_count++;
        }
    );

    // It should parse the one valid message and ignore the trailing bytes.
    ASSERT_EQ(message_count, 1);
}