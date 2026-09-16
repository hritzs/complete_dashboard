# GreekSoft Integration Architecture (September 2026 rewire)

This document is the single reference for the GreekSoft REST/WebSocket
rewire, the reconciler build-out, the Postgres transaction hardening, and
the new GreekSoft Apollo backup market-data feed. It reflects what was
verified directly against source code, the live database, the real
GreekSoft Postman collection, and GreekSoft's own REST/WebSocket
documentation (`libs/Greek RestAPI Documents/`) -- not assumptions. Where
something is still a plan rather than shipped code, it's marked as such.
Progress against this is tracked in `docs/TODO.md` under "Phase 5" and
"Phase 6".

## 1. Current market-data ingest architecture (verified, unchanged by this work)

```
                        ┌─────────────────────────────┐
  NSE multicast ───────▶│                             │
  233.1.2.5:34330       │                             │
                        │   feed-decoder (C++)        │──▶ ZMQ PUB :5556
  BSE multicast ───────▶│   socket_reader.cpp merges  │    (option chains,
  233.1.2.4:2004        │   BOTH sources into one     │     JSON, ~100ms)
  (PRIMARY, direct       │   packet queue -- no        │
   exchange feed)        │   staleness comparison,     │──▶ snapshot-service
                        │   whichever arrives first    │    (ZMQ SUB, WS
  XTS ticks (1512/1510) │   wins                       │     broadcast to UI)
  via market-data-      │                             │
  gateway's socket.io  ─▶ ZMQ SUB :5555 (FALLBACK)     │
  client                └─────────────────────────────┘
```

Verified 2026-09-15 by reading `services/feed-decoder/src/socket_reader.cpp`
directly: the PRIMARY path is a raw UDP multicast socket (kernel-tuned:
16MB rcvbuf, `SO_BUSY_POLL`, non-blocking) reading the real exchange feed
directly -- no broker involved. The FALLBACK path is a ZMQ SUB on
`tcp://127.0.0.1:5555`, fed by `market-data-gateway`'s XTS socket.io
client. Both are polled every loop iteration and pushed into the same
`ThreadSafeQueue<Packet>` as soon as either produces data -- **there is no
staleness/freshness comparison today**; it is a naive "first to arrive
wins" merge, not a primary/backup failover.

`market-data-gateway` also proxies `contract-master` for option-chain
construction (`/api/option-chain`, `/api/lot-size`, `/api/token`) and has a
known gap: `chain_fetcher/fetcher.go` explicitly errors on expiry-date
discovery ("not implemented -- use static config or separate service").

## 2. GreekSoft REST/WebSocket rewire

### 2.1 What was wrong before this work (all verified, not assumed)

- `libs/contracts` failed to compile: `contracts.go` and `events.go` both
  declared `OrderIntent`/`OrderUpdate`/`FillEvent` in the same package.
- `libs/go-common/db` had three independent compile bugs and was imported
  by nothing in the repo.
- Three mutually-inconsistent Postgres schema definitions existed
  (`libs/db/schema/schema.sql`, `libs/contracts/0001_initial_schema.up.sql`,
  and what `execution-gateway` actually queries at runtime).
- `libs/broker-greeksoft/auth.go` logged the plaintext password and its
  MD5 hash on every login.
- `libs/broker-greeksoft/orders.go`: any symbol outside a hardcoded
  5-symbol lot-size table silently sent `lot=1` to the broker regardless of
  the actual requested quantity -- a live mis-sizing bug, not just missing
  coverage.
- `services/reconciler` was, and largely still is, an empty stub
  (`func main() {}`), despite `execution-gateway`'s boot logic already
  explicitly assuming an external reconciler exists to resolve
  `PARTIAL`/`RECONCILIATION_REQUIRED` trades.

### 2.2 Resolutions (see `docs/TODO.md` Phase 5 for exact status)

- **`libs/contracts`**: the string-keyed shape in `events.go` is the real,
  live contract (matches Postgres `TEXT` ids and the reconciler's own
  code). The uint64/enum shape -- aimed at a future C++ trade-worker ABI
  bridge that doesn't exist yet (`fill_handler.cpp` is an empty stub
  excluded from its own build) -- was renamed to `CppOrderIntent` /
  `CppOrderUpdate` / `CppFillEvent` in `libs/contracts/cpp_abi.go` and
  documented as reserved/unused.
