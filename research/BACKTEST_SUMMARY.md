# GammaShort backtest — summary

Written 2026-10-13 from a full read of `GammaShort_Backtest_Final.py` (1,449
lines, in this folder). **The script has not been run here.** The numbers
quoted from its header are the author's, not reproduced.

This folder is research only. Nothing here changes the trading platform.

---

## 1. What the script is

One file, one pass over daily NIFTY tick files, simulating the whole
"GammaShort" book with every rule settled up to round 52 (2026-10-09):

- at most **one current-week (CW) short ATM straddle per day**, entered when a
  lookup table (LUT) says YES;
- an optional **next-week (NW) straddle** on the last two days before expiry.

PnL unit: **"normalised Rs" = index points × 25000 / entry underlying** (one
unit is sized 25000/U, so a day's result is comparable across index levels).

Reference result in the header (1,379 days, 2021-01-01 to 2026-08-24,
`DAY_INPUTS = backtest_files`, grid end 15:27):

| Variant | Net PnL | Max drawdown | Trades |
|---|---|---|---|
| NW check "live" | 10,640.5 | 144.4 | CW 713 + NW 71 |
| NW check "replay" | 10,434.7 | 144.4 | CW 713 + NW 58 |
| NW off | 10,216.7 | 120.6 | CW 720 |

---

## 2. What it does each day (CW leg)

### 2.1 Data preparation
1. Reads that day's file, keeps CE/PE rows of the **nearest expiry on or after
   the date** (CW); the next expiry is NW. Futures rows are dropped.
2. Builds a **1-second grid** 09:15 to 15:27 (15:37 from 2026-06-03).
   Underlying = first print of each second (`UnderlyingLTP`, the expiry's own
   synthetic forward). Seconds with no underlying are **dropped**, not filled.
3. Per strike (`strike_arrays`): CE/PE LTP, bid, ask = last print of the
   second, forward-filled (then back-filled, see section 6). Missing bid/ask
   fall back to the strike's LTP.
4. **IV**: Black-Scholes, r = 0, T = DTE/365, bisection, solved from the
   **out-of-the-money leg's LTP** (CE when underlying < strike, else PE), only
   in seconds where that leg actually printed (`IV_MODE = "tick"`), then held.
   IV outside 0.01–2.0 or LTP ≤ 0.1 is discarded.
5. **Fill prices**: options = mean of the next 30 seconds of bid (sell) or ask
   (buy); futures = mean of the next 3 seconds.
6. `ATMRef = round(underlying / 50) × 50`. The reference straddle is the real
   CE + PE LTP at that strike (no price is rebuilt from IV since round 52).

### 2.2 Entry signal
Checked each minute **09:16 to 13:30** (expiry day: to 09:59), reading the
last second **at or before hh:mm:00**. Six features become one table cell:

| Axis | Definition | Bins |
|---|---|---|
| DTE | trading DTE = raw DTE − 1 if the window to expiry crosses a weekend; index = clip(ceil − 1, 0, 6) | 7 |
| IV ratio | adjusted Build IV ÷ blended IDV, where blended = 0.2 × IDV_Pure + 0.5 × IDV_With_Prev + 0.3 × yesterday's Adj IV | 10 |
| Straddle ratio | yesterday's straddle ÷ today's ATM LTP straddle | 12 |
| Build IV | OTM IV × sqrt(raw DTE / trading DTE) | 7 |
| Opening gap | (underlying at 09:16 − yesterday's future) ÷ yesterday's future × 100 × 0.19 ÷ yesterday's Adj IV | 3 (±0.7501) |
| ADJ band | normalised adjusted-IV change: IV change × 0.1 ÷ its prev-2 base | 3 (−0.007 / +0.005) |

Thresholds:
- IV ratio: 0.6501, 0.8001, 0.9001, 0.9501, 1.0001, 1.0501, 1.1001, 1.2001, 1.3001
- Straddle ratio: 0.9501, 1.0001, 1.0501, 1.1001, 1.1501, 1.2001, 1.3001, **1.4001**, 1.5001, 1.7501, 2.0001
- Build IV: 0.0801, **0.0901, 0.1001**, 0.1201, 0.1601, 0.2001

Five stage tables: 09:16, 09:17, 09:18, 09:19, 09:20 onwards
(`LUT_0916.csv` … `LUT_0920_Onwards.csv`, 7×10×12×7×3×3 = 52,920 rows each).
**First YES wins; one trade per day.**

Filters applied before the table:
- **Straddle veto** — skip when straddle ratio exceeds 1.70 / 1.275 / 1.225 /
  1.175 / 1.15 / 1.125 for DTE 1 / 2 / 3 / 4 / 5 / 6.
- **Build-IV floor** — on DTE 1 and DTE 2 days, skip when Build IV < 0.12.
- **Event days** — no entry before 09:18; size one third.

### 2.3 Execution and hedge
- Sell the ATM straddle (equal CE and PE) at the 30-second average bid.
  **The strike is held all day; no roll.**
- Hedge in the **synthetic future, second by second**:
  - initial position delta is hedged at entry;
  - at a minute start: if points out > points allowed, re-hedge **75 %**;
  - inside the minute: if points out > **1.2 ×** points allowed, hedge fully;
  - DTE 1 / DTE 2 days use |net delta| > 0.25 / 0.20 per unit instead.
- Points allowed = max( min(IV / 57 × U, straddle / 4), 8 bps of U ).
- Futures traded at the 3-second average bid/ask.

### 2.4 Exits (tested once per minute)
| Exit | Rule |
|---|---|
| SL | loss ≥ **16 bps** of entry underlying on expiry day, **14 bps** otherwise |
| TP | profit ≥ TP bps, where TP = low + (round(Build IV, 2) − 0.08) / 0.12 × (high − low), rounded; low/high by DTE: 16/30, 13/25, 13/25, 10/20, 8/15, 8/15, 8/15 |
| TV | 2 × OTM premium at the traded strike < 0.00015 × underlying |
| TV4 | 2 × OTM premium at the current ATM < 0.0006 × underlying |
| IV drop | from 14:57, OTM IV < 0.10 and DTE ≤ 1 |
| EOD | end of the grid |

Exit at the 30-second average ask.

### 2.5 Costs
- Entry: 1.0 / 0.5 / 0.25 normalised Rs for DTE 1 / DTE 2 / other.
- Hedge: 0.4 / 0.3 / 0.2 bps of hedge notional.

---

## 3. Next-week leg (DTE 1 and DTE 2 days only)

- One check. "live" mode: at 09:21 if no CW trade was taken by 09:20.
  "replay" mode: at the first scan minute after 09:20 that passes the veto.
- Features from the **09:16 snapshot** of the next-week chain: NW ratio, NW
  Build IV (weekend-adjusted), gap normalised by next week's yesterday Adj IV
  → `NW_LUT.csv` (2×4×4×3 cells, 23 ON in production).
- Sell at the 30-second average bid from 09:21:00. Stop **15 bps** on
  entry − mid + futures. Re-hedge to neutral when |net delta| > 0.10, once a
  minute. Exit **15:15**. Hedge cost 0.2 bps. Not scaled on event days.

---

## 4. Side output: fixed entry times

For every day it also simulates entering at 09:16, 09:17 and 09:18 **ignoring
the LUT, veto, floor and event-time rules**, and records what production
would have said at that minute: `LUT_ON`, `LUT_OFF`, `VETO`, `FLOOR`,
`EVENT_PRE_0918`. This is the cheapest way to measure what the LUT adds.

---

## 5. Inputs, outputs, how to run

**Inputs** (defaults; each has a `GS_*` environment override)

| Input | Default location | Override |
|---|---|---|
| Tick files | `\\172.16.1.85\Shared\TickDataIV\<DDMMYYYY>\NIFTY.parquet` | `GS_TICK_DIR` |
| LUT folder | `D:\GammaShort\LUTS_r46V2_cwnw_adj007_005_T13_5_2_2_6` | `GS_LUT_DIR` |
| Day inputs | `…\GammaShortDailyProcess\GammaShortDailydata.csv` | `GS_DAILY_CSV`, `GS_DAY_INPUTS` |
| Event dates | `\\172.16.1.85\Shared\EventDates.xlsx` | `GS_EVENT_PATH` |
| Output | `D:\GammaShort\backtest_final_v2` | `GS_OUTPUT_DIR` |

Tick columns read: `Time_dt`/`Time`, `Strike`/`StrikePrice`, `Type`, `LTP`,
`Bid`, `Ask`, `DTE`, `Expiry`, `UnderlyingLTP`, `UnderlyingBid`,
`UnderlyingAsk`. The date comes from the **folder name**, so a file must sit
in `<dir>/<DDMMYYYY>/NIFTY.parquet`.

**Outputs** in `OUTPUT_BASE/run_<config hash>/`: `days/*.pkl` (one per day,
resumable), `daily_log.csv`, `trades.csv`, `entry_time_pnl.csv`,
`backtest_summary.xlsx`, `config.json`. Every uppercase constant and the LUT
files are in the hash, so changing a rule starts a clean result set.

**Run**
```
python GammaShort_Backtest_Final.py                      # run / resume
python GammaShort_Backtest_Final.py --only 2025-06-10    # chosen days
python GammaShort_Backtest_Final.py --summary            # rebuild logs only
```
Needs Python 3 with numpy, pandas, pyarrow, numba, openpyxl. About 300 MB per
NIFTY day; one worker holds a full day in memory.

**Code map**
- Sections 1–2: locations and the rule set (constants).
- Section 3: numba kernels — `compute_iv_array`, `opt_delta_gamma_arr`,
  `hedge_walk`, `futures_pnl`, `apply_exits`.
- Section 4: loaders — LUTs, event dates, day inputs, expiry-filtered reads.
- Section 5: `strike_arrays`, `simulate_cw`.
- Section 6: `nw_day`, `nw_decision`.
- Section 7: `process_day` (per-day worker; `decide()` orders CW and NW).
- Section 8: `summarise`, `main` (process pool, resume).

---

## 6. Things to know before trusting the numbers

Found by reading; none measured.

1. **Back-fill look-ahead.** `strike_arrays` does `ffill().bfill()` on prices
   and on IV. Seconds before a strike's first print take its first *later*
   value. Small for the ATM at 09:16, but it can leak a later price or IV
   into the 09:16 snapshot when the OTM leg has not printed yet.
2. **Fills look forward 30 seconds** (3 for futures) and assume the whole
   size fills at that average. By design, but it is an assumption about
   execution, not a measured fill.
3. **Exits are tested once a minute.** A move that crosses the stop inside a
   minute is exited at the next minute's price.
4. **Event-day entry cost is not scaled.** Gross and hedge notional are
   divided by 3; the entry slippage is charged in full.
5. **Strike step 50 is hard-coded** (NIFTY only).
6. **Bad day-input rows are skipped silently** (`except: continue`), so a
   malformed day shows up only as `no_day_inputs`.
7. **Heavy in-sample search.** The research notes on the share (quoted in the
   pasted transcript in this folder) say about 12 million configurations were
   searched in-sample with margins around 4 %, and recommend a train/test
   split before relying on any candidate.

---

## 7. Backtest vs this trading platform

Compared against `services/execution-gateway/internal/trading/lut.go` and the
monitor code in this repository. **They are not the same rule set.**

| Rule | Backtest | This platform |
|---|---|---|
| ADJ band | −0.007 / +0.005 | −0.008 / +0.004 |
| Straddle-ratio bins | 12 (has the 1.4001 edge) | 11 |
| Build-IV bins | 7 (has 0.0901 and 0.1001) | 5 |
| Table size | 7×10×12×7×3×3 | 7×10×11×5×3×3 |
| LUT files | `LUTS_r46V2_…` | Profilers `LUT_09xx.csv` |
| Weights, gap formula, 5 stages, 09:16–13:30, 09:59 cut-off on expiry day, weekend adjustment, event days start 09:18 | same | same |
| Stop-loss | 16 bps expiry day, 14 bps otherwise | 14 bps every day |
| Target | same ramp, rounded to whole bps | same ramp, not rounded |
| Stop / target check | once a minute | once a minute |
| Straddle veto | yes | not present |
| Build-IV floor (DTE 1–2) | yes | not present |
| TV / TV4 / IV-drop exits | yes | not present in the LUT path |
| Position | equal CE and PE, initial delta hedged in futures | CE and PE sized to be delta-neutral |
| Hedge instrument | synthetic future of the held expiry | synthetic (buy CE + sell PE or reverse) at the live ATM strike |
| Hedge trigger | minute start 75 %, plus intra-minute at 1.2× (fully) | minute end only, rounded down to whole lots |
| Points allowed | max(min(IV/57 × U, straddle/4), 8 bps) | same formula (divisors 57 and 4, 8 bps floor) |
| Wings | none | bought 2 % out of the money |
| Next-week leg | yes | Paper Sim only |
| Fill assumption | 30-second average bid/ask | real orders in small clips, chased |

So a backtest result does not describe what this platform would have done on
the same day. Example, 2026-10-13 (expiry day): the platform's stop was
14 bps (−31.46 points); the backtest would have used 16 bps (about −35.9).
The loss at the 09:18 check was −41.76, so both stop at 09:18.

---

## 8. Research directions (not started)

Listed so the next session does not have to rediscover them. Section 6 items
come first because they decide how much of the reported edge is real.

1. Reproduce the header numbers on the share's data.
2. Remove the back-fill and re-run; record the change.
3. Test exits on the second grid instead of the minute grid.
4. Vary the fill model: 10 / 30 / 60 seconds, plus and minus slippage.
5. Run with this platform's rule set (section 7) to see what the difference costs or earns.
6. Train the tables on early years, test on later years without refitting.
7. Then rule experiments: TV / TV4 / IV-drop thresholds, target shape per DTE,
   asymmetric ADJ bands, wings, re-entry after a stop, sizing, exit time.
