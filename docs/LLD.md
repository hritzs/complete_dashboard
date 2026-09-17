# Low-Level Design

Companion to `docs/HLD.md`. Current-state reference as of 2026-09-17,
built from direct code inspection (not the original plan in
`docs/architecture.MD`, which predates what was actually built).

## 1. Service reference

| Service | Language | Port | Started by `start_platform.sh`? | Purpose |
|---|---|---|---|---|
| `feed-decoder` | C++ | 5556 (ZMQ PUB) | Yes | UDP multicast + ZMQ fallback ingestion, option-chain/ATM/Greeks construction |
| `trade-worker` | C++ | — (ZMQ PUSH to 5557) | No (requires explicit `<trade_id> <symbol> <quantity>` args) | Per-trade state machine (`INIT→BUILDING→CHASING→ACTIVE→SQUARING_OFF→CLOSED`); reads SHM prices/chain (currently never populated — `market-state` is a stub) |
| `market-state` | C++ | — | No | **Empty stub** — all `.cpp` files are 0 lines; only real content is the `MarketState`/`MarketStateBlock` SHM struct headers, never written by anything |
| `risk-engine` | C++ | — | No | **Empty stub** — same as above, placeholder `main.cpp` only |
| `execution-gateway` | Go | 8005 | Yes | Order placement, chunking (`generateChunkedOrdersInternal`, 7-way split), fill verification (reconciler-DB primary / REST fallback), square-off/partial-square-off, the HTTP API the UI calls |
| `reconciler` | Go | 8021 | Yes | Consumes GreekSoft's Iris push feed, ACID-persists canonical order/fill state to Postgres, publishes to NATS (best-effort) |
| `greeksoft-feed-bridge` | Go | 8022 | Yes | Apollo (GreekSoft market data) websocket client; forwards backup ticks to feed-decoder via ZMQ `:5560` only when the primary feed is stale |
| `latency-dashboard` | Go | 8023 | Yes | Read-only REST API over `latency_samples` (written by reconciler); no NATS, no websocket |
| `snapshot-service` | Go | 8003 | Yes | ZMQ-subscribes to feed-decoder's chain broadcast, caches per symbol/expiry, re-broadcasts to UI websocket clients (`/ws/snapshots`, `/ws/data`, `/ws`); also relays PnL/Greeks pushes from execution-gateway (`POST /api/push-snapshots`) — **no DB access, no PnL computation of its own** |
| `contract-master` | Go | 8010 | Yes | Loads `IndexTokens.csv`/`BSEIndexTokens.csv`, upserts into Postgres, serves lot-size/token lookups |
| `market-data-gateway` | Go | 8020 | No | Thin REST proxy to contract-master only (`/api/option-chain`, `/api/lot-size`, `/api/token`) — its once-planned XTS Socket.IO→ZMQ publisher was dead code, removed 2026-09-17 |
| `session-manager` | Go | 50051 (gRPC) | No | Real, working: dynamically instantiates a broker client (via `libs/broker-registry`) and performs login, keyed by client ID; not wired into the running platform |
| `control-api` | — | — | — | **Removed entirely 2026-09-17** (broken build, orphaned duplicate module, unused by UI/start_platform.sh) |
| `trade-supervisor` | Go | — | No | **Empty stub** (`cmd/main.go` is `package cmd`, 0 lines) |
| `zmq-nats-bridge` | Go | — | No | **Empty stub** (`func main() {}`) |

## 2. `feed-decoder` internals

- **Entry**: `src/main.cpp` (real; `cmd/main.cpp` is an unused 0-byte leftover).
- **Ingestion** (`socket_reader.cpp`): primary UDP multicast (NSE
  233.1.2.5:34330, BSE 233.1.2.4:2004) + two ZMQ SUB fallbacks in the
  same thread — `tcp://127.0.0.1:5555` (XTS fallback; **currently has no
  publisher at all** since its would-be implementation in
  `market-data-gateway` was confirmed dead and removed) and
  `tcp://127.0.0.1:5560` (GreekSoft Apollo backup via
  `greeksoft-feed-bridge`, staleness-gated by the bridge itself).
