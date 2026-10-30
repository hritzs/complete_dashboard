"""Decision table for the opportunity-based delta study (research only).

From output/minute_bars (minute_bars.py), one row per day and decision minute:
inputs known at that minute, regimes known before the day, and the outcome of a
long and of a short trade in the future and in a bought option, intraday and
carried to the next day, all at the recorded bid / ask.

Units: PnL in bps of the entry underlying (future) or in bps of the entry
underlying per option contract (option premium move / underlying x 1e4), net of
the spread paid; a further 1 bps round trip is charged on futures and 0.5 point
per option leg (brokerage, taxes, slippage).

    research/.venv/bin/python research/delta_build.py
writes output/delta_study/decisions.parquet and daily.parquet.
"""
import glob
import math
import os
from multiprocessing import Pool

import numpy as np
import pandas as pd

HERE = os.path.dirname(os.path.abspath(__file__))
BARS = os.path.join(HERE, "output", "minute_bars")
OUT = os.path.join(HERE, "output", "delta_study")
DECISIONS = [9 * 60 + 16, 9 * 60 + 20, 9 * 60 + 30, 9 * 60 + 45, 10 * 60, 10 * 60 + 30, 11 * 60, 11 * 60 + 30,
             12 * 60, 13 * 60, 14 * 60]
EXIT_DAY = 15 * 60 + 20
NEXT_AM = 9 * 60 + 20
FUT_COST_BPS = 1.0
OPT_COST_PTS = 0.5
STOPS = [(25.0, 50.0), (40.0, 80.0)]          # (stop, target) in bps, futures intraday


def hhmm(m):
    return f"{m // 60:02d}:{m % 60:02d}"


def bar_at(u, m):
    """Last bar stamped <= m (what is known at m)."""
    x = u[u["min"] <= m]
    return None if x.empty else x.iloc[-1]


def atm_iv(o, u_px, m, series="cw"):
    """ATM implied vol from the ATM straddle mid (Brenner: straddle ~ 0.8 S sigma sqrt(T))."""
    x = o[(o["min"] <= m) & (o["series"] == series)]
    if x.empty:
        return np.nan, np.nan, np.nan
    dte_s = float(x["dte"].min())
    x = x.sort_values("min").groupby(["K", "Type"]).last().reset_index()
    ks = np.sort(x["K"].unique())
    if not len(ks):
        return np.nan, np.nan, np.nan
    k = ks[np.argmin(np.abs(ks - u_px))]
    ce = x[(x["K"] == k) & (x["Type"] == "CE")]
    pe = x[(x["K"] == k) & (x["Type"] == "PE")]
    if ce.empty or pe.empty:
        return np.nan, np.nan, np.nan
    mid = lambda r: float((r["bid"].iloc[0] + r["ask"].iloc[0]) / 2) if r["bid"].iloc[0] > 0 and r["ask"].iloc[0] > 0 else float(r["ltp"].iloc[0])
    st = mid(ce) + mid(pe)
    dte = float(ce["dte"].iloc[0])
    t = max(dte, 0.25) / 365.0
    iv = st / (0.8 * u_px * math.sqrt(t)) if st > 0 else np.nan
    # skew: 2% OTM put minus 2% OTM call, over the straddle
    kp = ks[np.argmin(np.abs(ks - u_px * 0.98))]
    kc = ks[np.argmin(np.abs(ks - u_px * 1.02))]
    p2 = x[(x["K"] == kp) & (x["Type"] == "PE")]
    c2 = x[(x["K"] == kc) & (x["Type"] == "CE")]
    skew = (mid(p2) - mid(c2)) / st if (not p2.empty and not c2.empty and st > 0) else np.nan
    return iv, st, skew


def opt_quote(o, series, k, typ, m, side):
    """Option price at minute m (last bar <= m): ask to buy, bid to sell."""
    x = o[(o["series"] == series) & (o["K"] == k) & (o["Type"] == typ) & (o["min"] <= m)]
    if x.empty:
        return np.nan
    r = x.iloc[-1]
    px = r["ask"] if side == "buy" else r["bid"]
    return float(px) if px > 0 else float(r["ltp"])


