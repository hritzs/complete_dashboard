"""
Sizing equation for the GammaShort book, fitted on early years and judged on
later years.

Reads the backtest run in research/output/backtest_final/run_*/days/*.pkl
(one record per day, written by GammaShort_Backtest_Final.py) and writes
research/output/sizing_equation_report.xlsx.  Nothing outside research/ is
touched.

    size = clip( 1 + DAMP x sum_f ( effect_f(bin of feature f) / average ), LO, HI )   x   drawdown factor

  effect_f(bin) = (average PnL of training trades in that bin - overall average),
                  shrunk towards 0 by n / (n + SHRINK) so thin bins count less.
  Every input is known at the entry minute (which table said YES, the six
  table inputs) or before the day starts (drawdown).  Sizes are rescaled so
  the average training size is 1.0: the comparison with flat sizing uses the
  same average capital.

    ./.venv/bin/python sizing_equation.py [train_end=2023-12-31]
"""
import glob
import os
import pickle
import sys

import numpy as np
import pandas as pd

HERE = os.path.dirname(os.path.abspath(__file__))
TRAIN_END = sys.argv[1] if len(sys.argv) > 1 else "2023-12-31"
LO, HI = 0.25, 1.50
SHRINK = 40.0        # a bin needs ~40 trades to get half its raw effect
DAMP = 0.6           # effects are added at 60 %: the inputs overlap

# feature -> bin edges (fixed by hand from the strategy's own bins, not searched)
BINS = {
    "IV_Ratio":  [0.90, 1.00, 1.10, 1.20, 1.35],
    "Str_Ratio": [0.95, 1.00, 1.05, 1.10, 1.20],
    "Build_IV":  [0.12, 0.14, 0.17, 0.20],
    "DTE":       [1.5, 2.5, 3.5, 4.5],            # days to expiry 1,2,3,4,5+
    "Table":     [0.5, 1.5, 2.5],                 # 09:16, 09:17, 09:18, later
}
LABELS = {
    "IV_Ratio":  ["<0.90", "0.90-1.00", "1.00-1.10", "1.10-1.20", "1.20-1.35", ">1.35"],
    "Str_Ratio": ["<0.95", "0.95-1.00", "1.00-1.05", "1.05-1.10", "1.10-1.20", ">1.20"],
    "Build_IV":  ["<0.12", "0.12-0.14", "0.14-0.17", "0.17-0.20", ">0.20"],
    "DTE":       ["1 (expiry)", "2", "3", "4", "5+"],
    "Table":     ["09:16", "09:17", "09:18", "09:19 / later"],
}


def load():
    files = sorted(glob.glob(os.path.join(HERE, "output", "backtest_final", "run_*", "days", "*.pkl")))
    D = pd.DataFrame([pickle.load(open(f, "rb")) for f in files]).sort_values("Date").reset_index(drop=True)
    D["Net_PnL"] = D["Net_PnL"].fillna(0.0)
    D["Leg"] = D["Leg"].fillna("")
    D["DTE"] = D["DTE_idx"] + 1
    D["Table"] = D["Stage"].map({"09:16": 0, "09:17": 1, "09:18": 2}).where(D["Leg"] == "CW").fillna(3)
    return D


def bin_of(col, s):
    return np.searchsorted(BINS[col], s.astype(float).values, side="right")


def fit(T):
    """Shrunk bin effects from training trades."""
    mu = T["Net_PnL"].mean()
    eff = {}
    for col in BINS:
        b = bin_of(col, T[col])
        rows = []
        for k in range(len(BINS[col]) + 1):
            x = T["Net_PnL"].values[b == k]
            n = len(x)
            raw = (x.mean() - mu) if n else 0.0
            rows.append(dict(bin=LABELS[col][k], n=n, avg=x.mean() if n else np.nan,
                             stop_pct=100 * (T["Exit_Reason"].values[b == k] == "SL").mean() if n else np.nan,
                             effect=raw * n / (n + SHRINK)))
        eff[col] = pd.DataFrame(rows)
    return mu, eff


def raw_size(T, mu, eff, cols):
    s = np.ones(len(T))
    for col in cols:
        s += DAMP * eff[col]["effect"].values[bin_of(col, T[col])] / mu
    return np.clip(s, LO, HI)


def stats(pnl):
    pnl = np.asarray(pnl, float)
    cum = np.cumsum(pnl)
    hw = np.maximum(np.maximum.accumulate(cum), 0.0)
    mdd = float((hw - cum).max()) if len(cum) else 0.0
    tr = pnl[pnl != 0]
    return dict(Net=pnl.sum(), MDD=mdd, Net_per_MDD=pnl.sum() / mdd if mdd else np.nan,
                Worst_day=pnl.min() if len(pnl) else 0.0, Per_trade=tr.mean() if len(tr) else 0.0,
                Win_pct=100 * (tr > 0).mean() if len(tr) else 0.0)


def dd_factor(flat_pnl, mode):
    """Size multiplier per day from the FLAT book's own history up to yesterday."""
    out = np.ones(len(flat_pnl))
    cum = hw = 0.0
    streak = 0
    for i, x in enumerate(flat_pnl):
        if mode == "after 3 losing days: 0.5":
            out[i] = 0.5 if streak >= 3 else 1.0
        elif mode == "drawdown > 100: 0.5":
            out[i] = 0.5 if hw - cum > 100 else 1.0
        cum += x
        hw = max(hw, cum)
        streak = streak + 1 if x < 0 else (0 if x > 0 else streak)
    return out


