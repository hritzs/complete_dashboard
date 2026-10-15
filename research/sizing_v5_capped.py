"""
Sizing v5: base size 1, CUT on poor days, at most 1.15 on good days -- the owner's
constraint (2026-10-15): "keep it one, lower the size if the day is not good, up
the size by max 1.15, keep the drawdown in mind, don't overfit".

Scores are the v4 ones, from the existing entry parameters (CW trades; NW size 1):
  S1 bucket score -- sum over the 8 table inputs of (bin avg - avg) x n/(n+20),
     TRAIN quintile bins (Table, DTE: own levels)
  S2 ridge score  -- ridge (lambda 10) on the standardised inputs + Table dummies
Tiers = TRAIN terciles of the score. Sizes are NOT rescaled: mid tier is 1.

CANDIDATE LIST (fixed before the first run; small on purpose):
  A  S1 tiers -> 0.75 / 1 / 1.15        B  S1 tiers -> 0.5 / 1 / 1.15
  C  S2 tiers -> 0.75 / 1 / 1.15        D  S2 tiers -> 0.5 / 1 / 1.15
  E  stage only: 09:16 table 1.15, 09:17 / 09:18 1, 09:19 and later 0.75
     (NOT clean: the stage pattern was seen in all periods in v4 -- reported, flagged)
  each also with a DRAWDOWN GUARD: the 1.15 is used only while the sized book's own
  drawdown up to yesterday is under 50 (about 1.3 stop-outs); otherwise the top
  tier trades at 1.
Decision rule (as before): best profit / max drawdown on VALIDATION, only if it
beats flat there on BOTH profit and profit / drawdown; otherwise flat.
Over-fitting checks: shuffle test on TEST for the winner; walk-forward by year
(score refitted each 1 January on all earlier trades only, applied to that year).
Note: v4 already showed S1 tier averages on TEST, so TEST is not untouched for S1.

    ./.venv/bin/python sizing_v5_capped.py
Writes research/output/sizing_v5_capped_report.xlsx
"""
import os

import numpy as np
import pandas as pd

from sizing_v4_params import DISCRETE, LAMBDA, PARAMS, SHRINK, load
from sizing_vrp import HERE, stats

TOP, GUARD_DD = 1.15, 50.0
MAPS = {"0.75/1/1.15": [0.75, 1.0, TOP], "0.5/1/1.15": [0.5, 1.0, TOP]}


def score_s1(D, fit, cw):
    T = D[fit]
    y = T["Net_PnL"].values
    mu = y.mean()
    s = np.zeros(len(D))
    for c in PARAMS:
        xt = T[c].astype(float).values
        xa = D[c].astype(float).values
        if c in DISCRETE:
            bt, ba, levels = xt, xa, sorted(set(xt))
        else:
            e = np.quantile(xt, [0.2, 0.4, 0.6, 0.8])
            bt, ba, levels = np.searchsorted(e, xt, side="right"), np.searchsorted(e, xa, side="right"), range(5)
        for k in levels:
            m = bt == k
            n = int(m.sum())
            if n:
                s[cw & (ba == k)] += (y[m].mean() - mu) * n / (n + SHRINK)
    return s


def score_s2(D, fit, cw):
    def design(df):
        X = [df[c].astype(float).values for c in PARAMS if c != "Table"]
        X += [(df["Table"].values == k).astype(float) for k in (1, 2, 3)]
        return np.column_stack(X)
    T = D[fit]
    y = T["Net_PnL"].values
    Xt = design(T)
    m_, s_ = Xt.mean(0), Xt.std(0)
    s_[s_ == 0] = 1
    Z = (Xt - m_) / s_
    beta = np.linalg.solve(Z.T @ Z + LAMBDA * np.eye(Z.shape[1]), Z.T @ (y - y.mean()))
    return np.where(cw, ((np.nan_to_num(design(D)) - m_) / s_) @ beta, 0.0)


def tier_sizes(score, fit, cw, levels):
    q = np.quantile(score[fit], [1 / 3, 2 / 3])
    return np.where(cw, np.array(levels)[np.searchsorted(q, score, side="right")], 1.0)


