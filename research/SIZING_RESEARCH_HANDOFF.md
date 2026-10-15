# GammaShort sizing / opportunity research: where it stands

Updated 2026-10-15. The sizing studies through v6 and the preregistered
relaxed-entry rerun are complete. The derived-strategy simulator remains a
scaffold (see sections 4.9, 4.10, and 7). The latest owner request is exact risk
formulas and a persistent Excel iteration ledger in section4.17, extended with
boost/cut algorithm explanations and executable sizing formulas in section4.18.
This includes
the tick-based percentile / relative-value entry research in section4.16.
Section4.15 retains
the higher-tax conviction sizing including 09:16, using annually fitted
past-only overlays and actual mean exposure. Earlier studies
4.11–4.14 retain their historical full-fit normalization and no-09:16-boost
rules; those are superseded for the latest experiment.

## 1. Goal

Find a **sizing equation** for the GammaShort book: trade size that rises
on high-opportunity days and falls on poor ones, plus a daily
**opportunity tracker** that shows the reading behind that size.

The owner's conditions for the original split-based studies (the current
higher-tax overlay in section 4.15 replaces that protocol with prior-year fits):

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

### 4.11 Full-history sizing, without 60/20/20 (owner request)

- The owner deferred the neural-network directional-strategy idea and asked
  to calculate sizing variations for the complete GammaShort history.
- `sizing_full_history.py` fits and evaluates 66 variants on all 1,430 saved
  production days, 2021-01-01 through 2026-10-14. Production PnL matches the
  frozen reference on all 1,428 overlapping dates. This analysis deliberately
  uses no train/validation/test split and is **in sample**. The earlier
  split-based reports remain preserved as separate studies.
- Keeps base multiplier 1, maximum 1.15, no rescaling to average 1, and no
  boosting 09:16 CW entries. Includes the full CW+NW book; scope of each
  sizing family is recorded in the report. Existing event sizing remains
  embedded in the underlying production PnL.
- Families: fixed-bin table/input/stability equations, S1 bucket and S2
  ridge terciles (including cut/skip variants), drawdown guards, IV/RV and
  premium/stop sizing, VRP terciles and risk cuts, calendar factors, LUT
  borderline/isolated-cell cuts, and drawdown/streak controls. The calendar
  combined variant uses DTE/event/weekday/absolute gap without the old
  monthly-expiry proxy. All fitted effects use the entire history.
- Flat: net **10,522.04**, MDD **144.37**, net/MDD **72.88**.
- Best historical ratio: Table + IV ratio, net **9,937.07**, MDD **124.74**,
  net/MDD **79.66**. It sacrifices 584.97 net profit for lower drawdown.
- Highest profit: Calendar Event, net **10,587.37**, MDD **145.50**,
  net/MDD **72.77**. The extra 65.33 profit comes with worse net/MDD.
- **No candidate improves both full-history net and net/MDD over flat.**
  These are historical calculations, not measured unseen-data alpha.
- Outputs: `output/full_history_sizing/full_history_sizing.xlsx`, ranking
  `candidates.csv`, `SUMMARY.md`, and `manifest.json` input hashes. Workbook
  includes yearly/leg attribution, daily sizes/PnL, fitting effects, score
  tiers, and feature coverage. Three CW entries have missing entry-window
  features and receive neutral contributions for those missing inputs.
- Size multiplies saved production PnL and existing costs linearly; there is
  no new execution replay, nonlinear liquidity impact or fee model.

### 4.12 Exact average-size-one optimization (latest owner instruction)

- The owner explicitly requested balancing boosts and cuts so the average
  size across traded days is 1, overriding the previous research instruction
  against average rescaling. Five boosts to 1.15 balance three cuts to 0.75.
- `sizing_normalized_optimization.py`: all 1,430 production days / 799
  recorded entries, including breakeven entries; non-trade days excluded from
  the mean. No split. Size floor 0.75, ceiling 1.15, CW 09:16 ceiling 1.
  All entries remain traded. The total multiplier budget is exactly 799.
- Uses bounded Euclidean projection `size = clip(raw + offset, lower,
  upper)`, with offset solved by bisection. This maintains mean 1 after
  capping; dividing by the mean and then clipping would break equality.
- Evaluates 65 normalized historical templates, flat, and three optimized
  policies. Template projection can raise former cut-only rules and alter
  NW exposure. Source template names are retained for traceability.
