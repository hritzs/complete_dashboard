"""Walk-forward test of the Profiler (LUT) tables (research only).

The live tables are built from every day up to the build date (the builder's
out-of-sample window, Liveprofilersupdate.py PROFILER_SKIP_*, is set in 2027),
so any backtest over that history is in-sample. Here the tables are rebuilt the
builder's way from a chosen span of days only and traded on days they never saw.

Training data: output/backtest_profile/run_*/days/*.pkl (GS_PROFILE=1): per day and
stage (0 = 09:16, 1 = 09:17, 2 = 09:18, 3 = 09:19, 4 = 09:20+), the first minute each
cell occurs, with the production simulator's net PnL there (event days at a third).

Builder (as Liveprofilersupdate.py, production settings from the r46V2 README):
  per stage, a cell's weight = PnL - tax x trades, taxes T = (13, 5, 2, 2, 6);
  YES cells = min-cut closure on the monotone 6-D graph (ranks as the builder's; networkx's
  partition, as the builder: a cell is NO only when it can reach the sink -- unconstrained
  cells come out YES, exactly as in the production tables);
  nesting L16 <= L17 <= L18 >= L19 >= L20.
  Not reproduced: the README's tax floor (-3) and "evidence gating", whose exact
  rules are not in the script; the NW leg (the CW book is compared).

Replay: per day, the first scanned minute (in time) whose cell is YES in its stage's
table, skipping vetoed / floored minutes -- the production decision on the CW book.

Books over the test span (default 2025-01-01 .. last day):
  A  tables rebuilt on ALL days                          (in-sample, like production)
  B  tables rebuilt on days before 2025-01-01 only       (out-of-sample)
  C  tables rebuilt every month on all days before it    (out-of-sample, as live would)
  D  the live tables, Profilers/12102026                 (in-sample)
  E  the research backtest's r46V2 tables                (in-sample)

    research/.venv/bin/python research/walkforward.py
writes output/walkforward/REPORT.md and walkforward.xlsx.
"""
import glob
import itertools
import os
import pickle
import sys
from collections import defaultdict

import networkx as nx
import numpy as np
import pandas as pd

HERE = os.path.dirname(os.path.abspath(__file__))
OUT = os.path.join(HERE, "output", "walkforward")
DIMS = (7, 10, 12, 7, 3, 3)
TAX = (13.0, 5.0, 2.0, 2.0, 6.0)
TEST_FROM = pd.Timestamp(os.environ.get("WF_TEST_FROM", "2025-01-01"))
LIVE_DIR = "/mnt/shared/Hardik/Profilers/12102026"
R46_DIR = "/mnt/shared/GammaShortData/GammaShort/LUTS_r46V2_cwnw_adj007_005_T13_5_2_2_6"
STAGE_FILES = ["LUT_0916.csv", "LUT_0917.csv", "LUT_0918.csv", "LUT_0919.csv", "LUT_0920_Onwards.csv"]


def load_days():
    run = sorted(glob.glob(os.path.join(HERE, "output", "backtest_profile", "run_*")))
    if not run:
        sys.exit("no output/backtest_profile/run_* -- run GS_PROFILE=1 GammaShort_Backtest_Final.py first")
    recs = []
    for f in glob.glob(os.path.join(run[-1], "days", "*.pkl")):
        r = pickle.load(open(f, "rb"))
        if r.get("Profile"):
            recs.append(dict(Date=pd.Timestamp(r["Date"]), Profile=r["Profile"], Leg=r.get("Leg", ""), Net_PnL=r.get("Net_PnL")))
    d = pd.DataFrame(recs).sort_values("Date").reset_index(drop=True)
    return d


# ---- the builder's graph: ranks as Liveprofilersupdate.py, sized to the production dims
NB = DIMS


def rank_of(c):
    d, i, s, b, og, a = c
    return (d + 1, NB[1] - i, s + 1, NB[3] - b, {0: 2, 1: 1, 2: 3}[og], {0: 2, 1: 1, 2: 3}[a])


def coord_of(r):
    og = {1: 1, 2: 0, 3: 2}[r[4]]
    a = {1: 1, 2: 0, 3: 2}[r[5]]
    return (r[0] - 1, NB[1] - r[1], r[2] - 1, NB[3] - r[3], og, a)


def base_graph():
    G = nx.DiGraph()
    BIG = 10 ** 12
    for r in itertools.product(*(range(1, n + 1) for n in NB)):
        for dim in (0, 1, 2, 3):
            if r[dim] > 1:
                nb = list(r); nb[dim] -= 1
                G.add_edge(r, tuple(nb), capacity=BIG)
        if r[4] in (2, 3):
            nb = list(r); nb[4] = 1
            G.add_edge(r, tuple(nb), capacity=BIG)
        if r[5] in (2, 3):
            nb = list(r); nb[5] = 1
            G.add_edge(r, tuple(nb), capacity=BIG)
    return G


BASE = None


def solve(profile, tax):
    """profile: coord -> [pnl, trades]. YES set as a bool array."""
    global BASE
    if BASE is None:
        BASE = base_graph()
    lut = np.zeros(DIMS, dtype=bool)
    G = BASE.copy()
    pos = False
    for c, (pnl, n) in profile.items():
        w = int(round((pnl - n * tax) * 100))
        r = rank_of(c)
        if w > 0 and n < 1:     # MIN_TRADES_TO_ANCHOR = 1, as the builder
            w = 0
        if w > 0:
            G.add_edge("SRC", r, capacity=w); pos = True
        elif w < 0:
            G.add_edge(r, "SNK", capacity=-w)
        else:                   # an observed cell that earns exactly its tax: as the builder, a unit edge to the sink
            G.add_edge(r, "SNK", capacity=1)
    if not pos:
        return lut
    G.add_node("SNK")
    _, (reach, _) = nx.minimum_cut(G, "SRC", "SNK", flow_func=nx.algorithms.flow.boykov_kolmogorov)
    for r in reach:
        if r != "SRC":
            lut[coord_of(r)] = True
    return lut


