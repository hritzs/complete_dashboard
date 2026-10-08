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
    src/nse_mbp_record.cpp
    src/minute_close.cpp
    src/normalizer.cpp
    src/greeks_calculator.cpp
    src/chain_builder.cpp
)

target_include_directories(feed-decoder PRIVATE include)
target_link_libraries(feed-decoder PRIVATE lzo2 pthread zmq rt)

# Read-only multicast recorder for feed audits (scripts/feedrec_nse.sh).
add_executable(nse-feedrec
    tools/nse_feedrec.cpp
    src/decompressor.cpp
)
target_include_directories(nse-feedrec PRIVATE include)
target_link_libraries(nse-feedrec PRIVATE lzo2)