- Optimization: seeded multi-start bounded coordinate pattern search on 34
  entry-input features, coefficients bounded [-1,1], coarse-to-fine steps.
  Objectives are profit, profit subject to DD <= flat, and net/DD subject
  to net >= flat. The best feasible point seen for each objective across
  all searches is retained. Global optimality is not claimed.
- Flat: net **10,522.04**, MDD **144.37**, net/MDD **72.88**.
- Best observed profit: net **11,495.56**, MDD **128.76**, net/MDD **89.28**,
  average size **1.000000**. Adds 9.25% net and reduces DD by 10.81%; this
  same policy also supplies the best observed DD-capped-profit result.
- Best observed ratio: net **11,243.25**, MDD **113.89**, net/MDD **98.72**,
  average size **1.000000**. Adds 6.85% net and reduces DD by 21.11%.
- Fifteen candidates beat flat on both full-history net and net/MDD.
  Both fitting and normalization use the full history; this is in-sample
  descriptive optimization and does not establish a causal live policy.
  Predictor values are entry-time inputs, but fitted coefficients, S1/S2
  scores and the normalization offset use historical outcomes/distributions.
- Saved production PnL and costs scale linearly, without quantity-dependent
  replay, market impact or fixed-fee recalculation. Arithmetic mean multiplier
  1 does not guarantee unchanged cash exposure/risk; original event sizing
  is embedded in base PnL. Event-weighted notional proxy is reported.
- Outputs: `output/normalized_sizing/normalized_sizing.xlsx`, ranking and
  coefficient CSVs, `SUMMARY.md`, and input hash manifest. Workbook includes
  daily sizes/PnL, yearly/leg attribution, model coefficients/transforms,
  search trace, equity/DD charts, and equal increase/decrease budgets.
- Five focused tests in `test_sizing_normalized_optimization.py` cover
  projection, exact mean after bounds, early-stage cap feasibility, the
  five-boosts/three-cuts example, invalid inputs, and optimizer hard limits.

### 4.13 DD-first optimization: mandatory cuts on estimated dicey days

- Latest owner instruction: optimize DD while ensuring uncertain/risky days
  do not receive a full position. Risk reduction must be a hard allocation
  rule, not just an aggregate performance side effect.
- The fixed specification `DRAWDOWN_SIZING_SPEC.md` precedes evaluation.
  `sizing_drawdown_risk.py` fits penalized loss-probability and downside models
  on raw entry features, excluding fullfit S1/S2 scores from the risk model.
  Dicey is the top quartile of 0.4×loss-probability percentile +
  0.4×expected-downside percentile + 0.2×pre-entry instability, or missing CW
  pre-entry range/spread readings. There are **201 flagged entries** of 799.
- Pre-entry instability averages underlying range, straddle range and spread
  percentiles. Missing readings and NW's unavailable CW readings are neutral
  0.5; missing CW data additionally forces the risk flag. Flat-book prior DD
  and loss streak are entry-known stress inputs. This is not an own-equity
  live DD halt.
- Size ceiling on all flagged entries is fixed at 0.90, 0.85 or 0.75 for the
  three experiments. Global floor 0.75, max 1.15, no 09:16 CW boost, exact
  whole-book trade mean 1. Normalization cannot lift a dicey position above
  its ceiling. LUTs retain tax vector (13,5,2,2,6) and the same entry set.
- Multi-start bounded coordinate search has three objectives per ceiling:
  minimum DD, maximum net/DD, maximum net. Every qualifying result must
  retain at least flat profit without worsening either DD or worst daily
  loss. All nine new policies qualify on the full history. Prior optima are
  benchmarks only; they do not enforce the new dicey ceilings.
- Lowest observed qualifying DD: ceiling **0.85**, net **10,749.08**, DD
  **105.41**, ratio **101.97**, worst day **-56.16**, versus flat
  **10,522.04 / 144.37 / 72.88 / -61.28**. Net improves **2.16%**, DD falls
  **26.98%**. Dicey mean/max **0.8057/0.85**, other mean **1.0653**, total
  mean **1.000000**. Every flagged day is below full size.
- Profit alternative with the same ceiling: net **11,328.04**, DD **124.86**,
  ratio **90.72**, worst day **-56.25**; net +7.66%, DD -13.51%. Flagged-day
  mean size **0.7883**, never above 0.85; whole-book mean remains 1.
