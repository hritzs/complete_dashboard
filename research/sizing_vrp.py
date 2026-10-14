"""
Opportunity tracker and sizing from the VOLATILITY RISK PREMIUM, decided on a
60 / 20 / 20 chronological split (same rules as sizing_equation_v2.py).

A short straddle earns when the volatility it sells (implied) is richer than
the volatility the market then delivers. Before entry we know:
  - the entry IV of the trade (Build_IV for the current week, NW_Build_IV for
    the next-week leg),
  - realized volatility up to YESTERDAY from the daily file: each row's
    Future (yesterday's close) against Future_Prev2 (the day before, SAME
    contract -- no expiry-roll jumps), annualised over the last 5 / 10 / 20
    rows.
Opportunity measures (all known at the entry second):
  VRP10 = IV / RV10,  VRP20 = IV / RV20,  SPREAD20 = IV - RV20,
  RV_TREND = RV5 / RV20 (volatility picking up -> less room).

  TRAIN (first 60 % of days)   tier edges (terciles) and tier sizes fitted here
  VALIDATION (next 20 %)       the ONE decision: which candidate, or flat
  TEST (last 20 %)             reported once for the chosen candidate

Candidates are fixed in advance (no tuning):
  Flat; tier sizing on each measure (TRAIN tercile edges, size per tier from
  the TRAIN average profit in the tier, shrunk and damped as in v2, clipped
  0.25-1.50, TRAIN average size 1); the same "cut only" (never above 1);
  continuous VRP20 size = clip(VRP20 / TRAIN median, 0.5, 1.5).
Decision rule: best profit / max drawdown on VALIDATION, only if it beats
flat there on BOTH profit and profit / drawdown; otherwise flat.

    ./.venv/bin/python sizing_vrp.py
Writes research/output/sizing_vrp_report.xlsx
"""
import glob
import os
import pickle

import numpy as np
import pandas as pd

HERE = os.path.dirname(os.path.abspath(__file__))
DAILY = os.environ.get("GS_DAILY_CSV", "/mnt/shared/Hardik/Project_Codes/GammaShortDailyProcess/GammaShortDailydata.csv")
LO, HI, SHRINK, DAMP = 0.25, 1.50, 40.0, 0.6     # as sizing_equation_v2.py
MEASURES = ["VRP10", "VRP20", "SPREAD20", "RV_TREND"]


def stats(p):
    p = np.asarray(p, float)
    cum = np.cumsum(p)
    hw = np.maximum(np.maximum.accumulate(cum), 0.0)
    mdd = float((hw - cum).max()) if len(p) else 0.0
    tr = p[p != 0]
    return dict(Net=p.sum(), MDD=mdd, Net_per_MDD=p.sum() / mdd if mdd else np.nan, Worst_day=p.min() if len(p) else 0.0,
                Trades=len(tr), Per_trade=tr.mean() if len(tr) else 0.0)


def realized_vol():
    """RV5/10/20 per Date from the daily file, using returns up to yesterday's close only."""
    d = pd.read_csv(DAILY, usecols=["Date", "Future", "Future_Prev2"])
    d["Date"] = d["Date"].astype(str).str[:10]
    d = d.drop_duplicates("Date", keep="last").sort_values("Date").reset_index(drop=True)
    r = np.log(d["Future"].astype(float) / d["Future_Prev2"].astype(float))
    r = r.where(np.isfinite(r) & (r.abs() < 0.2))           # drop broken rows
    out = pd.DataFrame({"Date": d["Date"]})
    for n in (5, 10, 20):
        out[f"RV{n}"] = r.rolling(n, min_periods=n).std() * np.sqrt(252)
    return out


