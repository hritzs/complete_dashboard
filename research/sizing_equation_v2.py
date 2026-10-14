"""
Sizing equation and early-exit rule for the GammaShort book, decided on a
60 / 20 / 20 chronological split.

  TRAIN (first 60 % of days)   every number in the equation is fitted here
  VALIDATION (next 20 %)       the ONE decision is taken here: which candidate
                               (or none) to use
  TEST (last 20 %)             reported once for the chosen candidate; never
                               used to fit or to choose

No forward-looking:
  - the split is by date, in order;
  - every input is known at the entry second: which table said YES, the table
    inputs, and the tick readings of the 60 s BEFORE entry
    (entry_window_features.py takes nothing later);
  - bin edges of the tick readings are TRAIN terciles; sizes are scaled by
    the TRAIN average size;
  - the early-exit rule acts at +k minutes on what is known at +k minutes.
The candidate list is short and fixed in advance (no tuning of the shrink,
damping or limits).

Decision rule (fixed before looking): take the candidate with the best
profit / max-drawdown on VALIDATION, but only if it beats flat sizing there on
BOTH profit and profit / drawdown; otherwise keep flat.

    ./.venv/bin/python sizing_equation_v2.py
Writes research/output/sizing_equation_v2_report.xlsx
"""
import glob
import os
import pickle

import numpy as np
import pandas as pd

HERE = os.path.dirname(os.path.abspath(__file__))
LO, HI, SHRINK, DAMP = 0.25, 1.50, 40.0, 0.6

FIXED_BINS = {
    "Table":     ([0.5, 1.5, 2.5], ["09:16", "09:17", "09:18", "09:19 / later"]),
    "IV_Ratio":  ([0.90, 1.00, 1.10, 1.20, 1.35], ["<0.90", "0.90-1.00", "1.00-1.10", "1.10-1.20", "1.20-1.35", ">1.35"]),
    "Str_Ratio": ([0.95, 1.00, 1.05, 1.10, 1.20], ["<0.95", "0.95-1.00", "1.00-1.05", "1.05-1.10", "1.10-1.20", ">1.20"]),
    "Build_IV":  ([0.12, 0.14, 0.17, 0.20], ["<0.12", "0.12-0.14", "0.14-0.17", "0.17-0.20", ">0.20"]),
    "DTE":       ([1.5, 2.5, 3.5, 4.5], ["1 (expiry)", "2", "3", "4", "5+"]),
}
TICK = ["u_range_bps", "str_range_pct", "str_chg_abs", "spread_pct"]     # 60 s before entry
TABLE_INPUTS = ["Table", "IV_Ratio", "Str_Ratio", "Build_IV", "DTE"]
CANDIDATES = {
    "Flat (size 1)": [],
    "Table": ["Table"],
    "Table + IV ratio": ["Table", "IV_Ratio"],
    "Five table inputs": TABLE_INPUTS,
    "Pre-entry stability": TICK,
    "Table + stability": ["Table"] + TICK,
    "Five table inputs + stability": TABLE_INPUTS + TICK,
}
EARLY = [(k, f) for k in (2, 3, 5) for f in (0.5, 0.75)]     # exit at +k min if the straddle is f x stop against us


def load():
    files = sorted(glob.glob(os.path.join(HERE, "output", "backtest_final", "run_*", "days", "*.pkl")))
    D = pd.DataFrame([pickle.load(open(f, "rb")) for f in files]).sort_values("Date").reset_index(drop=True)
    D["Net_PnL"] = D["Net_PnL"].fillna(0.0)
    D["Leg"] = D["Leg"].fillna("")
    D["DTE"] = D["DTE_idx"] + 1
    D["Table"] = D["Stage"].map({"09:16": 0, "09:17": 1, "09:18": 2}).where(D["Leg"] == "CW").fillna(3)
    D["entry"] = pd.to_numeric(D["Entry_Time"].astype(str).str.replace(":", "", regex=False), errors="coerce")
    F = pd.read_csv(os.path.join(HERE, "output", "entry_window_features.csv"))
    if "error" in F:
        F = F[F["error"].isna()]
    F["str_chg_abs"] = F["str_chg_pct"].abs()
    D = D.merge(F, how="left", on=["Date", "entry"])
    n = len(D)
    D["Part"] = np.where(np.arange(n) < int(0.6 * n), "Train", np.where(np.arange(n) < int(0.8 * n), "Validation", "Test"))
    return D


def stats(p):
    p = np.asarray(p, float)
    cum = np.cumsum(p)
    hw = np.maximum(np.maximum.accumulate(cum), 0.0)
    mdd = float((hw - cum).max()) if len(p) else 0.0
    tr = p[p != 0]
    return dict(Net=p.sum(), MDD=mdd, Net_per_MDD=p.sum() / mdd if mdd else np.nan, Worst_day=p.min() if len(p) else 0.0,
                Trades=len(tr), Per_trade=tr.mean() if len(tr) else 0.0)