- Full-history fitting, ranking thresholds, optimization and normalization
  are in sample; this does not establish predictive accuracy on future days
  or authorize live changes. Saved PnL and costs scale linearly, with no
  quantity-dependent replay. Event-weighted exposure is reported separately.
- `output/drawdown_risk_sizing/` stores the workbook with daily flags/reasons,
  sizes/PnL, year/leg/risk attribution, risk/sizing coefficients, transforms,
  search trace and equity/DD charts; also CSVs, summary and input hashes.
- Nine focused tests across risk and normalization pass. Independent saved
  artifact checks verify the 201 mandatory cuts, exact mean/budgets, bounds,
  prior-entry stress timing, qualification constraints, DD, year/leg totals,
  and risk/sizing model replay.

### 4.14 Trade comparisons and modest conviction boosts

- Owner requested trade comparisons and modest size boosts on strong estimated
  edge. `CONVICTION_SIZING_SPEC.md` fixed the gates/profiles before evaluating
  the new candidates. `sizing_conviction_comparison.py` reuses the frozen risk
  model/flags and 38 inputs from 4.13 rather than refitting those estimates.
- Strong estimate: CW S1 >=its upper-tercile threshold, S2 >=CW median, loss
  probability <=book median, and no dicey flag.166 entries qualify for this
  label;134 occur at 09:16 and cannot be boosted.32 are boost eligible. NW
  does not have the equivalent two-score confirmation and is not labeled
  high conviction. Risk always overrides estimated edge.
- Gentle profile: strong 1.05–1.10, ordinary <= 1.05. Maximum total-size budget
 792.35 cannot reach the required 799 while respecting 201 dicey caps and
  the 09:16 constraint. Reported infeasible; constraints were not relaxed.
- Modest-priority profile: strong 1.10–1.15, ordinary <= 1.10, dicey 0.75–0.85,
  09:16 <= 1, floor 0.75, exact book mean 1. Strong eligible entries are never
  sized below ordinary entries. Bounded normalization preserves both strong
  lower bounds and risk upper bounds. Three search objectives qualify against
  flat profit/DD/worst-day constraints.
- Selected by lowest qualifying DD, then higher PnL: new net **10,938.10**,
  DD **108.63**, ratio **100.69**, worst day**-56.25**. Previous DD-first was
  **10,749.08 /105.41 /101.97 /-56.16**. The new policy gains **189.02** net
  but has **3.22** more DD. Relative to flat it gains 416.07 net and retains
  lower DD/worst-day loss. It is not uniformly better than the prior policy.
- All 799 entries are preserved: **251 cuts,142 unchanged,406 boosts**. Only
  **32** boosts carry the strong-conviction label; the other 374 are ordinary
  budget redistribution, capped 1.10. Average strong eligible size **1.1406**,
  dicey size **0.8250**, overall size **1.000000**. All 201 dicey entries remain
  capped 0.85.134 strong 09:16 entries retain their no-boost protection.
- Chronological examples are not selected on PnL:2021-01-05 CW 09:17 is a
  strong-estimate boost 1.15 that loses-12.29 vs-10.68 flat;2021-02-03 CW 09:18
  boosts 1.15 and earns 62.23 vs 54.11 flat.2021-01-06 CW 09:17 is dicey at 0.85,
  losing-38.51 vs-45.31 flat. Scores do not guarantee a winning day.
- `output/conviction_sizing/` has all 799 rows in`trade_comparison.csv`, a
  detailed workbook with flat/previous/new sizes and PnL, strike/expiry and
  entry/exit metadata, risk/conviction reasons, action/group/year/leg
  attribution, coefficients, feasibility, charts, chronological examples,
  summary and hashes. Twelve focused tests and independent artifact replay
  checks pass. Full-history fitting/normalization remains in sample, with
  the same linear cost/PnL scaling limitations and no live changes.

### 4.15 Higher-tax conviction, including 09:16 (latest owner override)

- The owner explicitly permits a modest boost on a strong 09:16 entry,
  overriding the prior blanket ban, while keeping the original tax 13.
  Preserve the complete base vector **(13,5,2,2,6)** and legacy entry LUTs.
