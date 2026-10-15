"""Full-history GammaShort sizing analysis requested by the owner.

Fit and evaluate on all saved production days: deliberately IN SAMPLE.
No 60/20/20 selection, no average-size rescaling. Max 1.15; never boost
09:16 CW entries. Historical studies and their decisions are preserved.
Run: research/.venv/bin/python research/sizing_full_history.py
"""
import glob
import hashlib
import json
import pickle
from pathlib import Path

import numpy as np
import pandas as pd

from sizing_equation_v2 import CANDIDATES, FIXED_BINS, TICK
from sizing_v5_capped import dd_stats, guard, score_s1, score_s2, tier_sizes
from sizing_vrp import DAILY, realized_vol
from sizing_v6_borderline import B, flags

HERE = Path(__file__).resolve().parent
OUT = HERE / "output" / "full_history_sizing"


def load():
    run = HERE / "output/backtest_relax/run_027bf0249b"
    files = sorted(glob.glob(str(run / "days/*.pkl")))
    D = pd.DataFrame([pickle.load(open(f, "rb")) for f in files])
    D = D.sort_values("Date").reset_index(drop=True)
    if D.empty or D.Date.duplicated().any():
        raise ValueError("Missing or duplicate production days")
    D["Net_PnL"] = pd.to_numeric(D.Net_PnL, errors="coerce").fillna(0.)
    D["Leg"] = D.Leg.fillna("")
    ref = pd.read_csv(HERE / "output/backtest_final/run_027bf0249b/daily_log.csv")
    check = ref[["Date", "Net_PnL"]].merge(D[["Date", "Net_PnL"]], on="Date", validate="one_to_one")
    if len(check) != len(ref) or not np.allclose(check.Net_PnL_x.fillna(0), check.Net_PnL_y, atol=1e-8, rtol=0):
        raise ValueError("Production PnL differs from frozen reference")
    D["DTE"] = np.minimum(D.DTE_idx + 1, 5)
    D["Table"] = D.Stage.map({"09:16": 0, "09:17": 1, "09:18": 2}).where(D.Leg == "CW").fillna(3)
    D["entry"] = pd.to_numeric(D.Entry_Time.astype(str).str.replace(":", "", regex=False), errors="coerce")
    F = pd.read_csv(HERE / "output/entry_window_features.csv")
    if "error" in F:
        F = F[F.error.isna()]
    F["str_chg_abs"] = F.str_chg_pct.abs()
    feature_cols = ["Date", "entry", "straddle0", "u0"] + TICK
    if F.duplicated(["Date", "entry"]).any():
        raise ValueError("Duplicate entry-window feature keys")
    D = D.merge(F[feature_cols], how="left", on=["Date", "entry"], validate="one_to_one")
    D = D.merge(realized_vol(), how="left", on="Date", validate="one_to_one")
    D["IV"] = np.where(D.Leg == "NW", D.NW_Build_IV, D.Build_IV).astype(float)
    for n in (10, 20):
        D[f"VRP{n}"] = D.IV / D[f"RV{n}"]
    D["SPREAD20"] = D.IV - D.RV20
    D["RV_TREND"] = D.RV5 / D.RV20
    D["STR_SPOT"] = D.straddle0 / D.u0
    D["CUSHION"] = D.straddle0 / (D.Entry_U * D.SL_bps / 1e4)
    D["Weekday"] = pd.to_datetime(D.Date).dt.weekday
    D["Gap_abs"] = D.Norm_OG.abs()
    return D, len(check)


