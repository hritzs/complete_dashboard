# Low-Latency Trading Platform Architecture and 2-Week Execution Plan

## Executive Summary

This document proposes a production-oriented architecture for a low-latency options trading platform built around three core realities: the NSE FO multicast decoder is the fastest source of market truth, Greeksoft provides a persistent Iris WebSocket for interactive order execution and asynchronous order responses, and the current Python-heavy approach should be reduced so that Python does not sit in the latency-critical path [file:118][file:121][file:113]. The recommended target is a latency-tiered architecture with C++ on the hot path, Go or Java on the service/control path, PostgreSQL for durable truth, and shared memory plus ZeroMQ for fast internal coordination [file:113][web:146].

The objective over the next two weeks is not to build the fully finished end-state platform, but to deliver a management-reviewable Version 1 that proves the architecture, validates the broker integration path, establishes internal contracts, and demonstrates one working end-to-end trade flow from market data through order placement and reconciliation [file:118][file:121]. The plan below is intentionally staged to maximize implementation certainty, reduce architectural rework, and keep the design aligned with what the broker actually supports today [file:122][file:114].

## Problem Statement

The current system direction contains too much Python in areas that are likely to become latency bottlenecks if the platform scales to more active trades, more instruments, or more frequent strategy checks [file:113]. Python is acceptable for orchestration and external I/O, but it becomes a source of jitter and overhead when used for per-tick strategy execution, repeated chain scans, risk checks on every decision, or central coordination across many independent trades [file:113].

A second problem is that broker-specific behavior is still too close to core logic in prototypes. Greeksoft authentication, flag retrieval, dynamic `gcid`, dynamic `sessionId`, Iris and Apollo login, and heartbeat handling all require explicit lifecycle management, so broker integration must be isolated behind a strict adapter boundary rather than spread across UI, API, and strategy code [file:122][file:114][file:118].

A third problem is reliability under disconnects or missed events. Greeksoft documentation shows that real-time order responses are delivered over the Iris WebSocket, while REST endpoints such as order book and trade detail remain necessary to reconstruct truth if messages are missed or sessions restart [file:118][file:121]. A design that trusts only live WebSocket events is therefore incomplete and operationally unsafe [file:118].

## Architectural Goals

The platform should be designed to satisfy the following goals:

- Low-latency market reaction using direct exchange-feed processing as the primary market-data source [file:1].
- Deterministic per-trade execution logic through isolated worker processes rather than a single shared strategy engine [file:113].
- Broker-neutral internal contracts so a future FIX, binary, or alternate broker integration does not force a rewrite of the strategy core [file:113][file:118].
- Event-driven order lifecycle with reconciliation, replay, and restart recovery [file:121][file:114].
- Management visibility through auditable order state, service status, and implementation milestones.

## Why the Proposed Architecture Is Correct

Greeksoft’s own workflow requires a session-token flow, server flag retrieval, login-info retrieval, `jloginNew`, and then separate WebSocket login and heartbeat management for Iris and Apollo, which makes explicit session/state services non-optional [file:122][file:114][file:118][file:121]. The broker also documents that orders can be placed via REST or WebSocket, but order responses are received over WebSocket, confirming that the persistent interactive connection is the correct real-time execution path [file:118].

The internal design should therefore optimize everything around that broker reality: make market-data and strategy logic extremely fast and local, keep broker integration thin and isolated, and close the loop with a reconciler that treats WebSocket events as the fast path and REST polling as the verification path [file:118][file:121]. This produces a system that is both faster and safer than a Python-centric monolith or a purely REST-driven submission flow [file:113][file:118].

## Final Target Architecture

The recommended final target is a three-tier latency architecture.

### Tier 1: Ultra-Hot Path

This tier handles anything that is directly involved in market reaction and order decision latency.

