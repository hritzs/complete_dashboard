cmake_minimum_required(VERSION 3.15)
project(market-state)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

if(POLICY CMP0167)
    cmake_policy(SET CMP0167 NEW)
endif()

find_package(Boost CONFIG REQUIRED COMPONENTS system interprocess)

add_library(market-state-lib INTERFACE)

target_include_directories(market-state-lib INTERFACE
    "${CMAKE_CURRENT_SOURCE_DIR}/include"
)

target_link_libraries(market-state-lib INTERFACE
    cpp-common
    Boost::system
    Boost::interprocess
)

add_executable(market-state-exe src/main.cpp)

target_link_libraries(market-state-exe PRIVATE market-state-lib)

install(TARGETS market-state-exe DESTINATION bin)