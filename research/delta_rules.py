"""Rule search for the opportunity-based delta study, with regimes (research only).

Input: output/delta_study/decisions.parquet and daily.parquet (delta_build.py).

Regimes, per day, from EARLIER days only, with cut-offs fixed in advance (not fitted):
  ivp    ATM IV at 09:16 against the previous 250 days: low (< 1/3) / mid / high (> 2/3)
  vrp    09:16 IV minus the previous 10 days' realised vol: rich (> 0) / cheap
  trend  20-day move / (daily vol x sqrt 20): up (> 1) / range / down (< -1)
  comp   last 5 days' average range / last 20 days': compressed (< 0.8) / normal / expanded (> 1.2)
  expw   expiry week (DTE <= 2) / not
  idvc   IDV (GammaShort daily file) rising / falling against the previous day

Candidates: at a decision time, one input in one of its five TRAIN quintile bins, going
long or short (the direction the TRAIN mean says), on all days or in one regime state;
for every outcome (future intraday / with stop-target / to next morning / next close,
bought option intraday / next morning / next close). Kept on TRAIN when n >= 60 (40 in a
regime) and t >= 2.5; then on VALIDATION when the mean has the same sign, n >= 15 and
t >= 1.5. TEST is reported once for the survivors. The count of everything tried is
reported: with that many tries, some pass TRAIN by luck -- that is what VALIDATION is for.

Also: the straddle book (output/backtest_basket production PnL) by regime state.

    research/.venv/bin/python research/delta_rules.py
writes output/delta_study/REPORT.md and rules.xlsx.
"""
import glob
import math
import os
import pickle

import numpy as np
import pandas as pd

HERE = os.path.dirname(os.path.abspath(__file__))
DS = os.path.join(HERE, "output", "delta_study")
TRAIN_END, VAL_END = pd.Timestamp("2024-06-20"), pd.Timestamp("2025-08-13")
FEATURES = ["r1", "r5", "r15", "r30", "r60", "r_open", "pos_range", "range_bps", "orb", "rv_iv", "iv_chg", "st_chg", "skew",
            "gap", "prev_ret", "idv", "idv_chg", "adj_iv_chg"]
OUTCOMES = ["F_{}_day", "F_{}_sl25tp50", "F_{}_sl40tp80", "F_{}_nextam", "F_{}_nextpm", "O_{}_day", "O_{}_nextam", "O_{}_nextpm"]
REGIMES = ["ivp", "vrp", "trend", "comp", "expw", "idvc"]
GS_DAILY = os.environ.get("GS_DAILY_CSV", "/mnt/shared/Hardik/Project_Codes/GammaShortDailyProcess/GammaShortDailydata.csv")


def part(dates):
    d = pd.to_datetime(dates)
    return np.where(d <= TRAIN_END, "train", np.where(d <= VAL_END, "val", "test"))


def regimes(daily):
    x = daily.sort_index().copy()
    x.index = pd.to_datetime(x.index)
    lr = np.log(x["close"]).diff()
    rng = (x["high"] - x["low"]) / x["close"]
    out = pd.DataFrame(index=x.index)
    # everything below uses data up to the PREVIOUS day (shift(1)), except the 09:16 IV itself
    ivp = x["iv916"].rolling(250, min_periods=120).apply(lambda w: (w[:-1] < w[-1]).mean() if len(w) > 1 else np.nan, raw=True)
    out["ivp"] = pd.cut(ivp, [-0.01, 1 / 3, 2 / 3, 1.01], labels=["low", "mid", "high"])
    rv10 = lr.rolling(10).std().shift(1) * math.sqrt(252)
    out["vrp"] = np.where((x["iv916"] - rv10).isna(), None, np.where(x["iv916"] > rv10, "rich", "cheap"))
    vol20 = lr.rolling(20).std().shift(1)
    tr = (np.log(x["close"]).shift(1) - np.log(x["close"]).shift(21)) / (vol20 * math.sqrt(20))
    out["trend"] = pd.cut(tr, [-np.inf, -1, 1, np.inf], labels=["down", "range", "up"])
    comp = rng.rolling(5).mean().shift(1) / rng.rolling(20).mean().shift(1)
    out["comp"] = pd.cut(comp, [0, 0.8, 1.2, np.inf], labels=["compressed", "normal", "expanded"])
    out["gap"] = (x["open"] / x["close"].shift(1) - 1) * 1e4
    out["prev_ret"] = lr.shift(1) * 1e4
    # IDV (implied daily vol from the GammaShort daily file, built from earlier days) and its change
    g = pd.read_csv(GS_DAILY, usecols=["Date", "IDV_Pure", "Adj_IV_Chg"])
    g["Date"] = pd.to_datetime(g["Date"])
    g = g.set_index("Date").sort_index()
    out = out.join(pd.DataFrame({"idv": g["IDV_Pure"], "idv_chg": g["IDV_Pure"].diff(), "adj_iv_chg": g["Adj_IV_Chg"]}))
    out["idvc"] = np.where(out["idv_chg"].isna(), None, np.where(out["idv_chg"] > 0, "rising", "falling"))
    return out


def tstat(v):
    v = v.dropna()
    return (v.mean() / (v.std(ddof=1) / math.sqrt(len(v)))) if len(v) > 2 and v.std(ddof=1) > 0 else 0.0


