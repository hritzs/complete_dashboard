#!/usr/bin/env python3
"""Import minute option prices recorded elsewhere into the paper-sim data.

Input: a text/TSV/CSV file whose rows start with
    time  future  strike  CE  PE  [anything else...]
e.g. the "NIFTY Minute Straddle Simulation" P&L-history rows
(10:28:03  22,640.70  22,600  80.80  40.30 ...). Lines starting with # and
header lines are skipped; commas inside numbers are fine.

Output: LUT_DATA_DIR/YYYY-MM-DD_chain_import.jsonl (default
~/.trading-platform/lut). The gateway's paper sim merges it with what it
recorded itself: a minute / strike it recorded always wins; imported rows
only fill what is missing, and are labelled "imported" in the sim.
Re-running replaces the import file for that day (idempotent).

usage: scripts/lut_import_minutes.py FILE [--day YYYY-MM-DD] [--expiry 13-OCT-26] [--set current|next] [--src NAME]
"""
import argparse
import datetime as dt
import json
import os
import re

DATA = os.environ.get("LUT_DATA_DIR") or os.path.join(os.path.expanduser("~"), ".trading-platform", "lut")
ROW = re.compile(r"^\s*(\d{1,2}:\d{2})(:\d{2})?\s+([\d,]+\.?\d*)\s+([\d,]+\.?\d*)\s+([\d,]+\.?\d*)\s+([\d,]+\.?\d*)")


def num(s):
    return float(s.replace(",", ""))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("file")
    ap.add_argument("--day", default=dt.date.today().isoformat())
    ap.add_argument("--expiry", default="")
    ap.add_argument("--src", default="")
    ap.add_argument("--set", default="current", choices=["current", "next"], help="which expiry the rows are: current or the next weekly")
    a = ap.parse_args()
    src = a.src or os.path.basename(a.file)

    path = os.path.join(DATA, a.day + ("_chain_import.jsonl" if a.set == "current" else "_chain_next_import.jsonl"))
    existing = {}
    if os.path.exists(path):
        for line in open(path):
            try:
                m = json.loads(line)
                existing[m["t"]] = m
            except Exception:
                pass

    added, merged = 0, 0
    for line in open(a.file):
        if line.lstrip().startswith("#"):
            continue
        m = ROW.match(line.replace("\t", " "))
        if not m:
            continue
        t, fut, k, ce, pe = m.group(1).zfill(5), num(m.group(3)), num(m.group(4)), num(m.group(5)), num(m.group(6))
        ts = t + (m.group(2) or ":00")
        if fut <= 0 or k <= 0 or (ce <= 0 and pe <= 0):
            continue
        row = {"k": k, "ce": ce, "pe": pe, "cd": 0, "pd": 0, "cg": 0, "pg": 0, "src": src}
        cur = existing.get(t)
        if cur is None:
            existing[t] = {"t": t, "ts": ts, "f": fut, "atm": round(fut / 50) * 50, "exp": a.expiry, "src": src, "rows": [row]}
            added += 1
        elif not any(r["k"] == k for r in cur["rows"]):
            cur["rows"].append(row)
            merged += 1
        # first row per minute and strike wins (e.g. 09:18:32 before 09:18:59)

    os.makedirs(DATA, exist_ok=True)
    with open(path + ".tmp", "w") as fh:
        for t in sorted(existing):
            fh.write(json.dumps(existing[t]) + "\n")
    os.replace(path + ".tmp", path)
    times = sorted(existing)
    print(f"{a.day}: {len(existing)} imported minute(s) ({added} new, {merged} strike(s) added to existing) "
          f"{times[0] if times else '-'} .. {times[-1] if times else '-'}\n  {path}")


if __name__ == "__main__":
    main()