def day_rows(args):
    key, nxt = args
    try:
        u = pd.read_parquet(os.path.join(BARS, f"{key}_u.parquet")).sort_values("min")
        o = pd.read_parquet(os.path.join(BARS, f"{key}_o.parquet"))
    except Exception:
        return []
    un = on = None
    if nxt:
        try:
            un = pd.read_parquet(os.path.join(BARS, f"{nxt}_u.parquet")).sort_values("min")
            on = pd.read_parquet(os.path.join(BARS, f"{nxt}_o.parquet"))
        except Exception:
            un = on = None
    cw_exp, nw_exp = u["cw_expiry"].iloc[0], u["nw_expiry"].iloc[0]
    expiry_today = cw_exp == key
    b916 = bar_at(u, DECISIONS[0])
    if b916 is None:
        return []
    ivser = "nw" if expiry_today else "cw"   # the expiring contract's IV is noise on its last day
    iv916, st916, _ = atm_iv(o, float(b916["close"]), DECISIONS[0], ivser)
    day_open = float(u["open"].iloc[0])
    orb = u[u["min"] <= 9 * 60 + 30]
    orb_hi, orb_lo = float(orb["high"].max()), float(orb["low"].min())
    rows = []
    for m in DECISIONS:
        b = bar_at(u, m)
        if b is None or b["min"] < m - 2:
            continue
        px = float(b["close"])
        past = u[u["min"] <= m]
        ret = lambda n: (px / float(past[past["min"] <= m - n]["close"].iloc[-1]) - 1) * 1e4 if (past["min"] <= m - n).any() else np.nan
        r1m = np.log(past["close"].astype(float)).diff().dropna()
        rv = float(r1m.std() * math.sqrt(375 * 252)) if len(r1m) >= 5 else np.nan
        iv, st, skew = atm_iv(o, px, m, ivser)
        hi, lo = float(past["high"].max()), float(past["low"].min())
        row = dict(date=key, t=hhmm(m), m=m, px=px, dte=float(o.loc[o["series"] == "cw", "dte"].min()), expiry_today=expiry_today,
                   r1=ret(1), r5=ret(5), r15=ret(15), r30=ret(30), r60=ret(60), r_open=(px / day_open - 1) * 1e4,
                   pos_range=(px - lo) / (hi - lo) if hi > lo else 0.5, range_bps=(hi - lo) / px * 1e4,
                   orb=(1 if px > orb_hi else -1 if px < orb_lo else 0) if m > 9 * 60 + 30 else 0,
                   rv_iv=rv / iv if iv and iv > 0 else np.nan, iv=iv, iv_chg=(iv - iv916) if np.isfinite(iv916) else np.nan,
                   st_chg=(st / st916 - 1) if st916 and st916 > 0 else np.nan, skew=skew)
        # ---- outcomes: futures (bid / ask from the bar), long and short
        ask, bid = float(b["ask"] or px), float(b["bid"] or px)
        aft = u[u["min"] > m]
        ex = bar_at(u, EXIT_DAY)

        def fut(entry_side, exit_bar):
            if exit_bar is None:
                return np.nan
            if entry_side == "L":
                return (float(exit_bar["bid"] or exit_bar["close"]) - ask) / px * 1e4 - FUT_COST_BPS
            return (bid - float(exit_bar["ask"] or exit_bar["close"])) / px * 1e4 - FUT_COST_BPS
        for side in ("L", "S"):
            row[f"F_{side}_day"] = fut(side, ex) if m < EXIT_DAY else np.nan
            for sl, tp in STOPS:
                res = np.nan
                path = aft[aft["min"] <= EXIT_DAY]
                if len(path):
                    res = fut(side, ex)
                    for _, pb in path.iterrows():
                        lo_b = (float(pb["low"]) / px - 1) * 1e4
                        hi_b = (float(pb["high"]) / px - 1) * 1e4
                        adv, fav = (-lo_b, hi_b) if side == "L" else (hi_b, -lo_b)
                        if adv >= sl:
                            res = -sl - FUT_COST_BPS - (ask - bid) / px * 1e4
                            break
                        if fav >= tp:
                            res = tp - FUT_COST_BPS - (ask - bid) / px * 1e4
                            break
                row[f"F_{side}_sl{int(sl)}tp{int(tp)}"] = res
            if un is not None:
                row[f"F_{side}_nextam"] = fut(side, bar_at(un, NEXT_AM))
                row[f"F_{side}_nextpm"] = fut(side, bar_at(un, EXIT_DAY))
        # ---- outcomes: bought option, one strike OTM (CE for long, PE for short)
        ser = "nw" if expiry_today else "cw"
        ks = np.sort(o.loc[o["series"] == ser, "K"].unique())
        if len(ks):
            kc = ks[ks > px].min() if (ks > px).any() else np.nan
            kp = ks[ks < px].max() if (ks < px).any() else np.nan
            for side, k, typ in (("L", kc, "CE"), ("S", kp, "PE")):
                if not np.isfinite(k):
                    continue
                buy = opt_quote(o, ser, k, typ, m, "buy")
                if not np.isfinite(buy) or buy <= 0:
                    continue
                sell_day = opt_quote(o, ser, k, typ, EXIT_DAY, "sell") if m < EXIT_DAY else np.nan
                row[f"O_{side}_day"] = (sell_day - buy - 2 * OPT_COST_PTS) / px * 1e4
                row[f"O_{side}_prem"] = buy
                if on is not None and not (ser == "cw" and expiry_today):
                    # the same contract the next day (CW unless it expired today)
                    nser = ser
                    if ser == "cw" and nxt and nxt > cw_exp:
                        nser = None
                    elif ser == "nw" and un is not None and un["cw_expiry"].iloc[0] == nw_exp:
                        nser = "cw"
                    if nser:
                        for tag, mm in (("nextam", NEXT_AM), ("nextpm", EXIT_DAY)):
                            s2 = opt_quote(on, nser, k, typ, mm, "sell")
                            row[f"O_{side}_{tag}"] = (s2 - buy - 2 * OPT_COST_PTS) / px * 1e4
        rows.append(row)
    return rows


