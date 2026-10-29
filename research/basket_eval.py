"""Bucket / instrument basket over the GS_BASKET backtest (research only).

Input: output/backtest_basket/run_*/days/*.pkl, written by
    GS_BASKET=1 GammaShort_Backtest_Final.py
which records, for every day and the fixed entries 09:16 / 09:17 / 09:18, the
raw LUT inputs (DTE index, Build IV, straddle ratio, IV ratio; the day's
normalised gap and adjusted-IV change) and the forced PnL of the ATM straddle
and of short delta strangles (0.15-0.35 per leg), all on the production fills,
costs, SL / TP and event sizing.

Baseline: the LIVE tables (/mnt/shared/Hardik/Profilers/12102026, LUT_0916 /
0917 / 0918) applied with their own bins. A day trades at the first fixed
entry whose cell says YES (straddle-ratio veto and DTE-1 IV floor as recorded).
Only 09:16-09:18 are covered: the live LUT also checks 09:19 and 09:20+, so
these numbers compare the three opening minutes only.

Basket: bucket edges for IV ratio x Build IV x straddle ratio, x instrument,
x a cell rule (min days, min average). YES cells are fitted on TRAIN only.
Choice on VALIDATION (owner's protocol, SIZING_RESEARCH_HANDOFF.md): best
net / max drawdown, kept only if it beats the live tables on BOTH net and
net / MDD there. TEST is reported once for the choice (or for the live tables
when nothing qualifies). Nothing here changes live trading.

    research/.venv/bin/python research/basket_eval.py
writes output/basket_eval/basket_eval.xlsx and REPORT.md.
"""
import glob
import itertools
import os
import pickle
import sys

import numpy as np
import pandas as pd

HERE = os.path.dirname(os.path.abspath(__file__))
RUN = sorted(glob.glob(os.path.join(HERE, "output", "backtest_basket", "run_*")))
OUT = os.path.join(HERE, "output", "basket_eval")
LIVE_DIR = "/mnt/shared/Hardik/Profilers/12102026"
TIMES = ["0916", "0917", "0918"]
INSTR = {"ATM": "PnL_{t}", **{f"D{d:02d}": f"D{d:02d}_{{t}}_PnL" for d in (15, 20, 25, 30, 35)}}
TRAIN_END, VAL_END = pd.Timestamp("2024-06-20"), pd.Timestamp("2025-08-13")

# Live table bins (labels in LUT_*.csv), right-inclusive as the labels read ("<=0.65", "0.65-0.80").
LIVE_IVR = [0.65, 0.80, 0.90, 0.95, 1.00, 1.05, 1.10, 1.20, 1.30]
LIVE_SR = [0.95, 1.00, 1.05, 1.10, 1.15, 1.20, 1.30, 1.40, 1.50, 1.75, 2.00]
LIVE_BIV = [0.08, 0.09, 0.10, 0.12, 0.16, 0.20]
LIVE_OG = [-0.7501, 0.7501]
LIVE_ADJ = [-0.007, 0.005]

IVR_SETS = {"ivr_live": LIVE_IVR,
            "ivr_shift-": [round(x - 0.025, 3) for x in LIVE_IVR],
            "ivr_shift+": [round(x + 0.025, 3) for x in LIVE_IVR],
            "ivr_coarse": [0.80, 0.95, 1.05, 1.20]}
BIV_SETS = {"biv_live": LIVE_BIV,
            "biv_fine": [0.08, 0.10, 0.12, 0.14, 0.16, 0.18, 0.20],
            "biv_coarse": [0.10, 0.14, 0.18]}
SR_SETS = {"sr_live": LIVE_SR, "sr_coarse": [1.00, 1.10, 1.20, 1.40]}
RULES = [(n, thr) for n in (5, 10) for thr in (0.0, 5.0, 10.0)]   # (min days in cell, min avg PnL)


def b(x, edges):
    """Right-inclusive bin index: x <= edges[0] -> 0, edges[0] < x <= edges[1] -> 1, ..."""
    return int(np.searchsorted(edges, x, side="left"))