def load():
    files = sorted(glob.glob(os.path.join(HERE, "output", "backtest_final", "run_*", "days", "*.pkl")))
    D = pd.DataFrame([pickle.load(open(f, "rb")) for f in files]).sort_values("Date").reset_index(drop=True)
    D["Net_PnL"] = pd.to_numeric(D["Net_PnL"], errors="coerce").fillna(0.0)
    D["Leg"] = D["Leg"].fillna("")
    D = D.merge(realized_vol(), how="left", on="Date")
    iv = np.where(D["Leg"] == "NW", pd.to_numeric(D.get("NW_Build_IV"), errors="coerce"),
                  pd.to_numeric(D.get("Build_IV"), errors="coerce"))
    D["IV"] = iv
    D["VRP10"] = D["IV"] / D["RV10"]
    D["VRP20"] = D["IV"] / D["RV20"]
    D["SPREAD20"] = D["IV"] - D["RV20"]
    D["RV_TREND"] = D["RV5"] / D["RV20"]
    n = len(D)
    D["Part"] = np.where(np.arange(n) < int(0.6 * n), "Train", np.where(np.arange(n) < int(0.8 * n), "Validation", "Test"))
    return D


def main():
    D = load()
    traded = D["Leg"].isin(["CW", "NW"]).values
    part = D["Part"].values
    tr = traded & (part == "Train")
    T = D[tr]
    mu = T["Net_PnL"].mean()
    pnl = D["Net_PnL"].values

    # Tier sizes from TRAIN: terciles, average profit per tier, shrunk and damped.
    tiers, eff = {}, []
    for c in MEASURES:
        q = T[c].dropna().quantile([1 / 3, 2 / 3]).values
        tiers[c] = q
        x = T[c].values
        b = np.searchsorted(q, np.nan_to_num(x, nan=np.inf), side="right")
        adj = []
        for k, lab in enumerate([f"low (<{q[0]:.3f})", f"mid ({q[0]:.3f}-{q[1]:.3f})", f"high (>{q[1]:.3f})"]):
            m = (b == k) & np.isfinite(x)
            nk = int(m.sum())
            avg = T["Net_PnL"].values[m].mean() if nk else np.nan
            a = DAMP * ((avg - mu) * nk / (nk + SHRINK)) / mu if nk else 0.0
            adj.append(a)
            eff.append(dict(Measure=c, Tier=lab, Train_trades=nk, Avg_profit=avg,
                            Stopped_pct=100 * (T["Exit_Reason"].values[m] == "SL").mean() if nk else np.nan, Size_adjustment=a))
        tiers[c] = (q, np.array(adj))

    def tier_size(c):
        q, adj = tiers[c]
        x = D[c].values
        b = np.clip(np.searchsorted(q, np.nan_to_num(x, nan=np.inf), side="right"), 0, 2)
        raw = np.clip(1 + np.where(np.isfinite(x), adj[b], 0.0), LO, HI)
        s = np.ones(len(D))
        s[traded] = np.clip(raw[traded] / raw[tr].mean(), LO, HI)     # TRAIN average size = 1
        return s

    S = {"Flat (size 1)": np.ones(len(D))}
    for c in MEASURES:
        s = tier_size(c)
        S[f"{c} tiers"] = s
        S[f"{c} tiers [cut only]"] = np.minimum(s, 1.0)
    med = T["VRP20"].median()
    cont = np.ones(len(D))
    v = D["VRP20"].values
    cont[traded] = np.where(np.isfinite(v[traded]), np.clip(v[traded] / med, 0.5, 1.5), 1.0)
    cont[traded] = cont[traded] / cont[tr].mean()
    S["VRP20 continuous (VRP / train median, 0.5-1.5)"] = cont
    low20 = tiers["VRP20"][0][0]
    S["Half size when VRP20 in the TRAIN bottom tercile"] = np.where(traded & np.isfinite(v) & (v < low20), 0.5, 1.0)

    rows = []
    for name, sz in S.items():
        for p in ("Train", "Validation", "Test"):
            st = stats((pnl * sz)[part == p])
            st.update(Candidate=name, Period=p, Avg_size=sz[traded & (part == p)].mean())
            rows.append(st)
    R = pd.DataFrame(rows)[["Candidate", "Period", "Net", "MDD", "Net_per_MDD", "Worst_day", "Trades", "Per_trade", "Avg_size"]]
    V = R[R.Period == "Validation"].set_index("Candidate")
    flat = V.loc["Flat (size 1)"]
    ok = V[(V["Net"] > flat["Net"]) & (V["Net_per_MDD"] > flat["Net_per_MDD"])]
    chosen = ok["Net_per_MDD"].idxmax() if len(ok) else "Flat (size 1)"

    # Does the measure separate good and bad trades consistently in every period?
    cons = []
    for c in MEASURES:
        q, _ = tiers[c]
        for p in ("Train", "Validation", "Test"):
            m = traded & (part == p)
            x = D[c].values[m]
            b = np.searchsorted(q, np.nan_to_num(x, nan=np.inf), side="right")
            y = pnl[m]
            ok_ = np.isfinite(x)
            rho = pd.Series(x[ok_]).rank().corr(pd.Series(y[ok_]).rank()) if ok_.sum() > 2 else np.nan  # Spearman
            row = dict(Measure=c, Period=p, Spearman=rho)
            for k, lab in enumerate(["low", "mid", "high"]):
                sel = (b == k) & np.isfinite(x)
                row[f"{lab}_n"], row[f"{lab}_avg"] = int(sel.sum()), y[sel].mean() if sel.any() else np.nan
                row[f"{lab}_stop_pct"] = 100 * (D["Exit_Reason"].values[m][sel] == "SL").mean() if sel.any() else np.nan
            cons.append(row)
    C = pd.DataFrame(cons)

    cuts = D.groupby("Part")["Date"].agg(["min", "max", "size"]).reindex(["Train", "Validation", "Test"])
    pd.set_option("display.width", 250)
    print(cuts.to_string())
    print(f"\ntraded days with a VRP reading: {int((traded & np.isfinite(v)).sum())} of {int(traded.sum())}; train average profit {mu:.2f}\n")
    print("Tier profit by period (TRAIN tercile edges):")
    print(C.round(3).to_string(index=False))
    print("\nCandidates, validation sorted by profit / drawdown:")
    print(R[R.Period == "Validation"].sort_values("Net_per_MDD", ascending=False).round(2).to_string(index=False))
    print("\nflat:")
    print(R[R.Candidate == "Flat (size 1)"].round(2).to_string(index=False))
    print(f"\nDECISION on validation: {chosen}")
    print(R[(R.Candidate.isin(["Flat (size 1)", chosen])) & (R.Period == "Test")].round(2).to_string(index=False))

    out = os.path.join(HERE, "output", "sizing_vrp_report.xlsx")
    with pd.ExcelWriter(out) as w:
        pd.DataFrame({"Read me": [
            "Opportunity = how rich the IV sold at entry is against the volatility realized up to yesterday (daily closes, same contract).",
            "VRP10 / VRP20 = entry IV / realized vol of the last 10 / 20 days; SPREAD20 = IV - RV20; RV_TREND = RV5 / RV20.",
            "60 / 20 / 20 chronological split. Tier edges and sizes fitted on TRAIN, decided on VALIDATION, reported once on TEST.",
            "Tier size = 0.6 x (tier average - overall average) x n / (n + 40) / overall average (TRAIN), clipped 0.25-1.50, TRAIN average size 1.",
            "Decision rule (fixed in advance): best profit / max drawdown on VALIDATION, only if it beats flat there on profit AND profit / drawdown.",
            f"Decision: {chosen}.",
            "PnL in backtest units (one trade = Rs 25,000 notional). Current-week and next-week legs both sized (each with its own IV).",
        ]}).to_excel(w, sheet_name="Read_me", index=False)
        cuts.to_excel(w, sheet_name="Split")
        C.round(4).to_excel(w, sheet_name="Tiers_by_period", index=False)
        R.round(2).to_excel(w, sheet_name="Candidates", index=False)
        pd.DataFrame(eff).round(4).to_excel(w, sheet_name="Train_tier_sizes", index=False)
        cols = ["Date", "Part", "Leg", "Stage", "IV", "RV5", "RV10", "RV20"] + MEASURES + ["Exit_Reason", "Net_PnL"]
        D[cols].round(5).to_excel(w, sheet_name="Daily_opportunity", index=False)
    print(f"\nwrote {out}")


if __name__ == "__main__":
    main()