- `higher_tax_sizing.py` builds a separate monotone min-cut conviction layer
  at tax increments **+2/+4**: vectors (15,7,4,4,8) and (17,9,6,6,10).
  A base-approved cell must additionally survive the corresponding stronger
  tax. Boost candidates are **1.05/1.10**, including at 09:16. Dicey flags
  always override conviction, setting size **0.85**. NW has no CW-tax boost.
- `HIGH_TAX_CONVICTION_SPEC.md` fixes the experiment before evaluation. Each
  year's high-tax masks, risk models, imputations/scales, empirical rank
  distributions and risk thresholds fit on prior years only. Outcome-fitted
  S1/S2 scores and future-period normalization are excluded. 2021's 182
  entries are warmup; 617 entries from 2022 onward receive past-only scores.
- Counterfactual source: read-only round48 no-roll ps1 cache, 1,373 days
  through 2026-08-19. Production bucket axes, veto, entry cutoffs, event
  weights and modeled entry/hedge costs are retained. 2026 masks fit only
  through 2025-12-31. Existing base LUTs predate this refit and remain fixed.
- `lut_tax_closure.py` solves the same maximum-weight monotone closure as the
  original generator, using observed-node compression and iterative Dinic
  flow. It preserves cents rounding, minimum anchor evidence 1 and stage
  cascading. Exact objectives match brute-force tiny graph enumeration;
  tests also cover tax nesting and fractional event-only anchor exclusion.
- On scored 2022+ entries, flat net/DD is **7,593.21/144.37**. +2 with boost
  1.05 gives **7,409.32/144.15**, actual mean **0.9743**. +2 with boost 1.10
  gives **7,519.15/149.90**, mean **0.9912**. +4 with 1.05 gives
  **7,395.19/144.15**, mean **0.9718**; +4 with 1.10 gives
  **7,490.89/149.90**, mean **0.9861**. **None improves flat profit and
  profit/DD together.** Do not adopt based on the earlier full-fit gains.
- 09:16 boosts now occur: **66** under +2 and **61** under +4. Example:
  2022-01-13 passes base tax 13 plus the past-fitted tax-15 gate, is not
  dicey, and receives 1.10 in that candidate. Stronger hurdle is a research
  definition, not proof of reliable future edge.
- This is a **conditional sizing overlay**, not a causal end-to-end strategy
  result: the fixed base LUTs used wider history, and original cached feature
  timestamps/fill assumptions have not been certified. The forward overlay
  avoids its own future fitting but cannot undo base-entry selection bias.
- Exact mean 1 over unknown future trades is not asserted. These candidates
  report their actual means and include an ex-post constant-size comparison
  at the same realized mean; that diagnostic is not a live sizing decision.
  Earlier normalized research reports remain archived, not live policies.
- Outputs in `output/higher_tax_sizing/`: comparisons for all 799 entries,
  workbook with full-history and scored-period statistics, stage/year
  attribution, training cutoffs, five prior-only risk model JSON files, ten
  higher-tax mask NPZs, summary and source hashes. Seven new tests plus
  independent model/mask/cutoff/PnL/DD/mean/hash replay checks pass. No live
  or shared-input edits; saved net PnL/costs still scale linearly.

### 4.16 Tick-based percentile and relative-value entry study

- The owner asks whether existing tick data can improve entries using statistical
  arbitrage or percentiles. `TICK_ENTRY_SPEC.md` fixes eight hypotheses before
  performance scoring. `tick_entry_features.py` reads only the share, writes
  local research outputs, and uses strictly pre-decision timestamps. Features
  select ATM from the last known underlying, retain that strike throughout the
  lookback, use backward as-of snapshots with age<=3s, and never backfill.
- Signals:60-second sellable-premium midrank percentile (constant=0.5), local
  straddle residual against underlying/quadratic/time fitted outside the final
  five seconds, spread%, underlying range, premium change, and neighboring
  call/put butterfly quote credits. The residual is not proven mean reversion;
  butterfly quotes are not completed arbitrage trades.
- All720 CW dates extracted;716 have usable features. Unavailable dates:
  2021-01-21 and2025-04-07 (missing ATM legs),2024-09-16 and2026-06-02
  (stale/invalid current quotes). Features at17 valid entries select a different
  past-only ATM from the saved fill strike. Missing feature rules keep the
  original trade. Scored2022+ contains617 original entries (550CW,67NW),
  with547 usable CW features. 2021 is warmup. Quantile90 thresholds fit only
  earlier completed years, same-stage/DTE if n>=30, otherwise pooled CW n>=60.
