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
- [x] **OMSFeed redesigned and re-validated live, same day (2026-09-17).**
      Per the user's direction ("websocket as primary verifier, REST as
      fallback if it fails"), rebuilt `OMSFeed` to read
      reconciler-persisted Postgres state (today's `orders`/`fills`/
      `contracts`, quantity-weighted average price across partial fills)
      instead of opening a second Iris connection -- eliminates the
      collision entirely, no feature flag needed since it's always safe.
      `Executor.GetVerifiedFills` tries this first, falls back to REST
      only on a genuine error (including a cached reconciler
      `/api/health` check), never on a merely-empty result (that's the
      normal "still pending" case). Unit tests (health-check caching/
      pass/fail with `httptest`) plus a new integration test against
      live Postgres (quantity-weighted price math, the unfilled-order
      filter, the unhealthy-reconciler fallback trigger) all pass.
      **Validated live**: one real 1-lot NIFTY straddle build verified
      fully through the reconciler-DB path with zero REST fallbacks.
- [x] **Second live incident found and fixed the same day: the
      pre-existing "no matching order row" race actually lost a real
      fill.** Squaring off the validation trade above hit exactly the
      race this session's earlier summary had already flagged as a
      known-but-unfixed issue: execution-gateway's own order-row write
      (`broker_order_id`) can commit *after* GreekSoft's Iris push for
      that same order has already been dispatched and found no matching
      row (`ExecuteOrderIntent` does its own synchronous REST-based
      confirmation, up to ~2s, before writing `broker_order_id`, while
      Iris pushes often arrive within tens of milliseconds). The
      reconciler logged "no matching order row -- skipping" for both
      buy-back orders and never persisted the fills, leaving the DB
      showing the trade open/short 65/65 when the broker was actually
      flat (confirmed via live `NPRequest`, `netQty: 0` both legs) --
      corrected with user approval, same pattern as the first incident.
      **Fixed at the root**: `services/reconciler/cmd/main.go`'s
      `ApplyOrderUpdate` → `ErrOrderNotFound` path now hands off to a
      background `retryUnmatched` goroutine (6 attempts, 300ms apart,
      ~1.8s total) instead of giving up immediately, without blocking
      the Iris read loop. Not yet re-validated live (the fix landed
      after this session's live trades were already closed out) --
      worth confirming next time a real square-off runs.
- [x] **UI fix, same session**: the Latency tab showed "NetworkError
      when attempting to fetch resource" -- the browser could reach the
      vite dev server (port 3000) but not `services/latency-dashboard`'s
      own port (8023) directly, depending on how this environment's
      ports are exposed. Fixed by adding a `/api/latency` vite proxy
      rule (checked before the general `/api` rule) and switching
      `LatencyDashboard.jsx` to relative fetch paths -- the proxy runs
      server-side, so it only needs 8023 reachable from the machine
      running vite, not from the browser. Confirmed working via the
      proxy after a UI dev-server restart.
- [ ] **Not yet done**: `depthslicer.go` (depth-aware, impact-minimizing
      slice sizing replacing the fixed-7-chunk generator) -- the other
      half of Phase 1. Deliberately sequenced after `omsfeed.go` was
      fully built and tested rather than building both at once.
- [ ] Phase 2+ (autonomous SL that actually exits, TP, roll, hedge
      fix + fast-exit engine, wings/special-OMS ledger, event-bus
      priority arbitration) -- documented in the plan file, not started.

## Phase 11: Repo cleanup, UI end-to-end fix, complete HLD/LLD (2026-09-17)
Before starting Phase 2+, the user asked for the repo cleaned up and
fully documented. Three parallel research passes (dead-code survey, UI
functional audit, full-system architecture inventory) plus two
clarifying answers from the user scoped the work.

- [x] Removed 103 backup/dead files (`*.bak*`/`*.broken*`/`*.orig`/
      `*_old.*`) across `services/`, `libs/`, `ui/`, `scripts/`, plus 2
      tiny inert `.git.*.backup` pointer artifacts from a prior
      worktree recovery -- 19,763 lines removed in one commit.
      `refernce_py_code/`'s own ~250 patch/backup files are untouched
      per the standing agreement.
- [x] Removed confirmed-dead Go code (each verified via grep -- zero
      call sites outside its own definition): `internal/trading/`'s
      `zmq_loop.go`, `env.go`, `executor.go`, `builder.go` (whole
      files), `CalculateWeightedAveragePrice`, `PriceIntentFromLiveQuote`,
      `FormatExpiryForContractMaster`; `libs/broker-registry/factory.go`
      (whole file -- was ALSO the source of a pre-existing compile
      break, `xts.NewClient()` not implementing `broker.Client`, fixed
      as a byproduct of deleting the dead code that called it);
      `snapshot-service/internal/{zmq,websocket}` and
      `market-data-gateway/internal/{socketio,publisher}` (both dead,
      logic lives elsewhere or was never wired up).
- [x] Removed `services/control-api` entirely (user approved): the
      root module didn't compile (`cfg.GatewayURL`/`cfg.Greeksoft`
      undefined), plus an orphaned duplicate `control-api/cmd/` module
      with its own disconnected `go.mod`. Nothing live used it. Removed
      from `go.work`.
