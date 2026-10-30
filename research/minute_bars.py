"""Minute bars for every tick day (research only), the base of the
opportunity-based delta study (owner 2026-10-29: "an opportunity based delta
strategy, not straddle-sell based ... derive new rules").

From /mnt/shared/TickDataIV/<DDMMYYYY>/NIFTY.parquet|feather (read only), per day:
  output/minute_bars/<YYYY-MM-DD>_u.parquet   underlying of the current-week (CW) contract per minute:
                                              open/high/low/close, bid, ask (last), ticks; CW / NW expiry
  output/minute_bars/<YYYY-MM-DD>_o.parquet   CW and next-week (NW) options per minute and strike within
                                              +/-1000 of the 09:16 underlying: last bid / ask / ltp, DTE
Bars are stamped with the minute they CLOSE (09:16 = ticks of 09:15:00-09:15:59), so a
decision at hh:mm sees only bars stamped <= hh:mm.

    research/.venv/bin/python research/minute_bars.py [--workers 16] [--only YYYY-MM-DD,...]
Resumable: days already written are skipped.
"""
import argparse
import glob
import os
import sys
from multiprocessing import Pool

import pandas as pd

HERE = os.path.dirname(os.path.abspath(__file__))
OUT = os.path.join(HERE, "output", "minute_bars")
TICK_DIR = os.environ.get("GS_TICK_DIR", "/mnt/shared/TickDataIV")
BAND = 1000.0


def day_files(only=None):
    out = {}
    for f in glob.glob(os.path.join(TICK_DIR, "*", "NIFTY.parquet")) + glob.glob(os.path.join(TICK_DIR, "*", "NIFTY.feather")):
        ds = os.path.basename(os.path.dirname(f))
        try:
            dt = pd.Timestamp(year=int(ds[4:]), month=int(ds[2:4]), day=int(ds[:2]))
        except ValueError:
            continue
        if dt < pd.Timestamp("2021-01-01") or (only and dt.strftime("%Y-%m-%d") not in only):
            continue
        out[dt] = f            # feather listed last: wins when both exist
    return sorted(out.items())


def one(item):
    dt, path = item
    key = dt.strftime("%Y-%m-%d")
    fu, fo = os.path.join(OUT, f"{key}_u.parquet"), os.path.join(OUT, f"{key}_o.parquet")
    if os.path.exists(fu) and os.path.exists(fo):
        return key, "done"
    try:
        df = pd.read_feather(path) if path.endswith(".feather") else pd.read_parquet(path)
        df = df[df["Type"].astype(str).isin(["CE", "PE"])]
        ex = pd.to_datetime(df["Expiry"])
        exps = sorted(x for x in ex.unique() if pd.Timestamp(x).normalize() >= dt.normalize())
        if not exps:
            return key, "empty (no live expiry)"
        tcol = "Time_dt" if "Time_dt" in df.columns else "Time"
        t = pd.to_datetime(df[tcol])
        df = df.assign(_sec=(t.dt.hour * 3600 + t.dt.minute * 60 + t.dt.second).values, _exp=ex.values)
        df = df[(df["_sec"] >= 9 * 3600 + 15 * 60) & (df["_sec"] < 15 * 3600 + 30 * 60)]
        if df.empty:
            return key, "empty"
        # bar of the minute it closes: 09:15:xx -> 09:16
        df["min"] = (df["_sec"] // 60 + 1).astype("int32")
        df = df.sort_values("_sec", kind="stable")
        cw = df[df["_exp"] == exps[0]]
        u = cw.groupby("min").agg(open=("UnderlyingLTP", "first"), high=("UnderlyingLTP", "max"), low=("UnderlyingLTP", "min"),
                                  close=("UnderlyingLTP", "last"), bid=("UnderlyingBid", "last"), ask=("UnderlyingAsk", "last"),
                                  ticks=("UnderlyingLTP", "size")).reset_index()
        u.insert(0, "date", key)
        u["cw_expiry"] = pd.Timestamp(exps[0]).strftime("%Y-%m-%d")
        u["nw_expiry"] = pd.Timestamp(exps[1]).strftime("%Y-%m-%d") if len(exps) > 1 else ""
        ref = float(u.loc[u["min"] >= 9 * 60 + 16, "close"].iloc[0]) if (u["min"] >= 9 * 60 + 16).any() else float(u["close"].iloc[0])
        parts = []
        for tag, e in (("cw", exps[0]), ("nw", exps[1] if len(exps) > 1 else None)):
            if e is None:
                continue
            x = df[df["_exp"] == e]
            k = pd.to_numeric(x["Strike" if "Strike" in x.columns else "StrikePrice"], errors="coerce")
            x = x[(k - ref).abs() <= BAND].assign(K=k)
            x = x.groupby(["min", "K", "Type"]).agg(bid=("Bid", "last"), ask=("Ask", "last"), ltp=("LTP", "last"),
                                                    dte=("DTE", "last")).reset_index()
            x["series"], x["expiry"] = tag, pd.Timestamp(e).strftime("%Y-%m-%d")
            parts.append(x)
        o = pd.concat(parts, ignore_index=True)
        o.insert(0, "date", key)
        os.makedirs(OUT, exist_ok=True)
        u.to_parquet(fu, index=False)
        o.to_parquet(fo, index=False)
        return key, f"ok {len(u)} min, {o['K'].nunique()} strikes"
    except Exception as e:  # one bad day must not stop the run
        return key, f"error {type(e).__name__}: {str(e)[:120]}"


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--workers", type=int, default=16)
    ap.add_argument("--only", default="")
    a = ap.parse_args()
    only = {s.strip() for s in a.only.split(",") if s.strip()} or None
    items = day_files(only)
    print(f"{len(items)} tick days -> {OUT}", flush=True)
    bad = 0
    with Pool(a.workers) as pool:
        for i, (key, msg) in enumerate(pool.imap_unordered(one, items), 1):
            if msg.startswith(("error", "empty")):
                bad += 1
                print(key, msg, flush=True)
            if i % 100 == 0:
                print(f"  {i}/{len(items)}", flush=True)
    print(f"finished: {len(items)} days, {bad} not written", flush=True)


if __name__ == "__main__":
    sys.exit(main())