- **Schema ground truth**: `execution-gateway`'s actual SQL
  (`store_postgres.go`, `store_fills_postgres.go`, `store_legs_init.go`)
  was treated as the spec, confirmed by direct introspection of the live
  `trading` database. `libs/db/migrations/0004_reconcile_live_schema.sql`
  brings `schema.sql` in line with it.
- **`orders.broker_order_id` is deliberately not globally unique.**
  Verified against live data: the same `broker_order_id` (GreekSoft's
  `gorderid`) legitimately belongs to unrelated trades on different
  calendar dates (e.g. `120000001` appears on Sep 1, Sep 2, and Sep 12
  2026, all different `trade_id`s). This is normal exchange/broker
  behavior -- order numbers reset per trading day -- not a data quality
  bug. **Any code correlating an incoming broker push to an order row by
  `broker_order_id` must scope the lookup by trading day.** The reconciler
  (Phase 3, not yet built) must use `WHERE broker_order_id = $1 AND
  created_at::date = $2`, never a bare lookup.
- **`libs/go-common/db`**: left orphaned/unfixed deliberately. Reconciler
  gets its own `internal/persistence` package modeled on
  `execution-gateway`'s proven transaction pattern instead.

### 2.3 The real Iris `OrderResponse` frame shape (from GreekSoft's own docs, not the toy test fixture)

Confirmed from `libs/Greek RestAPI Documents/05_Greek RESTAPI_WebSocket.pdf`:

```json
{
  "response": {
    "svcName": "order",
    "serverTime": "1685016746000",
    "streaming_type": "OrderResponse",
    "data": {
      "side": "1", "qty": "50", "product": "0",
      "gtoken": "102036187",
      "order_status": "Pending",
      "eorderid": "1000000000101012",
      "gorderid": "120140064",
      "lu_time_exchange": "1369503632",
      "lu_time": "1685016632",
      "symbol": "NIFTY 25MAY23",
      "pending_qty": "50", "qty_filled_today": "0",
      "price": "18500.00", "trigger_price": "0.00",
      "reason": "", "cancelledBy": "",
      "tradeSymbol": "NIFTY", "instrument": "FUTIDX",
      "optionType": "XX", "strikePrice": "0.00"
    }
  }
}
```

Key findings that change the reconciler design from the original plan:

- **There is no separate `TradeResponse`/fill frame.** Fills are derived
  from `OrderResponse` frames: `order_status` transitions to `"Traded"`
  (confirmed status strings across the docs so far: `"Pending"`,
  `"Traded"`, `"RMS Rejected"`, `"Exchange Rejected"`) and `qty_filled_today`
  increments. The reconciler must diff `qty_filled_today` against the last
  known value to compute the incremental fill quantity, not treat every
  `OrderResponse` as a discrete fill event.
- **`eorderid` (exchange order id) is separate from `gorderid` (GreekSoft's
  own order id)** and, like `gorderid`, is only unique per trading day.
  `execution-gateway` currently never populates `orders.exchange_order_id`
  at all (confirmed: 0 of 122 live rows have it set) -- the reconciler is
  the natural place to start populating it from real `OrderResponse`
  frames.
- **`lu_time`/`lu_time_exchange` are real epoch timestamps** -- these
  replace the reconciler's current `normalize.go` hardcoded
  `BrokerTimestamp: time.Now()`.
- **No client-supplied correlation tag is echoed back.** Neither
  `OrderResponse` nor `getOrderBookDetailWithLegV2`'s REST response reliably
  round-trips the `Tag`/`UserTag` fields `orders.go`'s `PlaceOrder` sets
  (the REST orderbook's `tag` field appears to hold broker/RMS annotations
  like `"Ageing"`, not client data). Correlation must be by
  `(gorderid, trading day)`.

### 2.4 Iris websocket: verified working end-to-end, including heartbeat

Confirmed against the live account (147, GCID 36) via `cmd/wsprobe`:
session token → `jloginNew` → Iris websocket connect+login, login-ack
validation, and the mandatory `HeartBeat` cadence (every
`heartbeat_Intervals` seconds from `getFlagValues`, defaulting to 10s when
that call soft-fails) all work -- the connection stayed alive for the full
45s probe window with 3 heartbeat acks exchanged, where previously (no
heartbeat sent at all) a long-lived connection would have been at risk of
the broker dropping it. Reconnect-with-backoff (`ReadLoopWithReconnect`)
is implemented in `wsclient.go` via shared infrastructure in
`wscommon.go`, but has not yet been exercised against an actual mid-stream
disconnect (only the happy path has been live-tested so far).