- `feed-decoder` in C++: receives and decodes NSE FO multicast packets and normalizes raw exchange events into a structured internal feed [file:1].
- `market-state` in C++: owns shared-memory market truth including LTP, depth, and any strategy-facing price cache.
- `trade-worker` in C++: one process per active trade or strategy instance, responsible for SL, hedge, roll, square-off, and local state transitions [file:113].
- `risk-engine` in C++: validates order intents before they are sent outward so risk checks do not become a Python bottleneck [file:113].
- `execution-gateway` in C++ ideally, or Go if delivery speed matters more than absolute latency: transforms internal intents into broker payloads and sends them over the persistent Iris session [file:118].

### Tier 2: Warm Path

This tier handles high-value runtime services that matter for correctness and operations but are not part of the per-tick reaction loop.

- `session-manager` in Go or Java: handles session token, `getFlagValues`, `getLoginInfo`, `jloginNew`, Iris/Apollo logins, and heartbeats [file:122][file:114][file:118].
- `reconciler` in Go or Java: consumes WebSocket order updates and compares them with REST order/trade data to produce canonical execution truth [file:118][file:121].
- `contract-master` in Go or Java: owns token-symbol-expiry-lot metadata from contract dumps and broker APIs [file:121][file:125].
- `snapshot-service` in Go or Java: produces aggregated trade, position, and risk snapshots for operators.
- `trade-supervisor` in Go or Java: starts and monitors workers and restarts them on failure.

### Tier 3: Slow Path

This tier supports humans and administration.

- `control-api` in Go or Java: trade creation, pause/resume, manual square-off, historical views.
- `web-ui`: operator dashboard, trade monitors, service health, exception pages.
- reporting and backoffice utilities.
- long-form analytics, exports, and non-live administrative functions.

## Service-by-Service Detail

### 1. Feed Decoder

The feed decoder is the single source of raw market truth. It should remain in C++ and should not depend on broker APIs or UI flows [file:1]. Its responsibility is to consume exchange packets, decompress or normalize where necessary, and publish market events into a form that downstream components can consume without repeated reparsing [file:1].

The decoder should publish only internal normalized structures. It should not publish raw broker-facing formats or strategy-specific abstractions. This makes it reusable for multiple strategies and future brokers.

### 2. Market State Service

The market-state service owns a read-optimized view of live prices and depth. This should be maintained in shared memory so C++ trade workers can read current values with minimal overhead rather than crossing process boundaries for every lookup [file:113].

Recommended contents of market-state include:

- last traded price
- top bid/ask
- depth ladders where relevant
- last update timestamps
- token-to-index lookup tables
- optional derived values needed in the hot path

The market-state service should be treated as read-mostly for workers and write-owned only by the feed side.

### 3. Trade Worker

The trade-worker is the core strategy engine. Each active trade or strategy instance should run in its own process so the system achieves true OS-level parallelism and isolates faults [file:113]. A trade worker should own only the state relevant to its trade: entry state, hedge state, SL state, re-entry state, pending intents, fill-dependent transitions, and local timers.

The worker must never own broker sessions or global market feed subscriptions. It reads market-state, consumes canonical fill/order events, and emits `OrderIntent` messages when it decides to act.

### 4. Risk Engine

The risk engine should execute before the broker boundary and should sit in the hot path only if its logic is efficient and deterministic. Checks should include market-open state, stale-price checks, lot validation, max quantity, duplicate-order suppression, and per-trade exposure limits [file:121][file:114].

This service must use internal contracts, not Greeksoft-native payloads. That keeps the logic reusable and prevents broker field noise from infecting core decision code.

### 5. Session Manager

The session manager owns all Greeksoft login and runtime state. It must handle:

1. session token retrieval [file:118][file:121]
2. `getFlagValues` [file:114][file:121]
3. `getLoginInfo` [file:114][file:121]
4. `jloginNew` [file:114][file:121]
5. Iris login and heartbeat [file:118]
6. Apollo login and heartbeat [file:118]
7. reconnect attempts and recovery logic [file:121]

This component should be independent from strategy logic. A session issue should not require a strategy code change.

### 6. Execution Gateway

The execution gateway is the only component allowed to translate internal order intents into broker-native requests. It should map fields like `gtoken`, `gcid`, `iprocli`, `AccountNumber`, `responseformat`, `requesttype`, and `streamingtype` based on the correct request type and account mode shown in the Greeksoft materials [file:121][file:127].