def build_candidates(D):
    pnl = D.Net_PnL.to_numpy(float)
    cw = D.Leg.eq("CW").to_numpy()
    traded = D.Leg.isin(["CW", "NW"]).to_numpy()
    S, effects, coverage = {"Flat size 1": np.ones(len(D))}, [], []

    def add(name, size):
        s = np.asarray(size, float).copy()
        if not np.isfinite(s).all() or (s < 0).any():
            raise ValueError(f"Invalid size: {name}")
        s = np.minimum(s, 1.15)
        s[cw & D.Stage.eq("09:16").to_numpy()] = np.minimum(s[cw & D.Stage.eq("09:16").to_numpy()], 1.)
        s[~traded] = 1.
        S[name] = s

    def adjustments(col, mask, edges=None):
        x = pd.to_numeric(D[col], errors="coerce").to_numpy(float)
        valid = mask & np.isfinite(x)
        if not valid.any():
            raise ValueError(f"No available values for {col}")
        if edges is None:
            edges = np.quantile(x[valid], [1/3, 2/3])
        bins = np.searchsorted(edges, np.nan_to_num(x, nan=0.), side="right")
        mu = pnl[mask].mean()
        adjustment = np.zeros(len(D))
        for k in range(len(edges) + 1):
            m = valid & (bins == k)
            n = int(m.sum())
            a = .6 * (pnl[m].mean() - mu) * n / (n + 40) / mu if n else 0.
            adjustment[np.isfinite(x) & (bins == k)] = a
            effects.append(dict(Input=col, Scope="CW" if np.array_equal(mask, cw) else "Book",
                                Bin=k, Lower=edges[k-1] if k else np.nan,
                                Upper=edges[k] if k < len(edges) else np.nan,
                                Trades=n, Mean_PnL=pnl[m].mean() if n else np.nan, Adjustment=a))
        coverage.append(dict(Input=col, Scope_trades=int(mask.sum()), Available=int(valid.sum()), Missing=int((mask & ~np.isfinite(x)).sum())))
        return np.where(mask, adjustment, 0.)

    # Original fixed-bin/table/stability equations, refitted on all CW trades.
    cols = list(dict.fromkeys(c for v in CANDIDATES.values() for c in v))
    A = {c: adjustments(c, cw, FIXED_BINS[c][0] if c in FIXED_BINS else None) for c in cols}
    for name, inputs in CANDIDATES.items():
        if not inputs:
            continue
        raw = np.clip(1 + sum(A[c] for c in inputs), .25, 1.15)
        add("Bucket: " + name, raw)
        add("Bucket cut: " + name, np.minimum(raw, 1.))

    # Existing S1/S2 scores, fitted on all CW trades; no mean normalization.
    scores = {"S1": score_s1(D, cw, cw), "S2": score_s2(D, cw, cw)}
    for name, score in scores.items():
        for label, levels in {"mild": [.75, 1., 1.15], "deep": [.5, 1., 1.15],
                              "cut only": [.5, 1., 1.], "skip low": [0., 1., 1.]}.items():
            add(f"{name} {label}", tier_sizes(score, cw, cw, levels))
    for name in list(S):
        if name.startswith(("S1", "S2")) and not name.endswith(("cut only", "skip low")):
            add(name + " + DD guard", guard(S[name], pnl))

    # Volatility, stop-distance, VRP and calendar families on the whole book.
    for col in ("IV", "RV10", "RV20", "STR_SPOT", "CUSHION"):
        x = D[col].to_numpy(float)
        scope = cw if col in ("STR_SPOT", "CUSHION") else traded
        valid = scope & np.isfinite(x) & (x > 0)
        med = np.median(x[valid])
        raw = np.ones(len(D))
        raw[valid] = np.clip(x[valid] / med if col == "CUSHION" else med / x[valid], .5, 1.15)
        coverage.append(dict(Input=col, Scope_trades=int(scope.sum()), Available=int(valid.sum()), Missing=int((scope & ~valid).sum())))
        add("Vol/stop: " + col, raw)
        add("Vol/stop cut: " + col, np.minimum(raw, 1.))
    raw = np.ones(len(D))
    stop = D.SL_bps.to_numpy(float)
    valid = traded & np.isfinite(stop) & (stop > 0)
    raw[valid] = np.clip(14 / stop[valid], .5, 1.15)
    add("Stop: 14 / SL bps", raw)

    for col in ("VRP10", "VRP20", "SPREAD20", "RV_TREND"):
        a = adjustments(col, traded)
        add("VRP tiers: " + col, np.clip(1+a, .25, 1.15))
        add("VRP tiers cut: " + col, np.clip(1+a, .25, 1.))
        x = D[col].to_numpy(float)
        valid = traded & np.isfinite(x)
        high = np.quantile(x[valid], 2/3)
        add("VRP high cut: " + col, np.where(valid & (x > high), .5, 1.))
    calendar = []
    # Calendar effects are CW-only, preserving NW at size 1.
    for col in ("DTE", "Event", "Weekday", "Gap_abs"):
        if col == "Event":
            D[col] = D[col].astype(bool).astype(int)
        edges = None if col == "Gap_abs" else np.unique(D.loc[cw, col].astype(float))[:-1] + .5
        a = adjustments(col, cw, edges)
        calendar.append(a)
        add("Calendar: " + col, np.clip(1+a, .25, 1.15))
        add("Calendar cut: " + col, np.clip(1+a, .25, 1.))
    add("Calendar combined", np.clip(1+sum(calendar), .25, 1.15))
    add("Calendar combined cut", np.clip(1+sum(calendar), .25, 1.))

    f = flags(D, B.load_cw_luts(B.LUT_DIR))
    if not f["cell_check"][cw].all():
        raise ValueError("Stored LUT cells do not match their inputs")
    for label, key, level in (("Margin2 x0.75", "near2", .75), ("Margin2 x0.5", "near2", .5),
                              ("Margin5 x0.75", "near5", .75), ("Isolated x0.75", "isolated", .75),
                              ("Isolated x0.5", "isolated", .5)):
        add(label, np.where(cw & f[key], level, 1.))

    # Flat-book drawdown/streak, using only results up to the previous day.
    multipliers = {"3 losses cut": [], "DD100 cut": [], "DD gradual cut": []}
    cum = peak = 0.
    streak = 0
    for p in pnl:
        dd = peak-cum
        for name, value in zip(multipliers, (.5 if streak >= 3 else 1., .5 if dd > 100 else 1., max(.5, 1-dd/200))):
            multipliers[name].append(value)
        cum += p
        peak = max(peak, cum)
        streak = streak+1 if p < 0 else (0 if p > 0 else streak)
    for name, multiplier in multipliers.items():
        add(name, multiplier)
    return S, pd.DataFrame(effects), pd.DataFrame(coverage), scores


