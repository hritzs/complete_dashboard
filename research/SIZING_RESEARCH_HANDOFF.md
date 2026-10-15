# GammaShort sizing / opportunity research: where it stands

Updated 2026-10-15. The sizing studies through v6 and the preregistered
relaxed-entry rerun are complete. The derived-strategy simulator remains a
scaffold (see sections 4.9, 4.10, and 7).

## 1. Goal

Find a **sizing equation** for the GammaShort book: trade size that rises
on high-opportunity days and falls on poor ones, plus a daily
**opportunity tracker** that shows the reading behind that size.

The owner's standing conditions:

- **60 / 20 / 20 chronological split.** TRAIN is the first 60 % of days,
  VALIDATION the next 20 %, TEST the last 20 %. Every number is fitted on
  TRAIN. The one decision is taken on VALIDATION. TEST is reported once
  and never used to fit or choose.
- **Nothing forward-looking.** Every input must be known at the entry
  second, or before the day starts. Bin edges and size scaling come from
  TRAIN only.
- **The decision rule is fixed before looking:** take the candidate with
  the best profit / max-drawdown on VALIDATION, but only if it beats flat
  sizing there on BOTH profit and profit / drawdown. Otherwise stay flat.
- Candidate lists are short and fixed in advance. Shrink, damping and
  limits are not tuned.

## 2. Ground rules for this folder

- **Research only.** Nothing here changes the live trading platform: no
  edits to `services/` or `ui/` because of these results.
- All outputs go in `research/output/`.
- **Never write to `/mnt/shared`.** It is the research team's share; read
  it only.
- Python: `research/.venv/bin/python`, which has pandas, numpy, numba and
  openpyxl. There is no scipy: Spearman correlation is computed via ranks
  (`pd.Series.rank().corr()`).
- `research/` is committed. The last commits are `3f1547c` (backtest and
  first studies) and `b9009cf` (VRP study).

## 3. Data

