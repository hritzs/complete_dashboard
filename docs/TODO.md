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

## Phase 7: Square-off correctness (found via a real live trade, Sep 16 2026)
A real straddle was sold and squared off during this session. Investigating
why the DB ended up showing `-325/-325` open and `status=SQUARING_OFF` when
the broker (confirmed live via `NPRequest`/`getOrderBookDetailWithLegV2`)
was actually fully flat (CE net=0, PE net=0) surfaced three real,
independent bugs in `services/execution-gateway/internal/trading/service.go`,
not just the interrupted-process story it first looked like.

- [x] **`SquareOff`: verified-fill persistence was batched at the very end
      of the whole multi-chunk/multi-attempt loop, not per chunk.** An
      interrupted process (confirmed: this exact live run was killed
      mid-square-off) lost every already-broker-confirmed chunk's
      progress -- the orders had genuinely executed and moved the real
      position (`fills`/`filled_qty` for those orders were: 0 rows / 0,
      despite `orders.status='FILLED'`), so a later retry would start
      from the stale original quantity instead of the true remainder,
      risking an unintended over-buy on resume. Fixed: added
      `persistSQFProgress`, called after every chunk, not once at the end.
- [x] **`PartialSquareOff` had NO fill verification at all** (unlike
      `SquareOff`) -- it trusted the synchronous order-placement
      response's status (even bare `"SUBMITTED"` was treated as good
      enough) and unconditionally subtracted the *requested* quantity
      from the trade's remaining CE/PE, regardless of whether anything
      actually filled. Brought up to the same `VerifiedFillsProvider` +
      `waitForVerifiedFills` + incremental-checkpoint standard as
      `SquareOff`; now returns an explicit "partial square-off
      incomplete" error with real verified counts if it can't fully
      verify, instead of silently reporting success.
- [x] **No locking**: two calls for the same trade (in-process) could
      both pass the load-then-check-status race and proceed concurrently.
      Added `Service.tradeLocks` (per-trade-UID `sync.Mutex`, via
      `lockTrade`), applied to both `SquareOff` and `PartialSquareOff`.
      Verified with `-race`: `TestLockTrade_SerializesSameTrade` and
      `TestLockTrade_DoesNotSerializeDifferentTrades` both pass.
      **Does not protect against**: a second process instance, or a
      manual action taken directly against the broker outside this
      platform -- the live incident's order history showed two exit
      mechanisms overlapping (the automated chunked square-off, plus two
      orders with irregular, non-platform-pattern IDs that closed the
      exact remaining quantity in one shot each), which briefly
      overbought one leg by 65 lots before a corrective sell fixed it.
      Root cause of that second mechanism unconfirmed -- possibly a
      "Full Exit" action fired while the first was still in flight, or a
      direct GreekSoft terminal action; user asked to confirm.
- [x] **Corrected 2026-09-16, with user confirmation**: the affected trade
      (`trade_uid=TRD_U001_GREEKSOFT_147_NIFTY_22SEP26_23250_20260916095239`,
      `id=1`) was a one-time historical correction, not a live position --
      `trades.status` set to `CLOSEDSQF` (the same terminal status
      `SquareOff` sets on real success), both `trade_legs.current_quantity`
      set to 0 and `status='CLOSED'`. A separately-placed trade the same
      day (`id=5`) squared off correctly end-to-end (65/65 verified both
      legs, realized P&L recorded), confirming the Phase 7 fixes above
      actually work on a live trade, not just in theory.

## Phase 5: GreekSoft Integration Rewire (Sep 2026)
Full plan: see the design decisions and phase breakdown discussed in-session
(also mirrored in `docs/greeksoft-integration-architecture.md` once written).
Using the real GreekSoft Postman collection + official REST/WebSocket docs
(`libs/Greek RestAPI Documents/`) as the source of truth, not assumptions.

- [x] **Phase 0 - Schema ground truth**
  - [x] Introspected the live Postgres DB directly (three previously
        conflicting schema definitions existed; live `store_postgres.go`
        SQL treated as spec).
  - [x] `libs/db/migrations/0004_reconcile_live_schema.sql` - brings
        `schema.sql` in line with reality (orders/trades extra columns,
        `trade_legs` UNIQUE(trade_id, contract_id)).
  - [x] Documented why `orders.broker_order_id` is deliberately NOT unique
        (GreekSoft order numbers recycle per trading day - confirmed against
        live data, not assumed).
  - [x] Marked `libs/contracts/0001_initial_schema.up.sql` stale/unused.
