add_executable(risk-engine
    src/main.cpp
)

target_include_directories(risk-engine PUBLIC
    ${CMAKE_CURRENT_SOURCE_DIR}/include
)

target_link_libraries(risk-engine PRIVATE
    cpp-common
    contracts
)

message(STATUS "Configured service: risk-engine")