The execution gateway should also assign and maintain a stable internal intent correlation ID so that downstream acknowledgements, responses, or recovery data can always be tied back to the originating worker.

### 7. Reconciler

The reconciler is the single authority for execution truth. It should consume:

- Iris `OrderResponse` messages [file:118]
- REST order detail data [file:121]
- REST order book data [file:121]
- REST trade detail data [file:121]

The reconciler then normalizes the broker’s statuses into canonical internal states such as:

- `SUBMITTED`
- `ACKED`
- `PARTIAL_FILL`
- `FILLED`
- `CANCELLED`
- `RMS_REJECTED`
- `EXCHANGE_REJECTED`
- `EXPIRED`

This prevents every worker or API service from inventing its own interpretation of broker responses.

### 8. Contract Master

The contract master should ingest the contract universe using `getAllContract` and expose normalized lookup services for token, symbol, strike, expiry, option type, lot size, and asset type [file:121]. The existing prototype already demonstrates the operational need for this, as contract data is necessary for search, selection, and order construction [file:125].

This component should also version contract snapshots and support refresh logic so next-expiry and rollover logic remain reliable.

### 9. Control API and UI

The control API provides the operator-facing interface to create, monitor, pause, resume, or force-close trades. It should not directly talk to broker APIs. It should issue internal commands to the supervisor or workers and consume snapshots built from canonical internal state.

The UI should show:

- active trade list
- worker status
- service status
- broker session health
- order lifecycle per trade
- fills and rejections
- restart/reconnect history

## Internal Messaging and Data Contracts

The platform should use broker-neutral internal contracts. Suggested message types are:

### TradeCommand

Fields:
- `command_id`
- `trade_id`
- `strategy_id`
- `action`
- `params`
- `created_at`

### OrderIntent

Fields:
- `intent_id`
- `trade_id`
- `worker_id`
- `instrument_id`
- `side`
- `quantity`
- `order_type`
- `limit_price`
- `trigger_price`
- `product_type`
- `meta`
- `created_at`

### OrderUpdate

Fields:
- `intent_id`
- `broker_order_id`
- `exchange_order_id`
- `trade_id`
- `status`
- `filled_qty`
- `pending_qty`
- `avg_fill_price`
- `reason_code`
- `reason_text`
- `broker_timestamp`

### FillEvent

Fields:
- `fill_id`
- `intent_id`
- `broker_order_id`
- `trade_id`
- `instrument_id`
- `side`
- `fill_qty`
- `fill_price`
- `fill_time`

These internal contracts should initially be represented using strongly typed structs or schema definitions and not raw free-form JSON. Cap’n Proto can be introduced later for faster structured cross-service messaging if the team wants higher-performance typed serialization than JSON [page:1][page:2].

## Storage Architecture

PostgreSQL should be the durable system of record. Core tables should include:

- `broker_sessions`
- `contracts`
- `strategies`
- `trades`
- `trade_legs`
- `orders`
- `order_events`
- `fills`
- `positions`
- `risk_events`
- `worker_heartbeats`
- `service_heartbeats`
- `audit_logs`

The design should store both normalized columns and raw broker payload JSON for future replay, debugging, and parser correction [file:121][file:114]. Database writes must not be in the critical path of order submission.

## Performance Principles

The following principles should guide all implementation decisions.

### 1. Keep Python Out of the Hot Path

Python should not sit inside per-tick strategy execution, hot risk checks, or core order submission if the target is deterministic low latency [file:113]. If Python remains anywhere, it should be in control-plane or non-latency-critical utilities only.

### 2. Prefer Persistent Connections

Greeksoft’s Iris WebSocket exists specifically to maintain a live interactive session, send order requests, and receive asynchronous order responses [file:118]. Use the persistent connection for execution instead of building a request-per-order REST model.

### 3. Minimize Serialization Hops

Do not serialize and deserialize the same data multiple times between workers and execution. Shared memory should be used for market state, and compact internal contracts should be used between workers and service boundaries.

### 4. Use Process Isolation for Trades

