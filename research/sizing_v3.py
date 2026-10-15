"""
Sizing study v3: size varying on EXPECTED MOVE, STOP DISTANCE, REGIME / CALENDAR
and a VRP RISK FLAG -- the four drivers the owner chose (2026-10-15).
Same method as sizing_equation_v2.py / sizing_vrp.py:

  TRAIN (first 60 % of days)   every number fitted here (medians, terciles, tier sizes)
  VALIDATION (next 20 %)       the ONE decision: which candidate, or flat
  TEST (last 20 %)             reported once for the chosen candidate
  Every input is known at the entry second or before the day starts.

CANDIDATE LIST (fixed before the first run; nothing below is tuned):

A  Expected move / volatility targeting -- size = clip(TRAIN median X / X, 0.5, 1.5):
   A1 X = entry IV of the traded leg      A2 X = RV10 to yesterday
   A3 X = RV20 to yesterday               A4 X = entry straddle / spot (CW only)
B  Stop distance:
   B1 same rupee cost per stop: size = 14 / SL_bps (expiry day 16 bps -> smaller)
   B2 premium cushion: size = clip(Y / TRAIN median Y, 0.5, 1.5),
      Y = entry straddle / stop points (premium collected per point of stop; CW only)
C  Regime / calendar -- TRAIN tier sizes as v2
   (adj = 0.6 x (tier avg - avg) x n/(n+40) / avg, clip 0.25-1.50), each also [cut only]:
   C1 DTE of the traded leg (1 / 2 / 3 / 4 / 5+)   C2 event day (no / yes)
   C3 weekday                                      C4 |opening gap| (Norm_OG) TRAIN terciles
   C5 monthly-expiry week (no / yes)               C6 C1-C5 together
   (First-15-min range is not used: most entries are 09:16-09:18, before it is known.)
D  VRP risk flag, cut only -- half size when the measure is in its TRAIN top tercile:
   D1 VRP10   D2 VRP20   D3 SPREAD20
Every sized candidate is scaled so the TRAIN average size of traded days is 1
(except the cut-only ones, which are never above 1). A missing reading -> size 1.

Decision rule (fixed in advance): best profit / max drawdown on VALIDATION, only if
it beats flat there on BOTH profit and profit / drawdown; otherwise flat.
For the chosen candidate: a shuffle check on TEST (sizes re-assigned at random
among the period's traded days, 2000 times).

    ./.venv/bin/python sizing_v3.py
Writes research/output/sizing_v3_report.xlsx
"""
import os

import numpy as np
import pandas as pd

from sizing_vrp import HERE, load as load_vrp, stats

LO, HI, SHRINK, DAMP = 0.25, 1.50, 40.0, 0.6
CAL = ["DTE", "Event", "Weekday", "Gap_abs", "Monthly"]
CAL_NAMES = {"DTE": "C1 DTE", "Event": "C2 Event day", "Weekday": "C3 Weekday",
             "Gap_abs": "C4 |Opening gap|", "Monthly": "C5 Monthly-expiry week"}


def load():
    D = load_vrp()
    D["entry"] = pd.to_numeric(D["Entry_Time"].astype(str).str.replace(":", "", regex=False), errors="coerce")
    F = pd.read_csv(os.path.join(HERE, "output", "entry_window_features.csv"), usecols=["Date", "entry", "straddle0", "u0"])
    D = D.merge(F.drop_duplicates(["Date", "entry"]), how="left", on=["Date", "entry"])
    cw = (D["Leg"] == "CW").values
    D["STR_SPOT"] = np.where(cw, D["straddle0"] / D["u0"], np.nan)
    stop_pts = D["Entry_U"].astype(float) * D["SL_bps"].astype(float) / 1e4
    D["CUSHION"] = np.where(cw, D["straddle0"] / stop_pts, np.nan)
    nw_dte = np.busday_count(pd.to_datetime(D["Date"]).values.astype("datetime64[D]"),
                             pd.to_datetime(D["NW_Expiry"], errors="coerce").fillna(pd.Timestamp("2100-01-01")).values.astype("datetime64[D]")) + 1
    D["DTE"] = np.minimum(np.where(D["Leg"] == "NW", nw_dte, D["DTE_idx"] + 1), 5)
    D["Event"] = D["Event"].astype(bool).astype(int)
    D["Weekday"] = pd.to_datetime(D["Date"]).dt.weekday
    D["Gap_abs"] = D["Norm_OG"].abs()
    exp = pd.to_datetime(pd.Series(D["CW_Expiry"].dropna().unique()))
    monthly = set(exp.groupby(exp.dt.to_period("M")).max().dt.strftime("%Y-%m-%d"))
    D["Monthly"] = D["CW_Expiry"].isin(monthly).astype(int)
    return D