- Hold original taxes **(13,5,2,2,6)**, event weights, saved costs/exits and NW
  entries fixed, with multiplier1. Entry rejection skips CW for the day, without
  simulating later re-scans or NW fallback. Delaycheap defers adverse09:16/17
  signals one minute only when the saved next-minute LUT gate passes; otherwise
  skips. No future whole-period normalization or sizing boosts are introduced.
- Scored2022+ flat net/MDD/net-per-DD: **7593.21/144.37/52.60**. P20 gives
  **5992.09/173.71/34.50**, P10 **6560.90/153.02/42.88**. Rcheap, Rrich,
  Lspread, Mstress and Combined also fail the fixed qualification (net and
  net/DD above flat, worst day no worse). **None is adopted.**
- Delaycheap gives **7362.48/139.86/52.64**, worst day **-55.77** vs flat
  **-61.28**;128 delays and4 skips. Net falls3.04%, DD falls3.12%: a risk/profit
  tradeoff, not improved total profit. Its net random-trigger tail fraction is
  **.047**, or **.376** with eight-rule correction. The1,000 allocations per
  candidate preserve trigger counts by calendar-year/original-stage/DTE and
  feature availability. This remains exploratory and does not cover all past
  experiments or repair the legacy LUT selection bias.
- The scored observations include two positive call and two positive put
  butterfly quote credits. Quantity/age/skew checks are only preliminary;
  contract lots, costs, simultaneous execution and all-leg fill probabilities
  remain unresolved. No continuously scanned statistical-arbitrage strategy,
  cross-expiry hedge or delta-neutral residual trading was backtested here.
- Artifacts under `output/tick_entry_study/`: report, workbook with equity/DD
  curves, all799-trade and1430-day comparisons, full/scored statistics,
  coverage/matched-coverage, stage/year attribution, past-only cutoffs, placebo
  checks, apparent butterfly candidates, feature/source hashes and audit JSON.
  Eleven tests pass; `tick_entry_audit.py` independently verifies raw PnL,
  sequential DD, gates,312 recorded thresholds, NW/warmup invariance and hashes.
- This is **conditional entry-overlay research**, not certified end-to-end
  causal performance. Legacy LUTs used broader history, saved fills remain
  uncertified, and stored tick timestamps are assumed to reflect availability.
  No shared files, live services, UI, credentials or live positions changed.

### 4.17 Risk formulas and persistent iteration-by-iteration Excel ledger

- The owner requests exact formulas behind loss probability, expected downside,
  instability and the40/40/20 composite, plus in-depth Excel reporting for each
  iteration. `RISK_FORMULAS.md` documents the actual code, not a generic model.
- Loss probability uses a logistic model with an unpenalized intercept and L2
  lambda20. Downside is ridge lambda50 predicting max(-net_PnL,0), including
  zero for winners, clipped nonnegative. It is not conditional loss size or VaR.
  Instability averages CW percentile ranks of underlying range, LTP-straddle
  range and bid/ask spread; missing/NW components receive0.5. A missing CW
  component also independently forces dicey. Risk weights0.4/0.4/0.2 are fixed.
- The full-history threshold is0.6677157174036119, flagging201/799 entries.
  Its scalers, regressions, percentile references, threshold and mean-one size
  balancing are in sample. Annual high-tax risk models use previous years only,
  with2021 warmup; legacy base LUT and entry-second feature caveats remain.
- `research_iterations.json` registers14 iterations. `build_research_ledger.py`
  consolidates189 saved daily policy paths, without refitting strategies or
  editing any original workbook. The master workbook is
  `output/research_ledger/GammaShort_Research_Ledger.xlsx`. Every iteration has
  separate inputs/policy-map/trade/daily/summary/quarter/year/month/FY/group/
  source-table sheets. Costs/base metadata, per-policy size/net/delta/action/
  entry/available exit, execution counts and equity/DD are retained.
- Risk sheets contain coefficients, transformed inputs, raw instability inputs,
  component ranks, targets/outcomes, flags and exact saved-value replays for all
  799 original and617 scored annual entries. Annual model/scaler/reference
  samples and cutoffs are separate. Executable Excel formulas and equity/DD
  example charts are included; standalone Risk_Formulas files support actual
  spreadsheet-engine verification. Static replay values do not require Excel.