| What | Path | Notes |
|---|---|---|
| Backtest script | `research/GammaShort_Backtest_Final.py` | Tick backtest of the whole book: CW straddle plus optional NW leg, r46V2 tables. `research/BACKTEST_SUMMARY.md` explains it rule by rule. |
| Backtest run used by every study | `research/output/backtest_final/run_027bf0249b/` | `days/*.pkl` holds one record per day: 1,428 days, 2021-01-01 to 2026-10-12. Also `trades.csv`, `daily_log.csv`, `entry_time_pnl.csv`, `backtest_summary.xlsx`, `config.json`. Log: `output/backtest_final/run_full.log`. |
| Run inputs (read only) | ticks `/mnt/shared/TickDataIV`; tables `/mnt/shared/GammaShortData/GammaShort/LUTS_r46V2_cwnw_adj007_005_T13_5_2_2_6`; events `/mnt/shared/EventDates.xlsx` | Listed in `config.json`. |
| Daily file (realized vol) | `/mnt/shared/Hardik/Project_Codes/GammaShortDailyProcess/GammaShortDailydata.csv` | Columns Date, Future (yesterday's close), Future_Prev2 (the day before, same contract). Override with env `GS_DAILY_CSV`. |
| Pre-entry / post-entry tick readings | `research/output/entry_window_features.csv` | Written by `entry_window_features.py`. |
| History summary of the share's results | `research/output/GammaShort_history_summary.xlsx` | Written by `make_history_summary.py`. |

**PnL unit.** Everything is in backtest units, "normalised Rs": index
points × 25000 / entry underlying, i.e. one trade sized at Rs 25,000 of
notional. To get rupees, multiply by notional / 25000.

**Day record fields used** (`days/*.pkl`): `Date`, `Leg` (`CW` / `NW` /
blank = no trade), `Stage`, `Net_PnL`, `Exit_Reason` (`SL` = stopped out),
`Build_IV`, `NW_Build_IV`, which table said YES, and the table inputs (IV
ratio, straddle ratio, Build IV, DTE, ...).

**Split dates** (identical in v2 and VRP):

| Part | From | To | Days |
|---|---|---|---|
| Train | 2021-01-01 | 2024-06-20 | 856 |
| Validation | 2024-06-21 | 2025-08-13 | 286 |
| Test | 2025-08-14 | 2026-10-12 | 286 |

## 4. Studies done, in order

### 4.1 `sizing_equation.py` (v1, before the 60/20/20 rule)
- **Output:** `output/sizing_equation_report.xlsx`.
- **Split:** fitted on CW trades up to 2023-12-31 (403 trades, average
  14.76), judged on later days. Only two parts, so there was no separate
  validation set.
- **Equation:**
  `size = clip(1 + 0.6 × Σ effect(bin)/average, 0.25, 1.50)`, rescaled so
  the TRAIN average size is 1.
  `effect = (bin average − overall average) × n/(n+40)`.
- **Inputs:** which table said YES, IV ratio, straddle ratio, Build IV and
  DTE. NW trades kept size 1.
- **Results:**

| Variant | Train Net / MDD / Net÷MDD | Test Net / MDD / Net÷MDD |
|---|---|---|
| Flat | 6185 / 144 / 42.8 | 4326 / 126 / **34.2** |
| Table only | 6916 / 146 / 47.4 | 4654 / 158 / 29.4 |
| All five inputs | 7470 / 145 / 51.6 | 4502 / 161 / 27.9 |

- **Reading:** sizing added profit, but worsened drawdown out of sample.
  Test profit / drawdown was below flat for every variant.
- A shuffle check said the sized trades beat 99.8 % of random size
  re-assignments in test. So there is some ranking, just not enough to
  beat flat on risk.
- Drawdown rules (half size after 3 losing days; half size while drawdown
  > 100) did not help.
- This result is why the owner asked for the stricter 60/20/20 method.

### 4.2 `entry_window_features.py`
For each day and each entry minute (09:16 / 09:17 / 09:18 plus the actual
entry minute) it records two things.

**The 60 s before entry** (nothing later):
- `u_move_bps`, `u_range_bps`: the underlying's net move and range.
- `str_range_pct`, `str_chg_pct`: the ATM straddle's range and change.
- `spread_pct`: the CE+PE bid-ask spread.

**After entry**, at +1/+2/+3/+5/+10 minutes:
- `u_bps_k`: the underlying's move.
- `str_pts_k`: the unhedged straddle move.

The "pre-entry stability" inputs and the early-exit rules in v2 use these.

### 4.3 `sizing_equation_v2.py` (60/20/20 method)
- **Output:** `output/sizing_equation_v2_report.xlsx`.
- **Size:** `size = clip(1 + Σ adjustments, 0.25, 1.50)`, scaled to a
  TRAIN average of 1.
- **Adjustment:**
  `adj = 0.6 × (range avg − overall avg) × n/(n+40) / overall avg`, from
  TRAIN only.
- **Candidates** (about 50): Flat; Table; Table + IV ratio; Five table
  inputs; Pre-entry stability; combinations of these.
  - Each also as **[cut only]**: never above 1, only smaller on
    low-conviction days.
  - Each also with a **drawdown overlay**: half size after 3 losing days;
    half size while drawdown > 100; or size = 1 − dd/200 with a floor of
    0.5. Drawdown uses the flat book up to yesterday only.
- **Early-exit rules:** exit at +2/+3/+5 min if the straddle is 50 % or
  75 % of the stop against. These are valued at the unhedged mark + 0.5
  point, which overstates the exit loss.
- **Decision: FLAT.**
  - Best on validation: "Table + IV ratio [cut only]", Net÷MDD 16.71 vs
    flat 16.67. It had lower profit (1894 vs 2106), so it fails the
    both-better rule.
  - Uncapped "Table" had more profit but a much worse drawdown (14.38).
- **Flat, for reference:**

| | Net | MDD | Net÷MDD | Trades |
|---|---|---|---|---|
| Train | 7093 | 144 | 49.1 | 526 |
| Validation | 2106 | 126 | 16.7 | 176 |
| Test | 1312 | 121 | 10.8 | 96 |

- Early exit: the rule picked "+3 min, 50 % of stop" (Validation 2138 vs
  2106 flat). On Test it gave 1262 vs 1312: no gain.
- **Caveat:** the lookup tables were built by the research team on the
  whole history. So "which table said YES" is not out-of-sample in any
  period, and table-based sizing looks better in TRAIN than it can be in
  reality.

### 4.4 `sizing_vrp.py`: volatility risk premium (VRP) as opportunity
- **Output:** `output/sizing_vrp_report.xlsx`. Sheets: Read_me, Split,
  Tiers_by_period, Candidates, Train_tier_sizes, Daily_opportunity. The
  last one is the daily tracker: IV, RV5/10/20 and every measure, per day.
- **Idea:** a short straddle earns when the IV sold is rich against the
  volatility that is then delivered.
- **Inputs:**
  - IV = `Build_IV` (CW) or `NW_Build_IV` (NW).
  - RV = annualised std of log(Future / Future_Prev2) over the last
    5/10/20 rows, up to yesterday's close, same contract (no expiry-roll
    jumps). Broken rows (|r| ≥ 0.2) are dropped.