- [x] Verified every affected module builds/vets/tests clean
      (`-race` for execution-gateway) after each removal, individually,
      not just at the end.
- [x] Fixed confirmed UI bugs found by the functional audit
      (`ui/src/App.jsx`): `/api/manual/order`'s stray absolute
      `http://localhost:8005` URL (only call in the file that wasn't
      relative); `openModifyTradeModal` populating the wrong field
      names entirely (the modal always showed blank SL/TP and an
      unchecked Auto-Risk box instead of the trade's real config) and
      `handleModifyTrade` never actually sending
      `auto_risk_execution_enabled` even though the form collected it;
      a dead `calcAllowed` computation; `handlePortfolioSync` ("Sync
      Trade") doing nothing but a generic reload -- now calls the
      existing read-only `/api/trade/:uid/sync-preview` and surfaces
      CE/PE open qty + proposed status; the Testing tab's "Live ATM
      leg" display hardcoded to "lot=2 | qty=130" regardless of the
      actual user-editable lot inputs (the real submit logic was
      already correct -- only the display text was stale, including a
      wrong permanent "ONE ORDER" claim); `refreshLiveMetrics` silently
      swallowing snapshot-fetch failures forever (now logs on
      failure/recovery transitions, not per-poll); a dead
      `window._lastPrices` read; the Portfolio "Monitor Status" card's
      hardcoded "Running" for SL/Hedge regardless of trade state (now
      reflects `isTradeClosed`); Automation tab missing
      `hedge_start_time`/`roll_start_time` inputs (added, matching the
      existing `sl_start_time` pattern). Left "Minimum Hedge Points:
      19.50" as-is with a comment -- confirmed it's a real
      execution-gateway constant (`runtime.go`'s hedge floor), not a
      fabricated placeholder. Reworded "Recent Events"'s misleading
      "No current events" (implies zero events occurred) to "Not shown
      here yet" (the feature to show them here doesn't exist).
      Verified via `npx vite build` + curl-level endpoint checks --
      **no browser/screenshot tool is available in this environment**
      for a true interactive click-through, stated explicitly rather
      than implied as full coverage.
- [x] Pushed `greeksoft-websocket-discovery` to `origin` as a new
      branch (was 37 commits ahead of `origin/september-recovery`,
      never pushed before). Additive only -- did not touch `main` or
      any other branch.
- [x] Wrote `docs/HLD.md` and `docs/LLD.md` -- complete, accurate
      current-state architecture documentation (superseding
      `docs/architecture.MD`, which is the original pre-implementation
      plan and doesn't match what was actually built -- kept for
      historical reference, not deleted). Covers every service
      (including honestly marking `market-state`/`risk-engine`/
      `trade-supervisor`/`zmq-nats-bridge` as empty stubs, and
      `session-manager`/`market-data-gateway` as real-but-not-started),
      the ZMQ port map, the DB schema, sequence diagrams for order
      execution and market-data flow, and the testing patterns
      established across this session.
- [ ] Not in this pass, explicitly deferred: the actual Phase 2+
      Python-reference migration; building out the empty stub services;
      wiring session-manager/market-data-gateway into
      `start_platform.sh`.

## Phase 2 (of the Python-migration roadmap): autonomous SL that actually exits (2026-09-17)
Started the actual Python-reference migration. A fresh code read (not
assumed from memory) surfaced something more serious than "a stub is
missing a call": `runMonitorCycle`'s SL check
(`internal/trading/service.go`) was **already firing autonomously** --
no human involved -- and, on breach, only set `trade.Status =
"CLOSED_SL"` and deleted the runtime, **without ever placing an exit
order**. The system could mark a position closed and stop watching it
while the real position stayed open at the broker. This directly
contradicted `models.go`'s own doc comment on `SLPointsPerLot`
("alert-only and must never directly set a trade to CLOSED_SL") -- the
code had silently stopped honoring its own documented safety intent.

- [x] `runMonitorCycle`'s SL branch now calls
      `s.SquareOff(tradeUID, "SL")` -- the same function every manual
      square-off uses -- instead of flipping the status directly. This
      activates `SquareOff`'s existing `reason=="SL"` branch
      (`GenerateAggressiveChunkedOrders`, max-lots-per-order, fastest
      exit) that was confirmed **dead code** until now (nothing in the
      repo called `SquareOff` with `reason=="SL"` before this).
      Inherits `SquareOff`'s existing per-trade locking, verified-fill-
      based completion, and revert-on-error semantics for free.
- [x] Traced every `return` path inside `SquareOff` to confirm it never
      leaves a trade stuck at `SQUARING_OFF` on failure -- it always
      resolves to either a terminal status or reverts to the prior
      status. This is what makes "retry on the next `PollIntervalSec`
      tick" safe: a failed SL exit isn't lost, and can't deadlock the
      trade out of ever being retried.
- [x] `SquareOff`'s success path now sets `CLOSED_SL` (not `CLOSEDSQF`)
      when `reason=="SL"`, preserving the audit distinction --
      `CLOSED_SL` was already a recognized terminal status elsewhere in
      the file, just never reached. **Found and fixed a bug this same
      change introduced before it shipped**: the post-loop success
      check re-derived "did this succeed" from
      `tr.Status != "CLOSEDSQF"`, which would have misreported every
      successful SL exit as a failure. Caught by the new test
      (`TestSquareOff_ReasonDeterminesFinalStatus/SL`) failing
      immediately on first run. Fixed to check `remainingCE/PE == 0`
      directly instead of re-deriving success from a status string.
- [x] `slThresholdForTrade` fixes the SL threshold to the trade's
      **original** size (`trade.Lots`), not live `CEQty+PEQty` --
      matching the Python reference system's deliberate
      `FIXED_ORIGINAL_POSITION_SL` design, so a partial square-off,
      hedge, or roll can't drift the effective stop-loss tighter by
      shrinking the denominator. This codebase already used
      `trade.Lots` for exactly this reason a few lines above in the
      same function's `pnlPerStraddle` calc; the SL threshold just
      hadn't been updated to match.
- [x] Corrected `SLPointsPerLot`'s stale, contradictory doc comment.
- [x] Tests (`sl_trigger_test.go`): the threshold formula against known
      inputs (including the shrink-after-partial-exit case); a
      table-driven `SquareOff` test proving `reason="SL"` → `CLOSED_SL`
      and `reason="manual"` → `CLOSEDSQF`, via a purpose-built fake
      `Executor`/`VerifiedFillsProvider` (not a resurrection of the
      dead `MockExecutor` removed in Phase 11's cleanup). All pass with
      `-race`.
- [ ] **Live validation not yet run** -- requires deliberately setting
      an aggressively tight `sl_points_per_lot` on a real small trade
      to force a real breach, since market conditions can't be relied
      on to do it naturally. Flagged so it isn't forgotten; only to be
      run with explicit approval, same pattern as every other live test
      this session.
- [ ] Not in this pass: TP, roll, hedge-fix, fast-exit engine, wings,
      special-OMS ledger, event-bus priority arbitration --
      `tickRuntime`'s separate `SLPnLLimit`-based alert-only log is
      also untouched (a different, pre-existing config knob).

## Phase 2 continued: autonomous TP and time-based hard exit; SL unified with bps-of-spot (2026-09-17)
User asked to build TP (didn't exist as an autonomous exit at all) and a
15:15 hard time-based exit, then test SL/TP live with 1 lot and a
1-bps-of-synthetic-spot threshold. Two fields already existed unused in
`MonitorConfig` for exactly this: `SLPnLBpsOfSpot`/`TPPnLBpsOfSpot` (a
paired bps-of-spot pair with clearer semantics than the older
`SpotStopLossBps`, and the only one of the two with a TP counterpart) --
used these instead of `SpotStopLossBps`.

- [x] Added `bpsOfSpotThreshold(spot, bps)` = `spot*bps/10000.0`, the
      shared formula for all bps-of-spot comparisons, compared against
      `pnlPerStraddle` (already computed in `runMonitorCycle` from the
      original lot count, not live CE/PE quantity).
- [x] SL is now a single unified check: `SLPointsPerLot` OR
      `SLPnLBpsOfSpot`, first match wins -- structured as one evaluation
      rather than two independent `if`s specifically to avoid a double
      `SquareOff` call in the same tick if both are configured and both
      breach simultaneously (harmless either way since `SquareOff` no-ops
      on zero remaining quantity, but avoided structurally for clean
      logs).
- [x] New autonomous TP (`TPPnLBpsOfSpot`): fires when
      `pnlPerStraddle >= bpsOfSpotThreshold(spot, bps)`, verified exit via
      `SquareOff(tradeUID, "TP")`, final status `CLOSED_TP`.
- [x] New autonomous time-based hard exit (`SquareOffHardTime`, a field
      that existed but was completely unused): fires once
      `time.Now()` reaches the configured deadline, verified exit via
      `SquareOff(tradeUID, "TIME")`, final status `CLOSED_TIME`.
- [x] Added `executeAutoExit(tradeUID, reason, wantStatus)` -- shared
      helper for all three triggers (SL/TP/TIME): calls `SquareOff`, on
      success stops the runtime, on failure leaves it running so the next
      tick retries (same safe-retry property proven for SL in the
      previous phase, now shared by all three instead of duplicated).
- [x] All three terminal-status switches in `service.go` (skip guard,
      both PnL-reporting switches) and the UI's closed-trade lists
      (`CLOSED_TRADE_STATUSES`, `refreshLiveMetrics`'s skip filter, plus
      a new `.status-badge.closed_time` CSS rule) now recognize
      `CLOSED_SL`/`CLOSED_TP`/`CLOSED_TIME`.
- [x] `ModifyTradeRequest`/`ModifyTrade` gained `sl_pnl_bps_of_spot`,
      `tp_pnl_bps_of_spot`, and `square_off_hard_time` (accepts `HH:MM`
      or `HH:MM:SS`) so these can be configured on a live trade without a
      restart -- needed for the upcoming live validation test.
- [x] Fixed a doc-comment numerical error found while writing the tests:
      `SLPnLBpsOfSpot`'s worked example said "24200 spot, 14 bps = 338.8
      points" -- actually `24200*14/10000=33.88`, a 10x error. Cross-
      checked the formula itself against `SpotStopLossBps`'s own correct
      example (`24400*1/10000=2.44`) to confirm only the comment's
      arithmetic was wrong, not the design. Locked both correct examples
      into `TestBpsOfSpotThreshold`.
- [x] Extended `TestSquareOff_ReasonDeterminesFinalStatus` to cover TP
      and TIME. Full `go build`/`go vet`/`go test -race` clean; UI
      `vite build` clean. Rebuilt and restarted the `execution-gateway`
      binary so this is live.
- [x] **Live validation, 2026-09-17 ~15:11-15:32 IST, real 1-lot NIFTY
      straddles via GreekSoft account 147**:
      - Fixed a real GreekSoft login blocker first: `DeployStraddle`'s
        account default was the stale literal `"HRITIK"`, but the active
        `.env` block is configured for client `147`
        (`GREEK_USERNAME`/`GREEK_CLIENT_ID=147`), which was never actually
        read into the login path. Passing `account_id: "147"` explicitly
        fixed it. (A separate `GreekBrokerID`-wiring theory was tried and
        reverted -- the Postman collection confirmed `jloginNew` already
        hardcodes `brokerid: "1"` correctly, unrelated to the login
        failure.)
      - **TP test passed end-to-end**: armed `tp_pnl_bps_of_spot=1` on a
        live trade already at +2.40/straddle vs. a 2.33 threshold;
        `[RISK] TP_TRIGGER` fired within one poll tick, both legs bought
        back and verified 65/65, final status `CLOSED_TP`. First live
        proof of the TP mechanism built this session.
      - **SL/TIME test uncovered a real incident**: a second test trade
        got stuck in `PARTIAL` status (fill-persistence error), so its
        monitor runtime never started -- meaning the `sl_pnl_bps_of_spot`
        and `square_off_hard_time=15:24` configured on it were silently
        never being evaluated. The configured hard-cutoff did not fire.
        The trade sat as a real, live, completely unmonitored short
        options position at the broker for several minutes before being
        caught and manually flattened (verified 65/65 via the broker's
        own fill confirmation, not just a status flip). A second trade
        hit the exact same issue and was manually flattened at the
        user's requested 15:32 cutoff after its own auto-cutoff also
        failed to fire for the same reason.
      - **Root cause found and fixed**: `persistVerifiedFill`'s lookup
        (`store_fills_postgres.go`) matched local `orders` rows by
        `broker_order_id + broker_name + account_id` with no `ORDER BY`.
        GreekSoft's paper/UAT `broker_order_id` is only unique within a
        session, not globally -- confirmed live: order ids like
        `120000003` and `120000013` were reused the very next day for
        unrelated, even opposite-side, orders. The unscoped lookup could
        return yesterday's stale row instead of today's real one,
        producing a false "side mismatch" error that blocked the status
        transition to `ACTIVE`. Fixed with `ORDER BY created_at DESC
        LIMIT 1`, so reconciliation always matches the most recently
        placed local order. Deployed (rebuilt + restarted) with zero
        open positions and no new orders placed, per explicit
        instruction once the market was close to closing.
      - **Not yet done**: re-run the SL and TIME tests end-to-end now
        that the root cause is fixed (today's tests only proved TP
        cleanly; SL and TIME were proven only via manual intervention
        after the bug above blocked their automatic path). The
        synthetic-hedge autonomous trigger test (`force_one_lot_hedge_test`)
        was not attempted today -- ran out of market hours.

## Live validation, round 2 (2026-09-21 ~12:00-12:05 IST, real 1-lot NIFTY, GreekSoft 147)
Re-ran the tests that were blocked on 2026-09-17 by the stale-`broker_order_id`
reconciliation bug. That fix held: every trade went straight to `ACTIVE`
(`fully_verified=true`, `[MONITOR]` loop ticking within a second), never
`PARTIAL`.

- [x] **SL, autonomous, PASSED**: `sl_pnl_bps_of_spot=0.05` armed on a live
      trade; `[RISK] SL_TRIGGER source=bps_of_spot pnl_per_straddle=-0.15
      threshold=-0.12` fired on its own, both legs bought back and verified
      65/65, DB `CLOSED_SL`, legs at 0 with exit prices recorded, no
      persistence warnings.
- [x] **TIME, autonomous, PASSED**: `square_off_hard_time` set ~90s out with
      no SL/TP; `[RISK] TIME_TRIGGER` fired at exactly the configured second,
      verified 65/65 exit, DB `CLOSED_TIME`.
- [x] TP had already passed on 2026-09-17. SL, TP and TIME are now all
      proven live through the real autonomous path.
- [x] Corrected an earlier claim: a freshly deployed trade has NO SL armed
      (backend `sl_points_per_lot=0`, bps fields unset). The Modify modal's
      prefilled 30 / 14 only take effect when Save is pressed, because it
      sends every non-empty field -- clear any field you do not want armed.
      `sl_points_per_lot` is compared against total PnL in rupees, so the
      modal's default of 30 is a very tight ~0.46 pts/straddle for 1 NIFTY lot.
- [ ] **Hedge test deliberately NOT run -- found a bookkeeping gap first.**
      `ManualHedge` (service.go) places real orders (1 lot ATM CE + 1 lot ATM
      PE, opposite sides, and is a no-op when |net delta| < 1) but never
      books them into the trade's CEQty/PEQty/legs: it only calls
      `AppendIntent`/`MarkOrderSubmitted`. `SquareOff` then works from the
      stale original quantities, so a hedge followed by an exit can leave a
      residual real position at the broker (or count a hedge fill toward the
      exit's verification). `/api/manual/order` is still a stub
      ("Wire ExecuteGreeksoftOrder() call here"), so there is no manual route
      to flatten a stray leg. This is the roadmap's "hedge-fix" item and
      should be done before any live hedge test.
- [ ] Trade-level `trades.realized_pnl` stays 0.00 after every autonomous or
      manual exit: `computeVerifiedRealizedPnL` filters fills by a strategy
      key (last 10 chars of trade uid) that our orders never carry (we send
      `strategyName: "STOCK"`). Per-leg `trade_legs.realized_pnl` IS correct
      (e.g. +58.50 / -52.00), so the trade-level figure the UI shows for
      closed trades is stale. Fix by summing leg realized PnL or by carrying
      the trade uid in a field the broker echoes back.

## Hedge audit and fixes (2026-09-21, live-validated, real 1-lot NIFTY, GreekSoft 147)
Prompted by the hedge bookkeeping gap above and the question "should the
minute-end hedge be working and fixing delta?". Compared our trigger with the
Python reference (`refernce_py_code/trading/monitors/hedge_monitor.py`,
`snapshot_service.py`) and found several defects, fixed in commits 50abfdf and
148574b:

- [x] **Hedge orders were never booked** (`ManualHedge`). Now takes the
      per-trade lock, requires ACTIVE, hedges on the trade's own CE/PE tokens
      (a 1-lot CE + 1-lot PE opposite pair is a synthetic future, ~+/-1 lot of
      delta at any strike), sends MARKET orders (it used to send LIMIT with a
      nil price = 0.00), never retries a failed leg, verifies fills against the
      broker order book, persists them, and books only verified quantities
      into CEQty/PEQty. Live: hedge then autonomous TIME exit closed cleanly.
- [x] **Wrong delta sign for every short position.** The monitor cycle
      computed `CEDelta*CEQty + PEDelta*PEQty` (legs treated as long) while
      `DeployStraddle` correctly negated them, so the snapshot delta sign was
      inverted and every hedge went the wrong way (the first live forced hedge
      sold a synthetic on a book that was already net short delta). Now
      `shortLegGreeks` short-signs delta, gamma, theta and vega. Live check:
      deploy net_delta -2.99 vs monitor -3.09 (was +3.47 vs -3.41), and the
      forced hedge on delta -3.35 correctly BOUGHT the CE and SOLD the PE.
- [x] **Hardcoded 19.5-point minimum.** The reference floors points_allowed at
      `hedge_min_threshold_bps` (default 8) of LIVE spot; 19.5 was 8 bps of a
      ~24,400 spot frozen into a constant (18.72 at today's 23,400).
      `decideHedge` now uses `max(points_allowed, spot*bps/10000)`;
      `hedge_min_threshold_bps` is settable via the modify API and the UI
      modal; the UI no longer hardcodes 19.5.
- [x] **Hedge sizing.** Autonomous hedges always traded 1 lot. Now
      floor(|delta| / lot size) lots, capped at the trade's own lots and a
      1800-qty per-order ceiling (`ManualHedgeLots`; `ManualHedge` stays the
      1-lot wrapper for the manual button). Below one lot of delta nothing is
      placed (`DELTA_BELOW_ONE_LOT`): a lot-granular synthetic would overshoot.
      Consequence to know about: for a 1-lot trade the natural trigger can
      essentially never place a hedge (needs |delta| >= 65 units); it needs
      roughly 3+ lots to be reachable. The forced test flags still work.
- [x] The autonomous trigger wrote back its cycle-start copy of the trade after
      hedging (would have undone the booking) -- now reloads first; the forced
      one-lot test is single-shot after ANY attempt so a failed attempt cannot
      re-fire and double the hedge.
- [x] TIME exit re-proven, including on a hedged trade (CE short 130 closed as
      two 65 chunks, verified 130/130).
- [ ] **Not yet live-tested: the natural hedge path placing a real multi-lot
      hedge** (needs a multi-lot trade). Unit-tested (decision table, sizing,
      booking, hedge-then-exit round trip) and live-verified up to
      DELTA_BELOW_ONE_LOT.
- [ ] **Every fill is stored twice.** Both the reconciler (Iris push, fill_id =
      exchange trade id, e.g. `4122902`) and the gateway's
      `persistVerifiedFill` (fill_id = `GREEKSOFT:<order id>`) write a fills
      row for the same real fill, and `recomputeTradeLegFromPersistedFills`
      sums both. Flat trades hide it (buys and sells both double), but
      `trade_legs.current_quantity` and per-leg realized PnL are inflated 2x
      (seen: CE -260 for a 130 short). SquareOff is unaffected (it reads the
      trade record). Needs one owner for fill persistence or one shared
      fill_id scheme.
- [ ] `orders.phase` is stored as PRIMARY for HEDGE orders (intent.Phase is not
      persisted), so hedge orders cannot be told apart from entry orders in SQL.
- [ ] `computeVerifiedRealizedPnL` / `/api/debug/trade/reconciliation` match
      fills by a strategy key our orders never carry -> trade-level
      `realized_pnl` stays 0 and the debug reconciliation reads all zeros.
- [ ] Modify-modal defaults are only "suggested" values: Save sends every
      non-empty field, so a bare Save arms `sl_points_per_lot=30` (about 0.46
      pts per straddle for 1 NIFTY lot -- very tight) and `sl_pnl_bps_of_spot=14`.

## Latency, websocket proof, readable logs, backup (2026-09-21 ~12:30-12:50 IST)
- [x] **Backup before cleanup**: `/home/ubuntu/Desktop/api_gs/backups/backup-20260921-1229/`
      (`repo.bundle` full history, `working-tree.tar.gz` incl. the UNTRACKED
      `refernce_py_code/`, `trading-db.dump`, `env.backup` (secrets, mode 600),
      `logs.tar.gz`, `SHA256SUMS`, `MANIFEST.txt`). Verified: bundle OK, checksums OK,
      DB test-restored into a scratch database with identical row counts. Git tag
      `backup-20260921-1229` pushed.
- [x] **Latency tab was empty for two independent reasons, both fixed**
      (services/reconciler, services/latency-dashboard, ui/src/LatencyDashboard.jsx):
      1. The recorder only inserted when an order had exactly one `order_events`
         row. An ack and its fill arrive milliseconds apart and both go through the
         retry path, so that never held -- `latency_samples` had 0 rows ever.
      2. The value was wrong: "now - order created" was taken AFTER the 300ms retry
         backoff, so every order read ~310-335ms whatever the websocket did.
      Now latency = push-received time - order creation, upserted per (order, stage)
      keeping the minimum (`iris_confirmation`, `iris_fill`), with a unique partial
      index (migration 0007; the reconciler also creates it idempotently on start).
      **Measured live (4 orders): ack 7-11ms, fill 9-17ms, except the first order
      after a restart at 355ms/363ms (cold connection).** The old figure for the same
      orders was 309-671ms.
- [x] **Proof trades come over the websocket**: reconciler `/api/iris/status` (live
      liveness, heartbeat and order/trade push counts, matched / unmatched-gave-up),
      relayed at `/api/latency/iris`; `/api/latency/orders` labels each order
      IRIS_WS / REST_ONLY from whether its recorded events are websocket frames
      (all 20 of the 20 GreekSoft orders earlier today were IRIS_WS); a `[IRIS-WS]`
      log line per applied push. Every order shows ACKED + FILLED from Iris, plus a
      third FILLED event from the gateway's own REST order-book read.
- [x] **Readable terminal logs**: `scripts/watch_logs.sh` (merged, colour-coded;
      default key events; `--all`, `--errors`, `--trade <text>`, `--feed`) and
      `scripts/trade_timeline.sh <part of trade uid>|--last` (orders, how each state
      reached us, latencies, legs, key log lines, monitor health). `start_platform.sh`
      now ends with the viewer instead of a raw `tail -f`.
      Gotcha fixed while building it: mawk buffers non-interactive input, so live lines
      were held back until `-W interactive` (and a line-buffered tail) were used.
- [x] **Leaked monitor found by the timeline tool**: a manually squared-off trade kept
      its monitor goroutine for the life of the process, logging `[MINUTE-CHECK]
      status=SQUARING_OFF` every minute (16+ lines for one trade). Only the
      autonomous exits tore their runtime down. `runMonitor` now stops and drops its
      runtime once the trade's authoritative status is terminal. Verified live: 0
      minute-checks after close.
- [ ] **Recurring non-fatal issues shown by `watch_logs.sh --errors`** (not fixed):
      - reconciler: `NATS connect failed ... lookup nats` -- `.env` has
        `NATS_URL=nats://nats:4222` (a docker hostname) but no NATS runs locally, so
        order/fill events are never published;
      - login: `getFlagValues failed status=400` (falls back to jloginNew's websocket
        fields, works);
      - deploy: `FALLBACK BLOCKED | NIFTY has no safe hardcoded fallback` -- some path
        still asks for a fallback lot size (the real one comes from the chain);
      - feed-decoder: `[BSE] Unknown token` spam;
      - `computeVerifiedRealizedPnL: no fills found matching strategy` (see above).
- [ ] Next, as agreed: clean the whole repository, then write the complete docs of
      everything executed. NOTE the parent `~/Desktop/api_gs/` holds a lot of legacy
      material OUTSIDE this repo (old tarballs, `trading-platform*` copies, stray
      `.py`/`.docx` files) that a cleanup of "the complete repository" may or may not
      include -- needs a decision.

## Build paths audit: manual, custom and automated (2026-09-21 ~12:50-13:01 IST)
Asked "are the minute-end monitors, the automated UI straddle and the manual build working?"

- [x] **Minute-end monitors: working.** One `[MONITOR] minute=` evaluation per minute per
      trade (a 4-minute trade logged 4), with real decisions (`OK`, `DELTA_BELOW_ONE_LOT`,
      `HEDGE_TRIGGERED`) and the bps-of-spot floor (18.73 at spot 23,405).
- [x] **Manual build: working.** The UI's Sell Straddle / Sell Custom Straddle both POST
      `/api/trade/straddle`. Verified live by posting the exact UI payload (ATM strike and
      tokens from the live chain, `delta_neutral: true`, NRML): ACTIVE, 65/65, monitor
      running, clean square-off, 0 monitor lines after close. (Custom sell uses the same
      endpoint with distinct CE/PE strikes; not separately exercised live.)
- [x] **Automated build: was NOT working properly, now fixed and verified live.**
      `ConfigBuild` read only entry_time, lots, symbol and expiry. The form's exit_time,
      sl_bps, divisors and buffers were accepted and silently discarded, so the built trade
      had no exit time and no SL (a fresh trade has none armed) -- an unprotected entry -- and
      the path forced product MIS (GreekSoft product 0, intraday) where a manual build gets
      NRML. Now: `BuildRiskConfig` (exit_time -> SquareOffHardTime, sl_bps -> SLPnLBpsOfSpot,
      buffers, divisors) is validated at schedule time (bad exit, or exit not after entry ->
      400 before any order) and applied to the trade before it is persisted or any order is
      sent; MIS is no longer forced.
      **Live**: scheduled 12:56:39 for 12:57:54 with exit 13:00:24 and sl_bps 14 -> the
      scheduler entered at exactly 12:57:54, the trade was ACTIVE with product NRML,
      sl_bps=14 and exit=13:00:24 on it, `TIME_TRIGGER` fired at exactly 13:00:24, verified
      65/65 exit, `CLOSED_TIME`.
- [x] Scheduler operability: `GET /api/trade/straddle/scheduled`, `POST
      /api/trade/straddle/scheduled/cancel {job_id}`, jobs leave the pending set when they
      start (success or failure -- they used to linger after a failure), and the Automation tab
      lists pending builds with a Cancel button.
- [ ] **Pending scheduled builds are in memory only**: a gateway restart drops them silently.
      The API response and the UI say so; persist them if scheduled entries must survive
      restarts.
- [ ] **Accepted by the form but NOT implemented** (the API now reports them in
      `not_applied`, the UI logs a warning): `idv`, `idv_divisor`, `straddle_filter`,
      `roll_straddle_div`, `sl_start_time`, `hedge_start_time`, `roll_start_time`. The Go
      monitors have no start-time gates and no entry filters, and no roll.
- [ ] Note the new behaviour: the Automation form defaults `sl_bps` to 14, so an automated
      build is now armed with a 14 bps-of-spot SL (about 33 pts per straddle at 23,400) by
      default. `DeltaNeutral` is hard-set true for config builds.
- Platform fact (see memory): this environment's session runs to 15:40, so the 15:37 default
  exit is intentional -- an earlier note here that 15:37 was "after close" was wrong.

## Portfolio shows the day's closed trades with real realized PnL (2026-09-21)
Asked: after square-off, keep today's trades in the Portfolio with proper realized PnL so it is
clear what was executed.

- [x] **Why closed trades vanished**: the UI loader keeps a row only if it has legs or an
      active-like status, and `/api/straddles` (via `AllTrades()`) returns only 7 sparse columns
      -- a closed trade had zero lots, tokens and quantities and a non-active status, so it was
      dropped the moment it was squared off.
- [x] **Why realized PnL was always 0**: `upsertTrade` never wrote the `realized_pnl` or
      `closed_at` columns, and `LoadTrade` treats the (never-written) column as authoritative;
      `computeVerifiedRealizedPnL` also matched fills on a strategy key our orders never carry.
      Now `upsertTrade` writes both (a stale copy can never wipe a stored non-zero value),
      `SquareOff` stores the PnL computed from the trade's own orders, and the same computation
      is used on read so trades closed before this fix are also correct without a DB rewrite.
- [x] **Realized PnL is computed from the ORDERS table** (average cost, matched quantity, gross of
      brokerage/charges) -- not from `fills`, which double-counts (see the double-write item
      above). Checked by hand against today's trades: -19.50, +3.25, -260.00, -61.75, -16.25,
      -19.50, +22.75 = -351.00 gross for the day.
- [x] `GET /api/portfolio/today[?date=YYYY-MM-DD]`: the day's trades (open and closed) with close
      reason, closed_at, per-leg sold/bought averages and every execution (ENTRY / HEDGE / EXIT,
      leg, side, filled qty, price, status, broker order id). The Portfolio tab merges these in,
      shows a day summary strip (trades / open / closed / realized) and an Executions card per
      trade; action buttons are hidden on closed trades.
- [x] `orders.phase` and `hedge_group_id` are now persisted (BUILD / HEDGE / SQF / PSQF); older
      rows have the PRIMARY default so the intent id is used to classify them.
- [x] The changed `trades` upsert and `orders` insert SQL were exercised against the live database
      inside rolled-back transactions before deploying (a mistake there would silently stop
      trades being saved).
- [x] `start_platform.sh` now rebuilds `build/execution-gateway` on `normal` and `fast-restart`
      (it used to run whatever binary was last built by hand, i.e. potentially stale code) and
      refuses to start if the build fails.
- [ ] Not yet verified in a real browser (no browser available to the agent): the JSX compiles and
      the endpoint payload was checked through the dev-server proxy.
- [ ] The Ctrl+C in the live log view stops only the viewer; the services keep running.
      `stop_platform.sh` is stale (pkills `go run`/`main.go`, runs `docker compose down`) and does
      not reliably stop the Go binaries -- use `fuser -k <ports>` as documented in the README/chat.

## Incident 2026-09-21 15:17: automated build sold NOV instead of the weekly expiry
An automated build (Automation tab) sold NIFTY 23-NOV-26 23650 instead of the weekly 22-SEP-26.
Only the CE leg filled; the PE rested unfilled; the trade sat PARTIAL with no monitor; the
user cancelled the PE, squared off through the platform, and then had to sell a PE by hand.
Real gross loss for the episode: -279.50 (CE -78.00, PE -201.50). Four separate defects:

- [x] **Expiry was never asked.** The Automation tab had no expiry field; the request used the
      Terminal tab's global `selectedExpiry`, which after a symbol change / page load is set from
      whichever option-chain message the live feed sends first (arbitrary). Now: the Automation tab
      has a required Expiry dropdown with no default (cleared on every symbol change, the start
      button is disabled until one is chosen); `ConfigBuild` rejects a missing `target_expiry`
      (400) and checks at schedule time that the market data really has that symbol+expiry
      (and returns that same expiry -- a fallback chain is refused). The expiry is shown in the
      schedule response, the pending-builds list and the scheduler log lines.
- [x] **No retry / modify / chase for a build leg.** `executeBuild` ran a retry loop with
      `maxChunkRetries := 1` (so never twice), retried only on broker REJECTION (an order that
      was acknowledged but unfilled counted as success), then just watched fills for ~5s. The
      limit was `chain LTP at deploy - 2` (408.50 for the PE) while the market had moved to 404.30,
      and NOV options are illiquid, so the sell sat above the market. Now `chaseUnfilledBuild`:
      for every build order short of its quantity it RE-PRICES THE SAME ORDER (modify, never a
      second order, so a late fill cannot double the quantity) to live price - sell_buffer x round
      (rounds 1..3, never more than 10% below live), verifying fills after each round. If it cannot
      complete it CANCELS what still rests (also on any modify failure or missing live price) so
      nothing works unmonitored, then reports the verified result. Log prefix `[BUILD-CHASE]`.
      An executor without modify/cancel is left exactly as before. It never sends a new order.
- [x] **A PARTIAL trade's square-off bought a leg that was never sold.** The stored CEQty/PEQty
      were the INTENDED 65/65 although the PE never filled, and `SquareOff` sized its exit from
      them: it bought 65 CE and 65 PE, leaving an unintended long PE. Now a build that ends PARTIAL
      stores the VERIFIED quantities, and `SquareOff` for a PARTIAL or RECONCILIATION_REQUIRED trade
      re-derives the open quantity from the orders' filled quantities (`TradeOpenQuantities`) and
      fails closed if it cannot. Regression test reproduces the exact incident and was
      mutation-checked.
- [ ] **NOT verified against the live broker**: GreekSoft modify (`SmallModifyOrderRequest`) and
      cancel (`DELETE /Order/<id>`) are implemented from the Postman docs and were exercised only
      against fakes. Try them with a far-from-market limit order (explicit approval needed) before
      relying on the chase; if a call fails the chase stops and logs `[BUILD-CHASE] ... FAILED`.
- [ ] A PARTIAL trade still gets no monitor runtime (so its SL/TIME exit does not fire) even when
      it later fills completely; only a build that reaches ACTIVE inside `DeployStraddle` starts one.
- [ ] Orders placed OUTSIDE the platform (e.g. the manual PE sell, gorderid 2987) are ignored by the
      reconciler ("no matching order row") and are absent from the portfolio and its PnL.
- [x] The NOV trade was deleted from the database at the user's request; its rows are exported to
      `~/Desktop/api_gs/backups/deleted-trades/20260921-152359-NOV23650/` (CSV per table + README).
      The day's gross realized is now -325.00 over 8 trades (it would be -604.50 including the NOV
      episode and its manual PE leg).
- The gateway binary was rebuilt but the running process is the old one: after the close run
  `./start_platform.sh fast-restart` (it now rebuilds the gateway itself) to pick all of this up.
