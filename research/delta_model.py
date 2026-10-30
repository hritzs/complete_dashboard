"""Combined-signal, regime-aware delta (long / short) model, walk-forward (research only).

Input: output/delta_study/decisions.parquet + daily.parquet (delta_build.py); regimes and
IDV inputs as delta_rules.py.

Model: per decision time and outcome, ridge regression of the LONG outcome (bps) on the
inputs, the regime dummies, and every input x regime dummy (so one model can follow in a
trend and fade in a range). Inputs winsorised at the training 1 / 99 % and standardised
with training statistics. Walk-forward: the model for year Y is fitted on all rows before
1 Jan Y only (expanding window); every prediction is out-of-sample.

Trade: long when the prediction > thr, short when < -thr (each side's own net outcome).
Choice on VALIDATION (2024-06-21 .. 2025-08-13) over time x outcome x ridge strength x
threshold: best net / MDD with >= 30 trades and positive net; TEST (2025-08-14 ..)
reported once. The number of configurations tried is reported.

    research/.venv/bin/python research/delta_model.py
writes output/delta_study/MODEL_REPORT.md and model.xlsx.
"""
import os
import sys

import numpy as np
import pandas as pd

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import delta_rules as R  # noqa: E402

DS = os.path.join(HERE, "output", "delta_study")
BASE = ["r1", "r5", "r15", "r30", "r60", "r_open", "pos_range", "range_bps", "orb", "rv_iv", "iv_chg", "st_chg", "skew",
        "gap", "prev_ret", "idv", "idv_chg", "adj_iv_chg", "dte"]
DUMMIES = {"trend_up": ("trend", "up"), "trend_down": ("trend", "down"), "ivp_low": ("ivp", "low"), "ivp_high": ("ivp", "high"),
           "vrp_rich": ("vrp", "rich"), "comp_tight": ("comp", "compressed"), "comp_wide": ("comp", "expanded"),
           "idv_rising": ("idvc", "rising"), "expiry_wk": ("expw", "yes")}
OUTCOMES = ["F_{}_day", "F_{}_sl25tp50", "F_{}_sl40tp80", "F_{}_nextam", "F_{}_nextpm", "O_{}_day", "O_{}_nextam", "O_{}_nextpm"]
ALPHAS = [1.0, 10.0, 100.0]      # x number of training rows
THRESH = [0.0, 2.0, 5.0, 10.0]   # bps of predicted long return
VAL = (pd.Timestamp("2024-06-21"), pd.Timestamp("2025-08-13"))


def design(g, cols_stats=None):
    X = pd.DataFrame(index=g.index)
    for f in BASE:
        X[f] = g[f].astype(float)
    for name, (reg, st) in DUMMIES.items():
        X[name] = (g[reg].astype(str) == st).astype(float)
    for f in BASE:
        for name in DUMMIES:
            X[f"{f}*{name}"] = X[f] * X[name]
    return X


def fit_predict(Xtr, ytr, Xte, alpha):
    lo, hi = Xtr.quantile(0.01), Xtr.quantile(0.99)
    Xtr = Xtr.clip(lo, hi, axis=1).fillna(Xtr.median())
    Xte = Xte.clip(lo, hi, axis=1).fillna(Xtr.median())
    mu, sd = Xtr.mean(), Xtr.std().replace(0, 1)
    A = ((Xtr - mu) / sd).values
    B = ((Xte - mu) / sd).values
    ym = ytr.mean()
    n, k = A.shape
    w = np.linalg.solve(A.T @ A + alpha * n * np.eye(k), A.T @ (ytr.values - ym))
    return B @ w + ym


def stats(p):
    c = p.cumsum()
    mdd = float((c.cummax() - c).max()) if len(c) else 0.0
    t = p[p != 0]
    return dict(net=round(float(p.sum()), 1), trades=int(len(t)), win=round(100 * float((t > 0).mean()), 1) if len(t) else np.nan,
                avg=round(float(t.mean()), 2) if len(t) else np.nan, mdd=round(mdd, 1),
                net_mdd=round(float(p.sum()) / mdd, 2) if mdd > 0 else np.nan)