- **Measures:** VRP10 = IV/RV10, VRP20 = IV/RV20, SPREAD20 = IV − RV20,
  RV_TREND = RV5/RV20.
- **Candidates:**
  - TRAIN-tercile tier sizing on each measure.
  - The same "cut only".
  - Continuous VRP20 size = clip(VRP20 / TRAIN median, 0.5, 1.5).
  - Half size when VRP20 is in the bottom TRAIN tercile.
- **Decision:** "SPREAD20 tiers", by a hair. Test 1313 vs flat 1312;
  Net÷MDD 11.09 vs 10.84. Its tier adjustments are only +0.05 / −0.03 /
  −0.02, so it is effectively flat.
- **Findings:**
  - VRP does **not** rank trades: Spearman is about 0 in every period
    (−0.07 to +0.13).
  - Train-tier averages ran opposite to the idea. Rich IV did worse:
    VRP10 low 16.4 vs high 10.8.
  - The **top VRP tercile stops out more often in every period.** In
    TRAIN, VRP10 high stopped 22.7 % vs low 10.9 %; SPREAD20 high stopped
    30.4 % in validation and 25.0 % in test. Rich IV vs recent RV usually
    means the market is pricing a move, and it often comes.
- **Conclusion:** VRP is not a sizing signal. At best it is a risk flag
  (high VRP means more stops).

### 4.5 `sizing_v3.py`: expected move, stop distance, calendar, VRP flag
- **Asked 2026-10-15:** the owner chose all four drivers. The candidate
  list was fixed in the script header before the first run.
- **Output:** `output/sizing_v3_report.xlsx`.
- **Candidates:**
  - A: volatility targeting, size = clip(TRAIN median / X, 0.5, 1.5), with
    X = entry IV, RV10, RV20, or entry straddle / spot.
  - B1: equal rupee cost per stop, size = 14 / SL_bps.
  - B2: premium cushion, size ∝ entry straddle / stop points.
  - C1–C6: calendar tiers (DTE, event day, weekday, |opening gap|,
    monthly-expiry week, and all five together), each also cut-only.
  - D1–D3: half size in the TRAIN top tercile of VRP10 / VRP20 / SPREAD20.
- **Decision: C1 DTE.** Validation 2132 / 121 / 17.59 vs flat
  2106 / 126 / 16.67. Test 1313 / 120 / 10.96 vs flat 1312 / 121 / 10.84.
  Sizes run only 0.88–1.09; DTE 4 gets the cut (train average 9.6 vs 13.5).
  Shuffle check on TEST: beats only 62 % of random re-assignments, so the
  result is noise and the book is in effect flat.
- **Volatility targeting is backwards for this book:** size vs profit
  Spearman is negative in every period (−0.10 to −0.17 for IV and RV20).
  High-vol days are the better short-straddle days, so sizing them down
  costs profit and does not reduce drawdown (A2/A3 validation MDD 176–181
  vs 126).
