#!/usr/bin/env python3
"""Rebuild the LUT minute record for a day from a running gateway (API) and its log.

The gateway saves every recorded minute to LUT_DATA_DIR (default
~/.trading-platform/lut): YYYY-MM-DD.jsonl / .csv / _day.json. Minutes
recorded before that existed (or while it could not write) are only in the
log as "[LUT] HH:MM table=... -> YES/NO" lines; this merges them in.
Existing saved minutes are kept; only missing minutes are added. Safe to
run any number of times. Read-only on the log; writes only the LUT files.

usage: scripts/lut_backfill_from_log.py [--day YYYY-MM-DD] [--log PATH ...]
"""
import argparse
import csv
import datetime as dt
import glob
import io
import json
import math
import os
import re
import urllib.request

BASE = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DATA = os.environ.get("LUT_DATA_DIR") or os.path.join(os.path.expanduser("~"), ".trading-platform", "lut")

MIN_RE = re.compile(
    r"^(\d{4})/(\d\d)/(\d\d) [\d:]+ \[LUT\] (\d\d:\d\d) table=(\S+) fut=([\d.]+) atm=(\d+) ce=([\d.]+) pe=([\d.]+) "
    r"otm=(\w+)@([\d.]+) dte=([\d.]+)/([\d.]+) build_iv=([\d.]+) adj_iv=([\d.]+) \[(.*?)\] iv_ratio=([\d.]+) \[(.*?)\] "
    r"straddle=([\d.]+) str_ratio=([\d.]+) \[(.*?)\] og=([-\d.]+) \[(.*?)\] adj_chg=([-\d.]+) \[(.*?)\] "
    r"coord=(\(\S+\)) -> (YES|NO|SKIP \(.*\)) tp=(\d+)bps")
SKIP_RE = re.compile(r"^(\d{4})/(\d\d)/(\d\d) [\d:]+ \[LUT\] (\d\d:\d\d) table=(\S+) SKIP (.*)$")
ENTRY_RE = re.compile(
    r"^(\d{4})/(\d\d)/(\d\d) [\d:]+ \[LUT\] .*FIRST YES -- PAPER ENTRY \(NOT executed\) (\d\d:\d\d) table (\S+) NIFTY (\S+) (\d+) "
    r"SELL CE (\d+) \+ SELL PE (\d+) \((\d+) lots x (\d+)\) fut ([\d.]+) adj build IV ([\d.]+) SL (\d+)bps=([\d.]+)pts TP (\d+)bps=([\d.]+)pts")

CSV_HEADER = ["date", "time", "table", "answer", "skip", "future", "atm", "ce_ltp", "pe_ltp", "otm_leg", "otm_price",
              "dte_raw", "dte_trading", "adj_factor", "build_iv", "adj_build_iv", "bld_bucket", "iv_ratio", "iv_bucket",
              "straddle", "str_ratio", "str_bucket", "norm_og", "og_bucket", "adj_iv_chg", "adj_bucket", "coord", "tp_bps"]


def f(v, d):
    return f"{float(v):.{d}f}"


def csv_row(day, e):
    ans = "SKIP" if e.get("skip") else ("YES" if e.get("allowed") else "NO")
    return [day, e["time"], e.get("stage", ""), ans, e.get("skip", ""), f(e.get("underlying", 0), 2), f(e.get("strike", 0), 0),
            f(e.get("ce_ltp", 0), 2), f(e.get("pe_ltp", 0), 2), e.get("otm_leg", ""), f(e.get("otm_price", 0), 2),
            f(e.get("raw_dte", 0), 4), f(e.get("trading_dte", 0), 4), f(e.get("adj_factor", 0), 4), f(e.get("build_iv", 0), 4),
            f(e.get("adj_build_iv", 0), 4), e.get("bld_label", ""), f(e.get("iv_ratio", 0), 4), e.get("iv_label", ""),
            f(e.get("straddle", 0), 2), f(e.get("str_ratio", 0), 4), e.get("str_label", ""), f(e.get("norm_og", 0), 4),
            e.get("og_label", ""), f(e.get("adj_iv_chg", 0), 5), e.get("adj_label", ""), e.get("coord_text", ""), f(e.get("tp_bps", 0), 0)]


