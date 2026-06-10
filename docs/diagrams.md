# Trading Platform Architecture Diagrams

This document provides the architecture, execution flow, and recovery diagrams for the low-latency trading platform.

## Phase 1: 3-Week MVP Architecture

This diagram shows the simplified "steel thread" architecture for the initial 3-week build. It focuses on the core path from market data to execution for a single broker.

```mermaid
sequenceDiagram
    participant UI as Web UI (login.html)
    participant API as Control API (Go)
    participant TW as Trade Worker (C++)
    participant MS as Market State (C++ SHM)
    participant DEC as Feed Decoder (C++)
    participant EXEC as Execution Gateway (Go)
    participant SESS as Session Manager (Go)
    participant GREEK as Greeksoft Broker
    participant DB as PostgreSQL
    participant REC as Reconciler (Go)

    %% Initial Setup
    DEC->>MS: Write Live Prices
    SESS->>GREEK: Perform full login sequence

    %% Trade Execution Flow
    UI->>API: POST /start-trade
    API->>TW: Spawn Process

    loop Market Data Loop
        TW->>MS: Read Live Prices
    end

    TW-->>EXEC: Emit OrderIntent (via ZMQ)

    EXEC->>SESS: Get Live Session (gRPC)
    EXEC->>GREEK: Place Order (Iris WS)

    GREEK-->>REC: Order Confirmation (Iris WS)
    REC->>DB: Persist Fill/Status

    %% Status Check Flow
    UI->>API: GET /trade-status/{id}
    API->>DB: Query Order Status
    DB-->>API: Return Status
    API-->>UI: Display Status
```

## High-Level Runtime Architecture

This is the target end-to-end runtime architecture.

```text
                                 ┌──────────────────────────────┐
                                 │         Web UI / Ops         │
                                 │ trade control, monitoring    │
                                 └──────────────┬───────────────┘
                                                │
                                                ▼
                                 ┌──────────────────────────────┐
                                 │         Control API          │
                                 │ create/pause/exit trades     │
                                 └──────────────┬───────────────┘
                                                │
                                                ▼
                                 ┌──────────────────────────────┐
                                 │      Trade Supervisor        │
                                 │ spawn / restart workers      │
                                 └──────────────┬───────────────┘
                                                │
                 ┌──────────────────────────────┼──────────────────────────────┐
                 │                              │                              │
                 ▼                              ▼                              ▼
      ┌──────────────────┐         ┌──────────────────┐            ┌──────────────────┐
      │ Trade Worker A   │         │ Trade Worker B   │            │ Trade Worker N   │
      │ C++ per-trade    │         │ C++ per-trade    │            │ C++ per-trade    │
      └────────┬─────────┘         └────────┬─────────┘            └────────┬─────────┘
               │                            │                               │
               └──────────────┬─────────────┴──────────────┬────────────────┘
                              │                            │
                              ▼                            ▼
                    ┌─────────────────┐          ┌────────────────────┐
                    │  Risk Engine    │          │   Snapshot Service  │
                    │  C++ hot checks │          │ live P&L / status   │
                    └────────┬────────┘          └────────────────────┘
                             │
                             ▼
                    ┌──────────────────────┐
                    │  Execution Gateway   │
                    │  broker adapter      │
                    └────────┬─────────────┘
                             │
              ┌──────────────┴───────────────────────┐
              │                                      │
              ▼                                      ▼
   ┌──────────────────────┐              ┌────────────────────────┐
   │ Session Manager      │              │ Reconciler             │
   │ token/gcid/sessionId │              │ WS + REST truth merge  │
   │ Iris/Apollo heartbeat│              └───────────┬────────────┘
   └──────────┬───────────┘                          │
              │                                      ▼
              │                           ┌────────────────────────┐
              │                           │ PostgreSQL             │
              │                           │ trades/orders/fills    │
              │                           └────────────────────────┘
              │
              ▼
   ┌───────────────────────────────────────────────────────────────┐
   │                        Greeksoft                              │
   │ SessionToken -> FlagValues -> LoginInfo -> jloginNew         │
   │ Iris WS (execution + order responses)                        │
   │ Apollo WS (optional broker market feed)                      │
   │ REST recovery (orderbook / trade detail / positions)         │
   └───────────────────────────────────────────────────────────────┘


   ┌──────────────────────────┐
   │ NSE FO Decoder Repo      │
   │ multicast decode / parse │
   └──────────────┬───────────┘
                  ▼
         ┌────────────────────┐
         │ Market State       │
         │ SHM + symbol cache │
         └────────────────────┘
```

This layout follows the architecture needed by Greeksoft’s documented login and WebSocket flow, where session token, login info, jloginNew, heartbeat, and async OrderResponse handling are explicit responsibilities, and where REST remains the repair path for missed or disconnected order-state transitions.

```mermaid
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
```

## End-to-End Execution Flow

This flow matches the intended low-latency model where the strategy worker does not wait synchronously for broker responses. It emits an intent, returns to its decision loop, and later consumes canonical updates produced by the reconciler.

```mermaid
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
```

## Reliability and Recovery Flow

This design ensures the system does not trust a single event source. Broker WebSocket updates are the fast path, while REST polling is the repair and convergence path if messages are missed or sessions restart.

```mermaid
flowchart LR
    WS[Iris WebSocket Update] --> REC[Reconciler]
    RESTCHK[REST Order / Trade Polling] --> REC
    REC --> NORM[Normalize broker state]
    NORM --> DB[(PostgreSQL)]
    NORM --> EVT[Canonical internal events]
    EVT --> WRK[Trade Workers]

    DISC[WS disconnect / missed event] --> RESTCHK
```

## Codebase Responsibility Map

The decoder repo should be treated as the source for exchange-feed parsing and market-data normalization, while the API repo should be mined for broker-login flow, payload construction, WebSocket message shapes, and testing utilities rather than used as the final runtime design.

```mermaid
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
```