def main():
    d = pd.read_parquet(os.path.join(DS, "decisions.parquet"))
    daily = pd.read_parquet(os.path.join(DS, "daily.parquet"))
    rg = regimes(daily)
    d["date"] = pd.to_datetime(d["date"])
    d = d.join(rg, on="date")
    d["expw"] = np.where(d["dte"] <= 2, "yes", "no")
    d["part"] = part(d["date"])
    tried, kept = 0, []
    for t, g in d.groupby("t"):
        tr = g[g["part"] == "train"]
        for f in FEATURES:
            if f not in g or tr[f].notna().sum() < 100:
                continue
            edges = np.unique(np.nanquantile(tr[f], [0.2, 0.4, 0.6, 0.8]))
            b = pd.Series(np.searchsorted(edges, g[f], side="right"), index=g.index).where(g[f].notna())
            for q in range(len(edges) + 1):
                inbin = b == q
                for reg in [None] + REGIMES:
                    states = [None] if reg is None else [s for s in g[reg].dropna().unique()]
                    for st in states:
                        m = inbin if reg is None else inbin & (g[reg] == st)
                        nmin = 60 if reg is None else 40
                        for oc in OUTCOMES:
                            L, S = oc.format("L"), oc.format("S")
                            if L not in g:
                                continue
                            trv = g.loc[m & (g["part"] == "train"), L].dropna()
                            if len(trv) < nmin:
                                continue
                            tried += 2
                            side = "L" if trv.mean() > 0 else "S"
                            col = oc.format(side)
                            vtr = g.loc[m & (g["part"] == "train"), col].dropna()
                            ttr = tstat(vtr)
                            if ttr < 2.5 or vtr.mean() <= 0:
                                continue
                            vv = g.loc[m & (g["part"] == "val"), col].dropna()
                            tv = tstat(vv)
                            rec = dict(time=t, feature=f, bin=f"q{q + 1}/{len(edges) + 1}",
                                       lo=round(float(edges[q - 1]), 3) if q > 0 else None, hi=round(float(edges[q]), 3) if q < len(edges) else None,
                                       regime=f"{reg}={st}" if reg else "all", outcome=oc.format("x"), side=side,
                                       train_n=len(vtr), train_avg=round(vtr.mean(), 2), train_t=round(ttr, 2),
                                       val_n=len(vv), val_avg=round(vv.mean(), 2) if len(vv) else np.nan, val_t=round(tv, 2))
                            rec["survives"] = bool(len(vv) >= 15 and vv.mean() > 0 and tv >= 1.5)
                            if rec["survives"]:
                                ts = g.loc[m & (g["part"] == "test"), col].dropna()
                                rec.update(test_n=len(ts), test_avg=round(ts.mean(), 2) if len(ts) else np.nan, test_t=round(tstat(ts), 2),
                                           test_win=round(100 * (ts > 0).mean(), 1) if len(ts) else np.nan)
                            kept.append(rec)
    k = pd.DataFrame(kept)
    surv = k[k["survives"]].sort_values("val_t", ascending=False) if len(k) else k
    # straddle book by regime
    book = None
    runs = sorted(glob.glob(os.path.join(HERE, "output", "backtest_basket", "run_*", "days", "*.pkl")))
    if runs:
        b = pd.DataFrame([pickle.load(open(f, "rb")) for f in runs])[["Date", "Net_PnL", "Leg"]]
        b["date"] = pd.to_datetime(b["Date"])
        b = b.join(rg, on="date")
        b["part"] = part(b["date"])
        b = b[b["Leg"].isin(["CW", "NW"])]
        rows = []
        for reg in ["ivp", "vrp", "trend", "comp", "idvc"]:
            for st, gg in b.groupby(reg, observed=True):
                r = dict(regime=reg, state=st)
                for p in ("train", "val", "test"):
                    x = gg[gg["part"] == p]["Net_PnL"]
                    r[f"{p}_n"], r[f"{p}_avg"] = len(x), round(x.mean(), 2) if len(x) else np.nan
                rows.append(r)
        book = pd.DataFrame(rows)
    with pd.ExcelWriter(os.path.join(DS, "rules.xlsx")) as xw:
        k.to_excel(xw, sheet_name="kept_on_train", index=False)
        surv.to_excel(xw, sheet_name="survivors", index=False)
        if book is not None:
            book.to_excel(xw, sheet_name="straddle_by_regime", index=False)
    lines = [f"# Opportunity-based delta rules\n\nDecision rows: {len(d)} over {d['date'].nunique()} days "
             f"({d['date'].min().date()} to {d['date'].max().date()}).\n",
             f"Candidates tried: {tried}. Passed TRAIN (n, t >= 2.5): {len(k)}. Survived VALIDATION: {len(surv)}.\n",
             "Units: bps of the underlying per trade, net of spread and costs.\n"]
    if len(surv):
        cols = ["time", "feature", "bin", "lo", "hi", "regime", "outcome", "side", "train_n", "train_avg", "train_t",
                "val_n", "val_avg", "val_t", "test_n", "test_avg", "test_t", "test_win"]
        lines.append("\n## Survivors (TEST reported once)\n\n```\n" + surv[cols].head(40).to_string(index=False) + "\n```\n")
    if book is not None:
        lines.append("\n## Straddle book (production) by regime, average net PnL per traded day\n\n```\n" + book.to_string(index=False) + "\n```\n")
    open(os.path.join(DS, "REPORT.md"), "w").write("".join(lines))
    print("".join(lines))


if __name__ == "__main__":
    main()
