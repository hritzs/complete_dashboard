# Low-Level Design

Companion to `docs/HLD.md`. Current-state reference reviewed 2026-10-15,
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

## 10. Target-based Straddle Build (SBuild)

This is a separate order-management and position-management path inside
`execution-gateway`; it does not use the LUT's first-YES entry decision.
The main implementation is `internal/trading/sb_runner.go` (engine,
monitor, API), `sb_rule.go` (rule persistence and CRUD), `depth_entry.go`
(pure tranche planner), `sb_live.go` (broker OMS), `sb_pms.go` (verified
position ledger), and `sb_handoff.go` (transfer to the standard monitor).
The UI is `ui/src/StraddleBuildTab.jsx`, with live position rows in
`ui/src/SBPortfolioRows.jsx`.

### 10.1 Runtime and data sources

- `main.go` calls `StartSBEngine()` during gateway startup. It loads saved
  rules, restores today's saved runs, then starts a 100 ms loop.
- Each loop reads the current option chain from snapshot-service, with one
  read shared across rules having the same symbol and expiry. A chain read
  has an 800 ms timeout. The contract-master resolves lot size. LIVE mode
  requires a healthy order-event source and waits through a 20-second
  gateway/restart warmup before sending.
- A rule's live preview uses the current ATM row, CE/PE L1-L5 bids and
  asks, depth timestamps, option deltas, quote/ATM, target, and current
  PMS position. Entry planning requires both legs' depth timestamps to be
  within `max_depth_age_ms` (default 1,500 ms).
- The event payload is pushed as `sbuild_update` every third loop
  (approximately 300 ms under normal scheduling). The UI also loads rules
  and state through HTTP and polls a quote for the editor once a second.

### 10.2 Rule inputs and defaults

`SBConfig` contains rule ID/name, symbol, optional expiry (blank means the
nearest expiry), target straddle, desired contract quantity per option leg,
participation, maximum book levels, tranche gap, SL/TP bps, exit time,
straddle and hedge divisors, minimum hedge threshold, and maximum depth age.
Quantities are rounded to the nearest whole lot and must be at least one
lot. Defaults include NIFTY, 75% participation, five levels, a 50 ms
tranche gap, straddle divisor 4, hedge divisor 57, 8 bps minimum hedge
threshold, and 1,500 ms maximum depth age. A new rule defaults to SL 14
bps, TP 14 bps, and exit 15:37 IST. Symbol/expiry cannot change while a
rule is active; other edits are recorded and applied to the running rule.

Rules live at `~/.trading-platform/sbuild_rules.json` by default, overridable
with `SBUILD_RULES_PATH`. Each run is saved beside that file in
`sbuild_runs/<rule-id>.json`; the legacy one-rule file is migrated on load.
Writes use temp-file plus rename, and `stateMirror` mirrors the JSON through
the platform-state persistence mechanism. The file includes phase, rule,
verified fills, marks/Greeks, risk values, event trail, pending order intents,
and restart/halt information. Secrets and `.env` values are not part of
these records or this design document.

### 10.3 Entry authorizer: `PlanDepthEntry`

The planner is pure logic: it receives CE/PE depth and deltas from the same
snapshot, the target, lot size, current verified net delta, remaining
combined quantity, participation, and max levels. It never places an
order. Its decision sequence is:

1. Require a known lot size, positive target, remaining size, two-sided
   bids, and valid absolute deltas. Require the best-bid straddle to be
   **strictly greater** than target.
2. Count each leg's fillable bid quantity through the configured levels,
   retaining only levels whose price plus the other leg's best bid remains
   above target.
3. Apply the participation factor and round down to whole lots. When
   multiple rules build the same resolved symbol/expiry, the engine divides
   each rule's configured participation by the number of active builders;
   previews include the rule as a prospective builder.
4. Choose the smaller-capacity leg as the anchor. Size its partner against
   current option delta and existing PMS net delta, then reduce the anchor
   in lot steps until both legs fit capacity and remaining quantity.
