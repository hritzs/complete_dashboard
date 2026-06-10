cmake_minimum_required(VERSION 3.15)
project(cpp-common)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

set(ZeroMQ_DIR "C:/Users/Administrator/vcpkg/installed/x64-windows/share/zeromq")
list(APPEND CMAKE_PREFIX_PATH "C:/Users/Administrator/vcpkg/installed/x64-windows/share/zeromq")

find_package(ZeroMQ CONFIG REQUIRED)

add_library(cpp-common
    logger.cpp
    publisher.cpp
)

target_include_directories(cpp-common PUBLIC
    "${CMAKE_CURRENT_SOURCE_DIR}"
)

target_link_libraries(cpp-common PUBLIC
    libzmq
)