- Original raw readings sometimes differ below Excel's15-digit precision.
  Dense-order integer keys and17-digit raw text preserve exact archived ranking.
  Excel uses direct numeric SUMPRODUCT comparisons, not rounded text COUNTIF
  criteria. Regenerate frozen order keys when changing raw inputs.
- Legacy exports sometimes saved only selected policies or rounded sizes/PnL.
  Their limitations and source date ranges are explicit; unsaved candidate
  histories are not invented. Modern saved candidate paths are all included.
  Period DD resets at each month/quarter/year/FY start; Daily DD retains the
  global peak. PnL is normalized backtest units, not account returns. Modified
  entry metadata remains separate from original Base_ metadata; unavailable
  counterfactual strikes are not fabricated.
- **Future convention:** preserve the source artifact, register each new
  iteration with protocol/precision/coverage, rebuild and verify this ledger
  before considering its reporting complete. Commands and navigation are in
  the ledger README. This is a reporting addition, not new alpha or adoption.
- Verification passes for173 sheets,189 daily policy paths and20,223 period
  rows. The actual spreadsheet engine reproduces799 risk scores within6.7e-16
  and downside predictions within5.2e-14; every dicey flag agrees. Independent
  checks reproduce daily equity/DD, trade totals/execution counts, every
  monthly/quarterly/yearly/FY net and reset DD, inputs/coefficients and hashes.
  Both original799 and annual617 Python risk replays also pass. Evidence is
  stored in `verification.json`, `audit.json` and the ledger manifest.

### 4.18 Boost/cut algorithms, priorities and executable Excel sizing

- `SIZING_ALGORITHMS.md` explains21 algorithm families and the distinction
  between prediction, gating, allocation, constraints and entry changes.
  `sizing_algorithm_report.py` adds12 Algo_ sheets to the master ledger
  (185 total), plus standalone explained/calculated Excel workbooks.
- Latest annual research priority: warmup1; otherwise dicey0.85; otherwise
  higher-tax CW conviction1.05/1.10; otherwise1. Risk wins over conviction.
  Original taxes13/5/2/2/6 remain intact. 09:16 must pass original13 plus15/17
  to boost; NW cannot receive CW-tax boosts but can receive risk cuts.
- Higher-tax LUTs solve a monotone maximum-weight closure by minimum cut,
  implemented with iterative Dinic flow. Weights are cents-rounded
  total_post_cost_PnL - weighted_count*tax. Minimum anchor evidence and stage
  cascade remain unchanged. This is a cell-membership gate, not probability.
- Earlier historical priority uses shrunk-bucket S1 and ridge S2 profit scores,
  dicey flags and low estimated loss risk. Strong eligible entries get priority
  bounds; coordinate pattern search finds allocation coefficients; monotone
  bounded projection enforces caps and exact historical mean1. Ordinary boosts
  may simply fund mandatory cuts and are not strong-edge labels. Archived
  09:16 boost bans remain unchanged; later annual permission is separate.
- Reports include per-policy family mapping, version changes, branch priority,
 799 historical conviction gates,3,196 annual size decisions, and11,985 trade
  allocations across15 optimized policies. Coefficients, common projection
  offsets, lower/upper bounds, clipping status and reasons are explicit.
- Spreadsheet-engine verification reproduces annual sizes exactly and final
  optimized sizes within1e-14. All189 daily policy paths,20,223 period rows
  and799 risk formulas remain verified. No refits, new optimization search,
  strategy adoption, shared writes or live changes were performed.

## 5. Overall conclusion from the earlier studies

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

The owner-approved rule at that earlier stage was:

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
# tick entry study (shared tick inputs are read-only):
./.venv/bin/python tick_entry_features.py --workers 8
./.venv/bin/python tick_entry_eval.py
./.venv/bin/python tick_entry_audit.py
./.venv/bin/python -m unittest discover -p 'test_tick_entry*.py'
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

Sections 4.9 and 4.11–4.16 are complete. The latest tick-entry comparison finds
no rule improving both total net and net/DD; keep the current entry rule.
The one-minute delay's small DD gain comes with lower profit and weak corrected
placebo evidence. Higher-tax sizing including09:16 also failed flat qualification.
Full end-to-end causal verification remains open;
neural
directional research is deferred. The following are separate optional hypotheses, not
selected operating rules:

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