def load_days():
    if not RUN:
        sys.exit("no output/backtest_basket/run_* -- run GS_BASKET=1 GammaShort_Backtest_Final.py first")
    recs = [pickle.load(open(f, "rb")) for f in sorted(glob.glob(os.path.join(RUN[-1], "days", "*.pkl")))]
    d = pd.DataFrame(recs)
    d["Date"] = pd.to_datetime(d["Date"])
    d["Part"] = np.where(d["Date"] <= TRAIN_END, "train", np.where(d["Date"] <= VAL_END, "val", "test"))
    return d


def long_form(d):
    """One row per day and fixed entry with inputs and every instrument's PnL."""
    rows = []
    for t in TIMES:
        if f"B{t}_DI" not in d:
            continue
        x = pd.DataFrame({"Date": d["Date"], "Part": d["Part"], "T": t, "DI": d[f"B{t}_DI"], "BIV": d[f"B{t}_BIV"],
                          "SR": d[f"B{t}_SR"], "IVR": d[f"B{t}_IVR"], "OG": d["Norm_OG"], "ADJ": d["Adj_IV_Chg"],
                          "Blocked": d[f"B{t}_Veto"].fillna(False).astype(bool) | d[f"B{t}_Floor"].fillna(False).astype(bool)
                          | (d[f"Signal_{t}"] == "EVENT_PRE_0918")})
        for k, pat in INSTR.items():
            col = pat.format(t=t)
            x[k] = d[col] if col in d else np.nan
        rows.append(x)
    f = pd.concat(rows, ignore_index=True).dropna(subset=["DI", "BIV", "SR", "IVR"])
    f["DI"] = f["DI"].astype(int)
    return f


def live_table_yes(f):
    """YES / NO of the live tables for each row (their bins, their stage file)."""
    yes = pd.Series(False, index=f.index)
    for t in TIMES:
        t_ = pd.read_csv(os.path.join(LIVE_DIR, f"LUT_{t}.csv"))
        labels = [list(dict.fromkeys(t_[c].astype(str))) for c in t_.columns[:6]]
        on = set()
        for r in t_[t_["Trade"] == "YES"].itertuples(index=False):
            on.add(tuple(labels[i].index(str(r[i])) for i in range(6)))
        m = f["T"] == t
        for i in f.index[m]:
            r = f.loc[i]
            c = (r.DI, b(r.IVR, LIVE_IVR), b(r.SR, LIVE_SR), b(r.BIV, LIVE_BIV), b(r.OG, LIVE_OG), b(r.ADJ, LIVE_ADJ))
            yes.at[i] = c in on
    return yes & ~f["Blocked"]


def book(f, yes, instr):
    """Per day: the first fixed entry with YES, its PnL in instr (0 when none)."""
    g = f[yes].sort_values(["Date", "T"]).groupby("Date").first()
    days = f.drop_duplicates("Date").set_index("Date")["Part"]
    pnl = g[instr].reindex(days.index).fillna(0.0)
    tm = g["T"].reindex(days.index)
    return pd.DataFrame({"Part": days, "PnL": pnl, "T": tm})


def stats(bk, part):
    p = bk[bk["Part"] == part]["PnL"]
    cum = p.cumsum()
    mdd = float((cum.cummax() - cum).max()) if len(p) else 0.0
    n = int((bk[bk["Part"] == part]["T"].notna()).sum())
    net = float(p.sum())
    return dict(net=round(net, 1), trades=n, win=round(100 * float((p[p != 0] > 0).mean()), 1) if n else np.nan,
                mdd=round(mdd, 1), net_mdd=round(net / mdd, 2) if mdd > 0 else np.nan)