def main():
    D = load()
    cw = D["Leg"] == "CW"
    train = D["Date"] <= TRAIN_END
    T = D[cw & train]
    mu, eff = fit(T)
    print(f"days {len(D)} ({D.Date.iloc[0]} -> {D.Date.iloc[-1]}); train to {TRAIN_END}: {int(train.sum())} days, "
          f"{len(T)} CW trades, avg {mu:.2f}; test: {int((~train).sum())} days, {int((cw & ~train).sum())} CW trades")

    variants = {
        "Flat (size 1)": [],
        "Table only": ["Table"],
        "Table + IV ratio": ["Table", "IV_Ratio"],
        "Table + IV ratio + straddle ratio": ["Table", "IV_Ratio", "Str_Ratio"],
        "All five inputs": ["Table", "IV_Ratio", "Str_Ratio", "Build_IV", "DTE"],
    }
    sizes = {}
    for name, cols in variants.items():
        s = np.ones(len(D))
        if cols:
            r = raw_size(D[cw], mu, eff, cols)
            norm = r[train[cw].values].mean()             # average training size = 1
            s[cw.values] = np.clip(r / norm, LO, HI)
        sizes[name] = s

    rows = []
    for name, s in sizes.items():
        for part, m in (("Train", train.values), ("Test", ~train.values)):
            st = stats((D["Net_PnL"].values * s)[m])
            st.update(Variant=name, Period=part, Avg_size=s[cw.values & m].mean() if (cw.values & m).any() else 1.0)
            rows.append(st)
    R = pd.DataFrame(rows)[["Variant", "Period", "Net", "MDD", "Net_per_MDD", "Worst_day", "Per_trade", "Win_pct", "Avg_size"]]
    print("\n" + R.round(2).to_string(index=False))

    # is the test gain more than luck?  shuffle the test sizes among the test trades
    best = "All five inputs"
    m = cw.values & ~train.values
    rng = np.random.default_rng(7)
    pn, sz = D["Net_PnL"].values[m], sizes[best][m]
    real = (pn * sz).sum()
    sims = np.array([(pn * rng.permutation(sz)).sum() for _ in range(5000)])
    pct = 100 * (sims < real).mean()
    print(f"\nshuffle check ({best}, test trades): real {real:.1f}, shuffled mean {sims.mean():.1f}, "
          f"real beats {pct:.1f}% of 5000 shuffles")

    # drawdown factor on top
    dd_rows = []
    for mode in ("none", "after 3 losing days: 0.5", "drawdown > 100: 0.5"):
        f = np.ones(len(D)) if mode == "none" else dd_factor(D["Net_PnL"].values, mode)
        for name in ("Flat (size 1)", best):
            for part, mm in (("Train", train.values), ("Test", ~train.values)):
                st = stats((D["Net_PnL"].values * sizes[name] * f)[mm])
                st.update(Variant=name, Drawdown_rule=mode, Period=part)
                dd_rows.append(st)
    DDR = pd.DataFrame(dd_rows)[["Variant", "Drawdown_rule", "Period", "Net", "MDD", "Net_per_MDD", "Worst_day"]]
    print("\n" + DDR[DDR.Period == "Test"].round(1).to_string(index=False))

    by_fy = D.assign(Flat=D["Net_PnL"], Sized=D["Net_PnL"] * sizes[best]).groupby("FY")[["Flat", "Sized"]].sum()
    by_fy["Period"] = ["Train" if d <= TRAIN_END else "Test" for d in D.groupby("FY")["Date"].max()]
    print("\n" + by_fy.round(1).to_string())

    out = os.path.join(HERE, "output", "sizing_equation_report.xlsx")
    with pd.ExcelWriter(out) as w:
        note = pd.DataFrame({"Sizing equation": [
            "size = clip(1 + 0.6 x sum of (effect of each input's bin / average trade), 0.25, 1.50), rescaled so the average training size is 1",
            f"Fitted on current-week trades up to {TRAIN_END} ({len(T)} trades, average {mu:.2f}); judged on later days without refitting.",
            "effect = (bin average - overall average) x n / (n + 40).  Inputs: which table said YES, IV ratio, straddle ratio, Build IV, days to expiry.",
            "Next-week trades keep size 1.  PnL in backtest units (one trade = Rs 25,000 notional).",
            f"Shuffle check on test trades: the equation's sizes beat {pct:.1f}% of 5,000 random re-assignments of the same sizes.",
            "Source: research/output/backtest_final (run of GammaShort_Backtest_Final.py on the share's tick files, r46V2 tables).",
        ]})
        note.to_excel(w, sheet_name="Read_me", index=False)
        R.round(2).to_excel(w, sheet_name="Flat_vs_sized", index=False)
        DDR.round(2).to_excel(w, sheet_name="With_drawdown_rule", index=False)
        by_fy.round(1).to_excel(w, sheet_name="By_FY")
        for col, df in eff.items():
            d2 = df.copy()
            d2["size_adjustment"] = (DAMP * d2["effect"] / mu).round(3)
            d2.round(2).to_excel(w, sheet_name=f"Effect_{col}", index=False)
        daily = D[["Date", "FY", "Leg", "Stage", "DTE", "IV_Ratio", "Str_Ratio", "Build_IV", "Exit_Reason", "Net_PnL"]].copy()
        daily["Size"] = sizes[best].round(3)
        daily["Sized_PnL"] = (daily["Net_PnL"] * daily["Size"]).round(2)
        daily["Period"] = np.where(train, "Train", "Test")
        daily.to_excel(w, sheet_name="Daily", index=False)
    print(f"\nsaved {out}")
    for col, df in eff.items():
        print(f"\n{col}\n" + df.assign(size_adj=(DAMP * df['effect'] / mu)).round(2).to_string(index=False))


if __name__ == "__main__":
    main()
