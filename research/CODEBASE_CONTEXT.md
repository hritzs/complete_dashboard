# Trading platform — codebase summary for research

Written 2026-10-13. Describes the repository this folder sits in
(`trading-platform-git-recover`, branch `greeksoft-websocket-discovery`), so a
research session has the context without re-reading the code.

`research/` is offline research only. **Nothing here may change live code.**

> **Do not confuse two systems.** `New Text Document (5).txt` in this folder
> is a transcript from a *different* project: a Python / XTS dashboard on a
> Windows machine (`api_v2_main_29_07_wings_new`). Its file names
> (`trading/roller.py`, `lut_factors.py`, `order_executor.py`) and its rules
> do not exist here. This repository is Go + C++ on the GreekSoft broker.
> Both trade the same GammaShort strategy.

---

## 1. What the platform is

A live **NIFTY short-straddle** platform: it sells an ATM straddle sized to be
delta-neutral, buys protective wings, hedges at each minute end, and exits on
stop-loss, target, MTM rules or time. Broker: **GreekSoft** (account 147).
Market data: NSE feed decoded in C++; GreekSoft candles for minute-end prices.

## 2. Processes

Started in this order by `start_platform.sh` (Ctrl+C in that terminal stops
everything; the user rebuilds and restarts at any time).

| Service | Path | Port | Job |
|---|---|---|---|
| Contract master | `services/contract-master` | 8010 | instruments, lot sizes |
| Feed decoder (C++) | `services/feed-decoder` | 5556, 5557 | NSE feed → option chain |
| Snapshot service | `services/snapshot-service` | 8003 | serves the live chain |
| **Execution gateway** | `services/execution-gateway` | 8005 | all trading logic and the REST API (about 32,000 lines of Go) |
| Reconciler | `services/reconciler` | 8021 | holds the GreekSoft order websocket (Iris), records order and trade pushes |
| GreekSoft feed bridge | `services/greeksoft-feed-bridge` | 8022 | holds the GreekSoft market-data websocket (Apollo); serves live quotes at `/api/quotes` |
| Latency dashboard | `services/latency-dashboard` | 8023 | latency figures |
| UI (Solid.js, Vite) | `ui/` | 3000 | the screen |

Libraries: `libs/broker-greeksoft` (REST + websocket client), `libs/go-broker`,
`libs/broker-xts` (older broker), `libs/contracts`, `libs/db`.
Database: Postgres (trades, legs, orders, fills, shared broker session).
Day files: `~/.trading-platform/lut/` (minute records, chains, Paper Sim).
Logs: `logs/4_execution.log`, `logs/6_reconciler.log`,
`logs/7_greeksoft-feed-bridge.log`. `scripts/watch_logs.sh` filters them.

## 3. Execution gateway — where things are

All under `services/execution-gateway/internal/trading/` unless noted.

| Area | Files |
|---|---|
| Build, monitor, hedge, square-off | `service.go` (3,600 lines), `chunker.go`, `build_chase.go`, `hedge_decision.go`, `combined_sqf.go`, `wings.go`, `exit_wings.go` |
| LUT entry check | `lut.go` (maths and tables), `lut_sim.go` (engine, one tick per 100 ms), `lut_manual.go` (what-if, sell zones, opening sell levels), `lut_preopen.go`, `lut_store.go`, `lut_close.go` |
| LUT real build | `lut_build.go` (arms, fires once, disarms) |
| Minute-end prices | `gs_close.go` (GreekSoft candle at the boundary), `gs_overlay.go`, `gs_backfill.go` (fills restart gaps) |
| Exits by rule | `mtm_exit.go`, `exit_tiers.go`, `portfolio_mtm.go` |
| Paper Sim | `paper_sim.go`, `paper_mtm_sqf.go` |
| Straddle Build tab | `sb_runner.go`, `sb_live.go`, `sb_rule.go`, `sb_pms.go` |
| Mock tab | `mock_session.go` (off unless switched on in the tab) |
| Broker checks | `auto_broker_sync.go`, `trade_broker_sync.go`, `reject_reasons.go`, `order_value_cap.go`, `empty_trades.go` |
| Storage | `store_postgres.go`, `store_fills_postgres.go`, `state_db.go` |
| HTTP | `handlers.go`, routes in `services/execution-gateway/main.go` |
| GreekSoft adapter | `services/execution-gateway/internal/brokers/greeksoft/` |

UI tabs (`ui/src`): Terminal, Portfolio, Automation, LUT Build
(`LutBuildTab.jsx`, `LutActualBuild.jsx`), Straddle Build, Paper Sim
(`PaperSimTab.jsx`), Mock, Logs, Latency. Most of the rest is `App.jsx`.

## 4. How a LUT trade runs

1. **Daily inputs** load once: previous straddle, previous future, yesterday's
   adjusted IV, IDV pure / with-prev, normalised adjusted-IV change.
2. **Before the open** the LUT tab shows an estimate from the previous close
   and the "opening sell levels": for each of 09:16–09:20, the ATM straddle
   at and above which that minute's table says YES.
