cmake_minimum_required(VERSION 3.15)

# Explicitly list all active source files (ignoring incomplete ones like fill_handler.cpp for now)
add_executable(trade-worker
    src/main.cpp
    src/worker_loop.cpp
    src/state_machine.cpp
    src/intent_builder.cpp
)

# Tell the compiler where to find the header files
target_include_directories(trade-worker PRIVATE ${CMAKE_CURRENT_SOURCE_DIR}/include)

# Link ZeroMQ, POSIX threads (for core pinning), and Real-Time (for SHM)
target_link_libraries(trade-worker PRIVATE zmq pthread rt)