def main():
    D, reference_days = load()
    S, effects, coverage, scores = build_candidates(D)
    pnl = D.Net_PnL.to_numpy(float)
    traded = D.Leg.isin(["CW", "NW"]).to_numpy()
    years = pd.to_datetime(D.Date).dt.year.to_numpy()
    rows, annual, legs = [], [], []
    for name, size in S.items():
        p = pnl * size
        st = dd_stats(p)
        st.update(Candidate=name, Avg_size=size[traded].mean(), Min_size=size[traded].min(),
                  Max_size=size[traded].max(), Pct_cut=100*(size[traded]<1).mean(), Pct_up=100*(size[traded]>1).mean(),
                  Entries=int((traded & (size>0)).sum()))
        rows.append(st)
        for year in sorted(set(years)):
            annual.append(dict(Candidate=name, Year=year, **dd_stats(p[years == year])))
        for leg in ("CW", "NW"):
            legs.append(dict(Candidate=name, Leg=leg, Net=p[D.Leg.eq(leg)].sum()))
    R = pd.DataFrame(rows)
    flat = R.iloc[0]
    R["Extra_net"] = R.Net-flat.Net
    R["MDD_change"] = R.MDD-flat.MDD
    R["Net_MDD_change_pct"] = 100*(R.Net_per_MDD/flat.Net_per_MDD-1)
    R["Beats_flat_both_in_sample"] = (R.Net>flat.Net) & (R.Net_per_MDD>flat.Net_per_MDD)
    flat = R.loc[R.Candidate.eq("Flat size 1")].iloc[0]
    R = R.sort_values("Net_per_MDD", ascending=False).reset_index(drop=True)
    OUT.mkdir(exist_ok=True, parents=True)
    R.to_csv(OUT / "candidates.csv", index=False)
    daily = D[["Date", "Leg", "Stage", "Net_PnL"]].copy()
    for key, score in scores.items():
        daily[key+"_score"] = score
    sizes = pd.DataFrame({"Date": D.Date, **S})
    pnls = pd.DataFrame({"Date": D.Date, **{name: pnl*s for name, s in S.items()}})
    notes = [
        "Owner-requested full-history analysis: all days fitted/evaluated together; NO 60/20/20 split.",
        f"Dates {D.Date.min()} through {D.Date.max()}; {len(D)} days; {int(traded.sum())} recorded entries; {reference_days} days match the frozen production reference exactly.",
        "IN SAMPLE: PnL-fitted equations use outcomes across this same history. Rankings are descriptive, not unseen-data alpha or a live deployment decision.",
        "Sizing multiplier base 1; max 1.15; 09:16 CW never above 1; no average-size rescaling. Existing event-day base sizing remains inside the original PnL.",
        "Complete CW+NW book included; parameter/score/stability/calendar/borderline rules adjust CW only. IV/RV/VRP and drawdown rules can adjust both legs.",
        "Uses saved production PnL with linear size scaling, including its existing cost assumptions. No new tick replay, liquidity/impact model or fixed-fee recalculation.",
        "Normalized Rs = index points * 25000 / entry underlying. Extra_net is incremental historical profit, not factor-adjusted alpha. No Sharpe/CAGR without account-capital assumptions.",
        "Missing entry-window readings receive neutral adjustments for those inputs; availability counts are in Coverage. RV from same-contract closes up to yesterday.",
        "Whole-history drawdown is recomputed from the concatenated daily series; yearly results use the same full-history fit, not annual refits.",
        "Calendar candidates use DTE, Event, Weekday and absolute gap; combined calendar excludes the prior monthly-expiry proxy.",
        "Trades statistic counts nonzero PnL days; Entries counts actual positive-size recorded trades, including breakevens.",
        "Frozen 60/20/20 reports and their previous flat decisions remain separate historical analyses.",
    ]
    with pd.ExcelWriter(OUT / "full_history_sizing.xlsx") as w:
        pd.DataFrame({"Method": notes}).to_excel(w, sheet_name="Read_me", index=False)
        for name, table in {"Candidates": R, "By_year": pd.DataFrame(annual), "By_leg": pd.DataFrame(legs),
                            "Fit_effects": effects, "Coverage": coverage, "Daily": daily,
                            "Daily_sizes": sizes, "Daily_PnL": pnls}.items():
            table.to_excel(w, sheet_name=name, index=False)
        tiers = []
        cw = D.Leg.eq("CW").to_numpy()
        for key, score in scores.items():
            q = np.quantile(score[cw], [1/3, 2/3])
            bucket = np.searchsorted(q, score, side="right")
            for k, label in enumerate(("Low", "Mid", "High")):
                m = cw & (bucket == k)
                tiers.append(dict(Score=key, Tier=label, Lower_cut=q[0], Upper_cut=q[1],
                                  Trades=int(m.sum()), Mean_PnL=pnl[m].mean()))
        pd.DataFrame(tiers).to_excel(w, sheet_name="Score_tiers", index=False)
    inputs = [HERE / name for name in ("sizing_full_history.py", "sizing_equation_v2.py", "sizing_v4_params.py",
                                      "sizing_v5_capped.py", "sizing_v6_borderline.py", "sizing_vrp.py", "GammaShort_Backtest_Final.py")]
    inputs += [HERE / "output/entry_window_features.csv", Path(DAILY),
               HERE / "output/backtest_final/run_027bf0249b/daily_log.csv",
               HERE / "output/backtest_relax/run_027bf0249b/config.json"]
    inputs += [Path(B.LUT_DIR) / name for name in B.STAGE_FILES]
    digests = {str(p): hashlib.sha256(p.read_bytes()).hexdigest() for p in inputs}
    daily_hash = hashlib.sha256()
    for path in sorted((HERE / "output/backtest_relax/run_027bf0249b/days").glob("*.pkl")):
        daily_hash.update(path.name.encode())
        daily_hash.update(path.read_bytes())
    manifest = dict(Mode="full-history in-sample", Days=len(D), First_date=str(D.Date.min()), Last_date=str(D.Date.max()),
                    Candidates=len(S), Input_sha256=digests, Daily_pickles_sha256=daily_hash.hexdigest())
    (OUT / "manifest.json").write_text(json.dumps(manifest, indent=2)+"\n")
    cols = ["Candidate", "Net", "MDD", "Net_per_MDD", "Extra_net", "Avg_size"]
    top = R.head(10)
    lines = ["# GammaShort full-history sizing", "", *["- "+n for n in notes], "",
             "## Highest historical net / drawdown", "", "| Candidate | Net | MDD | Net/MDD | Extra net | Mean size |",
             "|---|---:|---:|---:|---:|---:|"]
    for _, row in top[cols].iterrows():
        lines.append("| " + row.Candidate + " | " + " | ".join(f"{row[c]:.3f}" for c in cols[1:]) + " |")
    best_net = R.loc[R.Net.idxmax()]
    lines += ["", f"Flat: net {flat.Net:.2f}, MDD {flat.MDD:.2f}, net/MDD {flat.Net_per_MDD:.2f}.",
              f"Best historical net: {best_net.Candidate}; net {best_net.Net:.2f}, MDD {best_net.MDD:.2f}, net/MDD {best_net.Net_per_MDD:.2f}.",
              f"Candidates beating flat on both historical net and net/MDD: {int(R.Beats_flat_both_in_sample.sum())}.",
              "", "Artifacts: full_history_sizing.xlsx and candidates.csv in this folder. Reproduce with research/.venv/bin/python research/sizing_full_history.py."]
    (OUT / "SUMMARY.md").write_text("\n".join(lines)+"\n")
    print("\n".join(notes[:5]))
    print(f"\n{len(R)} candidates; top 10 by full-history net/MDD:")
    print(top[cols].round(3).to_string(index=False))
    print("\nFlat:", flat[cols].to_dict())
    print("\nBest net:", R.loc[R.Net.idxmax(), cols].to_dict())
    print("\nWrote", OUT)


if __name__ == "__main__":
    main()