One process per active trade gives the platform true parallelism, fault isolation, and simpler reasoning about state [file:113].

### 5. Prepare for NUMA and Affinity Tuning

NUMA matters on larger multi-socket systems because memory local to the running core is accessed faster than remote-node memory [web:146][web:147]. For high-core-count production servers, worker placement, shared-memory placement, and CPU affinity should be tuned so hot components stay local [web:150].

### 6. Introduce io_uring Only After Profiling

`io_uring` is a Linux interface for high-performance async I/O, but it should be treated as a later optimization. The first and most important gains come from architectural simplification, persistent broker connections, shared memory, and reducing language/runtime overhead in the hot path [web:158][web:143].

## Reliability and Recovery Model

The system must assume that sessions can drop, WebSocket messages can be missed, workers can restart, and processes can fail.

### Broker Recovery

If the broker session drops:
- the session manager must rebuild login state [file:122][file:118]
- Iris and Apollo sessions must be re-established [file:118]
- the reconciler must query order and trade status to repair missed events [file:121]

### Worker Recovery

If a trade worker restarts:
- it reloads trade configuration
- it reloads most recent canonical order/fill state from the database
- it resubscribes to relevant event streams
- it resumes from the last consistent state machine checkpoint

### Reconciler Recovery

If the reconciler restarts:
- it replays recent raw broker events
- it queries open/pending/traded order sets from broker REST endpoints [file:121]
- it republishes any missed canonical state transitions

## Security and Operational Controls

For higher management review, the platform should include explicit operational controls:

- API secrets and broker credentials stored outside source code.
- environment-specific config for UAT versus production.
- audit logging for manual actions.
- service heartbeat monitoring.
- kill switch for strategy-wide or account-wide emergency stop.
- role-based UI actions for build, pause, exit, and admin functions.

## Two-Week Delivery Scope

The two-week goal should be an implementable Version 1 proving the architecture rather than the entire final-state system. The recommended committed scope is:

- C++ market-state path from decoder input to worker read path.
- one C++ trade worker implementing one simple trade lifecycle.
- Greeksoft session/login flow working end to end [file:122][file:114][file:118].
- execution through Iris WebSocket or broker-supported path with correct correlation [file:118][file:121].
- reconciler consuming live responses and validating via REST order/trade detail [file:121].
- PostgreSQL persistence for trades, orders, events, and fills.
- a minimal control API and dashboard page showing system state.

## Detailed 2-Week Application Plan

### Day 1: Architecture Freeze

- finalize service boundaries
- finalize language choice per service
- finalize internal message contracts
- finalize database entity list
- confirm UAT environment, credentials, and API/server flags [file:122][file:120]

**Deliverable:** approved architecture note and service/interface checklist.

### Day 2: Repository and Build Scaffolding

Create repo structure:

- `services/feed-decoder`
- `services/market-state`
- `services/trade-worker`
- `services/risk-engine`
- `services/session-manager`
- `services/execution-gateway`
- `services/reconciler`
- `services/control-api`
- `services/snapshot-service`
- `services/trade-supervisor`
- `libs/contracts`
- `libs/db`
- `infra/docker`
- `docs`

**Deliverable:** compiling skeletons with shared config conventions.

### Day 3: Internal Contract and DB Freeze

- define `TradeCommand`, `OrderIntent`, `OrderUpdate`, `FillEvent`
- create SQL schema migrations for primary tables
- define canonical order status enum
- define correlation and trace ID conventions

**Deliverable:** schema package and migration scripts checked in.

### Day 4: Greeksoft Session Manager Implementation

- implement session token request [file:118][file:121]
- implement `getFlagValues` [file:114][file:121]
- implement `getLoginInfo` [file:114][file:121]
- implement `jloginNew` [file:114][file:121]
- implement Iris login + heartbeat [file:118]
- implement Apollo login + heartbeat [file:118]

**Deliverable:** a standalone broker bootstrap service with logs proving full login flow.

### Day 5: Contract Master Implementation

- call `getAllContract` [file:121]
- parse and normalize contract data
- store in PostgreSQL
- expose token/symbol lookup APIs

