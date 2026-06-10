# Stage 1: Build
FROM ubuntu:22.04 AS builder

RUN apt-get update && apt-get install -y \
    build-essential cmake git pkg-config \
    libzmq3-dev liblzo2-dev

WORKDIR /build
COPY . .

# Build the C++ projects
RUN cmake -B build -S . -DCMAKE_BUILD_TYPE=Release
RUN cmake --build build -j $(nproc)

# Stage 2: Runtime
FROM ubuntu:22.04

# Install runtime libraries only
RUN apt-get update && apt-get install -y libzmq5 liblzo2-2 && rm -rf /var/lib/apt/lists/*

COPY --from=builder /build/build/services/feed-decoder/feed-decoder /usr/local/bin/
COPY --from=builder /build/build/services/trade-worker/trade-worker /usr/local/bin/

CMD ["trade-worker"]