- **Dispatch** (`packet_dispatch.cpp`): sniffs buffer shape → BSE NFCAST
  parser, JSON (ZMQ) parser, raw NSE 7208 parser, or LZO-compressed NSE
  multi-packet path.
- **Chain construction** (`chain_builder.cpp`): keyed by `SYMBOL|EXPIRY`;
  ATM/synthetic-future via a 4-step algorithm (monthly-future anchor →
  preliminary ATM → synthetic future from CE/PE at that strike → actual
  ATM), in `main.cpp`'s publisher thread.
- **Greeks** (`greeks_calculator.cpp`): Black-Scholes style, feeds `chain_builder`.
- **Output**: ZMQ PUB `tcp://*:5556`, one JSON message per symbol/expiry
  every 100ms:
  ```json
  {"type": "...", "symbol": "NIFTY", "synthetic_future": 23286.2,
   "future_ltp": ..., "atm": 23300, "expiry": "22-SEP-26",
   "primary_feed_age_ms": 0, "available_expiries": [...],
   "chain": [{"strike": 23300, "ce_token": ..., "pe_token": ...,
              "ce_ltp": ..., "pe_ltp": ..., "ce_iv": ..., "pe_iv": ...,
              "ce_delta": ..., "pe_delta": ..., "ce_gamma": ..., "pe_gamma": ...,
              "ce_vega": ..., "pe_vega": ..., "ce_theta": ..., "pe_theta": ...,
              "is_atm": true}]}
  ```
  `primary_feed_age_ms` is updated only on real UDP/XTS-fallback receipt
  (never on backup receipt, to avoid a feedback loop) — this is the
  liveness signal `greeksoft-feed-bridge` watches.
- Contracts loaded from `IndexTokens.csv`/`BSEIndexTokens.csv` at repo
  root, path resolved via `/proc/self/exe`.

## 3. `trade-worker` internals

State machine: `INIT → BUILDING → CHASING → ACTIVE → SQUARING_OFF →
CLOSED`. Reads two read-only SHM segments (`prices_shm`, `chain_shm`) —
since `market-state` never writes them, the hot loop just spin-waits
indefinitely (explicitly documented in the code itself). Writes
`OrderIntent` JSON via ZMQ PUSH to `tcp://127.0.0.1:5557`, which
execution-gateway binds as a SUB socket (a PUSH→bound-SUB pattern, only
exercised when `trade-worker` is run manually with explicit args — it
does not auto-start).

## 4. Order execution flow (`execution-gateway`)

```mermaid
sequenceDiagram
    participant UI
    participant EG as execution-gateway
    participant GS as GreekSoft REST
    participant Iris as GreekSoft Iris WS
    participant RC as reconciler
    participant PG as Postgres

    UI->>EG: POST /api/straddle/sell
    EG->>EG: generateChunkedOrdersInternal (7-way split)
    loop each order in chunk
        EG->>GS: place order (REST)
        GS-->>EG: broker_order_id, status
        EG->>PG: MarkOrderSubmitted (order row + broker_order_id)
        GS-->>Iris: OrderResponse + TradeResponse (near-instant)
        Iris-->>RC: push
        RC->>PG: ApplyOrderUpdate (ACID: order+event+fill+leg, one tx)
    end
    EG->>PG: GetVerifiedFills (reconciler-DB primary)
    alt DB/health check fails
        EG->>GS: GetOrderBook (REST fallback)
    end
    EG-->>UI: verified_ce/pe, fully_verified
```

Known race (fixed 2026-09-17, `services/reconciler/cmd/main.go`): the
Iris push can arrive and dispatch before execution-gateway's own
`MarkOrderSubmitted` write commits (its own REST-based confirmation can
take up to ~2s). `ApplyOrderUpdate`'s `ErrOrderNotFound` now retries in a
background goroutine (6× 300ms, ~1.8s) before giving up, instead of
dropping the push immediately — confirmed live to have actually lost a
fill before this fix existed.

## 5. Market-data flow