- [x] **Phase 2 - Fix `libs/contracts` compile break**
  - [x] Renamed the dead uint64/enum C++-ABI shape to `cpp_abi.go`
        (`CppOrderIntent`/`CppOrderUpdate`/`CppFillEvent`), leaving
        `events.go`'s string-keyed shape as the one real
        `contracts.OrderIntent`/`OrderUpdate`/`FillEvent`.
- [x] **Phase 2b - `libs/go-common/events`**
  - [x] Added `Subscribe[T]` generic helper (only `Publish` existed before).
  - [x] Fixed empty `go.mod` (`go mod tidy`, deps now resolve).
  - [ ] `libs/go-common/db` intentionally left as-is/orphaned (nobody imports
        it; reconciler gets its own persistence package instead - see below).
- [x] **Phase 1 - `libs/broker-greeksoft` REST + auth gap-fill** (complete)
  - [x] Removed plaintext-password debug log in `auth.go` (security fix).
  - [x] Fixed a real latent bug in `orders.go`: any symbol outside the
        5-symbol hardcoded lot-size table silently sent `lot=1` regardless
        of actual quantity. Added `OrderIntent.LotSize` (in `libs/go-broker`,
        XTS untouched/unaffected) so callers can supply the real per-contract
        lot size; unknown lot size is now a hard error, not a silent `lot=1`.
  - [x] Added `heartbeat_Intervals` capture from `getFlagValues` (needed by
        the Iris/Apollo heartbeat requirement documented in the official
        websocket PDF - previously not captured at all).
  - [x] `client.go`: added `getJSON` (GET+decode; only `postJSON` decoded
        before).
  - [x] `login_info.go` - `GetLoginInfo`
  - [x] `contract.go` - `GetAllContract`, `GetFullScripDetails`,
        `GetAllowedProduct`
  - [x] `marketdata.go` - `GetMarketStatus`, `GetIndianIndicesDataV2`,
        `GetQuoteForSingleSymbolV2`, `GetOHLC`, `GetTokenOnlyMbpData`
        (some response shapes -- `GetIndianIndicesDataV2`, `GetOHLC` --
        aren't confirmed against a live capture; decoded generically and
        flagged in code comments rather than guessed)
  - [x] `modify.go` - `ModifyOrder` (`SmallModifyOrderRequest`)
  - [x] `orderdetail.go` - `GetOrderDetail`, `GetTradeDetail` (response
        shapes not confirmed live; generic + flagged)
  - [x] `positions.go` - `NPRequest` (typed, confirmed shape from docs),
        `NPDetailRequest`, `GetNetPositionMTM`,
        `GetStrategyNameWiseNetPositionDetail` (generic + flagged)
  - [x] `margin.go` - `MarginDetailRequest`, `GetHoldingValueInfo` (generic
        + flagged; `HoldingValueInfo` request shape has a documented
        Postman-vs-official-docs discrepancy, noted in code)
  - [x] `orderbook.go` - added `GetOrderBookTyped` as a new typed function
        alongside the existing `GetOrderBook` rather than changing it in
        place -- execution-gateway's `collectGreeksoftVerifiedFills`/
        `findGreeksoftOrderStatus` do generic `map[string]interface{}`
        traversal on `GetOrderBook`'s result today, so retyping it in
        place would have silently broken that already-working
        fill-verification code.
  - [x] Fixed a real bug in `orders.go`'s lot-size handling while adding
        `ModifyOrder` alongside it (see security/bugfix note above).
