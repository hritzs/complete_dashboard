cmake_minimum_required(VERSION 3.15)
project(feed-decoder CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

add_executable(feed-decoder
    src/main.cpp
    src/socket_reader.cpp
    src/packet_dispatch.cpp
    src/decompressor.cpp
    src/message_parser.cpp
    src/normalizer.cpp
    src/greeks_calculator.cpp
    src/chain_builder.cpp
)

target_include_directories(feed-decoder PRIVATE include)
target_link_libraries(feed-decoder PRIVATE lzo2 pthread zmq rt)