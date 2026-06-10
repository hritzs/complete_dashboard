cmake_minimum_required(VERSION 3.15)
project(TradingPlatform VERSION 1.0)

# Set a global C++ standard for the whole project.
# This ensures all your components use the same standard.
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

# Add the subdirectories containing your libraries and services.
# CMake will find and process the CMakeLists.txt in each of these directories,
# creating targets that can be used by other parts of your project.
# Temporarily commented out to avoid ZeroMQ dependency while testing feed-decoder
# add_subdirectory(libs/cpp-common)
# add_subdirectory(services/market-state)

# Add other C++ libraries and services found in the project structure.
# Using EXISTS checks makes the build robust against missing CMakeLists.txt files.
# if(EXISTS "${CMAKE_CURRENT_SOURCE_DIR}/libs/contracts/CMakeLists.txt")
#     add_subdirectory(libs/contracts)
# endif()

# if(EXISTS "${CMAKE_CURRENT_SOURCE_DIR}/services/execution-gateway/CMakeLists.txt")
#     add_subdirectory(services/execution-gateway)
# endif()

# if(EXISTS "${CMAKE_CURRENT_SOURCE_DIR}/services/risk-engine/CMakeLists.txt")
#     add_subdirectory(services/risk-engine)
# endif()

# if(EXISTS "${CMAKE_CURRENT_SOURCE_DIR}/services/trade-worker/CMakeLists.txt")
#     add_subdirectory(services/trade-worker)
# endif()

# I see 'feed-decoder' in your build cache, so let's add it too.
# This check ensures the build won't fail if the directory doesn't exist yet.
if(EXISTS "${CMAKE_CURRENT_SOURCE_DIR}/services/feed-decoder/CMakeLists.txt")
    add_subdirectory(services/feed-decoder)
endif()

# Optional: Add an option to build tests, which can be toggled in VS Code.
option(BUILD_TESTING "Build the tests" ON)
if(BUILD_TESTING)
    enable_testing()
    # If you have a top-level 'tests' directory, you could add it here:
    # if(EXISTS "${CMAKE_CURRENT_SOURCE_DIR}/tests/CMakeLists.txt")
    #     add_subdirectory(tests)
    # endif()
endif()

# After creating this file, simply run "CMake: Configure" in VS Code
# with the 'trading-platform' folder open. It will discover and
# configure all your C++ components at once.add_subdirectory(services/trade-worker)
