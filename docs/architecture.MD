# Trading Platform Architecture and Implementation Guide

This document provides a structural companion to the detailed architecture plan. It includes the exact recommended repository and folder structure for execution. For visual diagrams of the architecture, see `diagrams.md`.

## Exact Recommended Repository Structure
```text
trading-platform/
├── README.md
├── docs/
│   ├── architecture.md
│   ├── broker-greeksoft.md
│   ├── deployment.md
│   ├── runbook.md
│   └── trade-lifecycle.md
│   └── diagrams.md
│
├── services/
│   ├── feed-decoder/
│   │   ├── CMakeLists.txt
│   │   ├── include/
│   │   │   ├── decoder/
│   │   │   ├── protocol/
│   │   │   └── utils/
│   │   ├── src/
│   │   │   ├── main.cpp
│   │   │   ├── socket_reader.cpp
│   │   │   ├── packet_dispatch.cpp
│   │   │   ├── decompressor.cpp
│   │   │   ├── message_parser.cpp
│   │   │   └── normalizer.cpp
│   │   └── tests/
│   │
│   ├── market-state/
│   │   ├── CMakeLists.txt
│   │   ├── include/
│   │   │   ├── shm/
│   │   │   ├── cache/
│   │   │   └── market_state.hpp
│   │   ├── src/
│   │   │   ├── main.cpp
│   │   │   ├── shm_writer.cpp
│   │   │   ├── token_index.cpp
│   │   │   ├── freshness.cpp
│   │   │   └── publisher.cpp
│   │   └── tests/
│   │
│   ├── trade-worker/
│   │   ├── CMakeLists.txt
│   │   ├── include/
│   │   │   ├── worker/
│   │   │   ├── state/
│   │   │   ├── strategy/
│   │   │   └── events/
│   │   ├── src/
│   │   │   ├── main.cpp
│   │   │   ├── worker_loop.cpp
│   │   │   ├── state_machine.cpp
│   │   │   ├── intent_builder.cpp
│   │   │   ├── fill_handler.cpp
│   │   │   └── risk_client.cpp
│   │   ├── strategies/
│   │   │   ├── short_straddle/
│   │   │   └── helpers/
│   │   └── tests/
│   │
│   ├── risk-engine/
│   │   ├── CMakeLists.txt
│   │   ├── include/
│   │   ├── src/
│   │   │   ├── main.cpp
│   │   │   ├── validator.cpp
│   │   │   ├── stale_market_guard.cpp
│   │   │   ├── position_limit.cpp
│   │   │   └── duplicate_suppression.cpp
│   │   └── tests/
│   │
│   ├── execution-gateway/
│   │   ├── CMakeLists.txt
│   │   ├── include/
│   │   ├── src/
│   │   │   ├── main.cpp
│   │   │   ├── order_mapper.cpp
│   │   │   ├── submitter.cpp
│   │   │   ├── correlation.cpp
│   │   │   └── error_normalizer.cpp
│   │   ├── go/
│   │   │   ├── main.go
│   │   │   └── broker_router.go
│   │   └── go.mod
│   │   └── tests/
│   │
│   ├── session-manager/
│   │   ├── cmd/
│   │   │   └── main.go
│   │   ├── internal/
│   │   │   ├── auth/
│   │   │   ├── flags/
│   │   │   ├── login/
│   │   │   ├── iris/
│   │   │   ├── apollo/
│   │   │   └── heartbeat/
│   │   └── go.mod
│   │
│   ├── reconciler/
│   │   ├── cmd/
│   │   │   └── main.go
│   │   ├── internal/
│   │   │   ├── ingest/
│   │   │   ├── normalize/
│   │   │   ├── repair/
│   │   │   ├── publish/
│   │   │   └── persistence/
│   │   └── go.mod
│   │
│   ├── contract-master/
│   │   ├── cmd/
│   │   │   └── main.go
│   │   ├── internal/
│   │   │   ├── ingest/
│   │   │   ├── parser/
│   │   │   ├── lookup/
│   │   │   └── refresh/
│   │   └── go.mod
│   │
│   ├── snapshot-service/
│   │   ├── cmd/
│   │   │   └── main.go
│   │   ├── internal/
│   │   │   ├── pnl/
│   │   │   ├── positions/
│   │   │   ├── trade_status/
│   │   │   └── websocket/
│   │   └── go.mod
│   │
│   ├── trade-supervisor/
│   │   ├── cmd/
│   │   │   └── main.go
│   │   ├── internal/
│   │   │   ├── spawn/
│   │   │   ├── registry/
│   │   │   ├── heartbeat/
│   │   │   └── restart/
│   │   └── go.mod
│   │
│   └── control-api/
│       ├── cmd/
│       │   └── main.go
│       ├── internal/
│       │   ├── handlers/
│       │   ├── routes/
│       │   ├── middleware/
│       │   └── service/
│       └── go.mod
│
├── libs/
│   ├── contracts/
│   │   ├── trade_command/
│   │   ├── order_intent/
│   │   ├── order_update/
│   │   ├── fill_event/
│   │   ├── contract_ref/
│   │   └── risk_event/
│   │
│   ├── cpp-common/
│   │   ├── logging/
│   │   ├── config/
│   │   ├── time/
│   │   ├── zmq/
│   │   ├── shm/
│   │   └── utils/
│   │
│   ├── go-common/
│   │   ├── config/
│   │   ├── db/
│   │   ├── logging/
│   │   └── events/
│   │
│   ├── broker-greeksoft/
│   │   ├── auth/
│   │   ├── models/
│   │   ├── iris/
│   │   ├── apollo/
│   │   ├── rest/
│   │   └── mapping/
│   │
│   ├── broker-xts/
│   │   ├── auth/
│   │   ├── models/
│   │   ├── interactive/
│   │   ├── marketdata/
│   │   └── mapping/
│   │
│   └── db/
│       ├── migrations/
│       └── schema/
│
├── ui/
│   ├── README.md
│   ├── src/
│   ├── public/
│   ├── package.json
│   └── vite.config.ts
│
├── infra/
│   ├── docker/
│   ├── systemd/
│   ├── scripts/
│   └── env/
│
└── tests/
    ├── integration/
    ├── replay/
    └── perf/
This structure is intentionally split by runtime responsibility so that market data, trade logic, and broker-specific session handling do not get mixed the way they currently are in prototype-style server/UI files.

What to take from each repo
Your two GitHub repos should not be merged blindly. They should be treated as source material.

1. nse-fo-low-latency-decoder
Use this repo as the foundation for:

multicast ingest

packet decode

decompression logic

exchange message parsing

token/symbol normalization pipeline

fast in-memory market event production

You should extract from it:

binary protocol parsing classes

packet structures

any existing fast buffer/memory handling

symbol/token mapping utilities if already present

logging/performance measurement helpers if they are lightweight enough

You should not overload this repo with:

broker logic

Greeksoft login/session code

UI or REST layers

trade lifecycle logic

This repo becomes:

services/feed-decoder

possibly some reusable low-level code into libs/cpp-common

2. api
Use this repo mainly as reference for:

Greeksoft login sequence prototypes

request/response model understanding

payload construction examples

WebSocket connect/login patterns

order placement field mapping

contract loading/search UI ideas

temporary admin/testing screens

From this repo, the most useful pieces are likely:

session token acquisition flow

getFlagValues, getLoginInfo, jloginNew calling patterns

Iris/Apollo WebSocket handling experiments

order request body shape

contract loading/cache/search behavior

quick manual operator tooling patterns

You should not keep from it as final architecture:

monolithic server flow

mixed UI + broker + business logic in one file

direct use of broker-native payloads deep inside business logic

production reliance on Python for per-trade hot loops

This repo should be split into:

libs/broker-greeksoft

services/session-manager

services/control-api

maybe some UI ideas into ui/

Repo mapping table
Existing repo part	New target place	Keep / Rewrite
NSE packet decode logic	services/feed-decoder	Keep, refactor cleanly
low-level parser structs	services/feed-decoder/include	Keep
market data normalization	services/feed-decoder / market-state	Keep, separate responsibilities
direct decoded output consumers	market-state	Rewrite as SHM publisher
Python broker login experiments	libs/broker-greeksoft/auth	Keep concept, rewrite properly
WebSocket session code	libs/broker-greeksoft/iris / apollo	Keep concept, rewrite cleanly
order payload builders	libs/broker-greeksoft/mapping	Keep concept, normalize
FastAPI endpoints	services/control-api	Partial keep, mostly rewrite
HTML manual tooling	ui/	Keep only as reference
single-file orchestration	nowhere	Discard in final architecture
What references to take specifically
Since I cannot reliably inspect the GitHub source tree directly in this environment, the safest and correct guidance is functional extraction, not file-name guessing.

From decoder repo, look for and reuse:
packet receiver / socket reader

multicast join / feed handling

decompress/unpack pipeline

binary struct definitions

message-type switch/dispatch

token extraction and normalized quote object creation

any benchmarked parsing path

These should inform:

feed-decoder/src

market-state/src

libs/cpp-common/utils

From API repo, look for and reuse:
auth/session call wrappers

response parsers for broker login flow

websocket login message builders

websocket heartbeat logic

NewOrderRequest / modify / cancel payload builders

contract-fetch and symbol lookup code

raw error handling against Greeksoft responses

These should inform:

libs/broker-greeksoft/auth

libs/broker-greeksoft/iris

libs/broker-greeksoft/apollo

libs/broker-greeksoft/rest

libs/broker-greeksoft/mapping

Clean split rule
Use this rule for every file you inspect in those repos:

If it deals with exchange packets → move to decoder/market-state side.

If it deals with Greeksoft login, WebSocket, order payloads → move to broker library side.

If it deals with trade logic → rewrite into C++ worker.

If it deals with UI or testing screens → move only as reference, not production logic.

If it mixes all of the above in one file → split and rewrite.

Final recommendation on usage
Your best approach is:

Do not continue building on the existing api repo structure as-is, because it is useful for learning the broker flow but not as the final runtime shape.

Promote the decoder repo into the primary production core, because that is your real latency edge.

Create a fresh top-level platform repo and import only the relevant parts from both repos into the new service boundaries.

Use Greeksoft docs + Postman collection as the final contract authority, not ad hoc code assumptions, because the broker fields and dynamic login flow are clearly documented there.

Immediate action list
Do this next:

Create the new repo/folder structure above.

Copy decoder parsing components into services/feed-decoder.

Extract broker login and payload code from api into libs/broker-greeksoft.

Build session-manager from those extracted broker modules.

Build market-state as SHM writer from decoder output.

Build one minimal C++ trade-worker.

Connect worker → execution-gateway → Greeksoft Iris → reconciler.

That path is the shortest route from your current repos to a correct production architecture based on the documented Greeksoft execution model and your existing low-latency decoder work.
Trading Platform Architecture Diagrams and Folder Structure
Purpose
This document provides a management-ready visual and structural companion to the detailed architecture plan. It includes Mermaid diagrams for the final target system, implementation-oriented service interactions, and the exact recommended repository and folder structure for execution over the next two weeks 
.

High-Level Architecture Diagram
text
flowchart TB
    UI[Web UI / Operator Console]
    API[Control API]
    SUP[Trade Supervisor]

    UI --> API
    API --> SUP

    subgraph HOT[Ultra-Hot Path]
        DEC[Feed Decoder\nC++]
        MS[Market State\nC++ Shared Memory]
        TW1[Trade Worker A\nC++]
        TW2[Trade Worker B\nC++]
        TWN[Trade Worker N\nC++]
        RISK[Risk Engine\nC++]
        EXEC[Execution Gateway\nC++ preferred / Go acceptable]

        DEC --> MS
        MS --> TW1
        MS --> TW2
        MS --> TWN
        TW1 --> RISK
        TW2 --> RISK
        TWN --> RISK
        RISK --> EXEC
    end

    SUP --> TW1
    SUP --> TW2
    SUP --> TWN

    subgraph WARM[Warm Path]
        SESS[Session Manager\nToken / gcid / sessionId / heartbeat]
        REC[Reconciler\nWS + REST verification]
        CM[Contract Master]
        SNAP[Snapshot Service]
        DB[(PostgreSQL)]
    end

    EXEC --> SESS
    EXEC --> REC
    CM --> EXEC
    REC --> DB
    SNAP --> DB
    API --> SNAP

    subgraph BROKER[Greeksoft Boundary]
        GST[Session Token]
        FLAG[getFlagValues]
        LI[getLoginInfo]
        JL[jloginNew]
        IRIS[Iris WebSocket\norders + order responses]
        APOLLO[Apollo WebSocket\nmarket feed optional]
        REST[REST APIs\norderbook / trade detail / recovery]

        GST --> FLAG --> LI --> JL --> IRIS
        JL --> APOLLO
        REST --> REC
        IRIS --> REC
    end

    SESS --> GST
    SESS --> FLAG
    SESS --> LI
    SESS --> JL
    SESS --> IRIS
    SESS --> APOLLO
This architecture reflects the broker-documented requirement that session token generation, getFlagValues, getLoginInfo, jloginNew, and WebSocket login/heartbeat be explicitly managed, and that real-time order responses arrive over Iris while REST remains the recovery and verification channel 
.

End-to-End Execution Flow
text
sequenceDiagram
    participant D as Feed Decoder
    participant M as Market State
    participant W as Trade Worker
    participant R as Risk Engine
    participant E as Execution Gateway
    participant S as Session Manager
    participant G as Greeksoft Iris
    participant C as Reconciler
    participant P as PostgreSQL

    D->>M: Publish normalized market updates
    W->>M: Read price / depth / state
    W->>R: Emit OrderIntent
    R->>E: Forward validated intent
    E->>S: Ensure live session context
    S->>G: Send order over Iris WS
    G-->>C: OrderResponse / fill / reject
    C->>P: Persist order events and fills
    C-->>W: Canonical OrderUpdate / FillEvent
This flow matches the intended low-latency model where the strategy worker does not wait synchronously for broker responses. It emits an intent, returns to its decision loop, and later consumes canonical updates produced by the reconciler 
.

Reliability and Recovery Flow
text
flowchart LR
    WS[Iris WebSocket Update] --> REC[Reconciler]
    RESTCHK[REST Order / Trade Polling] --> REC
    REC --> NORM[Normalize broker state]
    NORM --> DB[(PostgreSQL)]
    NORM --> EVT[Canonical internal events]
    EVT --> WRK[Trade Workers]

    DISC[WS disconnect / missed event] --> RESTCHK
This design ensures the system does not trust a single event source. Broker WebSocket updates are the fast path, while REST polling is the repair and convergence path if messages are missed or sessions restart 
.

Codebase Responsibility Map
text
flowchart TB
    NSE[nse-fo-low-latency-decoder repo]
    APIR[api repo]

    NSE --> FD[feed-decoder service]
    NSE --> MS2[market-state service]
    NSE --> CPP[cpp-common utilities]

    APIR --> BG[broker-greeksoft library]
    APIR --> SM[session-manager service]
    APIR --> CA[control-api reference]
    APIR --> UIREF[UI / testing reference only]

    BG --> EX[execution-gateway]
    BG --> REC2[reconciler request/response mapping]
The decoder repo should be treated as the source for exchange-feed parsing and market-data normalization, while the API repo should be mined for broker-login flow, payload construction, WebSocket message shapes, and testing utilities rather than used as the final runtime design 
.

Exact Recommended Repository Structure
text
trading-platform/
├── README.md
├── docs/
│   ├── architecture.md
│   ├── broker-greeksoft.md
│   ├── deployment.md
│   ├── runbook.md
│   ├── trade-lifecycle.md
│   └── diagrams.md
│
├── services/
│   ├── feed-decoder/
│   │   ├── CMakeLists.txt
│   │   ├── include/
│   │   │   ├── decoder/
│   │   │   ├── protocol/
│   │   │   └── utils/
│   │   ├── src/
│   │   │   ├── main.cpp
│   │   │   ├── socket_reader.cpp
│   │   │   ├── packet_dispatch.cpp
│   │   │   ├── decompressor.cpp
│   │   │   ├── message_parser.cpp
│   │   │   └── normalizer.cpp
│   │   └── tests/
│   │
│   ├── market-state/
│   │   ├── CMakeLists.txt
│   │   ├── include/
│   │   │   ├── shm/
│   │   │   ├── cache/
│   │   │   └── market_state.hpp
│   │   ├── src/
│   │   │   ├── main.cpp
│   │   │   ├── shm_writer.cpp
│   │   │   ├── token_index.cpp
│   │   │   ├── freshness.cpp
│   │   │   └── publisher.cpp
│   │   └── tests/
│   │
│   ├── trade-worker/
│   │   ├── CMakeLists.txt
│   │   ├── include/
│   │   │   ├── worker/
│   │   │   ├── state/
│   │   │   ├── strategy/
│   │   │   └── events/
│   │   ├── src/
│   │   │   ├── main.cpp
│   │   │   ├── worker_loop.cpp
│   │   │   ├── state_machine.cpp
│   │   │   ├── intent_builder.cpp
│   │   │   ├── fill_handler.cpp
│   │   │   └── risk_client.cpp
│   │   ├── strategies/
│   │   │   ├── short_straddle/
│   │   │   └── helpers/
│   │   └── tests/
│   │
│   ├── risk-engine/
│   │   ├── CMakeLists.txt
│   │   ├── include/
│   │   ├── src/
│   │   │   ├── main.cpp
│   │   │   ├── validator.cpp
│   │   │   ├── stale_market_guard.cpp
│   │   │   ├── position_limit.cpp
│   │   │   └── duplicate_suppression.cpp
│   │   └── tests/
│   │
│   ├── execution-gateway/
│   │   ├── CMakeLists.txt
│   │   ├── include/
│   │   ├── src/
│   │   │   ├── main.cpp
│   │   │   ├── order_mapper.cpp
│   │   │   ├── submitter.cpp
│   │   │   ├── correlation.cpp
│   │   │   └── error_normalizer.cpp
│   │   └── tests/
│   │
│   ├── session-manager/
│   │   ├── cmd/
│   │   │   └── main.go
│   │   ├── internal/
│   │   │   ├── auth/
│   │   │   ├── flags/
│   │   │   ├── login/
│   │   │   ├── iris/
│   │   │   ├── apollo/
│   │   │   └── heartbeat/
│   │   └── go.mod
│   │
│   ├── reconciler/
│   │   ├── cmd/
│   │   │   └── main.go
│   │   ├── internal/
│   │   │   ├── ingest/
│   │   │   ├── normalize/
│   │   │   ├── repair/
│   │   │   ├── publish/
│   │   │   └── persistence/
│   │   └── go.mod
│   │
│   ├── contract-master/
│   │   ├── cmd/
│   │   │   └── main.go
│   │   ├── internal/
│   │   │   ├── ingest/
│   │   │   ├── parser/
│   │   │   ├── lookup/
│   │   │   └── refresh/
│   │   └── go.mod
│   │
│   ├── snapshot-service/
│   │   ├── cmd/
│   │   │   └── main.go
│   │   ├── internal/
│   │   │   ├── pnl/
│   │   │   ├── positions/
│   │   │   ├── trade_status/
│   │   │   └── websocket/
│   │   └── go.mod
│   │
│   ├── trade-supervisor/
│   │   ├── cmd/
│   │   │   └── main.go
│   │   ├── internal/
│   │   │   ├── spawn/
│   │   │   ├── registry/
│   │   │   ├── heartbeat/
│   │   │   └── restart/
│   │   └── go.mod
│   │
│   └── control-api/
│       ├── cmd/
│       │   └── main.go
│       ├── internal/
│       │   ├── handlers/
│       │   ├── routes/
│       │   ├── middleware/
│       │   └── service/
│       └── go.mod
│
├── libs/
│   ├── contracts/
│   │   ├── trade_command/
│   │   ├── order_intent/
│   │   ├── order_update/
│   │   ├── fill_event/
│   │   ├── contract_ref/
│   │   └── risk_event/
│   │
│   ├── cpp-common/
│   │   ├── config/
│   │   ├── logging/
│   │   ├── time/
│   │   ├── zmq/
│   │   ├── shm/
│   │   └── utils/
│   │
│   ├── go-common/
│   │   ├── config/
│   │   ├── db/
│   │   ├── logging/
│   │   └── events/
│   │
│   ├── go-broker/
│   │   ├── interface.go
│   │   └── models.go
│   │
│   ├── broker-greeksoft/
│   │   ├── auth/
│   │   ├── models/
│   │   ├── iris/
│   │   ├── apollo/
│   │   ├── rest/
│   │   └── mapping/
│   │
│   └── db/
│       ├── migrations/
│       └── schema/
│
├── ui/
│   ├── src/
│   ├── public/
│   ├── package.json
│   └── vite.config.ts
│
├── infra/
│   ├── docker/
│   ├── systemd/
│   ├── scripts/
│   └── env/
│
└── tests/
    ├── integration/
    ├── replay/
    └── perf/
What to Take from nse-fo-low-latency-decoder
Use the decoder repository as the source for the exchange-side runtime core. The most important areas to extract are the multicast/socket ingest path, decompression and binary parsing logic, dispatch by message type, token extraction, and any fast normalized market-event pipeline already present in the repo 
​. These components should become the basis of services/feed-decoder and parts of services/market-state.

Recommended reuse pattern:

packet reader / socket layer -> services/feed-decoder/src/socket_reader.cpp

decompression logic -> services/feed-decoder/src/decompressor.cpp

protocol structs / message definitions -> services/feed-decoder/include/protocol/

message parser / dispatcher -> services/feed-decoder/src/message_parser.cpp and packet_dispatch.cpp

normalization helpers -> services/feed-decoder/src/normalizer.cpp

timing / logging utilities if lightweight -> libs/cpp-common/

Do not add broker, UI, or trade logic into this codebase. It should remain the clean market-data ingest foundation 
​.

What to Take from api
Use the API repository as broker-integration and control-plane reference material, not as the final runtime structure. The useful parts are the session token flow, Greeksoft login sequence, getFlagValues, getLoginInfo, jloginNew, WebSocket connection/login/heartbeat logic, order request payload construction, contract fetch/search handling, and operator testing concepts already visible in the prototype files and documents 
.

Recommended reuse pattern:

session token / auth wrapper ideas -> libs/broker-greeksoft/auth/

flag/login/jlogin request handling -> services/session-manager/internal/

Iris/Apollo WebSocket login and heartbeat logic -> libs/broker-greeksoft/iris/ and apollo/

broker payload models -> libs/broker-greeksoft/models/

order request mapping -> libs/broker-greeksoft/mapping/

contract fetch and search flow -> services/contract-master/internal/

simple control routes and manual actions -> services/control-api/internal/

UI concepts only -> ui/ as reference, not direct production carryover 

The monolithic mixing of server logic, broker calls, UI, and business logic shown by the prototype approach should not be preserved in the final architecture 
​.

Repo-to-Service Mapping Table
Source	Extract from it	New target	Keep / Rewrite
nse-fo-low-latency-decoder	multicast read + packet decode	services/feed-decoder	Keep and refactor 
​
nse-fo-low-latency-decoder	protocol structs and parsers	services/feed-decoder/include	Keep 
​
nse-fo-low-latency-decoder	normalized market event output	services/market-state	Keep concept, adapt to SHM
api	broker auth/login logic	services/session-manager	Rewrite cleanly 
api	Iris/Apollo WS logic	libs/broker-greeksoft/iris and apollo	Rewrite cleanly 
​
api	order payload builder	libs/broker-greeksoft/mapping	Keep concept, normalize 
api	contract loading/search	services/contract-master	Keep concept, rewrite service form 
api	FastAPI/admin endpoints	services/control-api	Partial keep, mostly rewrite 
​
api	HTML manual testing UI	ui/ reference only	Reference only 
​
Implementation Guidance
The correct approach is to create a fresh top-level platform repository and import only the useful code patterns from both repositories into the service boundaries shown above. The decoder repository should remain the production-grade exchange-feed core, while the API repository should be treated as a broker learning/prototyping source for Greeksoft integration behavior rather than the final deployment shape 
.

Management should view this as a controlled migration from two useful technical prototypes into a structured production architecture with clear service ownership, lower latency risk, and cleaner scaling potential 