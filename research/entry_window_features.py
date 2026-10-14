"""
What the market looked like just BEFORE each possible entry, and how the trade
stood in the first minutes AFTER it -- one row per (day, entry minute).

For every day of the backtest run and each entry minute 09:16 / 09:17 / 09:18
(plus the day's actual entry minute), from the share's tick file (read only):

  before entry (the 60 s ending at hh:mm:00, nothing later)
    u_move_bps    net move of the underlying
    u_range_bps   high - low of the underlying
    str_range_pct high - low of the ATM straddle (LTP), % of the straddle
    str_chg_pct   change of that straddle over the 60 s
    spread_pct    CE + PE bid-ask spread at the entry second, % of the straddle
  after entry (marks at +1, +2, +3, +5, +10 minutes)
    u_bps_k       move of the underlying since entry, bps
    str_pts_k     straddle (LTP) minus the straddle at entry, index points
                  (positive = against a short straddle), unhedged

Output: research/output/entry_window_features.csv
    ./.venv/bin/python entry_window_features.py [workers=16]
"""
import concurrent.futures
import glob
import os
import pickle
import sys

import numpy as np
import pandas as pd

HERE = os.path.dirname(os.path.abspath(__file__))
TICK_DIR = os.environ.get("GS_TICK_DIR", "/mnt/shared/TickDataIV")
OUT = os.path.join(HERE, "output", "entry_window_features.csv")
AFTER = (1, 2, 3, 5, 10)


def one_day(rec):
    day = rec["Date"]
    exp = rec.get("CW_Expiry")
    if not exp:
        return []
    y, m, d = day.split("-")
    path = os.path.join(TICK_DIR, d + m + y, "NIFTY.parquet")
    try:
        df = pd.read_parquet(path, columns=["Time_dt", "Strike", "Type", "LTP", "Bid", "Ask", "UnderlyingLTP"],
                             filters=[("Expiry", "==", pd.Timestamp(exp))])
    except Exception as e:                                   # noqa: BLE001
        return [dict(Date=day, error=type(e).__name__)]
    df["Type"] = df["Type"].astype(str)
    df = df[df["Type"].isin(["CE", "PE"])]
    if df.empty:
        return []
    t = df["Time_dt"]
    df["sec"] = (t.dt.hour * 3600 + t.dt.minute * 60 + t.dt.second).astype(int)
    S0 = 9 * 3600 + 15 * 60
    n = 6 * 3600 + 30 * 60
    grid = np.arange(S0, S0 + n)
    u = df.dropna(subset=["UnderlyingLTP"]).groupby("sec")["UnderlyingLTP"].first().reindex(grid).ffill()

    entries = {916, 917, 918}
    et = rec.get("Entry_Time")
    if rec.get("Leg") == "CW" and isinstance(et, str) and len(et) == 5:
        entries.add(int(et[:2]) * 100 + int(et[3:]))
    out = []
    cache = {}

    def leg(k, typ, col):
        key = (k, typ, col)
        if key not in cache:
            x = df[(df["Strike"] == k) & (df["Type"] == typ)]
            s = x.groupby("sec")[col].last().reindex(grid)
            cache[key] = s.where(s > 0).ffill()
        return cache[key]

    for th in sorted(entries):
        e = (th // 100) * 3600 + (th % 100) * 60
        i = e - S0
        if i < 60 or i >= n or not np.isfinite(u.iloc[i]):
            continue
        u0 = float(u.iloc[i])
        k = round(u0 / 50) * 50
        ce, pe = leg(k, "CE", "LTP"), leg(k, "PE", "LTP")
        strd = ce + pe
        w = slice(i - 60, i + 1)                              # the minute before, up to the entry second
        uw, sw = u.iloc[w].dropna(), strd.iloc[w].dropna()
        s0 = float(strd.iloc[i]) if np.isfinite(strd.iloc[i]) else np.nan
        if not (len(uw) > 5 and len(sw) > 5 and s0 > 0):
            continue
        spread = np.nan
        try:
            spread = float((leg(k, "CE", "Ask").iloc[i] - leg(k, "CE", "Bid").iloc[i]
                            + leg(k, "PE", "Ask").iloc[i] - leg(k, "PE", "Bid").iloc[i]) / s0 * 100)
        except Exception:                                     # noqa: BLE001
            pass
        row = dict(Date=day, entry=th, strike=k, u0=u0, straddle0=s0,
                   u_move_bps=(uw.iloc[-1] - uw.iloc[0]) / u0 * 1e4,
                   u_range_bps=(uw.max() - uw.min()) / u0 * 1e4,
                   str_range_pct=(sw.max() - sw.min()) / s0 * 100,
                   str_chg_pct=(sw.iloc[-1] - sw.iloc[0]) / sw.iloc[0] * 100,
                   spread_pct=spread)
        for a in AFTER:
            j = i + a * 60
            if j < n and np.isfinite(u.iloc[j]) and np.isfinite(strd.iloc[j]):
                row[f"u_bps_{a}"] = (float(u.iloc[j]) - u0) / u0 * 1e4
                row[f"str_pts_{a}"] = float(strd.iloc[j]) - s0
        out.append(row)
    return out


def main():
    workers = int(sys.argv[1]) if len(sys.argv) > 1 else 16
    recs = [pickle.load(open(f, "rb")) for f in sorted(glob.glob(os.path.join(HERE, "output", "backtest_final", "run_*", "days", "*.pkl")))]
    rows = []
    with concurrent.futures.ProcessPoolExecutor(max_workers=workers) as ex:
        for n_done, r in enumerate(ex.map(one_day, recs, chunksize=4), 1):
            rows.extend(r)
            if n_done % 100 == 0:
                print(f"  {n_done}/{len(recs)}", flush=True)
    F = pd.DataFrame(rows)
    F.to_csv(OUT, index=False)
    print(f"saved {OUT}: {len(F)} rows, {F['Date'].nunique()} days, errors {int(F.get('error', pd.Series(dtype=object)).notna().sum())}")


if __name__ == "__main__":
    main()