def parse_logs(paths, day):
    mins, entry = {}, None
    for p in paths:
        try:
            fh = open(p, errors="replace")
        except OSError:
            continue
        with fh:
            for line in fh:
                if "[LUT]" not in line:
                    continue
                m = MIN_RE.match(line)
                if m:
                    g = m.groups()
                    if f"{g[0]}-{g[1]}-{g[2]}" != day:
                        continue
                    raw, trd = float(g[11]), float(g[12])
                    coord = [int(x) for x in g[25].strip("()").split(",")]
                    mins[g[3]] = {
                        "time": g[3], "stage": g[4], "underlying": float(g[5]), "strike": float(g[6]),
                        "ce_ltp": float(g[7]), "pe_ltp": float(g[8]), "otm_leg": g[9], "otm_price": float(g[10]),
                        "raw_dte": raw, "trading_dte": trd, "adj_factor": math.sqrt(raw / trd) if trd > 0 else 1,
                        "t_years": max(raw / 365.0, 1e-5),
                        "build_iv": float(g[13]), "adj_build_iv": float(g[14]), "bld_label": g[15],
                        "iv_ratio": float(g[16]), "iv_label": g[17], "straddle": float(g[18]), "str_ratio": float(g[19]),
                        "str_label": g[20], "norm_og": float(g[21]), "og_label": g[22], "adj_iv_chg": float(g[23]),
                        "adj_label": g[24], "coord": coord, "coord_text": g[25], "allowed": g[26] == "YES",
                        **({"skip": re.sub(r"^SKIP \((.*); table says (\w*)\)$", r"\1", g[26]),
                            "table_says": re.sub(r"^SKIP \((.*); table says (\w*)\)$", r"\2", g[26])} if g[26].startswith("SKIP") else {}),
                        "tp_bps": float(g[27]), "dte_label": str(coord[0] + 1), "table_yes_cells_today": 0,
                    }
                    continue
                m = SKIP_RE.match(line)
                if m:
                    g = m.groups()
                    if f"{g[0]}-{g[1]}-{g[2]}" == day and g[3] not in mins:
                        mins[g[3]] = {"time": g[3], "stage": g[4], "skip": g[5].strip()}
                    continue
                m = ENTRY_RE.match(line)
                if m and entry is None:
                    g = m.groups()
                    if f"{g[0]}-{g[1]}-{g[2]}" != day:
                        continue
                    entry = {"time": g[3], "stage": g[4], "expiry": g[5], "strike": float(g[6]), "qty": int(g[7]),
                             "lots": int(g[9]), "lot_size": int(g[10]), "underlying": float(g[11]),
                             "sl_bps": float(g[13]), "sl_points": float(g[14]), "tp_bps": float(g[15]), "tp_points": float(g[16]),
                             "source": "LUT YES (rebuilt from log)"}
                    entry["sl_rupees"] = entry["sl_points"] * entry["qty"]
                    entry["tp_rupees"] = entry["tp_points"] * entry["qty"]
    return mins, entry


def from_api(url, day):
    """Full minute records (prices included, also SKIP minutes) from a
    running gateway's /api/lut/state. Read-only GET."""
    try:
        with urllib.request.urlopen(url, timeout=5) as r:
            st = json.load(r).get("state") or {}
    except Exception as e:
        print(f"  (gateway API not used: {e})")
        return {}, None
    if st.get("day") and st["day"] != day:
        return {}, None
    mins = {}
    for e in st.get("minutes") or []:
        e.pop("nearest_yes", None)
        e.pop("nearest_yes_0920", None)
        if e.get("time"):
            mins[e["time"]] = e
    return mins, st.get("entry")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--day", default=dt.date.today().isoformat())
    ap.add_argument("--log", nargs="*", default=sorted(glob.glob(os.path.join(BASE, "logs", "*execution*.log*"))))
    ap.add_argument("--api", default="http://localhost:8005/api/lut/state", help="'' to skip the gateway API")
    a = ap.parse_args()
    day = a.day
    os.makedirs(DATA, exist_ok=True)
    jpath, cpath, dpath = (os.path.join(DATA, day + s) for s in (".jsonl", ".csv", "_day.json"))

    saved = {}
    if os.path.exists(jpath):
        for line in open(jpath):
            try:
                e = json.loads(line)
                saved[e["time"]] = e
            except Exception:
                pass
    from_log, entry = parse_logs(a.log, day)
    api_mins, api_entry = from_api(a.api, day) if a.api else ({}, None)
    if api_entry:
        entry = api_entry
    added = []
    for src in (api_mins, from_log):  # the API has prices for SKIP minutes too
        for t, e in src.items():
            cur = saved.get(t)
            # replace a missing minute, or a log-only SKIP row without prices
            if cur is None or (cur.get("skip") and not cur.get("underlying") and e.get("underlying")):
                saved[t] = e
                added.append(t)
    rows = [saved[t] for t in sorted(saved)]

    tmp = jpath + ".tmp"
    with open(tmp, "w") as fh:
        for e in rows:
            fh.write(json.dumps(e) + "\n")
    os.replace(tmp, jpath)
    buf = io.StringIO()
    w = csv.writer(buf, lineterminator="\n")
    w.writerow(CSV_HEADER)
    for e in rows:
        w.writerow(csv_row(day, e))
    with open(cpath + ".tmp", "w") as fh:
        fh.write(buf.getvalue())
    os.replace(cpath + ".tmp", cpath)

    st = {"day": day}
    if os.path.exists(dpath):
        try:
            st = json.load(open(dpath))
        except Exception:
            pass
    if not st.get("underlying_0916"):
        first = next((e for e in rows if not e.get("skip") and e["time"] >= "09:16"), None)
        if first:
            st["underlying_0916"] = first["underlying"]
            st["og_source"] = f"captured live at {first['time']}:00 (rebuilt from log)"
    if not st.get("entry") and entry:
        if not entry.get("evaluation"):
            entry["evaluation"] = saved.get(entry["time"], {})
        entry.setdefault("ce_token", 0)
        entry.setdefault("pe_token", 0)
        st["entry"] = entry
    with open(dpath + ".tmp", "w") as fh:
        json.dump(st, fh, indent=2)
    os.replace(dpath + ".tmp", dpath)

    yes = sum(1 for e in rows if e.get("allowed"))
    print(f"{day}: {len(rows)} minute(s) saved ({len(added)} added from the log, {yes} YES); "
          f"09:16 future {st.get('underlying_0916')}; paper entry {'yes at ' + st['entry']['time'] if st.get('entry') else 'none'}")
    print(f"  {jpath}\n  {cpath}\n  {dpath}")


if __name__ == "__main__":
    main()
