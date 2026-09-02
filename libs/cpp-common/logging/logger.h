#pragma once
#include <string>
#include <iostream>

namespace common_lib {
class Logger {
public:
    static void init(const std::string& service_name) {
        std::cout << "Logger initialized for " << service_name << std::endl;
    }
    static void log(const std::string& level, const std::string& message) {
        std::cout << "[" << level << "] " << message << std::endl;
    }
};
} // namespace common_lib

#define LOG_INFO(msg) common_lib::Logger::log("INFO", (msg))
#define LOG_CRITICAL(msg) common_lib::Logger::log("CRITICAL", (msg))