- **VRP cut-only flags** lower drawdown but cost more profit than they
  save (Net÷MDD 12.8–16.4 vs 16.7).
- **Stop-distance sizing** (B1/B2) is worse than flat.

### 4.6 `sizing_v4_params.py`: the existing entry parameters, with a real size spread, plus entry rules
- **Asked 2026-10-15.** The candidate list was fixed in the script header.
- **Output:** `output/sizing_v4_params_report.xlsx`.
- **Sizing candidates:** a TRAIN-fitted score from the 8 table inputs.
  - S1: bucket score.
  - S2: ridge regression.
  - Score terciles map to sizes 0.5/1/1.5, cut only, boost only, or skip
    the low tercile.
- **Entry rules** use the fixed-time PnL side output, which matches
  `Net_PnL` exactly:
  - E1: wait one minute.
  - E2: next-minute confirmation.
  - E3–E5: add VETO / FLOOR / all no-trade days at 09:18.
- **Decision: both stay with the current book.**
- **But S1 does rank trades out of sample**, unlike every earlier
  measure. CW average by S1 tercile (low / mid / high):

  | Period | Low | Mid | High |
  |---|---|---|---|
  | Validation | 7.9 | 13.4 | 29.2 |
  | Test | 8.4 | 14.8 | 26.6 |

  - S1 "spread" sizing on validation: profit 2324 vs 2106, but max
    drawdown 174 vs 126, so it fails the rule.
  - Its TEST numbers (1371 / 113 / 12.18 vs 1312 / 121 / 10.84) were seen
    after the decision. They must not be used to switch now.
- **The stage table that said YES is the strongest, steadiest param.**
  09:16 entries average 26.9 / 27.2 / 21.3 (train / validation / test),
  against roughly 9–18 for later stages.
- **Entries: the current rules are the best of those tried.**
  - Waiting a minute loses profit (09:16 is the best minute).
  - The table's NO is worth a lot: LUT_OFF days are negative at every
    minute.
  - Adding VETO or FLOOR days lowers profit ÷ drawdown.
- **Historical proposal, superseded by the owner's later constraint:**
  paper-trade S1 "spread" or 1.5× on 09:16-table trades. Do not follow this
  proposal; the later policy forbids boosting the 09:16 table.

### 4.7 `sizing_v5_capped.py`: base 1, cut on poor days, max 1.15
- **Owner's constraint (2026-10-15):** no rescaling. Size is 1 normally,
  lower on poor days, at most 1.15 on good days. Mind drawdown; don't
  overfit.
- **Final policy from the owner:** keep the size at 1, cut on poor days,
  never exceed 1.15, and never size up the 09:16 table. The 09:16 table
  already has the highest cutoff and should not be boosted.
- **Output:** `output/sizing_v5_capped_report.xlsx`.
- **Candidates:**
  - S1 / S2 score tiers mapped to 0.75/1/1.15 or 0.5/1/1.15 (A–D).
  - E, stage only: 09:16 table 1.15, 09:17/09:18 1, later 0.75. Flagged
    as not clean, because the stage pattern was already seen in v4.
  - Each also with a **drawdown guard**: no 1.15 while the sized book's
    drawdown to yesterday is ≥ 50.
- **Score sizing (A–D) fails.** It is worse than flat on validation. In
  the walk-forward (refit each 1 January on earlier trades) its tier
  ranking flips from year to year: in 2023 the high tier averaged 3.8 and
  the low 10.9. The score is overfit, so drop it.
- **Decision on the original script:** E + drawdown guard. However, the
  owner overruled that result because the 09:16 table already had the
  strongest qualification and should not be sized up.
- **Current owner-approved rule:** base size 1, cut on poor days at most
  1.15, no 09:16 boost, and no rescaling to an average of 1.

### 4.8 `sizing_v6_borderline.py`: cut size on "cut to cut" days
- **Owner's request (2026-10-15):** trade smaller when the trade only
  just qualified.