def main():
    D = load()
    cw = (D["Leg"] == "CW").values
    part = D["Part"].values
    tr = cw & (part == "Train")
    T = D[tr]
    mu = T["Net_PnL"].mean()
    bins = dict(FIXED_BINS)
    for c in TICK:                                   # terciles of the TRAINING trades only
        q = T[c].dropna().quantile([1 / 3, 2 / 3]).values
        bins[c] = (list(q), [f"low (<{q[0]:.2f})", f"mid ({q[0]:.2f}-{q[1]:.2f})", f"high (>{q[1]:.2f})"])
    eff = {}
    for c, (edges, labels) in bins.items():
        x = T[c].astype(float).values
        b = np.searchsorted(edges, np.nan_to_num(x, nan=-1e18), side="right")
        rows = []
        for k, lab in enumerate(labels):
            m = (b == k) & np.isfinite(x)
            n = int(m.sum())
            avg = T["Net_PnL"].values[m].mean() if n else np.nan
            rows.append(dict(Input=c, Range=lab, Train_trades=n, Avg_profit=avg,
                             Stopped_pct=100 * (T["Exit_Reason"].values[m] == "SL").mean() if n else np.nan,
                             Size_adjustment=DAMP * ((avg - mu) * n / (n + SHRINK)) / mu if n else 0.0))
        eff[c] = pd.DataFrame(rows)

    def sizes(cols):
        s = np.ones(len(D))
        if not cols:
            return s
        raw = np.ones(len(D))
        for c in cols:
            edges, _ = bins[c]
            x = D[c].astype(float).values
            b = np.searchsorted(edges, np.nan_to_num(x, nan=-1e18), side="right")
            adj = eff[c]["Size_adjustment"].values[np.clip(b, 0, len(eff[c]) - 1)]
            raw += np.where(np.isfinite(x), adj, 0.0)          # a missing reading adds nothing
        raw = np.clip(raw, LO, HI)
        s[cw] = np.clip(raw[cw] / raw[tr].mean(), LO, HI)       # TRAIN average size = 1
        return s

    pnl = D["Net_PnL"].values

    def dd_mult(mode):
        """Multiplier per day from the flat book's own results up to YESTERDAY."""
        out = np.ones(len(D))
        cum = hw = 0.0
        streak = 0
        for i, x in enumerate(pnl):
            dd = hw - cum
            if mode == "half size after 3 losing days":
                out[i] = 0.5 if streak >= 3 else 1.0
            elif mode == "half size while drawdown > 100":
                out[i] = 0.5 if dd > 100 else 1.0
            elif mode == "size falls with drawdown (1 - dd/200, floor 0.5)":
                out[i] = max(0.5, 1.0 - dd / 200.0)
            cum += x
            hw = max(hw, cum)
            streak = streak + 1 if x < 0 else (0 if x > 0 else streak)
        return out

    DD_MODES = ["", "half size after 3 losing days", "half size while drawdown > 100",
                "size falls with drawdown (1 - dd/200, floor 0.5)"]
    rows, S = [], {}
    for base, cols in CANDIDATES.items():
        two = sizes(cols)
        # "cut only": never above 1 -- smaller on low-conviction days, never bigger on favoured ones
        for style, sz in (("", two), (" [cut only]", np.minimum(two, 1.0))):
            if style and not cols:
                continue
            for mode in DD_MODES:
                name = base + style + (" x " + mode if mode else "")
                S[name] = sz * (dd_mult(mode) if mode else 1.0)
                for p in ("Train", "Validation", "Test"):
                    st = stats((pnl * S[name])[part == p])
                    st.update(Candidate=name, Period=p, Avg_size=S[name][cw & (part == p)].mean())
                    rows.append(st)
    R = pd.DataFrame(rows)[["Candidate", "Period", "Net", "MDD", "Net_per_MDD", "Worst_day", "Trades", "Per_trade", "Avg_size"]]
    V = R[R.Period == "Validation"].set_index("Candidate")
    flat = V.loc["Flat (size 1)"]
    ok = V[(V["Net"] > flat["Net"]) & (V["Net_per_MDD"] > flat["Net_per_MDD"])]
    chosen = ok["Net_per_MDD"].idxmax() if len(ok) else "Flat (size 1)"

    # ---- early exit after entry (unhedged mark of the straddle; conservative: the hedge would offset part of it)
    sc = 25000.0 / D["Entry_U"].astype(float).values
    stop_pts = D["Entry_U"].astype(float).values * D["SL_bps"].astype(float).values / 1e4
    held_min = (pd.to_datetime(D["Exit_Time"], format="%H:%M:%S", errors="coerce")
                - pd.to_datetime(D["Entry_Time"], format="%H:%M", errors="coerce")).dt.total_seconds().values / 60.0
    erows, E = [], {"No early exit": pnl.copy()}
    for k, f in EARLY:
        mark = D[f"str_pts_{k}"].astype(float).values
        hit = cw & np.isfinite(mark) & (mark > f * stop_pts) & (held_min > k)
        p2 = pnl.copy()
        p2[hit] = -(mark[hit] + 0.5) * sc[hit] - D["Entry_Cost"].fillna(0).values[hit]
        E[f"Exit at +{k} min if straddle {int(f * 100)}% of stop against"] = p2
    for name, p2 in E.items():
        for p in ("Train", "Validation", "Test"):
            st = stats(p2[part == p])
            st.update(Rule=name, Period=p, Exits=int(((p2 != pnl) & (part == p)).sum()))
            erows.append(st)
    ER = pd.DataFrame(erows)[["Rule", "Period", "Net", "MDD", "Net_per_MDD", "Worst_day", "Exits"]]
    EV = ER[ER.Period == "Validation"].set_index("Rule")
    eflat = EV.loc["No early exit"]
    eok = EV[(EV["Net"] > eflat["Net"]) & (EV["Net_per_MDD"] > eflat["Net_per_MDD"])]
    echosen = eok["Net_per_MDD"].idxmax() if len(eok) else "No early exit"

    cuts = D.groupby("Part")["Date"].agg(["min", "max", "size"]).reindex(["Train", "Validation", "Test"])
    print(cuts.to_string())
    print(f"\ntrain CW trades {int(tr.sum())}, average {mu:.2f}\n")
    pd.set_option("display.width", 250)
    pd.set_option("display.max_colwidth", 90)
    print(f"{R.Candidate.nunique()} sizing candidates. Validation, best 12 by profit / drawdown:")
    print(R[R.Period == "Validation"].sort_values("Net_per_MDD", ascending=False).head(12).round(2).to_string(index=False))
    print("\nflat on each period:")
    print(R[R.Candidate == "Flat (size 1)"].round(2).to_string(index=False))
    print(f"\nSIZING decision on validation: {chosen}")
    print(R[(R.Candidate.isin(["Flat (size 1)", chosen])) & (R.Period == "Test")].round(2).to_string(index=False))
    print("\n" + ER.round(2).to_string(index=False))
    print(f"\nEARLY-EXIT decision on validation: {echosen}")
    print(ER[(ER.Rule.isin(["No early exit", echosen])) & (ER.Period == "Test")].round(2).to_string(index=False))
    allE = pd.concat(eff.values(), ignore_index=True)
    print("\n" + allE.round(2).to_string(index=False))

    out = os.path.join(HERE, "output", "sizing_equation_v2_report.xlsx")
    with pd.ExcelWriter(out) as w:
        pd.DataFrame({"Read me": [
            "60 / 20 / 20 chronological split. Fitted on TRAIN, decided on VALIDATION, reported once on TEST.",
            "size = clip(1 + sum of the size adjustments of the chosen inputs, 0.25, 1.50), scaled so the TRAIN average size is 1.",
            "size adjustment = 0.6 x (range average - overall average) x n / (n + 40) / overall average, from TRAIN trades only.",
            "Decision rule (fixed in advance): best profit / max drawdown on VALIDATION, only if it beats flat there on profit AND profit / drawdown; else flat.",
            f"Sizing decision: {chosen}.   Early-exit decision: {echosen}.",
            "Early exit is valued at the unhedged straddle mark at +k minutes plus 0.5 point, so it overstates the exit loss (the hedge would offset part).",
            "'[cut only]' never sizes above 1: smaller on low-conviction days, never bigger on favoured ones. Drawdown multipliers use the flat book's results up to yesterday only.",
            "Caveat: the lookup tables themselves were built by the research team on the whole history, so 'which table said YES' is not out-of-sample in any period.",
            "PnL in backtest units (one trade = Rs 25,000 notional). Source: research/output/backtest_final and entry_window_features.csv.",
        ]}).to_excel(w, sheet_name="Read_me", index=False)
        cuts.to_excel(w, sheet_name="Split")
        R.round(2).to_excel(w, sheet_name="Sizing_candidates", index=False)
        ER.round(2).to_excel(w, sheet_name="Early_exit_rules", index=False)
        allE.round(3).to_excel(w, sheet_name="Size_adjustments", index=False)
        d = D[["Date", "Part", "Leg", "Stage", "DTE", "IV_Ratio", "Str_Ratio", "Build_IV"] + TICK + ["Exit_Reason", "Net_PnL"]].copy()
        d["Size_chosen"] = S[chosen].round(3)
        d.to_excel(w, sheet_name="Daily", index=False)
    print(f"\nsaved {out}")


if __name__ == "__main__":
    main()
