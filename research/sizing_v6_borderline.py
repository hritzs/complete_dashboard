"""
Sizing v6: cut size on BORDERLINE ("cut to cut") days -- the owner's request
(2026-10-15): when the trade only just qualified, trade it smaller.

Borderline is read from the lookup tables alone -- nothing is fitted on profit:
  MARGIN m  an input sat within m (relative) of a table threshold, and the cell
            across that threshold is NO in the stage table that said YES; also the
            straddle ratio within m below its veto, or Build IV within m above the
            0.12 floor (DTE 1-2). Inputs: IV ratio, straddle ratio, Build IV,
            opening gap, ADJ change.
  ISOLATED  the traded cell has at most 1 YES neighbour (+/-1 on IV ratio,
            straddle ratio, Build IV) in its stage table.
CW trades only; NW trades size 1. Cut only, never above 1.

CANDIDATE LIST (fixed before the first run):
  M2 x0.75   margin 2 %  -> 0.75       M2 x0.5   margin 2 %  -> 0.5
  M5 x0.75   margin 5 %  -> 0.75       I  x0.75  isolated    -> 0.75
  I  x0.5    isolated    -> 0.5
  (The E combinations -- 09:16 table x1.15 -- were dropped at the owner's instruction:
   the 09:16 table already has the highest cutoff, so it is not to be sized up.)
Decision rule as before: best profit / max drawdown on VALIDATION, only if it beats
flat there on BOTH profit and profit / drawdown. Shuffle check on TEST for a winner.
The split is used only to decide; the borderline flags need no fitting.

    ./.venv/bin/python sizing_v6_borderline.py
Writes research/output/sizing_v6_borderline_report.xlsx
"""
import ast
import os

os.environ.setdefault("GS_LUT_DIR", "/mnt/shared/GammaShortData/GammaShort/LUTS_r46V2_cwnw_adj007_005_T13_5_2_2_6")

import numpy as np
import pandas as pd

import GammaShort_Backtest_Final as B
from sizing_v4_params import load
from sizing_v5_capped import dd_stats
from sizing_vrp import HERE, stats

STAGE_IDX = {"09:16": 0, "09:17": 1, "09:18": 2, "09:19": 3, "09:20+": 4}
AXES = [(1, "IV_Ratio", B.IV_THRESHOLDS), (2, "Str_Ratio", B.STR_THRESHOLDS), (3, "Build_IV", B.BLD_THRESHOLDS),
        (4, "Norm_OG", B.OG_THRESHOLDS), (5, "Adj_IV_Chg", np.array([B.ADJ_LOWER, B.ADJ_HIGHER]))]


def flags(D, luts):
    n = len(D)
    out = {"near2": np.zeros(n, bool), "near5": np.zeros(n, bool), "isolated": np.zeros(n, bool),
           "nbrs": np.full(n, np.nan), "why2": [""] * n, "cell_check": np.ones(n, bool)}
    for i, r in enumerate(D.itertuples()):
        if r.Leg != "CW":
            continue
        lut = luts[STAGE_IDX[r.Stage]]
        c = ast.literal_eval(r.Cell)
        out["nbrs"][i] = B.neighbour_support(lut, c)
        out["isolated"][i] = out["nbrs"][i] <= 1
        di = c[0]
        for m, key in ((0.02, "near2"), (0.05, "near5")):
            why = []
            for ax, col, th in AXES:
                x = float(getattr(r, col))
                k = c[ax]
                if key == "near2" and int(np.searchsorted(th, x, side="right")) != k:
                    out["cell_check"][i] = False
                for edge_i, nb in ((k - 1, k - 1), (k, k + 1)):     # lower edge -> cell k-1, upper edge -> cell k+1
                    if 0 <= edge_i < len(th) and 0 <= nb < B.DIMS[ax] and abs(x - th[edge_i]) < m * abs(th[edge_i]):
                        cc = list(c); cc[ax] = nb
                        if not lut[tuple(cc)]:
                            why.append(col)
            v = B.VETO_STR.get(di)
            if v is not None and v * (1 - m) < r.Str_Ratio <= v:
                why.append("veto")
            if di in B.IV_FLOOR and B.IV_FLOOR[di] <= r.Build_IV < B.IV_FLOOR[di] * (1 + m):
                why.append("floor")
            out[key][i] = bool(why)
            if key == "near2":
                out["why2"][i] = ",".join(sorted(set(why)))
    return out