5. Walk both books for the exact quantities. Authorize only if both
   quantity-weighted prices sum to strictly more than target. The deepest
   bid reached per leg becomes that leg's IOC limit floor. The final tail
   of at most two lots uses a separate delta-reducing plan.

The plan, all intermediate quantities/prices, reason, and authorization
are visible in the rule state and PLAN event. This makes the decision
auditable against the book snapshot used to create it.

### 10.4 SHADOW and LIVE order paths

```mermaid
sequenceDiagram
    participant UI as Straddle Build tab
    participant EG as execution-gateway / SBuild runner
    participant SS as snapshot-service
    participant CM as contract-master
    participant GS as GreekSoft REST
    participant IR as Iris / reconciler order events
    participant PMS as SBuild private PMS
    participant DB as trade store / platform state

    UI->>EG: GET rules, state, quote
    UI->>EG: POST rule / start (SHADOW or confirmed LIVE)
    EG->>SS: current chain, ATM, L1-L5 and Greeks
    EG->>CM: lot size for symbol and expiry
    EG->>EG: PlanDepthEntry from live book and PMS position
    alt SHADOW
        EG->>PMS: simulate one-lot fills at book levels, capped by plan floors
    else LIVE
        EG->>DB: persist pending intent before sending
        EG->>GS: one-lot LIMIT IOC sell(s), paired where ratio permits
        GS-->>IR: order/trade status push
        IR-->>EG: terminal order confirmation
        opt No terminal push
            EG->>GS: broker order-book fill lookup
        end
        EG->>PMS: book only verified fills
    end
    EG->>DB: persist run state and fills
    EG-->>UI: sbuild_update (state, plan, fills, risk, events)
```

SHADOW executes the planned lot sequence against the captured depth levels;
it is a deterministic simulation, not an exchange fill guarantee. LIVE
creates a stored trade in `SBUILD` status and requires the exact `SELL LIVE`
confirmation, broker-session hours (09:15–15:40 IST), a healthy order-event
stream, and no existing position on that rule.

Each LIVE order is recorded as pending before submission. The OMS waits up
to 15 seconds for terminal order events. On timeout it checks GreekSoft's
own order book; a confirmed fill is booked, but an order whose outcome is
still unknown is left pending and the run enters HALTED. HALTED means no
more orders are sent until the operator reconciles with the broker. Fills
are applied to PMS only after they are confirmed. Rejections stop/replan
the tranche and consecutive rejects can stop building; an unconfirmed
order is a hard halt because actual position size is unknown.

The current fast-build implementation sends paired one-lot CE/PE IOCs when
the planned ratio allows. If one side fills and its partner does not, it
immediately attempts to complete the missing lot at progressively more
marketable limits. A one-sided lag is repaired before normal progression.
When both sides are level, a no-fill IOC is retried at a fresh quote; three
consecutive no-fill misses stop the tranche. A price that no longer meets
the plan floor or a changed ATM while the legs are level stops that tranche
for replanning. The execution details and unit tests are in `sb_live.go`
and `sb_live_test.go`; a unit test of the planner does not verify broker
latency, partial-fill races, or exchange behavior.

### 10.5 Position ledger, risk monitor, and exits

The private PMS (`sb_pms.go`) is the source of truth for the SBuild runner's
position. It accepts only fills the OMS has verified, tracks signed
quantity and average price by token/strike, realized and marked PnL,
build-specific CE/PE quantities and averages, net delta/gamma, and all
fills. Short options mark at ask (cost to cover), long options at bid. It
retains last marks/Greeks for legs that leave the visible chain window.
The builder's risk and remaining-size decisions use the whole position,
including earlier ATM strikes and hedges.

Once per new minute the runner checks SL, TP, exit time, and hedge on the
filled position. SL/TP are configured bps of spot and measured per filled
straddle, not per requested target size. The hedge calculation uses the
current ATM straddle, average ATM IV, configured straddle/IV divisors, a
minimum threshold floor, current net delta/gamma, and caps hedge size by
the filled position. LIVE hedges and exits use MARKET orders and await
confirmation; SHADOW applies simulated fills. An exit stops further
building and flattens all open strikes. Buy-to-cover orders are ordered
before sells when flattening mixed legs.