def main():
    D = load()
    traded = D["Leg"].isin(["CW", "NW"]).values
    part = D["Part"].values
    tr = traded & (part == "Train")
    T = D[tr]
    mu = T["Net_PnL"].mean()
    pnl = D["Net_PnL"].values

    def norm(raw):
        """Sizes on traded days, TRAIN average 1; non-traded days 1."""
        s = np.ones(len(D))
        s[traded] = raw[traded] / raw[tr].mean()
        return s

    def target(col, invert):
        x = D[col].values.astype(float)
        med = np.nanmedian(T[col].values.astype(float))
        r = med / x if invert else x / med
        return norm(np.where(np.isfinite(r), np.clip(r, 0.5, 1.5), 1.0))

    # Calendar tiers: discrete levels as they are, |gap| as TRAIN terciles.
    gq = T["Gap_abs"].dropna().quantile([1 / 3, 2 / 3]).values
    lvl = {c: D[c].values.astype(float) for c in CAL}
    lvl["Gap_abs"] = np.where(np.isfinite(lvl["Gap_abs"]), np.searchsorted(gq, np.nan_to_num(lvl["Gap_abs"]), side="right"), np.nan)
    adj_tab, eff = {}, []
    for c in CAL:
        x = lvl[c][tr]
        a = {}
        for k in sorted(set(x[np.isfinite(x)])):
            m = x == k
            n = int(m.sum())
            avg = T["Net_PnL"].values[m].mean()
            a[k] = DAMP * ((avg - mu) * n / (n + SHRINK)) / mu
            lab = f"tercile {int(k)} (edges {gq[0]:.3f}/{gq[1]:.3f})" if c == "Gap_abs" else str(int(k))
            eff.append(dict(Input=CAL_NAMES[c], Level=lab, Train_trades=n, Avg_profit=avg,
                            Stopped_pct=100 * (T["Exit_Reason"].values[m] == "SL").mean(), Size_adjustment=a[k]))
        adj_tab[c] = a

    def cal_size(cols):
        raw = np.ones(len(D))
        for c in cols:
            raw += np.array([adj_tab[c].get(v, 0.0) if np.isfinite(v) else 0.0 for v in lvl[c]])
        s = norm(np.clip(raw, LO, HI))
        s[traded] = np.clip(s[traded], LO, HI)
        return s

    S = {"Flat (size 1)": np.ones(len(D)),
         "A1 Vol target: entry IV": target("IV", True),
         "A2 Vol target: RV10": target("RV10", True),
         "A3 Vol target: RV20": target("RV20", True),
         "A4 Vol target: entry straddle / spot": target("STR_SPOT", True),
         "B1 Stop distance: 14 / SL_bps": norm(np.nan_to_num(np.where(traded, 14.0 / D["SL_bps"].values, 1.0), nan=1.0)),
         "B2 Stop distance: premium cushion (straddle / stop)": target("CUSHION", False)}
    for c in CAL:
        s = cal_size([c])
        S[CAL_NAMES[c]] = s
        S[CAL_NAMES[c] + " [cut only]"] = np.minimum(s, 1.0)
    s = cal_size(CAL)
    S["C6 Calendar all five"] = s
    S["C6 Calendar all five [cut only]"] = np.minimum(s, 1.0)
    for tag, c in (("D1", "VRP10"), ("D2", "VRP20"), ("D3", "SPREAD20")):
        hi = T[c].dropna().quantile(2 / 3)
        v = D[c].values
        S[f"{tag} VRP flag: half size in TRAIN top tercile of {c}"] = np.where(traded & np.isfinite(v) & (v > hi), 0.5, 1.0)

    rows = []
    for name, sz in S.items():
        for p in ("Train", "Validation", "Test"):
            st = stats((pnl * sz)[part == p])
            st.update(Candidate=name, Period=p, Avg_size=sz[traded & (part == p)].mean(),
                      Min_size=sz[traded & (part == p)].min(), Max_size=sz[traded & (part == p)].max())
            rows.append(st)
    R = pd.DataFrame(rows)[["Candidate", "Period", "Net", "MDD", "Net_per_MDD", "Worst_day", "Trades", "Per_trade", "Avg_size", "Min_size", "Max_size"]]
    V = R[R.Period == "Validation"].set_index("Candidate")
    flat = V.loc["Flat (size 1)"]
    ok = V[(V["Net"] > flat["Net"]) & (V["Net_per_MDD"] > flat["Net_per_MDD"])]
    chosen = ok["Net_per_MDD"].idxmax() if len(ok) else "Flat (size 1)"

    # Ranking check: does each driver's size line up with the trade's profit (per period)?
    rk = []
    for name, sz in S.items():
        if name.startswith("Flat"):
            continue
        row = dict(Candidate=name)
        for p in ("Train", "Validation", "Test"):
            m = traded & (part == p)
            row[f"Spearman_{p}"] = pd.Series(sz[m]).rank().corr(pd.Series(pnl[m]).rank()) if np.ptp(sz[m]) > 0 else np.nan
        rk.append(row)
    RK = pd.DataFrame(rk)

    shuffle = None
    if chosen != "Flat (size 1)":
        m = traded & (part == "Test")
        base = stats((pnl * S[chosen])[part == "Test"])["Net_per_MDD"]
        rng = np.random.default_rng(7)
        beat = 0
        for _ in range(2000):
            s2 = S[chosen].copy()
            s2[m] = rng.permutation(s2[m])
            beat += stats((pnl * s2)[part == "Test"])["Net_per_MDD"] < base
        shuffle = beat / 2000

    cuts = D.groupby("Part")["Date"].agg(["min", "max", "size"]).reindex(["Train", "Validation", "Test"])
    pd.set_option("display.width", 250)
    pd.set_option("display.max_colwidth", 70)
    print(cuts.to_string())
    print(f"\ntrain traded days {int(tr.sum())}, average {mu:.2f}\n")
    print("Validation, sorted by profit / drawdown:")
    print(R[R.Period == "Validation"].sort_values("Net_per_MDD", ascending=False).round(2).to_string(index=False))
    print("\nTRAIN tier sizes (calendar):")
    print(pd.DataFrame(eff).round(3).to_string(index=False))
    print("\nSize vs profit, Spearman by period:")
    print(RK.round(3).to_string(index=False))
    print(f"\nDECISION on validation: {chosen}")
    print(R[(R.Candidate.isin(["Flat (size 1)", chosen]))].round(2).to_string(index=False))
    if shuffle is not None:
        print(f"shuffle check on TEST: beats {100 * shuffle:.1f} % of random size re-assignments")

    out = os.path.join(HERE, "output", "sizing_v3_report.xlsx")
    with pd.ExcelWriter(out) as w:
        pd.DataFrame({"Read me": [
            "Size varying on expected move (A), stop distance (B), regime / calendar (C) and a VRP risk flag (D); candidate list fixed before running (see sizing_v3.py header).",
            "60 / 20 / 20 chronological split. Medians, terciles and tier sizes fitted on TRAIN, decided on VALIDATION, reported once on TEST.",
            "Decision rule (fixed in advance): best profit / max drawdown on VALIDATION, only if it beats flat there on profit AND profit / drawdown; else flat.",
            f"Decision: {chosen}." + (f" Shuffle check on TEST: beats {100 * shuffle:.1f} % of random re-assignments." if shuffle is not None else ""),
            "A4 and B2 use the entry straddle (entry_window_features.csv), CW only; NW trades keep size 1 there.",
            "PnL in backtest units (one trade = Rs 25,000 notional).",
        ]}).to_excel(w, sheet_name="Read_me", index=False)
        cuts.to_excel(w, sheet_name="Split")
        R.round(3).to_excel(w, sheet_name="Candidates", index=False)
        RK.round(3).to_excel(w, sheet_name="Size_vs_profit", index=False)
        pd.DataFrame(eff).round(4).to_excel(w, sheet_name="Train_calendar_sizes", index=False)
        d = D[["Date", "Part", "Leg", "Stage", "IV", "RV10", "RV20", "STR_SPOT", "CUSHION", "SL_bps", "DTE", "Event",
               "Weekday", "Norm_OG", "Monthly", "VRP20", "SPREAD20", "Exit_Reason", "Net_PnL"]].copy()
        d["Size_chosen"] = S[chosen].round(3)
        d.round(5).to_excel(w, sheet_name="Daily", index=False)
    print(f"\nwrote {out}")


if __name__ == "__main__":
    main()
