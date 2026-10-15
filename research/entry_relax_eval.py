"""
Evaluate the relaxed intraday entry (GS_RELAX=1 backtest run, see the backtest's
RELAX_VARIANTS): from a start time on, a 09:20+ NO cell is accepted when enough of
its neighbour cells are YES. A relaxed trade replaces the day's later production
entry, or fills a no-trade day.

CANDIDATES (fixed before the run finished):
  R1 from 10:30, >= 1 YES neighbour     R2 from 11:00, >= 1
  R3 from 12:00, >= 1                   R4 10:30-11:59 >= 2, from 12:00 >= 1
  each with the relaxed trades at size 1 and at size 0.75 (base 1, cut only).
Decision rule as before: best profit / max drawdown on VALIDATION, only if it beats
the current book there on BOTH profit and profit / drawdown; otherwise keep it.

    ./.venv/bin/python entry_relax_eval.py
Writes research/output/entry_relax_report.xlsx
"""
import glob
import os
import pickle

import numpy as np
import pandas as pd

from sizing_v5_capped import dd_stats
from sizing_vrp import HERE, stats

VARIANTS = ["R1", "R2", "R3", "R4"]


def main():
    files = sorted(glob.glob(os.path.join(HERE, "output", "backtest_relax", "run_*", "days", "*.pkl")))
    D = pd.DataFrame([pickle.load(open(f, "rb")) for f in files]).sort_values("Date").reset_index(drop=True)
    D["Net_PnL"] = pd.to_numeric(D["Net_PnL"], errors="coerce").fillna(0.0)
    D["Leg"] = D["Leg"].fillna("")
    n = len(D)
    part = np.where(np.arange(n) < int(0.6 * n), "Train", np.where(np.arange(n) < int(0.8 * n), "Validation", "Test"))
    base = D["Net_PnL"].values

    # Same days and same production PnL as the run every other study uses?
    ref = sorted(glob.glob(os.path.join(HERE, "output", "backtest_final", "run_*", "days", "*.pkl")))
    R0 = pd.DataFrame([pickle.load(open(f, "rb")) for f in ref])[["Date", "Net_PnL"]].fillna(0.0)
    chk = D[["Date", "Net_PnL"]].merge(R0, on="Date", how="outer", suffixes=("", "_ref"))
    print(f"days {n}; production PnL equal to backtest_final on {np.isclose(chk.Net_PnL, chk.Net_PnL_ref).mean() * 100:.2f} % of days")

    series, prof = {"Current book": base.copy()}, []
    for v in VARIANTS:
        col = f"Relax_{v}_PnL"
        if col not in D:
            continue
        p = pd.to_numeric(D[col], errors="coerce").values
        hit = np.isfinite(p)
        for size in (1.0, 0.75):
            e = base.copy()
            e[hit] = size * p[hit]
            series[f"{v} relaxed trades x{size:g}"] = e
        for per in ("Train", "Validation", "Test"):
            m = hit & (part == per)
            repl = m & (D["Leg"].values == "CW")
            prof.append(dict(Variant=v, Period=per, Relaxed_trades=int(m.sum()), On_no_trade_days=int((m & ~repl).sum()),
                             Replacing_later_entry=int(repl.sum()), Relaxed_avg=p[m].mean() if m.any() else np.nan,
                             Replaced_avg=base[repl].mean() if repl.any() else np.nan,
                             Relaxed_stop_pct=100 * D.loc[m, f"Relax_{v}_Exit"].astype(str).str.startswith("SL").mean() if m.any() else np.nan))
    rows = []
    for name, e in series.items():
        for per in ("Train", "Validation", "Test"):
            r = dd_stats(e[part == per]); r.update(Rule=name, Period=per); rows.append(r)
    R = pd.DataFrame(rows)[["Rule", "Period", "Net", "MDD", "Net_per_MDD", "Worst_day", "Worst_5day", "Longest_DD_days", "Trades", "Per_trade"]]
    V = R[R.Period == "Validation"].set_index("Rule")
    f = V.loc["Current book"]
    ok = V[(V["Net"] > f["Net"]) & (V["Net_per_MDD"] > f["Net_per_MDD"])]
    chosen = ok["Net_per_MDD"].idxmax() if len(ok) else "Current book"
    P = pd.DataFrame(prof)

    pd.set_option("display.width", 250)
    print("\nRelaxed trades by period:"); print(P.round(2).to_string(index=False))
    print("\nValidation by profit / drawdown:")
    print(R[R.Period == "Validation"].sort_values("Net_per_MDD", ascending=False).round(2).to_string(index=False))
    print(f"\nDECISION: {chosen}")
    print(R[R.Rule.isin(["Current book", chosen])].round(2).to_string(index=False))
    if chosen != "Current book":
        tm = part == "Test"
        changed = np.flatnonzero(tm & (series[chosen] != base))
        delta = series[chosen][changed] - base[changed]
        pool = np.flatnonzero(tm)
        b0 = stats(series[chosen][tm])["Net_per_MDD"]
        rng = np.random.default_rng(7)
        cnt = 0
        for _ in range(2000):
            e = base.copy(); e[rng.choice(pool, len(changed), replace=False)] += rng.permutation(delta)
            cnt += stats(e[tm])["Net_per_MDD"] < b0
        print(f"shuffle check on TEST: beats {cnt / 20:.1f} % of the same changes on random days")

    out = os.path.join(HERE, "output", "entry_relax_report.xlsx")
    with pd.ExcelWriter(out) as w:
        pd.DataFrame({"Read me": [
            "Relaxed intraday entry: from a start time on, a 09:20+ NO cell is accepted when enough neighbour cells are YES (entry_relax_eval.py header).",
            "Decision: best profit / max drawdown on VALIDATION, only if it beats the current book there on profit AND profit / drawdown.",
            f"Decision: {chosen}.",
        ]}).to_excel(w, sheet_name="Read_me", index=False)
        R.round(3).to_excel(w, sheet_name="Rules", index=False)
        P.round(3).to_excel(w, sheet_name="Relaxed_trades", index=False)
        cols = ["Date", "Leg", "Entry_Time", "Net_PnL"] + [c for c in D.columns if c.startswith("Relax_")]
        D[cols].to_excel(w, sheet_name="Daily", index=False)
    print(f"\nwrote {out}")


if __name__ == "__main__":
    main()