def main():
    d = pd.read_parquet(os.path.join(DS, "decisions.parquet"))
    d["date"] = pd.to_datetime(d["date"])
    d = d.join(R.regimes(pd.read_parquet(os.path.join(DS, "daily.parquet"))), on="date")
    d["expw"] = np.where(d["dte"] <= 2, "yes", "no")
    years = sorted(d["date"].dt.year.unique())
    rows, preds = [], {}
    for t, g in d.groupby("t"):
        g = g.sort_values("date")
        X = design(g)
        for oc in OUTCOMES:
            L, S = oc.format("L"), oc.format("S")
            if L not in g:
                continue
            ok = g[L].notna() & g[S].notna()
            for alpha in ALPHAS:
                pred = pd.Series(np.nan, index=g.index)
                for y in years[1:]:
                    tr = ok & (g["date"].dt.year < y)
                    te = g["date"].dt.year == y
                    if tr.sum() < 150 or not te.any():
                        continue
                    pred[te] = fit_predict(X[tr], g.loc[tr, L], X[te], alpha)
                preds[(t, oc, alpha)] = pred
                for thr in THRESH:
                    pnl = pd.Series(0.0, index=g.index)
                    lm, sm = ok & (pred > thr), ok & (pred < -thr)
                    pnl[lm] = g.loc[lm, L]
                    pnl[sm] = g.loc[sm, S]
                    pnl.index = g["date"].values
                    r = dict(time=t, outcome=oc.format("x"), alpha=alpha, thr=thr)
                    for name, (a, b) in (("train", (pd.Timestamp("2022-01-01"), pd.Timestamp("2024-06-20"))), ("val", VAL),
                                         ("test", (pd.Timestamp("2025-08-14"), pd.Timestamp("2100-01-01")))):
                        s = pnl[(pnl.index >= a) & (pnl.index <= b)]
                        r.update({f"{name}_{k}": v for k, v in stats(s).items()})
                    rows.append(r)
    res = pd.DataFrame(rows)
    cand = res[(res["val_trades"] >= 30) & (res["val_net"] > 0)].sort_values("val_net_mdd", ascending=False)
    tried = len(res)
    hide = [c for c in res.columns if c.startswith("test_")]
    lines = [f"# Combined-signal regime delta model (walk-forward)\n\nConfigurations tried: {tried} "
             f"(11 times x {len(OUTCOMES)} outcomes x {len(ALPHAS)} ridge strengths x {len(THRESH)} thresholds). "
             f"Positive on VALIDATION with >= 30 trades: {len(cand)} ({100 * len(cand) / max(tried, 1):.0f} %). "
             "All predictions out-of-sample (yearly expanding refit). Units: bps of the underlying per trade, net.\n\n"
             "'train' = the out-of-sample predictions of 2022 - Jun 2024 (each year from a model fitted on the years before).\n\n"]
    if len(cand):
        top = cand.head(12)
        lines.append("## Top on VALIDATION (chosen = first row; TEST shown for all 12 so luck can be judged)\n\n```\n" +
                     top[["time", "outcome", "alpha", "thr", "train_net", "train_trades", "val_net", "val_trades", "val_win", "val_mdd",
                          "val_net_mdd", "test_net", "test_trades", "test_win", "test_avg", "test_mdd"]].to_string(index=False) + "\n```\n")
        lines.append(f"\nAll {len(cand)} validation-positive configurations on TEST: {int((cand['test_net'] > 0).sum())} positive, "
                     f"median test net {cand['test_net'].median():+.1f}, median test avg {cand['test_avg'].median():+.2f} bps.\n")
    res.to_excel(os.path.join(DS, "model.xlsx"), index=False)
    open(os.path.join(DS, "MODEL_REPORT.md"), "w").write("".join(lines))
    print("".join(lines))


if __name__ == "__main__":
    main()
