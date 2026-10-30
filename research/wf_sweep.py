"""Dumb vs LUT, walk-forward, with regularised LUTs and a dumb-with-veto hybrid (research only).

Input: output/backtest_profile (GS_PROFILE=1): per day and stage the first minute each
cell occurs, with the production simulator's net PnL there. Dumb 09:16 / 09:18 are the
stage-0 / stage-2 points at 09:16 / 09:18 (same simulator; event days have no 09:16).

Every table is built only from days BEFORE the quarter it trades (refit each quarter,
expanding window). Choice on VALIDATION (2023-01-01 .. 2024-12-31); TEST
(2025-01-01 ..) reported for every row so the choice can be judged.

Books:
  dumb 09:16 / dumb 09:18                         every day, no table
  LUT tax x m, min n                              walkforward.build with taxes T*m and a cell
                                                  needing n trades to count positive
  dumb 09:18 veto mean<v n>=k                     trade 09:18 every day unless the 09:18 cell's
                                                  past average is below v over >= k days

    research/.venv/bin/python research/wf_sweep.py
writes output/walkforward/SWEEP_REPORT.md and sweep.xlsx.
"""
import os
import sys
from collections import defaultdict
from multiprocessing import Pool

import numpy as np
import pandas as pd

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import walkforward as W  # noqa: E402

OUT = os.path.join(HERE, "output", "walkforward")
VAL = (pd.Timestamp("2023-01-01"), pd.Timestamp("2024-12-31"))
TEST_FROM = pd.Timestamp("2025-01-01")
TAX_MULT = [0.5, 1.0, 1.5, 2.0, 3.0]
MIN_TRADES = [1, 3, 5, 10]
VETO = [(v, k) for v in (0.0, -5.0, -10.0) for k in (3, 5, 10)]

DAYS = None


def quarters():
    q = pd.period_range(VAL[0], DAYS["Date"].max(), freq="Q")
    return [(p.start_time, p.end_time) for p in q]


def build_with(days, mult, nmin):
    prof = [defaultdict(lambda: [0.0, 0.0]) for _ in range(5)]
    for P in days["Profile"]:
        for st, th, c, pnl, n, veto, floor in P:
            prof[st][c][0] += pnl
            prof[st][c][1] += n
    raw = []
    for s in range(5):
        tax = W.TAX[s] * mult
        # a positive cell with fewer than nmin trades is set to weight 0 (the builder's own
        # MIN_TRADES_TO_ANCHOR handling): it can no longer pull anything to YES
        prof_s = {c: ([v[1] * tax, v[1]] if (v[0] - v[1] * tax > 0 and v[1] < nmin) else v) for c, v in prof[s].items()}
        raw.append(W.solve(prof_s, tax))
    L18 = raw[2]; L17 = raw[1] & L18; L16 = raw[0] & L17
    L19 = raw[3] & L18; L20 = raw[4] & L19
    return [L16, L17, L18, L19, L20]


def lut_book(args):
    mult, nmin = args
    out = []
    for a, b in quarters():
        past = DAYS[DAYS["Date"] < a]
        cur = DAYS[(DAYS["Date"] >= a) & (DAYS["Date"] <= b)]
        if cur.empty:
            continue
        L = build_with(past, mult, nmin)
        out.append(W.replay(cur, lambda dt, L=L: L))
    return (f"LUT tax x{mult:g}, min {nmin}", pd.concat(out))


def point(P, stage, th):
    for st, t, c, p, n, veto, floor in P:
        if st == stage and t == th:
            return c, p
    return None, None


def dumb(th, stage):
    return pd.Series([point(P, stage, th)[1] or 0.0 for P in DAYS["Profile"]], index=DAYS["Date"].values)


def veto_book(args):
    v, k = args
    vals = []
    for a, b in quarters():
        past = DAYS[DAYS["Date"] < a]
        hist = defaultdict(list)
        for P in past["Profile"]:
            c, p = point(P, 2, 918)
            if c is not None:
                hist[c].append(p)
        cur = DAYS[(DAYS["Date"] >= a) & (DAYS["Date"] <= b)]
        for dt, P in zip(cur["Date"], cur["Profile"]):
            c, p = point(P, 2, 918)
            if c is None:
                vals.append((dt, 0.0)); continue
            h = hist.get(c, [])
            skip = len(h) >= k and np.mean(h) < v
            vals.append((dt, 0.0 if skip else p))
    s = pd.Series(dict(vals))
    return (f"dumb 09:18 veto mean<{v:g} n>={k}", s)


def stats(s):
    c = s.cumsum()
    mdd = float((c.cummax() - c).max()) if len(c) else 0.0
    t = s[s != 0]
    return dict(net=round(float(s.sum()), 1), trades=int(len(t)), win=round(100 * float((t > 0).mean()), 1) if len(t) else np.nan,
                avg=round(float(t.mean()), 2) if len(t) else np.nan, mdd=round(mdd, 1), net_mdd=round(float(s.sum()) / mdd, 2) if mdd > 0 else np.nan)


def init(d):
    global DAYS
    DAYS = d


def main():
    d = W.load_days()
    init(d)
    books = {"dumb 09:16": dumb(916, 0), "dumb 09:18": dumb(918, 2)}
    jobs = [(m, n) for m in TAX_MULT for n in MIN_TRADES]
    with Pool(int(os.environ.get("SWEEP_WORKERS", "6")), initializer=init, initargs=(d,)) as pool:
        for name, s in pool.imap_unordered(lut_book, jobs):
            books[name] = s
            print("done", name, flush=True)
        for name, s in pool.imap_unordered(veto_book, VETO):
            books[name] = s
            print("done", name, flush=True)
    rows = []
    for name, s in books.items():
        s = s.sort_index()
        r = dict(book=name)
        for part, (a, b) in (("val", VAL), ("test", (TEST_FROM, pd.Timestamp("2100-01-01")))):
            r.update({f"{part}_{k}": v for k, v in stats(s[(s.index >= a) & (s.index <= b)]).items()})
        rows.append(r)
    res = pd.DataFrame(rows).sort_values("val_net_mdd", ascending=False)
    base = res.set_index("book")
    best = res.iloc[0]
    lines = [f"# Dumb vs LUT, walk-forward (quarterly refit on the past only)\n\nDays {d['Date'].min().date()} .. {d['Date'].max().date()}; "
             f"VALIDATION {VAL[0].date()} .. {VAL[1].date()}, TEST {TEST_FROM.date()} .. end. CW book, units backtest Rs per 25k.\n\n"
             f"Chosen on VALIDATION (best net / MDD): **{best['book']}**.\n\n```\n",
             res[["book", "val_net", "val_trades", "val_win", "val_mdd", "val_net_mdd", "test_net", "test_trades", "test_win", "test_avg", "test_mdd", "test_net_mdd"]].to_string(index=False),
             "\n```\n"]
    with pd.ExcelWriter(os.path.join(OUT, "sweep.xlsx")) as xw:
        res.to_excel(xw, sheet_name="books", index=False)
        pd.DataFrame({k: v.sort_index() for k, v in books.items()}).to_excel(xw, sheet_name="daily")
    open(os.path.join(OUT, "SWEEP_REPORT.md"), "w").write("".join(lines))
    print("".join(lines))


if __name__ == "__main__":
    main()