- **Borderline is read from the tables only**, with no profit fitting:
  - **Margin:** an input within 2 % / 5 % of a threshold, with a NO cell
    across it. This includes the veto and the 0.12 floor.
  - **Isolated:** at most 1 YES neighbour cell.
- **Output:** `output/sizing_v6_borderline_report.xlsx`.
- **Decision: flat.** Every cut is below flat on validation (13.4–16.0
  vs 16.7); single-cut rules lose validation profit / drawdown.
- **Borderline trades are not worse.** Average profit:

  | Period | Borderline (2 % margin) | Firm |
  |---|---|---|
  | Train | 20.1 | 11.1 |
  | Validation | 11.7 | 15.5 |
  | Test | 15.3 | 13.8 |

  - Borderline trades are better in 4 of 6 years.
  - They stop out less often in train and test.
- **Isolated cells are too rare to test:** 15 trades in all.
- **The straddle ratio is the input most often near its edge** (134
  trades), then IV ratio (75).
- **Final call from the owner:** do not boost any stage; keep the book at
  base size 1 and cut only on poor days, with a hard cap of 1.15.

### 4.9 Intraday entry relaxed as the day passes (backtest re-run)
- **Owner's request (2026-10-15):** make the intraday (09:20+) entry a
  little less rigid as the day goes on.
- **How it is run:**
  - `GammaShort_Backtest_Final.py` has a research-only switch
    `GS_RELAX=1`. When it is on, each day also records the first 09:20+
    minute a relaxed rule would take, before the production entry.
    Production results are unchanged; checked identical on sample days.
  - Fields: `Relax_R*_Time`, `_PnL`, `_Exit`, `_Support`.
- **Rule:** accept a NO cell of the 09:20+ table when enough neighbour
  cells (±1 on IV ratio, straddle ratio, Build IV) are YES. The veto,
  floor and expiry cut-off still apply.
  - R1: from 10:30, ≥ 1 YES neighbour.
  - R2: from 11:00, ≥ 1.
  - R3: from 12:00, ≥ 1.
  - R4: 10:30–11:59 needs ≥ 2; from 12:00, ≥ 1.
- **Why counts, not shares:** the first draft used shares of neighbours
  (≥ 2/3, ≥ 1/2). It was replaced because YES cells form thin strips:
  only 29 NO cells have ≥ 1/2 YES neighbours. The change was based on
  trigger counts only, with no PnL seen.
- **Output:** `output/backtest_relax/run_027bf0249b/days/` (1,430 day
  records through 2026-10-14); evaluator `entry_relax_eval.py`; report
  `output/entry_relax_report.xlsx`.
- **Comparison and split:** the frozen production reference has 1,428 days
  through 2026-10-12. The relaxed run has two additional dates (2026-10-13
  and 2026-10-14); the evaluator excludes those relaxed-only dates before
  splitting so the 60/20/20 split matches the reference. Production PnL
  matches the reference exactly on all 1,428 comparable dates. The evaluator
  now fails closed if dates are missing or production PnL differs.
- **Decision: current book (flat).** On VALIDATION the current book was
  2,106.41 / 126.39 / 16.67 (net / MDD / net÷MDD). The best relaxed result
  was R2 at 0.75 size: 2,093.83 / 137.88 / 15.19, so it failed both
  validation gates. Every other R1–R4 size also failed to beat flat on both
  net and net÷MDD.
- **TEST, reported once:** current book 1,311.65 / 120.99 / 10.84
  (net / MDD / net÷MDD; 96 trades). Since validation selected the current
  book, no relaxed candidate was selected and no TEST shuffle check was due.
- The original evaluator split all 1,430 rows, which shifted the split
  against the 1,428-day reference and counted the two unmatched dates in its
  comparison. It has been corrected to align to the reference first and
  assert exact production-PnL equality before evaluation.

### 4.10 Derived-strategy simulator (scaffold only)
- `GammaShort_Backtest_Final.py` now has `GS_DERIVED`, `simulate_legs`,
  and a generic option-leg / futures-hedge simulation helper. This addition
  is incomplete: the helper is not called from `process_day`, no concrete
  candidate definitions or eligibility conditions are wired, and there are
  no derived-strategy results.
