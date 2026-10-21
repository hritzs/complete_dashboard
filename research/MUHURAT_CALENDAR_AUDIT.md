# Diwali / Muhurat session annotation — 2026-10-21

The five source dates previously described as having no morning observations
are scheduled NSE F&O Diwali/Muhurat trading sessions. They are not evidence
of a missing normal morning session. The verified dates, official market-open
and market-close times in Asia/Kolkata, announcement dates and direct NSE
circular links are saved in `muhurat_sessions.csv`.

| Date | Scheduled trading session (IST) | NSE F&O circular |
|---|---|---|
| 2021-11-04 | 18:15–19:15 | [50049](https://archives.nseindia.com/content/circulars/FAOP50049.pdf) |
| 2022-10-24 | 18:15–19:15 | [54024](https://archives.nseindia.com/content/circulars/FAOP54024.pdf) |
| 2023-11-12 | 18:15–19:15 | [59126](https://nsearchives.nseindia.com/content/circulars/FAOP59126.pdf) |
| 2024-11-01 | 18:00–19:00 | [64630](https://nsearchives.nseindia.com/content/circulars/FAOP64630.pdf) |
| 2025-10-21 | 13:45–14:45 | [70320](https://nsearchives.nseindia.com/content/circulars/FAOP70320.pdf) |

These published schedules were available before each session. The new
complement's frozen opening range remains 09:15–09:30, with decisions
09:31–10:30. None overlaps these special sessions. All five dates remain
in the original 1430-date calendar, with no complement trade and zero
complement PnL. They are explicitly classified as
`Scheduled_session_outside_policy_window`. This is a calendar admission rule,
not a prediction or an outcome-dependent exclusion.

No evening-to-morning timestamp shift or special-session trade is introduced.
A Muhurat-specific strategy would require a separate specification for its
opening range, liquidity, execution, expiry and exit deadlines. Existing source
records extending 1–10 seconds past scheduled close remain observed data;
they do not extend the exchange's official executable trading window.

The other six ineligible dates remain separate data-quality problems: five
entire-file null underlying columns and one insufficient opening-coverage date.
No claim is made that all Muhurat data is complete or that this reference is a
full historical exchange calendar. Unexplained missing mornings require
investigation; missing exits and unexpected execution failures still block
reporting.

`annotate_complement_calendar.py` preserves the original eligibility CSV/XLSX
under `output/complement_strategy/calendar_provenance/`, checks the existing
raw-error/checkpoint evidence and adds classification only to the reporting
audit. All 1430 replay checkpoint SHA256 values are recorded unchanged in
`calendar_annotation_manifest.json`; frozen strategy code, specification,
raw exceptions and trading results are untouched. The report exports explicit
Diwali reasons and includes `Eligibility_audit` and `Muhurat_calendar` sheets.
