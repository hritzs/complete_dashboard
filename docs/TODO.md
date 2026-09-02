# Low-Latency Trading Platform - Comprehensive Implementation Task List

This document tracks the development progress for the low-latency MVP build, mapped exactly to the 3-tier architecture plan and internal repository file structures.

## Phase 1: Shared Libraries & Contracts
- [ ] **`libs/cpp-common` (C++ Utilities)**
  - [x] `zmq/` - Implement ZeroMQ pub/sub and req/rep wrappers.
  - [x] `shm/` - Create shared memory reader/writer templates.
  - [ ] `logging/` - Implement a lock-free, latency-safe logger.
  - [ ] `time/` - Microsecond/nanosecond timestamp utilities.
- [ ] **`libs/contracts` (Internal Messaging schemas)**
  - [x] `order_intent.hpp` - Standardized internal order intent structure.
  - [ ] `order_update.hpp/.proto` - Standardized execution and fill status structure.
  - [ ] `trade_command.hpp/.proto` - Start, stop, modify, and square-off commands.
- [x] **`libs/db` (Database Setup)**
  - [x] Defined SQL schemas for `trades`, `orders`, and `fills`.

## Phase 2: Ultra-Hot Path (C++)
- [ ] **`services/feed-decoder` (Market Data Ingest)**
  - [x] `src/socket_reader.cpp` - ZMQ Subscriber to ingest raw JSON ticks from Go Gateway.
  - [x] `src/normalizer.cpp` - Mount and parse `GreekTokens.csv` dynamically.
  - [x] `src/message_parser.cpp` - Decode TransCode 7208 (Options Ticks).
  - [x] `src/decompressor.cpp` - Extract LZO/LZ4 stream payloads if applicable.
  - [x] `src/packet_dispatch.cpp` - Push structured events to publisher.
  - [x] `src/greeks_calculator.cpp` - Ported native Black-Scholes and Newton-Raphson IV logic into C++.
  - [x] `src/chain_builder.cpp` - Assemble option chain mapping leveraging the Greeks calculator.

- [ ] **`services/market-state` (Shared Memory Hub)**
  - [x] `include/shm/market_state.hpp` - Define memory-mapped price/depth structures.
  - [x] `src/shm_writer.cpp` - Lock-free writer receiving decoded ticks.
  - [ ] `src/token_index.cpp` - Fast O(1) lookups for active tokens.
  - [ ] `src/freshness.cpp` - Stale price detection logic.

- [ ] **`services/trade-worker` (Per-Trade Strategy Engine)**
  - [x] `src/worker_loop.cpp` - Core spin-wait loop reading from SHM array.
  - [x] `src/intent_builder.cpp` - ZMQ publisher for `OrderIntent` emission.
  - [ ] `src/state_machine.cpp` - Transition handling (entry, hedge, SL, roll).
  - [ ] `src/fill_handler.cpp` - Listen to Reconciler for canonical fill/reject events.
  - [ ] `strategies/short_straddle/` - Concrete implementation of Delta-Neutral straddle.

- [ ] **`services/risk-engine` (Pre-Trade Checks)**
  - [ ] `src/validator.cpp` - Inline validations for lot limits and formatting.
  - [ ] `src/stale_market_guard.cpp` - Reject intents if SHM timestamps are outdated.
  - [ ] `src/position_limit.cpp` - Max exposure and drawdown gating.
  - [ ] `src/duplicate_suppression.cpp` - Debounce identical intents from trade loop.

## Phase 3: Warm Path (Go Broker Connectivity)
- [x] **Broker Abstraction (`libs/go-broker`)**
  - [x] `interface.go` - `Client` templates for `PerformFullLogin` and `PlaceOrder`.
  - [x] `models.go` - `SessionDetails` and generic `OrderResponse`.

