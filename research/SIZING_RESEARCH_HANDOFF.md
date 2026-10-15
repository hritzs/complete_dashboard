# GammaShort sizing / opportunity research: where it stands

Written 2026-10-15. This research is now closed out to the final owner-approved policy. The work is complete; no further sizing change is recommended unless a new pre-registered candidate is explicitly approved.

## 1. Goal

Find a sizing equation for the GammaShort book that raises size on strong opportunity days and cuts it on poor ones, while keeping a daily opportunity readout that explains the sizing decision.

The operating conditions were strict:

- 60 / 20 / 20 chronological split: TRAIN first, VALIDATION next, TEST last.
- No forward-looking inputs. Anything used must be known before entry or by the day start.
- Candidate lists are fixed before running. No tuning after seeing validation.
- Decision rule is fixed before looking: a candidate only wins if it beats flat on both profit and profit / drawdown in VALIDATION; otherwise stay flat.

## 2. Ground rules for the folder

- Research only. Nothing here changes the live platform. No edits to `services/` or `ui/` based on these results.
- All outputs live in `research/output/`.
- Never write into `/mnt/shared`; read only.
- Use `research/.venv/bin/python` with pandas, numpy, numba and openpyxl.
- No final decision is made from TEST. TEST is reported once only.

## 3. Final proven outcome

The owner-approved operating rule is:

- Base size = 1.
- Cut size only on poor days.
- Hard cap = 1.15.
- No rescaling to the TRAIN average.
- No 09:16 size boost.
- Do not expand any stage beyond its current cutoff.
- If a candidate does not clearly beat flat on both profit and profit / drawdown on validation, keep the book flat.

This is the final rule that survived the full research chain: table-driven sizing, score-based sizing, multivariate sizing, calendar and regime tests, volatility targeting, stop-distance sizing, VRP, drawdown overlays, early-exit overlays, borderline-day cuts, and the relaxed 09:20+ entry study.

## 4. What was actually tested

### 4.1 Early table and sizing studies

The first sizing attempts raised profit but worsened risk. In the early v1 and v2 runs, a sized book could beat flat in profit but not in profit / drawdown; the owner therefore tightened the rule to the 60 / 20 / 20 validation gate.

### 4.2 VRP and realized-vol studies

VRP and realized-vol metrics were tested as daily opportunity reads and sizing inputs. The consistent finding was that richer IV versus recent RV often coincided with more stop-outs, not a better trade ranking. The VRP measures did not produce a robust out-of-sample winner.

### 4.3 Score-based sizing and table-parameter scoring

The table itself gave the strongest structure, but the score-based size spreads did not survive validation. In the fixed rules, the score winners lifted profit but broke the drawdown constraint or looked like overfit patterns rather than a stable edge. The proof requirement was not met.

### 4.4 Capped rule and final owner override

The capped design was the first version that aligned with the owner’s practical constraints:

- 1 base size,
- cut on poor days,
- max 1.15,
- no rescaling,
- no 09:16 boost.

This rule was then explicitly confirmed by the owner. Higher stage size on 09:16 was rejected because the 09:16 table already has the strongest qualification and should not be oversized.

### 4.5 Borderline-day tests

The borderline-day study asked whether trades that were just barely qualified should be reduced. They were tested using table-edge margins and isolation checks. The result was flat: every cut variant fell below flat in validation, and the borderline trades were not meaningfully worse on a risk-adjusted basis.

### 4.6 Relaxed intraday entry study

A separate relaxed 09:20+ entry study was run under `GS_RELAX=1` in the backtest. The idea was to allow a later entry when enough neighboring table cells were still YES after the rigid 09:16 / 09:17 / 09:18 structure. This was a research-only branch, not a live change. It was not used to override the final capped policy.

## 5. Final research conclusion

No opportunity measure tried in this project reliably beat the flat book out of sample on both profit and profit / drawdown under the fixed validation rule.

The practical conclusion is simple:

- Do not rescale the book to a train average.
- Keep the book at base size 1.
- Cut only on poor days.
- Keep the cap at 1.15.
- Do not boost 09:16 table trades.
- Keep drawdown visible, but do not overfit to a score that is not robust.

The book's edge appears fairly uniform across the tested readings. The strongest consistent pattern is that rich IV versus recent realized volatility tends to produce more stop-outs, which is a risk effect rather than a profit-ranking effect.

## 6. Research artifacts

The final state of the work is captured in:

- `research/SIZING_RESEARCH_HANDOFF.md` — this note
- `research/TODO.md` — continuation checklist and current decision trail
- `research/output/RESEARCH_SUMMARY.md` — compact summary of the final policy

## 7. Final status

This is a research completion point, not an open hypothesis list.

The remaining rule is: unless there is a new, pre-registered candidate with a clean validation win that beats flat on both profit and profit / drawdown, stay with the flat base book and cut only on clearly poor days.

No platform changes are required or recommended from this research.

## 8. How to rerun the research

```bash
cd research
./.venv/bin/python sizing_equation.py
./.venv/bin/python entry_window_features.py 16
./.venv/bin/python sizing_equation_v2.py
./.venv/bin/python sizing_vrp.py
./.venv/bin/python sizing_v3.py
./.venv/bin/python sizing_v4_params.py
./.venv/bin/python sizing_v5_capped.py
./.venv/bin/python sizing_v6_borderline.py

GS_RELAX=1 GS_OUTPUT_DIR=$PWD/output/backtest_relax GS_TICK_DIR=/mnt/shared/TickDataIV \
  GS_LUT_DIR=/mnt/shared/GammaShortData/GammaShort/LUTS_r46V2_cwnw_adj007_005_T13_5_2_2_6 \
  GS_EVENT_PATH=/mnt/shared/EventDates.xlsx \
  GS_DAILY_CSV=/mnt/shared/Hardik/Project_Codes/GammaShortDailyProcess/GammaShortDailydata.csv \
  GS_WORKERS=20 ./.venv/bin/python GammaShort_Backtest_Final.py
```

These are still research-only commands. The live platform remains unchanged.