**Deliverable:** searchable contract master with sample lookups.

### Day 6: Market-State Path

- connect decoder output to market-state service
- define shared-memory structures
- build worker-side read library
- validate token-index lookup and freshness timestamps

**Deliverable:** worker can read live price values from SHM.

### Day 7: Trade Worker v1

- implement one worker state machine
- support trade creation, basic decision loop, and order-intent emission
- no advanced strategy branching yet

**Deliverable:** one worker process emitting valid intents from live data.

### Day 8: Execution Gateway v1

- map `OrderIntent` to broker payloads
- implement order submission path
- add intent correlation ID handling
- add structured error and timeout handling

**Deliverable:** worker-generated intent can submit one order successfully.

### Day 9: Reconciler v1

- consume Iris order responses [file:118]
- query order detail and trade detail for verification [file:121]
- normalize status transitions
- publish canonical `OrderUpdate` and `FillEvent`

**Deliverable:** end-to-end order lifecycle visible internally.

### Day 10: Persistence and Recovery v1

- persist trades, intents, order events, fills
- add worker reload from DB state
- add broker recovery hooks and reconciliation on restart

**Deliverable:** restart-safe lifecycle for basic cases.

### Day 11: Control API and Dashboard v1

- create trade
- list active trades
- list worker state
- show order events and fills
- manual pause/exit action

**Deliverable:** operator-visible management UI.

### Day 12: Hardening and Edge Cases

- simulate broker disconnect
- simulate missed WebSocket updates
- verify REST-based repair path [file:121][file:118]
- verify duplicate event suppression
- verify idempotent replay logic

**Deliverable:** resilience test results.

### Day 13: Performance Pass

- measure order-intent-to-submit latency
- measure worker loop times
- measure broker response handling latency
- identify JSON or IPC hotspots
- remove unnecessary copies or synchronous logging

**Deliverable:** performance baseline with next-step optimization list.

### Day 14: Management Demo Package

- architecture review deck or document
- demo script
- deployment notes
- current limitations and next-phase roadmap
- risk register and mitigations

**Deliverable:** management-ready package with working proof and clear next steps.

## Management View: What Will Be Demonstrated in 2 Weeks

At the end of the two-week window, management should expect to see:

- a clear target architecture with strong separation between market-data core, broker boundary, and control plane
- a live Greeksoft login/session stack proving integration feasibility [file:122][file:118]
- a working end-to-end order submission and response path [file:118][file:121]
- canonical persisted order/fill data
- a resilient design showing how WebSocket and REST work together for correctness [file:118][file:121]
- a roadmap from Version 1 to a fuller production rollout

## Risks and Mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| Broker session flow behaves differently in UAT | Delays implementation | Build session manager first and validate every login step independently [file:122][file:114] |
| Missing or unstable fields in broker payloads | Parsing and reconciliation errors | Preserve raw payload JSON and normalize in one reconciler layer [file:121] |
| Too much scope in two weeks | Incomplete system | Commit only to one end-to-end flow and one trade-worker lifecycle |
| Python accidentally remains in hot path | Latency/jitter issues | Freeze service responsibilities and prohibit hot-path Python components [file:113] |
| Worker restart logic becomes complex | Operational fragility | Keep v1 worker state machine intentionally small and event-driven |

## Phase 2 After the Initial 2 Weeks

Once Version 1 is demonstrated, the next phase should include:

- richer strategy lifecycle support
- advanced hedge and re-entry flows
- stronger UI and trade controls
- more extensive performance tuning
- optional migration from JSON internal messages to Cap’n Proto contracts [page:1][page:2]
- NUMA/affinity tuning on production hardware [web:146][web:150]
- deeper load testing and market-open stress testing

## Conclusion

The recommended platform architecture is not a generic microservice layout; it is a latency-budgeted design built around direct exchange data, isolated per-trade workers, broker-specific session management, and a reconciliation-first execution model [file:113][file:118][file:121]. This is the right balance between speed, reliability, and implementation feasibility for a two-week delivery window and gives management a clear path from prototype to production-grade trading infrastructure [file:122][file:114].