- [x] **Execution Gateway (`services/execution-gateway`)**
  - [x] `main.go` - Setup ZeroMQ subscriber and local HTTP Tester.
  - [x] `order_mapper.go` - Map internal JSON `OrderIntent` into broker payloads.
  - [x] `submitter.go` - Execute raw API requests securely.
  - [x] `internal/correlation/correlation.go` - Assign and maintain intent correlation IDs.
  - [x] `internal/errors/normalizer.go` - Standardize broker-specific errors.

- [ ] **Market Data Gateway (`services/market-data-gateway`)**
  - [x] **REMOVED PYTHON DEPENDENCY** - Replaced `marketdata_service.py` entirely with Native Go.
  - [x] `internal/chain_fetcher/fetcher.go` - REST API fetching for daily Option Chain tokens (CE/PE).
  - [x] `internal/publisher/publisher.go` - (Step 1) ZeroMQ Publisher to blast raw JSON ticks to C++ Feed Decoder.
  - [x] `internal/socketio/client.go` - (Step 2) Go-based Socket.IO client to connect to XTS and route 1512/1510 ticks.
  - [x] `cmd/main.go` - (Step 3) Main entrypoint: XTS Login, execute chain_fetcher, and start Socket.IO client.

 - [x] **Session Manager (`services/session-manager`)**
  - [x] `internal/auth/` - Token retrieval/generation flows.
  - [x] `internal/flags/` - Fetch broker server flags/properties.
  - [ ] `internal/iris/` - Establish interactive Iris WS sessions + Heartbeats.
  - [ ] `internal/apollo/` - Establish Apollo market data sessions.

- [ ] **Reconciler (`services/reconciler`)**
  - [ ] `internal/ingest/` - Subscribe to broker WebSocket updates (Iris / XTS WS).
  - [ ] `internal/repair/` - Fallback REST polling for missed WS events/disconnects.
  - [x] `internal/normalize/` - Transform fills/rejects into `OrderUpdate`.
  - [ ] `internal/persistence/` - Flush verified truth to PostgreSQL.
  - [ ] `internal/publish/` - Broadcast canonical events back to `trade-worker`.

- [ ] **Contract Master (`services/contract-master`)**
  - [ ] `internal/ingest/` - Download daily broker contract dumps.
  - [ ] `internal/parser/` - Clean and map expiries, lots, and ticks.
  - [ ] `internal/lookup/` - In-memory and DB-backed token resolution cache.

## Phase 4: Control Plane & UI
- [ ] **Trade Supervisor (`services/trade-supervisor`)**
  - [ ] `internal/spawn/` - Orchestrate `fork`/`exec` for C++ `trade-worker` binaries.
  - [ ] `internal/registry/` - Track active worker Process IDs (PIDs).
  - [ ] `internal/heartbeat/` - Monitor worker health.
  - [ ] `internal/restart/` - Automatically restart crashed workers from DB state.

- [x] **Control API (`services/control-api`)**
  - [x] `internal/routes/` - Expose API for `/start-trade`, `/pause`, `/exit` (Started via main.go).
  - [x] `internal/handlers/` - Pass commands via ZMQ to Supervisor/Workers.

- [ ] **Snapshot Service (`services/snapshot-service`)**
  - [ ] `internal/pnl/` - Live PnL combining Reconciler positions + SHM Prices.
  - [ ] `internal/positions/` - Aggregate net delta/gamma/vega per active trade.
  - [x] `internal/websocket/server.go` - Implemented Gorilla WebSocket server for broadcasting.
  - [x] `internal/zmq/subscriber.go` - Add ZeroMQ subscriber to listen for C++ chain updates.
  - [x] `cmd/main.go` - Wire ZMQ subscriber to WebSocket broadcaster.

- [ ] **User Interface (`ui/`)**
  - [x] `package.json` - Scaffold Vite + SolidJS structure.
  - [ ] Implement Options Chain table with live Greek updates.
  - [x] Implement Active Trades view and Deploy UI basics.
  - [x] Implement Manual Action buttons (Square-off, Hedge).  