def main():
    D = load()
    part = D["Part"].values
    cw = (D["Leg"] == "CW").values
    traded = D["Leg"].isin(["CW", "NW"]).values
    pnl = D["Net_PnL"].values
    F = flags(D, B.load_cw_luts(B.LUT_DIR))
    print(f"cell recomputed from the stored inputs matches the stored cell on {F['cell_check'][cw].mean() * 100:.1f} % of CW trades")

    def cut(flag, lv):
        return np.where(cw & flag, lv, 1.0)
    S = {"Flat (size 1)": np.ones(len(D)),
         "M2 x0.75": cut(F["near2"], 0.75), "M2 x0.5": cut(F["near2"], 0.5),
         "M5 x0.75": cut(F["near5"], 0.75),
         "I x0.75": cut(F["isolated"], 0.75), "I x0.5": cut(F["isolated"], 0.5)}

    rows = []
    for name, sz in S.items():
        for per in ("Train", "Validation", "Test"):
            r = dd_stats((pnl * sz)[part == per])
            m = traded & (part == per)
            r.update(Candidate=name, Period=per, Pct_cut=100 * (sz[m] < 1).mean(), Pct_up=100 * (sz[m] > 1).mean())
            rows.append(r)
    R = pd.DataFrame(rows)[["Candidate", "Period", "Net", "MDD", "Net_per_MDD", "Worst_day", "Worst_5day",
                            "Longest_DD_days", "Trades", "Pct_cut", "Pct_up"]]
    V = R[R.Period == "Validation"].set_index("Candidate")
    f = V.loc["Flat (size 1)"]
    ok = V[(V["Net"] > f["Net"]) & (V["Net_per_MDD"] > f["Net_per_MDD"])]
    chosen = ok["Net_per_MDD"].idxmax() if len(ok) else "Flat (size 1)"
    shuffle = None
    if chosen != "Flat (size 1)":
        tm = part == "Test"
        m = cw & tm
        base = stats((pnl * S[chosen])[tm])["Net_per_MDD"]
        rng = np.random.default_rng(7)
        cnt = 0
        for _ in range(2000):
            s2 = S[chosen].copy(); s2[m] = rng.permutation(s2[m]); cnt += stats((pnl * s2)[tm])["Net_per_MDD"] < base
        shuffle = cnt / 2000

    # Profile: borderline vs firm trades, by period and by year
    yr = pd.to_datetime(D["Date"]).dt.year.values
    prof = []
    for fl in ("near2", "near5", "isolated"):
        for grp, keyv in [("Period", p) for p in ("Train", "Validation", "Test")] + [("Year", y) for y in sorted(set(yr))]:
            m = cw & ((part == keyv) if grp == "Period" else (yr == keyv))
            b, nb = m & F[fl], m & ~F[fl]
            prof.append(dict(Flag=fl, Group=grp, Key=keyv, Border_n=int(b.sum()), Border_avg=pnl[b].mean() if b.any() else np.nan,
                             Border_stop_pct=100 * (D["Exit_Reason"].values[b] == "SL").mean() if b.any() else np.nan,
                             Firm_n=int(nb.sum()), Firm_avg=pnl[nb].mean() if nb.any() else np.nan,
                             Firm_stop_pct=100 * (D["Exit_Reason"].values[nb] == "SL").mean() if nb.any() else np.nan))
    P = pd.DataFrame(prof)
    why = pd.Series([w for w, c in zip(F["why2"], cw) if c and w]).str.split(",").explode().value_counts()

    pd.set_option("display.width", 250)
    print("Borderline vs firm CW trades:"); print(P.round(1).to_string(index=False))
    print("\nWhich input made a trade borderline (2 %):", why.to_dict())
    print("YES-neighbour count of traded cells:", pd.Series(F["nbrs"][cw]).value_counts().sort_index().to_dict())
    print("\nValidation by profit / drawdown:")
    print(R[R.Period == "Validation"].sort_values("Net_per_MDD", ascending=False).round(2).to_string(index=False))
    print(f"\nDECISION: {chosen}")
    print(R[R.Candidate.isin(["Flat (size 1)", chosen])].round(2).to_string(index=False))
    if shuffle is not None:
        print(f"shuffle check on TEST: beats {100 * shuffle:.1f} % of random re-assignments")

    out = os.path.join(HERE, "output", "sizing_v6_borderline_report.xlsx")
    with pd.ExcelWriter(out) as w:
        pd.DataFrame({"Read me": [
            "Cut size on borderline ('cut to cut') days. Borderline is read from the lookup tables only (sizing_v6_borderline.py header).",
            "Decision: best profit / max drawdown on VALIDATION, only if it beats flat there on profit AND profit / drawdown.",
            f"Decision: {chosen}." + (f" Shuffle check on TEST: beats {100 * shuffle:.1f} % of random re-assignments." if shuffle is not None else ""),
            "PnL in backtest units (one trade = Rs 25,000 notional).",
        ]}).to_excel(w, sheet_name="Read_me", index=False)
        R.round(3).to_excel(w, sheet_name="Candidates", index=False)
        P.round(3).to_excel(w, sheet_name="Borderline_profile", index=False)
        d = D[["Date", "Part", "Leg", "Stage", "Cell", "IV_Ratio", "Str_Ratio", "Build_IV", "Norm_OG", "Adj_IV_Chg",
               "Exit_Reason", "Net_PnL"]].copy()
        d["Near_2pct"], d["Near_5pct"], d["Why_2pct"] = F["near2"], F["near5"], F["why2"]
        d["YES_neighbours"], d["Isolated"] = F["nbrs"], F["isolated"]
        d["Size_chosen"] = S[chosen]
        d.to_excel(w, sheet_name="Daily", index=False)
    print(f"\nwrote {out}")


if __name__ == "__main__":
    main()