The one thing still not observed live: a real `OrderResponse` push frame.
`wsprobe` is intentionally passive and never places an order, so nothing
triggers one during a capture window -- the frame shape in section 2.3 is
from GreekSoft's own documentation, not yet cross-checked against a live
capture.

## 3. New: GreekSoft Apollo backup market-data feed (design)

### 3.1 Goal

Use GreekSoft's Apollo market-data **websocket** (confirmed in the same
docs -- login/heartbeat/subscribe-for-`marketPicture` flow, structurally
identical to Iris but for ticks instead of orders) as a **staleness-gated
backup** to the existing primary feed, without slowing down or
restructuring the existing hot path. Explicitly websocket-based, not REST
polling (`getQuoteForSingleSymbol_V2`/`getToken_OnlyMbpData`) -- REST is
too slow for this purpose.

### 3.2 Why not just add a third naive-merge ZMQ source (like the existing XTS fallback)

The existing PRIMARY/FALLBACK merge in `socket_reader.cpp` has no
staleness comparison -- it's "whichever arrives first." That's acceptable
for XTS-as-fallback today, but doing the same for Apollo would let a
slow/lagging Apollo tick silently overwrite a fresher primary-feed value
with no ordering guarantee. Apollo should only ever be *used* when the
primary paths have actually gone stale, per the explicit ask.

### 3.3 Design: staleness detection lives in a new Go bridge, not in C++

```
                    ┌────────────────────────────────────────┐
GreekSoft Apollo    │  New Go bridge (own process/goroutines  │
websocket (marketP- │  = own hot path, doesn't share a thread │
icture, subscribe   │  with the primary XTS ingest):          │
per-token) ────────▶│                                          │
                    │  1. Apollo WS client (login + mandatory │
                    │     heartbeat, in libs/broker-greeksoft,│
                    │     mirrors IrisDiscoveryClient's shape)│
                    │                                          │
feed-decoder's      │  2. Passively SUBs to :5556 (feed-      │
option-chain        │     decoder's own periodic publish),    │──▶ ZMQ PUB
publish :5556       │     reading the primary_feed_age_ms      │    on a NEW
(read-only          │     field it reports -- never relays    │    port
observation) ──────▶│     the chain data itself.               │    (e.g. :5560)
                    │                                          │
                    │  3. Only forwards an Apollo tick when   │
                    │     primary_feed_age_ms exceeds a        │
                    │     staleness threshold. Per-minute      │
                    │     health log: primary/backup message   │
                    │     counts, current staleness state.     │
                    └────────────────────────────────────────┘
                                       │
                                       ▼
                  feed-decoder gets a THIRD ZMQ SUB block on the
                  new port, added to socket_reader.cpp following
                  the exact existing pattern (small, low-risk
                  addition -- not a rewrite of the current merge
                  loop).
```

Rationale for putting staleness detection in Go rather than C++:

- The broker-session/websocket-auth machinery for Apollo already has to
  live in Go (`libs/broker-greeksoft`, alongside Iris) -- there's no
  reason to duplicate a websocket client in C++.
- Keeping it a separate OS process gives it its own hot path: a slow
  Apollo reconnect or GreekSoft-side hiccup can't block or add latency to
  the primary UDP multicast read loop, which remains untouched.
- `feed-decoder`'s change is additive and mechanical (one more
  `zmq_socket`/`connect`/`recv` block matching the existing XTS one) --
  it doesn't need to know anything about GreekSoft, sessions, or
  websockets at all, keeping the hot path free of broker-specific logic.

