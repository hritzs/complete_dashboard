#!/usr/bin/env python3
"""Compare the NSE multicast feed against GreekSoft Apollo, packet by packet.

Inputs (~/.trading-platform/feedrec/):
  YYYY-MM-DD_nse.csv     NSE,rx_ns,token,exch_send_ns,ltt_s,ltp,ltq,volume,bid,ask
  YYYY-MM-DD_apollo.csv  APOLLO,rx_ns,token,bcast_s,ltt_s,lut_s,ltp,ltq,tot_vol,bid,ask

Reports, per feed: how long after the exchange trade time (LTT, whole
seconds on both feeds) each update reached us, and for NSE also after the
exchange's own send stamp (TimeStamp2, ~15us). Then the SAME trade in both
feeds -- matched on (token, cumulative volume) -- and which arrived first.
With --minute HH:MM: each feed's last trade before that minute boundary
(the 1-minute candle close a terminal shows) next to the price seen at the
boundary's first tick.

usage: feed_compare.py [YYYY-MM-DD] [--minute 09:17 --strike-tokens CE,PE]
"""
import argparse
import csv
import datetime as dt
import os
import statistics as st

DIR = os.path.expanduser(os.environ.get("FEEDREC_DIR", "~/.trading-platform/feedrec"))
IST = dt.timezone(dt.timedelta(hours=5, minutes=30))


def load(path, kind):
    rows = []
    if not os.path.exists(path):
        return rows
    with open(path) as f:
        for r in csv.reader(f):
            try:
                if kind == "NSE" and r[0] == "NSE":
                    rows.append(dict(rx=int(r[1]), tok=int(r[2]), send=int(r[3]), ltt=int(r[4]), ltp=float(r[5]),
                                     ltq=int(r[6]), vol=int(r[7]), bid=float(r[8]), ask=float(r[9])))
                elif kind == "APOLLO" and r[0] == "APOLLO":
                    rows.append(dict(rx=int(r[1]), tok=int(r[2]), bcast=int(r[3]), ltt=int(r[4]), lut=int(r[5]),
                                     ltp=float(r[6]), ltq=int(r[7] or 0), vol=int(r[8] or 0),
                                     bid=float(r[9] or 0), ask=float(r[10] or 0)))
            except (ValueError, IndexError):
                pass
    return rows


def pct(xs, p):
    xs = sorted(xs)
    return xs[min(len(xs) - 1, int(p / 100 * len(xs)))]


def summary(name, xs):
    if not xs:
        return f"  {name}: no data"
    return (f"  {name}: n={len(xs)} min={min(xs):.1f} p50={st.median(xs):.1f} "
            f"p90={pct(xs, 90):.1f} p99={pct(xs, 99):.1f} max={max(xs):.1f} ms")


def first_seen(rows):
    """(token, volume) -> the first row carrying that cumulative volume."""
    out = {}
    for r in rows:
        if r["vol"] > 0:
            out.setdefault((r["tok"], r["vol"]), r)
    return out


def close_before(rows, tok, boundary_s):
    """Last trade with exchange trade time strictly before the boundary."""
    best = None
    for r in rows:
        if r["tok"] == tok and 0 < r["ltt"] < boundary_s:
            if best is None or (r["ltt"], r["vol"]) >= (best["ltt"], best["vol"]):
                best = r
    return best


def first_after(rows, tok, boundary_ns):
    for r in rows:
        if r["tok"] == tok and r["rx"] >= boundary_ns:
            return r
    return None


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("day", nargs="?", default=dt.datetime.now(IST).strftime("%Y-%m-%d"))
    ap.add_argument("--minute", help="HH:MM boundary to compare candle close vs first tick")
    ap.add_argument("--strike-tokens", help="CE,PE NSE tokens for --minute")
    a = ap.parse_args()

    nse = load(os.path.join(DIR, a.day + "_nse.csv"), "NSE")
    apo = load(os.path.join(DIR, a.day + "_apollo.csv"), "APOLLO")
    nse.sort(key=lambda r: r["rx"])
    apo.sort(key=lambda r: r["rx"])
    print(f"{a.day}: NSE {len(nse)} updates, Apollo {len(apo)} updates")

    print("\nNSE multicast (feed-decoder's source):")
    print(summary("our receive - exchange send stamp (TimeStamp2)", [(r["rx"] - r["send"]) / 1e6 for r in nse]))
    print(summary("our receive - trade time (LTT, whole s)", [(r["rx"] / 1e9 - r["ltt"]) * 1e3 for r in nse if r["ltt"]]))
    print("\nGreekSoft Apollo:")
    print(summary("our receive - BCastTime (whole s)", [(r["rx"] / 1e9 - r["bcast"]) * 1e3 for r in apo if r["bcast"]]))
    print(summary("our receive - trade time (ltt, whole s)", [(r["rx"] / 1e9 - r["ltt"]) * 1e3 for r in apo if r["ltt"]]))
    print("  (whole-second stamps: each value is real delay + 0..1000 ms rounding; the MIN is the best latency bound)")

    if nse and apo:
        n1, a1 = first_seen(nse), first_seen(apo)
        both = [k for k in a1 if k in n1]
        d = [(a1[k]["rx"] - n1[k]["rx"]) / 1e6 for k in both]
        print(f"\nSame trade in both feeds (token + cumulative volume): {len(both)} matched "
              f"of {len(a1)} Apollo / {len(n1)} NSE distinct trades")
        print(summary("Apollo arrival - NSE arrival (+ = NSE first)", d))
        if d:
            print(f"  NSE first in {sum(x > 0 for x in d)}, Apollo first in {sum(x < 0 for x in d)}")
        print(f"  trades only NSE delivered: {len(set(n1) - set(a1))}  (Apollo conflates updates)")

    if a.minute and a.strike_tokens:
        h, m = map(int, a.minute.split(":"))
        day = dt.datetime.strptime(a.day, "%Y-%m-%d").replace(tzinfo=IST)
        b = day.replace(hour=h, minute=m)
        bs, bns = int(b.timestamp()), int(b.timestamp()) * 10**9
        print(f"\nAt {a.minute}: candle close (last trade before :00) vs first tick after :00")
        tot = {}
        for leg, tok in zip(("CE", "PE"), map(int, a.strike_tokens.split(","))):
            for name, rows in (("NSE", nse), ("Apollo", apo)):
                c, f = close_before(rows, tok, bs), first_after(rows, tok, bns)
                cl = c["ltp"] if c else float("nan")
                ft = f["ltp"] if f else float("nan")
                tot.setdefault(name, [0, 0])
                tot[name][0] += cl
                tot[name][1] += ft
                print(f"  {leg} {name:6s} close {cl:8.2f}  first tick {ft:8.2f}" +
                      (f" (rx {dt.datetime.fromtimestamp(f['rx'] / 1e9, IST):%H:%M:%S.%f}, ltt {dt.datetime.fromtimestamp(f['ltt'], IST):%H:%M:%S})" if f else ""))
        for name, (cl, ft) in tot.items():
            print(f"  straddle {name:6s} close {cl:8.2f}  first tick {ft:8.2f}")


if __name__ == "__main__":
    main()