def guard(size, pnl):
    """Top size only while the sized book's own drawdown up to yesterday is < GUARD_DD."""
    out = size.copy()
    cum = hw = 0.0
    for i in range(len(pnl)):
        if out[i] > 1.0 and hw - cum >= GUARD_DD:
            out[i] = 1.0
        cum += pnl[i] * out[i]
        hw = max(hw, cum)
    return out


def dd_stats(p):
    st = stats(p)
    cum = np.cumsum(p)
    hw = np.maximum(np.maximum.accumulate(cum), 0.0)
    under = (hw - cum) > 1e-9
    longest = run = 0
    for u in under:
        run = run + 1 if u else 0
        longest = max(longest, run)
    w5 = pd.Series(p).rolling(5).sum().min() if len(p) >= 5 else np.nan
    st.update(Longest_DD_days=longest, Worst_5day=w5)
    return st


def main():
    D = load()
    part = D["Part"].values
    cw = (D["Leg"] == "CW").values
    traded = D["Leg"].isin(["CW", "NW"]).values
    fit = cw & (part == "Train")
    pnl = D["Net_PnL"].values

    sc = {"S1": score_s1(D, fit, cw), "S2": score_s2(D, fit, cw)}
    S = {"Flat (size 1)": np.ones(len(D))}
    for lab, (s, lv) in {"A": ("S1", "0.75/1/1.15"), "B": ("S1", "0.5/1/1.15"),
                         "C": ("S2", "0.75/1/1.15"), "D": ("S2", "0.5/1/1.15")}.items():
        S[f"{lab} {s} {lv}"] = tier_sizes(sc[s], fit, cw, MAPS[lv])
    st = D["Stage"].values
    S["E stage only (flagged: pattern seen before)"] = np.where(cw, np.where(st == "09:16", TOP, np.where(np.isin(st, ["09:17", "09:18"]), 1.0, 0.75)), 1.0)
    for k in list(S):
        if not k.startswith("Flat"):
            S[k + " + DD guard"] = guard(S[k], pnl)

    rows = []
    for name, sz in S.items():
        for per in ("Train", "Validation", "Test"):
            r = dd_stats((pnl * sz)[part == per])
            m = traded & (part == per)
            r.update(Candidate=name, Period=per, Avg_size=sz[m].mean(), Pct_cut=100 * (sz[m] < 1).mean(), Pct_up=100 * (sz[m] > 1).mean())
            rows.append(r)
    R = pd.DataFrame(rows)[["Candidate", "Period", "Net", "MDD", "Net_per_MDD", "Worst_day", "Worst_5day", "Longest_DD_days",
                            "Trades", "Avg_size", "Pct_cut", "Pct_up"]]
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
            s2 = S[chosen].copy()
            s2[m] = rng.permutation(s2[m])
            cnt += stats((pnl * s2)[tm])["Net_per_MDD"] < base
        shuffle = cnt / 2000

    # Walk-forward: refit each 1 January on all earlier CW trades, apply to that year (A-D, no guard).
    yr = pd.to_datetime(D["Date"]).dt.year.values
    wf = []
    for y0 in range(2022, yr.max() + 1):
        fit_y = cw & (yr < y0)
        apply_ = yr == y0
        scy = {"S1": score_s1(D, fit_y, cw), "S2": score_s2(D, fit_y, cw)}
        row = dict(Year=y0, Fit_trades=int(fit_y.sum()))
        fl = stats(pnl[apply_])
        row.update(Flat_Net=fl["Net"], Flat_MDD=fl["MDD"])
        for lab, (s, lv) in {"A": ("S1", "0.75/1/1.15"), "B": ("S1", "0.5/1/1.15"),
                             "C": ("S2", "0.75/1/1.15"), "D": ("S2", "0.5/1/1.15")}.items():
            szy = tier_sizes(scy[s], fit_y, cw, MAPS[lv])
            r = stats((pnl * szy)[apply_])
            row[f"{lab}_Net"], row[f"{lab}_MDD"] = r["Net"], r["MDD"]
            if lab == "A":
                q = np.quantile(scy[s][fit_y], [1 / 3, 2 / 3])
                b = np.searchsorted(q, scy[s], side="right")
                for k, t in enumerate(["low", "mid", "high"]):
                    sel = cw & apply_ & (b == k)
                    row[f"S1_{t}_avg"] = pnl[sel].mean() if sel.any() else np.nan
        wf.append(row)
    WF = pd.DataFrame(wf)
    tot = WF.drop(columns="Year").sum(numeric_only=True)
    # MDD over the whole walk-forward stretch, not the sum of yearly MDDs
    wf_all = {"Flat": pnl.copy()}
    for lab, (s, lv) in {"A": ("S1", "0.75/1/1.15"), "B": ("S1", "0.5/1/1.15"),
                         "C": ("S2", "0.75/1/1.15"), "D": ("S2", "0.5/1/1.15")}.items():
        z = np.ones(len(D))
        for y0 in range(2022, yr.max() + 1):
            fit_y = cw & (yr < y0)
            scy = score_s1(D, fit_y, cw) if s == "S1" else score_s2(D, fit_y, cw)
            z[yr == y0] = tier_sizes(scy, fit_y, cw, MAPS[lv])[yr == y0]
        wf_all[lab] = pnl * z
    WFT = pd.DataFrame([dict(Variant=k, **dd_stats(v[yr >= 2022])) for k, v in wf_all.items()])

    pd.set_option("display.width", 250); pd.set_option("display.max_colwidth", 60)
    print("Validation, sorted by profit / drawdown:")
    print(R[R.Period == "Validation"].sort_values("Net_per_MDD", ascending=False).round(2).to_string(index=False))
    print(f"\nDECISION: {chosen}")
    print(R[R.Candidate.isin(["Flat (size 1)", chosen])].round(2).to_string(index=False))
    if shuffle is not None:
        print(f"shuffle check on TEST: beats {100 * shuffle:.1f} % of random re-assignments")
    print("\nWalk-forward by year (refit each 1 Jan on earlier trades only):")
    print(WF.round(1).to_string(index=False))
    print("\nWalk-forward 2022 onward, whole stretch:")
    print(WFT.round(2).to_string(index=False))

    out = os.path.join(HERE, "output", "sizing_v5_capped_report.xlsx")
    with pd.ExcelWriter(out) as w:
        pd.DataFrame({"Read me": [
            "Base size 1; cut on poor-score days; at most 1.15 on good-score days. Candidate list fixed before running (sizing_v5_capped.py header).",
            "Scores from the existing entry parameters (S1 bucket score, S2 ridge), TRAIN terciles. NW trades size 1.",
            "DD guard: the 1.15 only while the sized book's drawdown up to yesterday is under 50; otherwise 1.",
            "Decision: best profit / max drawdown on VALIDATION, only if it beats flat there on profit AND profit / drawdown.",
            f"Decision: {chosen}." + (f" Shuffle check on TEST: beats {100 * shuffle:.1f} % of random re-assignments." if shuffle is not None else ""),
            "Walk-forward: score refitted every 1 January on all earlier trades, applied to that year only.",
            "Caveats: v4 already showed S1 tier averages on TEST; the tables were built on the whole history.",
        ]}).to_excel(w, sheet_name="Read_me", index=False)
        R.round(3).to_excel(w, sheet_name="Candidates", index=False)
        WF.round(3).to_excel(w, sheet_name="Walk_forward_by_year", index=False)
        WFT.round(3).to_excel(w, sheet_name="Walk_forward_total", index=False)
        d = D[["Date", "Part", "Leg", "Stage"] + PARAMS[1:] + ["Exit_Reason", "Net_PnL"]].copy()
        d["S1_score"], d["S2_score"] = sc["S1"].round(3), sc["S2"].round(3)
        d["Size_chosen"] = S[chosen]
        d["PnL_sized"] = (pnl * S[chosen]).round(3)
        d.to_excel(w, sheet_name="Daily", index=False)
    print(f"\nwrote {out}")


if __name__ == "__main__":
    main()