def main():
    import argparse
    ap = argparse.ArgumentParser()
    ap.add_argument("--workers", type=int, default=16)
    ap.add_argument("--only", default="", help="comma-separated YYYY-MM-DD (test run; written to decisions_test.parquet)")
    a = ap.parse_args()
    os.makedirs(OUT, exist_ok=True)
    keys = sorted(os.path.basename(f)[:10] for f in glob.glob(os.path.join(BARS, "*_u.parquet")))
    pairs = [(k, keys[i + 1] if i + 1 < len(keys) else None) for i, k in enumerate(keys)]
    only = {x.strip() for x in a.only.split(",") if x.strip()}
    if only:
        pairs = [p for p in pairs if p[0] in only]
    with Pool(a.workers) as pool:
        out = [r for rows in pool.imap(day_rows, pairs, chunksize=4) for r in rows]
    d = pd.DataFrame(out)
    d.to_parquet(os.path.join(OUT, "decisions_test.parquet" if only else "decisions.parquet"), index=False)
    if only:
        print(f"{len(d)} decision rows over {d['date'].nunique()} days (test)")
        return
    # daily series for regimes (known before the day: built from earlier days only in delta_eval)
    first = d[d["t"] == "09:16"].set_index("date")
    daily = pd.DataFrame({"iv916": first["iv"], "dte": first["dte"]})
    closes = {}
    for k in keys:
        try:
            u = pd.read_parquet(os.path.join(BARS, f"{k}_u.parquet"), columns=["min", "open", "high", "low", "close"])
        except Exception:
            continue
        closes[k] = dict(open=float(u["open"].iloc[0]), high=float(u["high"].max()), low=float(u["low"].min()),
                         close=float(u.loc[u["min"] <= 15 * 60 + 30, "close"].iloc[-1]))
    daily = daily.join(pd.DataFrame.from_dict(closes, orient="index"), how="outer")
    daily.index.name = "date"
    daily.to_parquet(os.path.join(OUT, "daily.parquet"))
    print(f"{len(d)} decision rows over {d['date'].nunique()} days -> {OUT}")


if __name__ == "__main__":
    main()
