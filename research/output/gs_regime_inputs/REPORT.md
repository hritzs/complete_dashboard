# GS regime-complement input audit

Four sampled morning prefixes; feasibility audit only. No strategy trades or PnL tested.
The2021-03-25 stress case was chosen after seeing worst GS PnL and is not an unbiased prediction test.
Median OI value-change gaps are60–181seconds across these samples; OI is a slower confirmation candidate, not a second-by-second trigger.
Volume decreases within contracts. Its field semantics must be verified before any cumulative-volume differencing.
Existing latest-GS hedges use net-delta or delta/gamma-distance triggers, not an option-chain regime detector.
The candidate combines completed-bar direction, realized variance and fixed-contract IV breadth. OI remains optional.
See research/GS_REGIME_COMPLEMENT_SPEC.md and the Research_spec Excel sheet for definitions, provisional recipe and required execution study.

      Date                          Selection          Window     Expiry  Contracts  ATM_reference_U_before_0920  Median_OI_change_gap_seconds  Median_OI_unchanged_fraction  OI_change_events  Volume_decrease_events  Fractional_second_rows  Tick_size       Tick_mtime_ns
2021-01-04 Previously fixed data-audit sample 09:15–10:30 IST 2021-01-07         18                     14098.65                         181.0                      0.993866               447                   29047                       0  216480965 1769697776507707800
2024-02-29 Previously fixed data-audit sample 09:15–10:30 IST 2024-02-29         18                     21925.40                         180.0                      0.992943               458                   33367                       0  306460636 1766621725905414500
2026-10-13 Previously fixed data-audit sample 09:15–10:30 IST 2026-10-13         18                     22446.65                          60.0                      0.972325              1350                   25196                       0  414305188 1791921647261867300
2021-03-25 Ex-post worst-GS stress diagnostic 09:15–10:30 IST 2021-03-25         18                     14484.10                         181.0                      0.994234               449                   36615                       0  239105069 1766610084962183800