When a LIVE run is COMPLETE or stopped part-way, handoff to the standard
trade monitor requires both a short CE and short PE, no pending orders,
and a usable stored trade. The largest short CE and PE are the main legs;
other strikes and hedges are carried as extra `trade_legs`. The run enters
HANDED_OFF and stops its own risk monitor; standard Portfolio/monitor owns
hedging and exits. It follows the stored trade to terminal status and then
marks the SBuild run EXITED. If handoff cannot safely occur, the SBuild
monitor retains ownership. A partial handoff records the requested and
filled quantities in the trade configuration.

### 10.6 Pause, stop, exit, and restart semantics

- **Pause (LIVE or SHADOW):** disallows new entry orders; already in-flight
  orders finish and are booked. Risk monitoring of the built position
  continues. Resume is permitted only in the broker session for LIVE.
- **Stop:** LIVE stops future entry orders but keeps monitoring/hedging the
  open position; Exit is the action that flattens. SHADOW stops its run and
  leaves the simulated position displayed. A HALTED LIVE run's Stop is an
  explicit acknowledgement that requires subsequent operator reconciliation.
- **Exit now:** LIVE requires exact `EXIT`, refuses while an order is in
  flight, resolves pending fills from the broker order book first, then
  sends market flatten orders. It does not double-close a position after
  handoff; that must be exited from its normal Portfolio row. SHADOW
  flattens its simulated PMS immediately.
- **Restart:** fills rebuild PMS from saved records. Today's active runs
  resume after the first minute-end risk check. A LIVE restart with a
  pending order halts because fill status is unknown; an open prior-day
  LIVE position also halts for broker reconciliation. Prior-day SHADOW
  runs are marked STOPPED rather than resumed.

### 10.7 API and UI contract

The gateway routes are registered in `execution-gateway/main.go`:

| Endpoint | Method | Purpose |
|---|---|---|
| `/api/sbuild/rule` | GET / POST | List/default rules; create/update a rule |
| `/api/sbuild/rule/delete` | POST | Delete a rule with no open run/position |
| `/api/sbuild/shadow/start` | POST | Start SHADOW, or LIVE with `mode=LIVE` and `confirm=SELL LIVE` |
| `/api/sbuild/shadow/stop` | POST | Stop future entries / acknowledge a halt |
| `/api/sbuild/shadow/state` | GET | Get each rule's state, plan, fills, risk, and audit events |
| `/api/sbuild/pause` | POST | Pause or resume entry OMS |
| `/api/sbuild/exit` | POST | Flatten; LIVE requires `confirm=EXIT` |
| `/api/sbuild/expiries` | GET | Available expiries and lot size for a symbol |
| `/api/sbuild/quote` | GET | Current ATM quote for the rule editor |

The UI displays target-vs-bid, depth ladders, plan authorization, filled
quantity, average CE/PE sale price, PnL, delta, phase, rejection/halt state,
pending orders, and recent events. It listens for `sbuild_update`; it is an
operator display/control surface, not an independent trading engine.

### 10.8 SBuild code and test map

- Pure planning: `depth_entry.go`, `depth_entry_test.go`.
- Runner lifecycle, state machine, restart: `sb_runner.go`,
  `sb_runner_test.go`.
- Rule save/update/delete and HTTP: `sb_rule.go`.
- LIVE orders, quote refresh, retries, confirmation and flatten: `sb_live.go`,
  `sb_live_test.go`.
- Verified fill ledger/marking: `sb_pms.go`, `sb_pms_test.go`.
- Handoff into regular Portfolio monitor: `sb_handoff.go`,
  `sb_handoff_test.go`.
- User controls and status: `ui/src/StraddleBuildTab.jsx`,
  `ui/src/SBPortfolioRows.jsx`, and SBuild event handling in `ui/src/App.jsx`.
- Incremental broker-side test coverage is not the same as a live end-to-end
  test. The recent faster paired-IOC behavior needs controlled verification
  of fills, imbalance completion, cancellations, fresh depth, ATM movement,
  order confirmations, and final position reconciliation before relying on
  it at larger size.