def build(days):
    prof = [defaultdict(lambda: [0.0, 0.0]) for _ in range(5)]
    for P in days["Profile"]:
        for st, th, c, pnl, n, veto, floor in P:
            prof[st][c][0] += pnl
            prof[st][c][1] += n
    raw = [solve(prof[s], TAX[s]) for s in range(5)]
    L18 = raw[2]; L17 = raw[1] & L18; L16 = raw[0] & L17
    L19 = raw[3] & L18; L20 = raw[4] & L19
    return [L16, L17, L18, L19, L20]


def load_csv_tables(lut_dir):
    out = []
    for name in STAGE_FILES:
        t = pd.read_csv(os.path.join(lut_dir, name), dtype=str)
        labels = [list(dict.fromkeys(t[c])) for c in t.columns[:6]]
        lut = np.zeros(tuple(len(x) for x in labels), dtype=bool)
        for r in t[t["Trade"].str.upper() == "YES"].itertuples(index=False):
            lut[tuple(labels[i].index(r[i]) for i in range(6))] = True
        if lut.shape != DIMS:
            sys.exit(f"{lut_dir}/{name}: shape {lut.shape}, expected {DIMS}")
        out.append(lut)
    return out


def replay(days, luts_for):
    """luts_for(date) -> 5 tables. PnL per day of the first YES minute (CW book)."""
    pnl = []
    for dt, P in zip(days["Date"], days["Profile"]):
        L = luts_for(dt)
        take = 0.0
        for st, th, c, p, n, veto, floor in sorted(P, key=lambda x: (x[1], x[0])):
            if veto or floor:
                continue
            if L[st][c]:
                take = p
                break
        pnl.append(take)
    return pd.Series(pnl, index=days["Date"].values)


def stats(s):
    c = s.cumsum()
    mdd = float((c.cummax() - c).max())
    t = s[s != 0]
    return dict(net=round(float(s.sum()), 1), trades=int(len(t)), win=round(100 * float((t > 0).mean()), 1) if len(t) else np.nan,
                avg=round(float(t.mean()), 2) if len(t) else np.nan, mdd=round(mdd, 1), net_mdd=round(float(s.sum()) / mdd, 2) if mdd > 0 else np.nan)


def main():
    os.makedirs(OUT, exist_ok=True)
    d = load_days()
    test = d[d["Date"] >= TEST_FROM].reset_index(drop=True)
    print(f"{len(d)} days with profiles ({d['Date'].min().date()} .. {d['Date'].max().date()}); test span {len(test)} days from {TEST_FROM.date()}", flush=True)
    books = {}
    print("A: building on all days ...", flush=True)
    A = build(d)
    books["A  rebuilt on ALL days (in-sample)"] = replay(test, lambda dt: A)
    print("B: building on days before the test span ...", flush=True)
    B = build(d[d["Date"] < TEST_FROM])
    books["B  rebuilt on 2021-2024 only (out-of-sample)"] = replay(test, lambda dt: B)
    months = sorted(test["Date"].dt.to_period("M").unique())
    cache = {}
    for m in months:
        start = m.to_timestamp()
        print(f"C: building for {m} on days before {start.date()} ...", flush=True)
        cache[m] = build(d[d["Date"] < start])
    books["C  rebuilt every month on the past (out-of-sample)"] = replay(test, lambda dt: cache[pd.Timestamp(dt).to_period("M")])
    for name, lut_dir in (("D  live tables Profilers/12102026 (in-sample)", LIVE_DIR), ("E  research r46V2 tables (in-sample)", R46_DIR)):
        try:
            T = load_csv_tables(lut_dir)
            books[name] = replay(test, lambda dt, T=T: T)
        except SystemExit as e:
            print(f"skipped {name}: {e}")
    rows = [dict(book=k, **stats(v)) for k, v in books.items()]
    res = pd.DataFrame(rows)
    monthly = pd.DataFrame({k[:1]: v for k, v in books.items()})
    monthly.index = pd.to_datetime(monthly.index)
    monthly = monthly.groupby(monthly.index.to_period("M")).sum().round(1)
    yes_cells = pd.DataFrame({"stage": ["09:16", "09:17", "09:18", "09:19", "09:20+"],
                              "A_all": [int(x.sum()) for x in A], "B_to2024": [int(x.sum()) for x in B]})
    with pd.ExcelWriter(os.path.join(OUT, "walkforward.xlsx")) as xw:
        res.to_excel(xw, sheet_name="books", index=False)
        monthly.to_excel(xw, sheet_name="monthly")
        yes_cells.to_excel(xw, sheet_name="yes_cells", index=False)
        pd.DataFrame(books).to_excel(xw, sheet_name="daily")
    lines = [f"# Walk-forward of the Profiler tables\n\nTraining data: {len(d)} days {d['Date'].min().date()} .. {d['Date'].max().date()}. "
             f"Test span: {TEST_FROM.date()} .. {d['Date'].max().date()} ({len(test)} days). CW book; taxes {TAX}; "
             f"units: backtest Rs per 25k notional.\n\n```\n", res.to_string(index=False), "\n```\n\n## By month\n\n```\n",
             monthly.to_string(), "\n```\n\n## YES cells per stage\n\n```\n", yes_cells.to_string(index=False), "\n```\n"]
    open(os.path.join(OUT, "REPORT.md"), "w").write("".join(lines))
    print("".join(lines))


if __name__ == "__main__":
    main()
