"""Drawdown-control policies on the GammaShort straddle book (research only).

Input: the production PnL of every day in output/backtest_basket/run_*/days/*.pkl
(identical to the reference production run on every shared date).

Policies (all decided from results known before the day):
  pause    after N losing trades in a row, skip the next K trading days
  equity   trade only while the shadow equity (every trade at size 1, traded or not)
           is at / above its average over the last M trades
  halve    half size after a losing trade until the next winning one
  ddsched  size by the drawdown of the SIZED book: the live schedule
           (<100: 1, <135: 0.75, <170: 0.5, else 0.25), a gentler one (floor 0.5), one cut

Choice on VALIDATION (owner's protocol): the best net / max drawdown among the policies
that beat flat size on BOTH net and net / MDD there; TEST reported once. Each period is
replayed from a clean start (equity and drawdown reset at the period's first day).

    research/.venv/bin/python research/dd_control.py
writes output/dd_control/REPORT.md and dd_control.xlsx.
"""
import glob
import os
import pickle

import numpy as np
import pandas as pd

HERE = os.path.dirname(os.path.abspath(__file__))
OUT = os.path.join(HERE, "output", "dd_control")
TRAIN_END, VAL_END = pd.Timestamp("2024-06-20"), pd.Timestamp("2025-08-13")


def load():
    f = sorted(glob.glob(os.path.join(HERE, "output", "backtest_basket", "run_*")))[-1]
    d = pd.DataFrame([pickle.load(open(x, "rb")) for x in glob.glob(os.path.join(f, "days", "*.pkl"))])
    d["Date"] = pd.to_datetime(d["Date"])
    d = d.sort_values("Date")
    d = d[d["Leg"].isin(["CW", "NW"])]       # traded days only: a policy acts trade to trade
    d["part"] = np.where(d["Date"] <= TRAIN_END, "train", np.where(d["Date"] <= VAL_END, "val", "test"))
    return d[["Date", "part", "Net_PnL"]].reset_index(drop=True)


def replay(p, size_of):
    """p: trade PnL at size 1, in order. size_of(state) -> size for the next trade."""
    st = dict(eq=0.0, peak=0.0, shadow=[], losses=0, pause=0, last=None)
    out = np.zeros(len(p))
    for i, x in enumerate(p):
        sz = size_of(st)
        out[i] = sz * x
        st["eq"] += sz * x
        st["peak"] = max(st["peak"], st["eq"])
        st["shadow"].append(x)
        st["losses"] = st["losses"] + 1 if x < 0 else 0
        st["last"] = x
        if st["pause"] > 0:
            st["pause"] -= 1
    return out


def policies():
    pol = {"flat": lambda st: 1.0}
    for n in (2, 3):
        for k in (1, 2, 3, 5):
            def f(st, n=n, k=k):
                if st["pause"] > 0:
                    return 0.0
                if st["losses"] >= n:
                    st["pause"] = k            # skip k trades from now (this one included)
                    st["losses"] = 0
                    return 0.0
                return 1.0
            pol[f"pause {n} losses -> skip {k}"] = f
    for m in (10, 20, 40):
        def g(st, m=m):
            sh = st["shadow"]
            if len(sh) < m:
                return 1.0
            c = np.cumsum(sh)
            return 1.0 if c[-1] >= c[-m:].mean() else 0.0
        pol[f"equity >= avg of last {m}"] = g
    pol["halve after a loss"] = lambda st: 0.5 if (st["last"] is not None and st["last"] < 0) else 1.0
    sched = {"live DD schedule 100/135/170": [(100, 1.0), (135, 0.75), (170, 0.5), (1e9, 0.25)],
             "gentler DD schedule (floor 0.5)": [(120, 1.0), (170, 0.75), (1e9, 0.5)],
             "one cut: 0.5 beyond 150": [(150, 1.0), (1e9, 0.5)]}
    for name, steps in sched.items():
        def h(st, steps=steps):
            dd = st["peak"] - st["eq"]
            return next(sz for lim, sz in steps if dd < lim)
        pol[name] = h
    return pol


def stats(x):
    c = np.cumsum(x)
    mdd = float((np.maximum.accumulate(c) - c).max()) if len(c) else 0.0
    net = float(c[-1]) if len(c) else 0.0
    return dict(net=round(net, 1), mdd=round(mdd, 1), net_mdd=round(net / mdd, 2) if mdd > 0 else np.nan,
                trades=int((x != 0).sum()), avg_size=None)


def main():
    os.makedirs(OUT, exist_ok=True)
    d = load()
    rows = []
    for name, fn in policies().items():
        r = dict(policy=name)
        for part in ("train", "val", "test"):
            p = d[d["part"] == part]["Net_PnL"].fillna(0).values
            x = replay(p, fn)
            s = stats(x)
            r.update({f"{part}_{k}": v for k, v in s.items() if k != "avg_size"})
            r[f"{part}_size"] = round(float(np.mean(np.where(p != 0, x / np.where(p == 0, 1, p), 0))), 2)
        rows.append(r)
    res = pd.DataFrame(rows)
    base = res[res["policy"] == "flat"].iloc[0]
    ok = res[(res["policy"] != "flat") & (res["val_net"] > base["val_net"]) & (res["val_net_mdd"] > base["val_net_mdd"])]
    ok = ok.sort_values("val_net_mdd", ascending=False)
    choice = ok.iloc[0]["policy"] if len(ok) else "flat"
    show = ["policy", "train_net", "train_mdd", "train_net_mdd", "val_net", "val_mdd", "val_net_mdd", "val_trades", "val_size"]
    lines = [f"# Drawdown control on the straddle book\n\nTrades: {len(d)} ({d['Date'].min().date()} to {d['Date'].max().date()}); "
             f"each period replayed from a clean start. Units: backtest Rs per 25k notional.\n\n```\n",
             res[show].to_string(index=False), "\n```\n\n",
             f"Beat flat on VALIDATION net and net/MDD: {len(ok)} of {len(res) - 1}.\n\nChoice: **{choice}**.\n\n"]
    t = res[res["policy"] == choice].iloc[0]
    tb = res[res["policy"] == "flat"].iloc[0]
    lines.append(f"TEST (reported once): {choice}: net {t['test_net']}, MDD {t['test_mdd']}, net/MDD {t['test_net_mdd']}, "
                 f"trades {t['test_trades']}; flat: net {tb['test_net']}, MDD {tb['test_mdd']}, net/MDD {tb['test_net_mdd']}.\n")
    res.to_excel(os.path.join(OUT, "dd_control.xlsx"), index=False)
    open(os.path.join(OUT, "REPORT.md"), "w").write("".join(lines))
    print("".join(lines))


if __name__ == "__main__":
    main()
