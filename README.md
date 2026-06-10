# Trading Platform

This repository contains the source code for a low-latency, multi-service trading platform.

## Architecture

The system is designed as a set of cooperating microservices, separated by language and latency requirements.
- **Hot Path (C++):** Services that require the lowest possible latency, such as market data decoding and trade execution logic.
- **Warm Path (Go):** Services that manage state, broker connections, and operational control.
- **UI (TypeScript/SolidJS):** A web-based interface for monitoring and control.

For detailed architecture diagrams and documentation, please see the `/docs` directory.

## Getting Started

### Prerequisites
- Docker & Docker Compose
- C++ Compiler (GCC/Clang) with C++17 support
- Go (version 1.21 or later)
- CMake (version 3.15 or later)

### Building the System

1.  **Configure C++ projects:**
    ```bash
    cmake -B build .
    ```

2.  **Build C++ services:**
    ```bash
    cmake --build build
    ```

## Running the Manual Test UI (with Docker)

The easiest way to run the test UI and its backend proxy is with Docker, which avoids needing to install Go on your local machine.

1.  **Start the Services:**
    From the project root directory, run the following command:
    ```bash
    docker-compose up --build
    ```
    This command will build the `control-api` service image and start the container.

2.  **Open the UI:**
    Open your web browser and navigate to:
    ```
    http://localhost:8080
    ```
    You can now use the web interface to log in and place test orders. All API requests are proxied through the `control-api` service running in Docker.

## Project Structure

- `docs/`: Architecture, runbooks, and design documents.
- `infra/`: Docker, Systemd, and other deployment configurations.
- `libs/`: Shared libraries (`cpp-common`, `go-common`, `broker-greeksoft`).
- `services/`: Individual microservices (`feed-decoder`, `session-manager`, etc.).
- `tests/`: Integration and performance tests.
- `ui/`: Frontend SolidJS application.