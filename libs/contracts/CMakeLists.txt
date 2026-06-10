# libs/contracts/CMakeLists.txt

add_library(contracts INTERFACE)

target_include_directories(contracts INTERFACE
    ${CMAKE_CURRENT_SOURCE_DIR}
)

message(STATUS "Configured library: contracts")