3. **Each minute from 09:16** the engine takes the prices the minute opens at
   (last trade before hh:mm:00, from the GreekSoft candle; own feed as
   fallback), reduces them to a table cell and records YES / NO.
   - Underlying = **synthetic future** (ATM strike + CE − PE).
   - Cell = (DTE, IV ratio, straddle ratio, Build IV, opening gap, ADJ band).
   - Five tables: 09:16, 09:17, 09:18, 09:19, 09:20+.
4. **First YES while armed** fires the real build, then the LUT disarms itself
   and only monitors.
5. **Build**: CE and PE quantities chosen so the position is delta-neutral
   (example 2026-10-13: 86 lots CE to 68 lots PE), sent in small clips over
   about 17 seconds, each verified from the broker's pushes. Wings bought
   with it, 2 % out of the money.
6. **Each minute end** the monitor checks, in order: hedge, stop-loss, target,
   time. Hedge = synthetic at the live ATM strike, rounded down to whole lots.
   Points allowed = max(min(IV/57 × U, straddle/4), 8 bps of U).
7. **Exit**: lot by lot, main legs, hedge legs and wings reduced in step.
   Session ends 15:40; default exit 15:37.

LUT constants (`lut.go`): weights 0.2 / 0.5 / 0.3; ADJ band −0.008 / +0.004;
stop 14 bps; target low/high by DTE 16/30, 13/25, 13/25, 10/20, 8/15, 8/15,
8/15; strike step 50; last scan 13:30; table 7×10×11×5×3×3.
How these differ from the backtest: `BACKTEST_SUMMARY.md` section 7.

## 5. Facts verified live

- **Lot size 65**; freeze quantity 3511, so the largest order is 3510 (54 lots).
- **Broker limits** (set at the broker, dealer 147): an MTM loss limit and a
  single-order value limit of ₹2.25 crore counted as (strike + price) × qty on
  **sell** orders only. Both rejected hedges on 2026-10-12.
- **Order tags**: the broker stores the `tag` we send and returns it in its
  `userTag` column of the order book, order detail and trade detail. The
  `userTag` we send is not kept. Its own `tag` column is empty.
- **GreekSoft candles** (`get_ohlc`): a finished bar is stamped when it was
  closed (the first trade after its minute); the newest bar is still forming.
  The reply is **sometimes cut off hours early**. Code that reads a candle
  must check it is fresh (`gsBarCloseAt` in `gs_backfill.go`).
- **One GreekSoft login per account.** A second login logs the first out.

## 6. Trades so far this week

| Day | Trade | Result |
|---|---|---|
| 2026-10-12 | 22650 straddle, 77 lots, LUT YES 09:17 | Hedge rejected by broker limits at 09:23; stop at 09:24; −₹2,17,379.50 |
| 2026-10-13 (expiry) | 22350 straddle, 77 lots, LUT YES 09:16 at 132.95 (level 131.65) | Future +140 points in two minutes; hedged 09:17; stop at 09:18 (−41.76 vs −31.46 points); −₹2,22,345.50 |

On 2026-10-13 the loss first crossed the stop at 09:17:32 and the exit ran at
09:18:00, about 10 points per straddle later. The stop is checked once a
minute, which is also what the backtest assumes.

## 7. Rules for any work in this repository

From the owner's standing instructions.

1. **Never place, cancel or modify a real broker order on your own.**
2. **Never open a second GreekSoft websocket or do a separate login.** It
   logs the platform out. Reading with the saved session is safe
   (`libs/broker-greeksoft/cmd/bookprobe` has no login code).
3. **Never put old or broken code back in the working tree**, even briefly.
   The owner rebuilds at any time.
4. **Minute-end prices are GreekSoft 1-minute candle closes** everywhere; the
   own feed is only the fallback.
5. **Execution before screen detail.** The screen shows a minute only once
   its data is final.
6. Broker session runs to **15:40**.
7. `.env` holds credentials. Never print it.
8. **Research stays in `research/`.** Proposals for live code are written up
   and asked about first.

## 8. Related projects

- `~/Desktop/overnight-nifty-long` — the overnight (positional) system, split
  out on 2026-10-12 into its own program. The copy under
  `services/execution-gateway/internal/overnight` is a disconnected backup.
- The Windows dashboard described in the pasted transcript (see the note at
  the top).

## 9. Research data: what is and is not on this machine

- **Here**: `~/.trading-platform/lut/<day>.csv` / `.jsonl` (the LUT's recorded
  minutes), `<day>_chain.jsonl` and `<day>_chain_next.jsonl` (the chain each
  minute, current and next week, from 2026-10-06), and the logs.
- **Not checked from here**: the tick share `\\172.16.1.85\Shared\TickDataIV`,
  the LUT folders and the daily inputs CSV the backtest reads. The transcript
  shows them reachable from the Windows machine. Whether this Linux machine
  can reach that share, and whether Python with numpy / pandas / pyarrow /
  numba is installed here, has not been checked.