def fit_cells(f, edges, instr, n_min, thr):
    ivr, biv, sr = edges
    key = list(zip(f["T"], f["DI"], f["IVR"].map(lambda x: b(x, ivr)), f["SR"].map(lambda x: b(x, sr)),
                   f["BIV"].map(lambda x: b(x, biv)), f["OG"].map(lambda x: b(x, LIVE_OG)), f["ADJ"].map(lambda x: b(x, LIVE_ADJ))))
    f = f.assign(cell=key)
    tr = f[(f["Part"] == "train") & ~f["Blocked"]].dropna(subset=[instr])
    g = tr.groupby("cell")[instr].agg(["count", "mean"])
    on = set(g[(g["count"] >= n_min) & (g["mean"] > thr)].index)
    return f["cell"].map(lambda c: c in on) & ~f["Blocked"], len(on)


def main():
    os.makedirs(OUT, exist_ok=True)
    d = load_days()
    f = long_form(d)
    base_yes = live_table_yes(f)
    base = book(f, base_yes, "ATM")
    rows = [dict(variant="LIVE tables (ATM straddle)", cells=np.nan,
                 **{f"{p}_{k}": v for p in ("train", "val", "test") for k, v in stats(base, p).items()})]
    books = {"LIVE tables (ATM straddle)": base}
    for (ik, iv), (bk_, bv), (sk, sv) in itertools.product(IVR_SETS.items(), BIV_SETS.items(), SR_SETS.items()):
        for instr in INSTR:
            for n_min, thr in RULES:
                yes, ncell = fit_cells(f, (iv, bv, sv), instr, n_min, thr)
                bkk = book(f, yes, instr)
                name = f"{instr} | {ik} {bk_} {sk} | n>={n_min} avg>{thr:g}"
                books[name] = bkk
                rows.append(dict(variant=name, cells=ncell,
                                 **{f"{p}_{k}": v for p in ("train", "val") for k, v in stats(bkk, p).items()}))
    res = pd.DataFrame(rows)
    b0 = res.iloc[0]
    ok = res[(res["val_net"] > b0["val_net"]) & (res["val_net_mdd"] > b0["val_net_mdd"])].sort_values("val_net_mdd", ascending=False)
    choice = ok.iloc[0]["variant"] if len(ok) else "LIVE tables (ATM straddle)"
    t = stats(books[choice], "test")
    res.loc[res["variant"] == choice, [f"test_{k}" for k in t]] = list(t.values())
    # Recent days: what the live tables and the choice would have done
    recent = pd.DataFrame({"live_T": base["T"], "live_PnL": base["PnL"],
                           "choice_T": books[choice]["T"], "choice_PnL": books[choice]["PnL"]})
    recent = recent[recent.index >= "2026-09-01"]
    forced = d.set_index("Date")[[c for c in ["PnL_0916"] + [f"D{x:02d}_0916_PnL" for x in (15, 20, 25, 30, 35)] if c in d]]
    recent = recent.join(forced)
    with pd.ExcelWriter(os.path.join(OUT, "basket_eval.xlsx")) as xw:
        res.sort_values("val_net_mdd", ascending=False).to_excel(xw, sheet_name="variants", index=False)
        recent.round(2).to_excel(xw, sheet_name="recent_days")
    with open(os.path.join(OUT, "REPORT.md"), "w") as fh:
        fh.write(f"# Bucket / instrument basket\n\nRun: {RUN[-1]}\nDays: {d['Date'].min().date()} to {d['Date'].max().date()} ({len(d)})\n")
        fh.write("Fixed entries 09:16-09:18 only (live also checks 09:19 / 09:20+).\n\n")
        fh.write("## Live tables (baseline)\n\n```\n" + pd.DataFrame([b0]).drop(columns=["variant", "cells"]).to_string(index=False) + "\n```\n\n")
        fh.write(f"## Variants beating the live tables on VALIDATION (net and net/MDD): {len(ok)} of {len(res) - 1}\n\n```\n")
        fh.write(ok.head(15).drop(columns=[c for c in res.columns if c.startswith("test_")]).to_string(index=False) + "\n```\n\n")
        fh.write(f"## Choice: {choice}\n\nTEST (reported once): {t}\n\n")
        fh.write("## Recent days (forced PnL at 09:16; live and chosen decisions)\n\n```\n" + recent.round(2).to_string() + "\n```\n")
    print(open(os.path.join(OUT, "REPORT.md")).read())


if __name__ == "__main__":
    main()