- Ideas discussed for a separate study included long-gamma on LUT-NO days,
  an iron fly on a preregistered risk subset, and a late-day short on DTE
  1–2. None is approved by results. Choose and specify one (or a small
  fixed list) before implementation, including entry time, strike/legs,
  hedge cadence/threshold, exits, costs, comparison baseline, and split.
- This is research-only. Do not move the scaffold or conclusions into
  `services/` or `ui/` based on this handoff.

## 5. Overall conclusion so far

The working answer is: keep the book at base size 1 and only cut size on
poor days, with a hard cap of 1.15. Do not rescale to average 1, and do
not boost the 09:16 table. None of the candidate opportunity measures
reliably beats flat sizing out of sample on both profit and profit /
drawdown:

- the table and its inputs;
- pre-entry tick stability;
- drawdown overlays;
- early exit;
- the volatility risk premium;
- volatility targeting, stop distance, calendar / regime and VRP cut-only
  flags (4.5); the DTE tiers technically won but were noise-level;
- score-based sizing and cut-to-cut borderline edits.

The book's edge looks fairly uniform across these readings. The only
consistent pattern is "rich IV vs recent RV leads to more stop-outs", which
is a risk effect, not a profit-ranking one.

The owner-approved operational rule is now:

- base size = 1
- cut only on poor days
- max size = 1.15
- no rescaling to TRAIN average
- no boost of 09:16 table trades
- keep drawdown in view; no overfitting
- if a candidate is not a clear validation winner, keep flat

## 6. How to run

```
cd research
./.venv/bin/python sizing_equation.py            # v1 (train_end=2023-12-31)
./.venv/bin/python entry_window_features.py 16   # needs the tick share; writes the CSV
./.venv/bin/python sizing_equation_v2.py
./.venv/bin/python sizing_vrp.py
./.venv/bin/python sizing_v3.py
./.venv/bin/python sizing_v4_params.py
./.venv/bin/python sizing_v5_capped.py
./.venv/bin/python sizing_v6_borderline.py
# relaxed intraday entry (about 75 min; separate output folder):
GS_RELAX=1 GS_OUTPUT_DIR=$PWD/output/backtest_relax GS_TICK_DIR=/mnt/shared/TickDataIV \
  GS_LUT_DIR=/mnt/shared/GammaShortData/GammaShort/LUTS_r46V2_cwnw_adj007_005_T13_5_2_2_6 \
  GS_EVENT_PATH=/mnt/shared/EventDates.xlsx \
  GS_DAILY_CSV=/mnt/shared/Hardik/Project_Codes/GammaShortDailyProcess/GammaShortDailydata.csv \
  GS_WORKERS=20 ./.venv/bin/python GammaShort_Backtest_Final.py
```

Each sizing script reads the run in `output/backtest_final/run_*/days/*.pkl`
and writes its own report workbook. They run in seconds;
`entry_window_features.py` is the slow one.

## 7. Open point: what to do next

Section 4.9 is complete and leaves the current book unchanged. The following
are separate optional hypotheses, not selected operating rules:

- **First 15 min range vs RV:** only usable for late entries; most entries
  are at 09:16–09:18.
- **Kelly-style sizing** from a TRAIN-fitted win-rate / payoff by bucket,
  heavily shrunk.
- **Sizing up on high-vol days** (the reverse of volatility targeting).
  This hypothesis came from inspecting results and would require a new
  preregistered test. It does not override the owner's size cap or ban on
  boosting the 09:16 table.
- **Derived strategies** (long gamma, iron fly, late-day short) remain
  unimplemented hypotheses; see section 4.10. Do not combine their result
  with the current short-straddle sizing conclusion.

Keep the method:
- inputs known at entry;
- TRAIN-only fitting;
- a fixed candidate list and fixed decision rule written down before
  running;
- TEST reported once;
- a shuffle check for any candidate that wins.