**Why `:5556` + `primary_feed_age_ms`, not the originally-planned watch on
`:5555`**: the first implementation watched `tcp://127.0.0.1:5555` (the
XTS fallback ingest port) for raw traffic. A live platform run exposed
this as wrong: `market-data-gateway` (the only thing that would ever
publish to `:5555`) isn't started by `start_platform.sh`, so that port is
always silent -- confirmed from real logs showing `primary_stale=true`
for an entire session while the actual primary UDP feed was flowing the
whole time, meaning the bridge was permanently, wrongly forwarding backup
data. `:5556` (feed-decoder's own periodic option-chain publish) was
considered next and also ruled out: it's purely timer-driven and
republishes last-known values every 100ms regardless of whether any new
tick arrived, so raw message arrival there isn't a valid liveness signal
either. The fix: `socket_reader.cpp`/`.hpp` now maintain an atomic
`decoder::g_last_primary_tick_epoch_ms`, updated whenever the UDP or
XTS-fallback path receives real data (deliberately *not* updated by the
Apollo backup path itself, to avoid a feedback loop where the backup's
own traffic would make the bridge think primary had recovered).
`main.cpp`'s publisher includes this as `primary_feed_age_ms` in every
`:5556` message, and the bridge reads that field instead of watching for
raw traffic on any port.

### 3.4 Status

The Apollo websocket client itself (`libs/broker-greeksoft/apollo_client.go`,
`apollo_models.go`) is built and **confirmed live** against account 147 via
the new `cmd/apolloprobe`: login, mandatory heartbeat, and `marketPicture`
subscribe all work, with real ticks captured for RELIANCE (token
101002885) plus `OpenInterest`, `MarketStatus`, and an unsolicited `index`
(Nifty 50) broadcast. Real frame data corrected several fields the docs'
example omitted (`exchange_token`, `segment`, `IndicativeImbalanceQty`,
`LimitImbalanceQty`, `CasRefPrice`, `IndicativeClosePrice`) -- the typed
structs in `apollo_models.go` now reflect the live wire shape, not just
the documentation.

The staleness-gated Go bridge (`services/greeksoft-feed-bridge`, box 2) and
feed-decoder's third ZMQ SUB block for the new `:5560` backup port (box 3)
are both built. The bridge was confirmed live: with no primary feed
running in the test environment, it correctly treated the primary as
stale immediately and forwarded real RELIANCE/Nifty 50/Nifty Bank ticks,
correctly formatted for feed-decoder's `parse_json` (which does a plain
substring search for `"ExchangeInstrumentID"`/`"LastTradedPrice"` or
`"IndexName"`/`"IndexValue"` -- not a real JSON parser, so extra fields
are harmless). `feed-decoder` itself builds and links cleanly with the new socket, and
the full pipeline was confirmed end-to-end: with a real `feed-decoder`
process running, publishing a synthetic message on `:5560` in the bridge's
exact output format produced the expected "First Backup (GreekSoft
Apollo) ZMQ packet received!" log line.

**Staleness detection itself was verified end-to-end with the real
signal** (see the "Why `:5556` + `primary_feed_age_ms`" note above for
how the original approach was found wrong via a live run): with a fresh
`feed-decoder` and fresh bridge running together, `primary_feed_age_ms:0`
was observed directly on the wire while primary was live, and the
bridge's 1-minute health log confirmed correct behavior --
`primary_status_msgs=12593 apollo_frames=2522 forwarded=1100
primary_stale=false primary_age=73.765586ms` -- i.e. it detected the
primary feed as healthy and stopped forwarding backup data partway
through (`forwarded` < `apollo_frames` received), exactly as intended.
Section 3's design is fully implemented and verified, including the
staleness decision itself, not just the transport plumbing. Tracked in
`docs/TODO.md` under "Phase 6".

## 4. Future work / explicitly deferred

- **Apollo REST endpoints beyond the websocket** (if any exist) were not
  found documented in either the Postman collection or the official docs
  reviewed so far -- only IP/port capture and the websocket flow are
  confirmed. Not building a REST Apollo client speculatively.
- **`market-data-gateway`'s missing expiry-date discovery**
  (`chain_fetcher/fetcher.go`) -- pre-existing gap, unrelated to this
  work, not fixed here.
- **`execution-gateway`'s existing non-transactional write paths**
  (`MarkOrderExecution`, `insertOrderIntent`, `PersistVerifiedFills`'s
  per-fill-not-per-batch transaction) -- planned as Phase 5 of the
  GreekSoft rewire, not yet started.
- **XTS reconciliation** is explicitly out of scope for the reconciler
  work (XTS executor has no `GetVerifiedFills`/`VerifiedFillsProvider`
  implementation and was excluded from this effort by direct instruction).
- **C++ `services/feed-decoder/cmd/main.cpp`** is a dead 0-byte file (the
  real entry point is `src/main.cpp`); several stray duplicate `.h`/`.hpp`
  pairs exist under `include/decoder/`. Flagged for cleanup, not yet
  removed pending confirmation nothing references the `.h` copies.
- **`market-state` and `risk-engine` C++ services are pure stubs** and
  `trade-worker` reads from shared-memory segments
  (`prices_shm`/`chain_shm`) that nothing currently writes -- pre-existing
  gaps, out of scope for this rewire, noted here only so they aren't
  mistaken for something this work was supposed to fix.