```mermaid
sequenceDiagram
    participant EX as Exchange (UDP)
    participant FD as feed-decoder
    participant SS as snapshot-service
    participant UI

    EX->>FD: multicast ticks
    FD->>FD: chain_builder (ATM, synthetic future, Greeks)
    FD->>SS: ZMQ :5556 chain JSON (100ms cadence)
    SS->>SS: cache by symbol|expiry
    SS-->>UI: WS /ws/snapshots broadcast
    UI->>SS: GET /api/option-chain/{SYMBOL}?expiry=
```

## 6. ZMQ port map

| Port | Direction | Publisher | Subscriber(s) | Purpose |
|---|---|---|---|---|
| 5555 | SUB in feed-decoder | **none** (dead code removed 2026-09-17) | feed-decoder | Intended XTS tick fallback — currently a gap, not wired to anything |
| 5556 | PUB in feed-decoder | feed-decoder | snapshot-service, greeksoft-feed-bridge (staleness watch) | Option-chain JSON broadcast |
| 5557 | SUB in execution-gateway | trade-worker (manual runs only) | execution-gateway | OrderIntent transport |
| 5560 | PUB in greeksoft-feed-bridge | greeksoft-feed-bridge | feed-decoder | Apollo backup ticks, staleness-gated |

## 7. Postgres schema (key tables)

| Table | Purpose |
|---|---|
| `broker_sessions` | Shared session tokens (`broker_specific` JSONB) — the shared-GreekSoft-session mechanism |
| `contracts` | Instrument master (broker_token, symbol, expiry, strike, option_type, lot_size) |
| `contract_master_raw` | Applied via `libs/db/migrations/0003_contract_master_raw.sql`, not in `schema.sql` proper |
| `strategies` | Strategy metadata, referenced by `trades.strategy_id` |
| `trades` | One row per straddle/strangle position |
| `trade_legs` | Per-contract-leg state within a trade (current_quantity, avg prices, realized_pnl, status) |
| `orders` | One row per broker order (broker_order_id, side, quantity, filled_qty, status) |
| `order_events` | Append-only audit trail of every order status transition |
| `fills` | Individual verified fills (fill_id unique per order, quantity-weighted price aggregation happens at read time) |
| `audit_logs` | Manual operator action log |
| `latency_samples` | Stage-discriminated latency measurements (`stage='iris_confirmation'` today) |

Migrations `0004`/`0005` are `ALTER TABLE` reconciliations against live
schema drift, not new tables; `0006` added `latency_samples`.

## 8. Testing patterns established this session

- **Unit tests**: table-driven, no external dependencies, run with
  `go test ./... -race` in every touched module.
- **Integration tests**: `//go:build integration` tag, require a real
  local Postgres (default DSN
  `postgres://postgres:postgres@localhost:5432/trading?sslmode=disable`,
  overridable via `POSTGRES_DSN`), create uniquely-prefixed test rows and
  clean up via `t.Cleanup` (registered so cleanup runs before the DB
  connection closes — an earlier version of this pattern had a bug where
  `defer db.Close()` ran first). Run via
  `go test -tags=integration ./internal/.../... -v`.
- **Live-broker verification before/after a DB correction**: never
  correct a DB row believed stale without first confirming the broker's
  actual position via a live `NPRequest` call, and never apply the
  correction without explicit user confirmation (established after two
  real incidents this session where a DB row was corrected this way).
- **Platform restart discipline**: run services in the foreground /
  harness-tracked background (not detached `nohup ... &`), so the user
  can observe and stop them — established after user feedback mid-session.

## 9. Known pre-existing gaps (not introduced this session, not fixed)

- `services/session-manager`: even after `libs/broker-registry`'s dead
  code was removed (which fixed one compile error), `internal/server/grpc.go`
  references an undefined `manager.SessionManager` and
  `internal/manager/manager_test.go` references an undefined
  `xts.BrokerName` — a separate, deeper pre-existing break, confirmed via
  `git stash` to predate this session's changes.
- `libs/broker-xts`: `client_test.go` calls `NewClient()` with 4
  arguments; the real `NewClient()` takes none — a pre-existing test
  compile break. Not touched (XTS is explicitly off-limits per this
  project's standing constraint).
- `services/control-api`: removed entirely 2026-09-17 (see
  `docs/HLD.md` §5 and `docs/TODO.md`) rather than fixed, since nothing
  live used it.
