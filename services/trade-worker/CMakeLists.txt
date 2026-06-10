cmake_minimum_required(VERSION 3.10)
project(trade-worker)

set(CMAKE_CXX_STANDARD 17)

# Find ZeroMQ
find_package(PkgConfig)
pkg_check_modules(ZMQ REQUIRED libzmq)

add_executable(trade-worker src/main.cpp)
target_link_libraries(trade-worker PRIVATE ${ZMQ_LIBRARIES})