- [ ] **Phase 3 - Reconciler core (live Iris consumption, "TBT")**
  - [x] Confirmed via `wsprobe` against the real account (147) that the
        Iris login chain works end-to-end (session token -> jloginNew ->
        Iris websocket connect+login, GCID 36).
  - [x] ~~Confirmed via official docs: no separate `TradeResponse`/fill
        frame exists~~ **This was wrong, corrected 2026-09-16.** A live
        capture during a real trade showed GreekSoft *does* push a
        distinct `streaming_type: "TradeResponse"` frame on execution
        (`order_status: "Executed"`, exact `tradeid`/`traded_qty`/
        `traded_price`), separate from `OrderResponse`. The docs review
        missed it entirely, and the reconciler was silently dropping
        every such frame (`ingest.Dispatch` had no case for it, so it
        fell through to `KindUnknown`) -- meaning fills were never
        actually being captured via Iris. Fixed: added
        `StreamingTypeTradeResponse`, `GreeksoftTradeResponse`,
        `normalize.ParseTradeResponse` (uses the frame's exact
        `traded_price` as the fill price, more accurate than
        `OrderResponse`'s approximation, and the broker's own `tradeid`
        as the fill's identity), and a `KindTradeResponse` case in
        `cmd/main.go`'s dispatch switch feeding the same
        `ApplyOrderUpdate` path as `OrderResponse`. Covered by
        `TestDispatch_TradeResponse` / `TestParseTradeResponse_RealFixture`.
  - [x] Added mandatory heartbeat (every `heartbeat_Intervals` sec, shared
        `wscommon.go` infra) + reconnect-with-backoff (`ReadLoopWithReconnect`)
        to `wsclient.go`. **Confirmed live** via `cmd/wsprobe` against
        account 147: connection stayed alive 45s+ with 3 heartbeat
        acks exchanged (previously would have had no heartbeat at all).
  - [x] Iris login-ack is now validated in `NewIrisDiscoveryClient` (reads
        and checks the first frame is a successful `LoginResponse`) instead
        of firing-and-forgetting.
  - [x] `services/reconciler/internal/ingest` - real frame dispatch
        (OrderResponse + TradeResponse, see the correction above).
  - [x] `services/reconciler/internal/normalize` - rewritten around
        GreekSoft's real OrderResponse/TradeResponse shapes.
  - [x] `services/reconciler/internal/persistence` - transactional
        order/fill upserts, correlated by `(broker_order_id, trading day)`
        - NOT by `broker_order_id` alone (confirmed non-unique globally).
        Also computes each update's Iris confirmation latency (order-row
        creation -> push processed), logged on every update in
        `cmd/main.go` as `confirm_latency=...`.
  - [x] `services/reconciler/internal/publish` - NATS publish via the newly
        fixed `go-common/events`.
  - [x] `services/reconciler/cmd/main.go` - rewritten, wires everything
        together with graceful shutdown.
  - [x] **Real bug found and fixed via a real placed trade (2026-09-16)**:
        the reconciler's Iris connection was in a permanent disconnect
        loop during the exact window a live straddle was placed, so it
        captured zero fills for that trade -- confirmed by inspecting the
        DB directly: every `fills`/`order_events` row for that trade had
        `"Source": "GREEKSOFT_ORDERBOOK"` (execution-gateway's own
        pre-existing synchronous REST verification), none had the shape
        the reconciler's Iris pipeline would have produced. Root cause:
        `ReadLoopWithReconnect` redialed Iris reusing the *original*
        session on every reconnect instead of re-authenticating --
        GreekSoft allows only one valid session per account, so
        execution-gateway's own separate login (to place the trade)
        invalidated the reconciler's session, the server closed Iris with
        a graceful "close 1000 (normal)", and the reconnect loop then
        retried forever with a session that could never work again (only
        a full process restart, which re-logs in, recovered it). Fixed:
        `ReadLoopWithReconnect`/`ApolloReadLoopWithReconnect` now take the
        `*broker.AccountConfig` and call `PerformFullLogin` fresh on
        every reconnect, not just the first connect. This is a
        self-healing fix, not yet re-verified against another live
        concurrent-login collision (the underlying multi-process-login
        architecture issue below is still real).
  - [x] **Fixed the architectural issue itself, not just made it
        self-healing**: reconciler, greeksoft-feed-bridge, and
        execution-gateway now share one GreekSoft session via the
        existing (previously unused for this) `broker_sessions` table
        instead of each independently calling `PerformFullLogin`.
        `libs/broker-greeksoft/shared_session.go` adds `Client.LoginShared`
        (reuse a session from `broker_sessions` if fresh enough --
        default 10 min -- else perform a real login and save it) and
        `InvalidateShared` (mark a session dead on disconnect so nobody,
        including this process's own next reconnect, reuses it).
        `ReadLoopWithReconnect`/`ApolloReadLoopWithReconnect` now take a
        `*sql.DB` (nil = always fresh login, e.g. `wsprobe`/`apolloprobe`
        keep their old behavior) and call `InvalidateShared` right before
        each reconnect attempt. Migration
        `0005_broker_sessions_shared.sql` adds
        `broker_sessions.broker_specific JSONB` to hold the gcid/
        session_id/iris+apollo endpoints alongside the existing
        `session_token`. `greeksoft-feed-bridge` gained its first-ever DB
        connection (optional -- falls back to unshared login if
        `POSTGRES_DSN` is unset/unreachable) purely for this.
        **Confirmed live**: started reconciler fresh (empty
        `broker_sessions`), then started the bridge ~30s later -- bridge's
        log shows "reusing shared session for account=147" (no fresh
        `jloginNew`), and reconciler's Iris connection stayed alive
        continuously through 3 heartbeats spanning well past the bridge's
        login, with zero disconnects. This directly reproduces and fixes
        the exact collision confirmed in the live-trade incident above.
- [ ] **Phase 4 - Reconciler recovery path** (not started)
- [ ] **Phase 5 - ACID hardening in execution-gateway** (not started; see
      `store_postgres.go`'s `MarkOrderExecution`/`insertOrderIntent` and
      `store_fills_postgres.go`'s `PersistVerifiedFills` for the specific
      transaction-boundary gaps identified)
- [ ] **Phase 6 - HLD/LLD docs** (`docs/greeksoft-integration-*.md`)

## Phase 6: Market Data Multi-Feed Architecture (Apollo backup feed)
New work item (Sep 2026): use GreekSoft's Apollo market-data **websocket**
(not REST - REST polling is too slow for this) as a staleness-gated backup
to the existing primary feed(s), on its own independent path so it can't
slow down the hot path. See `docs/greeksoft-integration-architecture.md`
for the full design once written.

Current verified state of `services/feed-decoder/src/socket_reader.cpp`
(read directly from source, not assumed): it already merges TWO sources
into one packet queue with no staleness gating at all - PRIMARY is direct
UDP multicast from the exchange (NSE 233.1.2.5:34330 / BSE 233.1.2.4:2004,
real exchange feed, no broker involved), and a FALLBACK ZMQ SUB on
`tcp://127.0.0.1:5555` fed by `market-data-gateway`'s XTS socket.io client -
both are pushed to the queue as soon as either arrives, whichever comes
first, with no freshness comparison between them.

**Bug found and fixed via a real live run (2026-09-15)**: the bridge's
first implementation watched `tcp://127.0.0.1:5555` for liveness, but
`market-data-gateway` isn't started by `start_platform.sh` at all, so
nothing ever publishes there -- confirmed from an actual platform run's
logs: `primary_stale=true` for the entire ~2.5 minute session while real
primary UDP ticks were flowing continuously in feed-decoder's own log the
whole time, meaning the bridge was permanently (and wrongly) forwarding
backup data. feed-decoder's periodic option-chain publisher on `:5556`
was also considered and ruled out as a fix: it's purely timer-driven
(republishes last-known values every 100ms regardless of upstream
liveness), so raw message arrival there isn't a valid signal either.

Fixed by adding a genuine liveness signal: `socket_reader.cpp`/`.hpp` now
maintain `decoder::g_last_primary_tick_epoch_ms`, an atomic timestamp
updated whenever either the UDP or XTS-fallback path receives real data
(deliberately NOT updated by the Apollo backup path itself, to avoid a
feedback loop). `main.cpp`'s publisher includes this as `primary_feed_age_ms`
in every `:5556` message. The bridge now subscribes to `:5556` (not
`:5555`) and reads that field.

**Confirmed via a full live re-test** (fresh `feed-decoder` + fresh
bridge, both rebuilt with the fix): `primary_feed_age_ms:0` observed
directly on the wire while primary was live; after the expected momentary
"stale" at process startup (before the first `:5556` message arrives),
the bridge's 1-minute health log showed `primary_status_msgs=12593
apollo_frames=2522 forwarded=1100 primary_stale=false
primary_age=73.765586ms` -- i.e. it correctly detected the primary feed
as healthy and **stopped forwarding backup data** partway through
(`forwarded` < `apollo_frames`), exactly the intended behavior. The
original bug is confirmed fixed, not just theoretically addressed.

- [x] `libs/broker-greeksoft` Apollo websocket client (`apollo_client.go`,
      `apollo_models.go`) - login + mandatory heartbeat +
      `marketPicture` subscribe/unsubscribe, mirroring the Iris discovery
      client's shape. **Confirmed live** via new `cmd/apolloprobe` against
      account 147: real `marketPicture` (RELIANCE, token 101002885),
      `OpenInterest`, `MarketStatus`, and an unsolicited `index` (Nifty 50)
      broadcast all captured and correctly typed from the actual wire
      data, not just the docs example (which omitted several real fields:
      `exchange_token`, `segment`, `IndicativeImbalanceQty`,
      `LimitImbalanceQty`, `CasRefPrice`, `IndicativeClosePrice`).
- [x] New Go bridge process (`services/greeksoft-feed-bridge`): subscribes
      to Apollo's websocket feed, passively watches the existing primary
      ZMQ port `:5555` for liveness (never relays it), and only forwards
      Apollo ticks on its own new ZMQ port `:5560` when the primary has
      been silent past `STALENESS_THRESHOLD_SEC` (default 5s). **Confirmed
      live**: with nothing publishing on `:5555` in the test environment,
      the bridge correctly detected staleness immediately and forwarded
      real RELIANCE/Nifty 50/Nifty Bank ticks in feed-decoder's expected
      `{"ExchangeInstrumentID":...,"LastTradedPrice":...}` /
      `{"IndexName":...,"IndexValue":...}` JSON shape.
- [x] `feed-decoder`'s `socket_reader.cpp` got a third ZMQ SUB block for
      the new Apollo-backup port `:5560`, following the exact same pattern
      already used for the XTS fallback socket. Builds and links cleanly
      (`cmake --build build --target feed-decoder`). **Confirmed
      end-to-end**: ran the real `feed-decoder` binary and published a
      synthetic message in the bridge's exact output format on `:5560`;
      feed-decoder logged "First Backup (GreekSoft Apollo) ZMQ packet
      received!" -- the full pipeline (Apollo -> bridge -> ZMQ :5560 ->
      feed-decoder) is wired correctly end-to-end.
- [x] Periodic (per-minute) feed-health logging in the bridge: primary/
      backup tick counts, forwarded count, staleness state. Per-tick
      logging was intentionally avoided after live testing showed
      broadcast frames can arrive several times in quick succession
      (would flood logs at production volume) -- only the stale/fresh
      transition is logged immediately, ongoing volume via the health
      summary.
- [ ] Remove/flag redundant code encountered along the way (e.g.
      `services/feed-decoder/cmd/main.cpp` is a dead 0-byte file; several
      stray duplicate `.h`/`.hpp` pairs exist under `include/decoder/`).
- [x] Wired both new services into `start_platform.sh`: Reconciler
      (health :8021) and GreekSoft Feed Bridge (health :8022), following
      the exact `start_if_needed` pattern already used for every other
      service, plus added them to the force-restart cleanup list.
      **Important pre-existing risk noticed while doing this, not
      introduced by this change**: `start_platform.sh` also starts
      `trade-worker`, whose `main.cpp` hardcodes a demo trade
      (`TRD_NIFTY_001`, NIFTY, 50 lots) and pushes it as a real
      `OrderIntent` over ZMQ to execution-gateway on startup -- running
      the full script now will attempt to place that trade for real
      unless trade-worker's demo trade is disabled/parameterized first.
      Flagging this rather than running the script silently.
- [x] Made `GREEK_APOLLO_TOKENS` optional in the bridge (unsolicited
      broadcasts like the Nifty 50/Nifty Bank index ticks work without
      any subscription, confirmed live -- only per-instrument
      `marketPicture` ticks need explicit tokens).
- [x] Unified GreekSoft account-id resolution across `wsprobe`,
      `apolloprobe`, the bridge, and the reconciler to match the rest of
      the platform's `.env` convention (`GREEK_CLIENT_ID`/`GREEK_USERNAME`,
      e.g. "147") instead of requiring a separate `GREEK_ACCOUNT_ID` no
      other service sets.
- [x] Fixed `services/trade-worker/src/main.cpp`'s hardcoded demo trade
      (`TRD_NIFTY_001`, NIFTY, 50 lots, ran unconditionally on every
      launch) -- now requires explicit `<trade_id> <symbol> <quantity>`
      CLI args and exits with a usage message if none are given.
      Confirmed via direct run: exits code 1, no worker started, no
      OrderIntent sent. `start_platform.sh` updated to not attempt to
      auto-start it. Also fixes a latent bug where `init_shm()` never
      checked whether `mmap` actually succeeded before running the hot
      loop against `prices_shm`/`chain_shm` (which nothing currently
      writes).
- [x] Fixed a real UI bug found via a live screenshot after clearing the
      DB: `ui/src/App.jsx`'s `loadPortfolioFromBackend()` persists a
      "remembered" trade list to `localStorage`
      (`trading-platform-portfolio-items-v1`, by design, for resilience
      when the backend is briefly unreachable) but was treating "backend
      confirms zero trades" the same as "fetch failed" -- both paths just
      kept showing whatever was cached, with no way to ever clear a
      stale entry short of a manual `localStorage.removeItem`. Fixed to
      clear both the in-memory state and the cache on a genuinely
      successful empty response, while still preserving the resilience
      fallback for actual network/API failures (verified `safeFetchJson`
      throws on those, so they still hit the `catch` block, not the new
      clearing branch). Also found and fixed a second gap: the existing
      2s `portfolioSnapshotTimer` only re-fetches PnL/greeks for trades
      *already* in the cached list, never re-validates the list itself
      against the backend -- added a 20s `portfolioResyncTimer` calling
      `loadPortfolioFromBackend()` so a trade cleared server-side while a
      tab stays open self-corrects without requiring a page reload or any
      manual browser console command, not just once at page mount.
      Verified `npx vite build` succeeds after both changes.
- [x] Cleared `orders`/`trades`/`fills`/`order_events`/`trade_legs` (via
      `TRUNCATE ... RESTART IDENTITY`) and confirmed sequences reset to 1.
      Verified DB<->execution-gateway<->UI wiring against the cleared DB:
      rebuilt `execution-gateway` fresh (the `build/` binary was stale,
      predating this session's library changes -- also confirmed
      `execution-gateway` isn't an actual CMake target, so `cmake --build`
      silently no-ops for it; use `go build` directly), ran it, and
      confirmed `/api/health`, `/api/trades`, `/api/positions`,
      `/api/straddles/active` all return clean empty-state JSON with no
      errors against the empty tables.

## Phase 8: Iris order-confirmation latency dashboard (Sep 16 2026)
Design doc: see the plan approved for this phase (Plan Mode, same session
as Phase 7). Scope was deliberately narrowed after a Plan-agent review
found the original NATS-based draft would be silently empty (no NATS
server runs in this environment, and nothing consumes `orders.update`
today) and would have skewed p95/p99 by sampling every Iris push per
order instead of just the first.

- [x] `libs/db/migrations/0006_latency_samples.sql` - new `latency_samples`
      table (`stage`-discriminated so future latency sources, e.g.
      feed-decoder tick-to-publish, can reuse it without a schema change).
      Applied to the live DB; `schema.sql` updated to match.
- [x] `services/reconciler/internal/persistence/latency.go` -
      `RecordConfirmationLatencyIfFirst`, gated on `COUNT(*) FROM
      order_events WHERE order_id = $1) = 1` so only the first Iris push
      per order is sampled (not every status transition, which would have
      recorded several ever-growing numbers per order). Runs outside
      `ApplyOrderUpdate`'s own transaction -- best-effort observability,
      never affects order-of-record persistence. Wired into
      `cmd/main.go`'s `applyAndPublish`, which is only reachable from the
      live Iris push loop, not the REST-recovery path.
- [x] Integration test extended (`store_integration_test.go`): asserts
      exactly one `latency_samples` row after the first push, and
      **still** exactly one after a second push for the same order --
      this is what actually proves the anti-skew gate holds. Passed
      against live local Postgres.
- [x] `services/latency-dashboard` (new service, REST-only, no NATS/
      websocket): `GET /api/latency/recent`, `GET /api/latency/stats`
      (count/avg/p50/p95/p99/max via Postgres `percentile_cont`, not a Go
      stats library -- the right-sized tool at this platform's order
      volume). Unit tests (`httptest` + fake store) and integration tests
      (percentile correctness, time-window filtering, against live
      Postgres) all pass. Manually smoke-tested end-to-end: inserted a
      synthetic sample, confirmed both endpoints reflected it, cleaned up.
- [x] `start_platform.sh` - wired in on port 8023 (port-kill list, pkill
      pattern, `start_if_needed` block, health check).
- [x] `ui/src/LatencyDashboard.jsx` (new component) + a "Latency" tab in
      `App.jsx` - stat cards + a live-updating recent-orders list, REST
      polling every 5s (same pattern the existing portfolio tab already
      uses, reusing existing `.metric-card`/`.log-line` CSS classes
      rather than adding new ones). `npx vite build` passes; dev server
      verified to serve the page without error. **Not verified via an
      actual browser click-through** (no real order flowed through it
      during this session to populate the tab) -- worth a manual check
      the next time a live order is placed.
- [ ] Deferred (explicitly out of scope for this pass, see the approved
      plan): `start_platform.sh` auto-applying migrations 0004-0006 (only
      0003 is auto-applied today); feed-decoder (C++) tick-to-publish
      latency instrumentation (no existing groundwork; a legitimate
      fast-follow using the same `latency_samples` table with a new
      `stage` value); cleanup of stale `ui/src/*.bak*` files.

## Phase 9: Python-reference migration research (COMPLETE - unblocked 2026-09-17)
The user wants the Python reference system's straddle trading logic
(SL/MTM/straddle-price exits, hedge/roll monitors, chunked fast-exit
rules) ported to C++/Go/GreekSoft for better latency and TBT support.
`refernce_py_code/` initially only had orchestration code (`api/routes.py`,
`api/websocket.py`, `background/tasks.py`); the user then added the
actual `trading/*.py` modules (`builder.py`, `trade_manager.py`,
`fast_exit_monitor.py`, `square_off.py`, `event_bus.py`,
`special_oms_db.py`, a `trading/monitors/` package, and ~250 dated
patch/backup/diagnose files alongside them, ignored).

- [x] Three parallel research passes read every real module (order
      execution/square-off/chunking; exit/risk monitor thresholds and
      formulas; infra/event-bus/special-OMS/DB), plus a fourth pass
      confirming this Go platform's actual current state (chunking
      already ported and matching Python's algorithm; verification is
      REST-poll-based; no autonomous SL/TP/roll/fast-exit exists yet;
      the one existing autonomous action -- a hedge trigger -- doesn't
      fold fills back into the leg-quantity model). Findings and the
      full design are in the plan file used to build Phase 10 below
      (not duplicated here in full -- see git history/session log for
      the complete per-module algorithm writeups).
- [x] User gave explicit architecture direction on *how* to port, not
      just *what*: replace Python's fixed-7-chunk/REST-poll/asyncio
      model with a TBT (Iris push)-driven execution engine, an explicit
      OMS/PMS split to minimize market impact cost, and true (goroutine)
      parallelism instead of asyncio's cooperative concurrency. See
      Phase 10.
- [ ] `refernce_py_code/` stays in the repo, untouched, until the port
      reaches functional parity (Phase 10's later sub-phases).

## Phase 10: TBT-driven OMS/PMS execution engine (in progress, 2026-09-17)
Phase 1 of the approved migration plan. Scope: replace REST-poll-based
fill verification with Iris-push-based verification and make concurrent
order placement genuinely parallel. Does **not** change what
SquareOff/PartialSquareOff/DeployStraddle are allowed to do, and does
**not** yet add any autonomous SL/TP/roll/fast-exit decision logic (that
is Phase 2+ of the plan, deliberately deferred -- real-money risk that
needs its own review/tests/paper-soak).

- [x] **Moved `ingest`/`normalize` out of `services/reconciler/internal/`
      into `libs/broker-greeksoft/ingest` and `libs/broker-greeksoft/normalize`.**
      Go's `internal/` visibility rule meant execution-gateway could not
      import the reconciler's copies to parse the identical Iris wire
      format for its own real-time use -- moving them to the shared lib
      (same package names, so only import paths changed) lets both
      services share one parsing implementation instead of forking a
      second one. Verified: `libs/broker-greeksoft`, `services/reconciler`
      (including the `-tags=integration` test against live Postgres) all
      still build/vet/test clean after the move.
- [x] **`services/execution-gateway/internal/brokers/greeksoft/omsfeed.go`
      (new)**: `OMSFeed` maintains an in-memory, Iris-push-fed
      `map[brokerOrderID]state` (side, status, cumulative filled qty,
      average price) and implements `trading.VerifiedFillsProvider` --
      a drop-in alternative to the existing REST-poll-based
      `Executor.GetVerifiedFills`. Deliberately advisory/real-time-only:
      it persists nothing and is never the durable source of truth --
      `services/reconciler` remains the sole ACID writer to Postgres
      from the same Iris stream (the PMS side of the OMS/PMS split;
      OMSFeed is the OMS side). Placed in `internal/brokers/greeksoft`
      rather than `internal/trading` (a refinement found during
      implementation, not in the original plan text) because the
      GreekSoft-token-to-internal-token mapping it needs
      (`resolveGreeksoftShortToken`) already lives there, and
      `internal/trading` must stay broker-agnostic (documented
      constraint already in `reconciliation_types.go`: broker packages
      import `trading`, so `trading` must never import a broker package
      back). Handles both `OrderResponse` and `TradeResponse` pushes,
      preferring `TradeResponse`'s exact `traded_price` and inheriting
      token from a prior `OrderResponse` for the same order (confirmed
      live this session: GreekSoft always sends both for a fill).
      State only moves forward (a stale/out-of-order push can't regress
      a known filled quantity).
- [x] `Executor` gained an `OMSFeed`/`VerifyViaIris` field;
      `GetVerifiedFills` delegates to the Iris feed when both are set,
      else falls back to the existing, already-proven REST path
      unchanged. `waitForVerifiedFills`/`verifySubmittedFills`
      (`verified_execution.go`) needed **zero changes** -- they just
      call whichever provider the executor exposes, so every call site
      (BUILD, `SquareOff`, `PartialSquareOff`, `ManualHedge`) gets the
      faster verification uniformly through one central switch instead
      of separate per-call-site plumbing.
- [x] `main.go`: gated behind `EXEC_VERIFY_MODE=iris` (unset/anything
      else keeps the existing REST-poll default). When enabled, starts
      an `OMSFeed.Start(...)` using the same shared-session login
      (`LoginShared`) pattern as the rest of this platform, so it
      doesn't collide with the reconciler's or greeksoft-feed-bridge's
      GreekSoft sessions.
- [x] Tests: 7 unit tests in `omsfeed_test.go` covering fill tracking,
      token resolution, the anti-regression rule, `TradeResponse`
      token-inheritance from a prior `OrderResponse`, the
      unknown-order-gets-zero-token case, the update-notification
      channel, and multiple independent orders. All pass with `-race`,
      no real broker/network dependency (constructs `normalize.*`
      payloads directly, doesn't need a live Iris frame).
- [x] **`EXEC_VERIFY_MODE=iris` validated live on 2026-09-17 -- found a
      real bug, now DISABLED pending redesign.** With user approval, ran
      one real 1-lot NIFTY straddle (`TRD_U001_GREEKSOFT_147_NIFTY_
      22SEP26_23300_20260917105853`) with the flag on. Confirmed
      `OMSFeed` opens its **own separate Iris websocket connection** --
      and GreekSoft allows only **one live Iris connection per account**,
      not just one HTTP session (LoginShared's shared session token is
      safe to reuse; a second live websocket for the same account is
      not). `OMSFeed`'s connection and `services/reconciler`'s existing
      one repeatedly kicked each other offline
      (`"disconnecting the current session, User has been logged in on
      another session"`), and this directly caused a real fill to be
      lost: the reconciler's connection dropped at the exact moment the
      original SELL orders' `TradeResponse` pushes would have arrived,
      so those fills were never persisted. The subsequent square-off's
      BUY-back fills *were* persisted fine (connection was stable by
      then), leaving the DB showing the trade net **long** 65 CE/65 PE
      when the broker was actually flat -- confirmed via a live
      `NPRequest` call (`netQty: 0` on both legs) and corrected with
      user approval (`trades.status='CLOSEDSQF'`,
      `trade_legs.current_quantity=0`), same pattern as the earlier
      Phase 7 incident.
      **Fix applied**: `EXEC_VERIFY_MODE=iris` commented out in `.env`
      (REST verification, the proven path, is active again -- confirmed
      the platform is healthy: reconciler stable, execution-gateway
      restarted cleanly, no active straddles). `omsfeed.go`'s doc
      comments now carry an explicit, unmissable warning not to call
      `Start()` while `reconciler` runs for the same account.
      **Not yet done**: the actual redesign -- `OMSFeed` should read
      reconciler-persisted state (e.g. Postgres `LISTEN/NOTIFY`, or fast
      polling -- reconciler already writes within milliseconds of an
      Iris push) instead of opening a second competing Iris connection.
      Until that's built, Phase 1's TBT-verification goal is only
      half-delivered: the reconciler-side latency win (Phase 8's
      dashboard) is real and live; the execution-gateway-side
      REST-poll-to-Iris-push swap is designed and coded but cannot be
      safely enabled yet.
- [ ] **Not yet done**: `depthslicer.go` (depth-aware, impact-minimizing
      slice sizing replacing the fixed-7-chunk generator) -- the other
      half of Phase 1. Deliberately sequenced after `omsfeed.go` was
      fully built and tested rather than building both at once.
- [ ] Phase 2+ (autonomous SL that actually exits, TP, roll, hedge
      fix + fast-exit engine, wings/special-OMS ledger, event-bus
      priority arbitration) -- documented in the plan file, not started.
