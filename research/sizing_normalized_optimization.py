"""Owner-requested full-history sizing optimization, arithmetic trade mean = 1.

Run with research/.venv/bin/python research/sizing_normalized_optimization.py.
Research only: parameters AND normalization use the complete observed history.
"""
import hashlib
import json
from pathlib import Path

import numpy as np
import pandas as pd
from openpyxl.chart import LineChart, Reference

from sizing_full_history import build_candidates, load
from sizing_v5_capped import dd_stats

HERE = Path(__file__).resolve().parent
OUT = HERE / "output/normalized_sizing"
FLOOR, CAP, SEED = .75, 1.15, 42


def project(raw, lower, upper):
    """Euclidean projection onto bounds and sum(size)=number of entries.

    Solves size=clip(raw+offset, lower, upper) by monotone bisection.
    Dividing by the mean then clipping would break the exact mean constraint.
    """
    raw = np.asarray(raw, float)
    lower, upper = np.asarray(lower, float), np.asarray(upper, float)
    if (raw.ndim != 1 or not len(raw) or lower.shape != raw.shape or upper.shape != raw.shape
            or not all(np.isfinite(a).all() for a in (raw, lower, upper))
            or (lower > upper).any() or lower.sum() > len(raw) or upper.sum() < len(raw)):
        raise ValueError("Invalid/infeasible sizing inputs")
    left, right = float(np.min(lower-raw)), float(np.max(upper-raw))
    for _ in range(43):
        offset = (left+right)/2
        if np.clip(raw+offset, lower, upper).sum() < len(raw):
            left = offset
        else:
            right = offset
    offset = (left+right)/2
    size = np.clip(raw+offset, lower, upper)
    if abs(size.mean()-1) > 1e-10:
        raise AssertionError("Mean size is not one")
    return size, offset


def design(D, traded, scores):
    """Entry-known predictors; all medians and scales fit on the full history."""
    features, transformations = {}, []
    cw = D.Leg.eq("CW").to_numpy()[traded]
    numeric = {c: D[c].to_numpy(float)[traded] for c in
               ("IV", "RV10", "RV20", "VRP10", "VRP20", "SPREAD20", "RV_TREND")}
    for c in ("IV_Ratio", "Str_Ratio", "Norm_OG", "Adj_IV_Chg", "Fixed_IDV",
              "u_range_bps", "str_range_pct", "str_chg_abs", "spread_pct"):
        numeric[c] = np.where(cw, D[c].to_numpy(float)[traded], np.nan)
    for name, score in scores.items():
        numeric[name+"_fullfit"] = np.where(cw, score[traded], np.nan)
    for name, values in numeric.items():
        valid = np.isfinite(values)
        median = np.median(values[valid])
        values = np.where(valid, values, median)
        mean, std = values.mean(), values.std()
        if std <= 1e-12:
            continue
        features[name] = np.clip((values-mean)/std, -3, 3)
        transformations.append(dict(Feature=name, Median=median, Mean=mean, Std=std,
                                    Missing=int((~valid).sum()), Clip_standardized=3))
    for field in ("Stage", "DTE", "Weekday"):
        values = D[field].to_numpy()[traded]
        levels = sorted(set(values[cw]))
        for level in levels[1:]:
            name = f"CW {field}={level}"
            features[name] = (cw & (values == level)).astype(float)
            transformations.append(dict(Feature=name, Encoding="CW indicator"))
    features["CW Event"] = (cw & D.Event.to_numpy(bool)[traded]).astype(float)
    features["NW"] = (~cw).astype(float)
    names = list(features)
    X = np.column_stack(list(features.values()))
    if not np.isfinite(X).all():
        raise ValueError("Invalid design matrix")
    return X, names, pd.DataFrame(transformations)


def metrics(p):
    equity = np.cumsum(p)
    dd = np.maximum.accumulate(np.r_[0., equity])[1:]-equity
    return float(p.sum()), float(dd.max())


def optimize(X, pnl, lower, upper, base_net, base_dd):
    """Deterministic multi-start bounded coordinate pattern search.

    Coefficients in [-1,1]. Full-history size projection at every evaluation.
    Profit with DD cap uses hard feasibility, not a hidden soft DD penalty.
    This is a numerical local search, not a guarantee of global optimality.
    """
    rng = np.random.default_rng(SEED)
    evaluations, trace = 0, []
    objectives = ("Max profit", "Profit DD capped", "Ratio profit floored")
    best_seen = {}

    def evaluate(beta):
        nonlocal evaluations
        evaluations += 1
        size, offset = project(1+np.einsum("ij,j->i", X, beta), lower, upper)
        net, dd = metrics(pnl*size)
        result = dict(beta=beta.copy(), size=size, offset=offset, net=net, dd=dd)
        for objective in objectives:
            if objective not in best_seen or value(result, objective) > value(best_seen[objective], objective):
                best_seen[objective] = result
        return result

    def value(result, objective):
        net, dd = result["net"], result["dd"]
        if objective == "Profit DD capped" and dd > base_dd+1e-9:
            return -np.inf
        if objective == "Ratio profit floored" and net < base_net-1e-9:
            return -np.inf
        benefit = (net/dd)/(base_net/base_dd) if objective == "Ratio profit floored" else net/base_net
        return benefit-1e-5*np.mean(result["beta"]**2)

    seeds = [evaluate(np.zeros(X.shape[1]))]
    for scale in (.025, .075, .15, .3):
        seeds += [evaluate(np.clip(rng.normal(0, scale, X.shape[1]), -1, 1)) for _ in range(24)]
    # One-feature starts help objectives with a narrow hard-feasible region.
    for k in range(X.shape[1]):
        for direction in (-1, 1):
            beta = np.zeros(X.shape[1]); beta[k] = direction*.08
            seeds.append(evaluate(beta))
    winners = {}
    for objective in objectives:
        starts = sorted(seeds, key=lambda r: value(r, objective), reverse=True)[:2]
        # Keep the original starts as well as previous objective winners;
        # replacing the original starts can lose a useful local optimum.
        starts += [r for r in winners.values() if np.isfinite(value(r, objective))]
        winner = starts[0]
        for start_no, current in enumerate(starts):
            for step in (.12, .06, .03, .015, .0075, .00375):
                for _ in range(3):
                    changed = False
                    for k in range(X.shape[1]):
                        candidate = current
                        for direction in (-1, 1):
                            beta = current["beta"].copy()
                            beta[k] = np.clip(beta[k]+direction*step, -1, 1)
                            trial = evaluate(beta)
                            if value(trial, objective) > value(candidate, objective)+1e-10:
                                candidate = trial
                        if candidate is not current:
                            changed = True
                            current = candidate
                    if not changed:
                        break
                trace.append(dict(Objective=objective, Start=start_no, Step=step,
                                  Net=current["net"], MDD=current["dd"], Evaluations=evaluations))
            if value(current, objective) > value(winner, objective):
                winner = current
        winners[objective] = winner
        print(f"Optimized {objective}: net {winner['net']:.2f}, MDD {winner['dd']:.2f}; evaluations {evaluations}", flush=True)
    # Report the best feasible policy observed anywhere in the search for
    # each objective, including policies discovered by another objective.
    for objective, result in best_seen.items():
        trace.append(dict(Objective=objective, Start=-1, Step=0., Net=result['net'],
                          MDD=result['dd'], Evaluations=evaluations))
    return best_seen, pd.DataFrame(trace), evaluations


def main():
    D, reference_days = load()
    originals, _, coverage, scores = build_candidates(D)
    traded = D.Leg.isin(["CW", "NW"]).to_numpy()
    td = D[traded]
    pnl = D.Net_PnL.to_numpy(float)
    p = pnl[traded]
    lower = np.full(len(td), FLOOR)
    upper = np.where(td.Leg.eq("CW") & td.Stage.eq("09:16"), 1., CAP)
    base_net, base_dd = metrics(p)
    S = {"Flat size 1": np.ones(len(D))}
    for name, raw in originals.items():
        if name == "Flat size 1":
            continue
        s = np.ones(len(D))
        s[traded], _ = project(raw[traded], lower, upper)
        S["Normalized: "+name] = s
    X, names, transforms = design(D, traded, scores)
    print(f"{len(D)} days, {traded.sum()} entries, {X.shape[1]} model features; optimizing...", flush=True)
    winners, trace, evaluations = optimize(X, p, lower, upper, base_net, base_dd)
    coefficient_rows = []
    for objective, result in winners.items():
        s = np.ones(len(D)); s[traded] = result["size"]
        S["Optimized: "+objective] = s
        coefficient_rows += [dict(Objective=objective, Feature=name, Coefficient=float(beta),
                                 Normalization_offset=result["offset"]) for name, beta in zip(names, result["beta"])]
    rows, yearly, attribution = [], [], []
    yr = pd.to_datetime(D.Date).dt.year.to_numpy()
    # Existing event reduction is embedded in base PnL; report its exposure proxy
    # separately rather than claiming that equal arithmetic means equal capital.
    base_exposure = np.where(td.Leg.eq("CW") & td.Event.astype(bool), 1/3, 1.)
    for name, size in S.items():
        v = size[traded]
        if abs(v.mean()-1)>1e-10 or (v<FLOOR-1e-12).any() or (v>upper+1e-12).any():
            raise AssertionError(f"Sizing constraints failed: {name}")
        st = dd_stats(pnl*size)
        st.update(Candidate=name, Mean_size=v.mean(), Size_sum=v.sum(), Min_size=v.min(), Max_size=v.max(),
                  Increase_budget=np.maximum(v-1,0).sum(), Decrease_budget=np.maximum(1-v,0).sum(),
                  Pct_cut=100*np.mean(v<1-1e-9), Pct_up=100*np.mean(v>1+1e-9),
                  Extra_net=st["Net"]-base_net, Net_gain_pct=100*(st["Net"]/base_net-1),
                  DD_change_pct=100*(st["MDD"]/base_dd-1),
                  Event_weighted_exposure_ratio=float(np.dot(v,base_exposure)/base_exposure.sum()),
                  Beats_both=st["Net"]>base_net+1e-8 and st["Net_per_MDD"]>base_net/base_dd+1e-8)
        rows.append(st)
        for y in sorted(set(yr)):
            yearly.append(dict(Candidate=name, Year=y, Mean_size=size[traded & (yr==y)].mean(),
                               **dd_stats((pnl*size)[yr==y])))
        for leg in ("CW", "NW"):
            mask = D.Leg.eq(leg).to_numpy()
            attribution.append(dict(Candidate=name, Leg=leg, Net=(pnl*size)[mask].sum(), Mean_size=size[mask].mean()))
    R = pd.DataFrame(rows).sort_values("Net_per_MDD", ascending=False).reset_index(drop=True)
    OUT.mkdir(parents=True, exist_ok=True)
    R.to_csv(OUT/"candidates.csv", index=False)
    coefficients = pd.DataFrame(coefficient_rows)
    coefficients.to_csv(OUT/"optimized_coefficients.csv", index=False)
    daily = D[["Date", "Leg", "Stage", "Event", "Net_PnL"]].copy()
    sizes = pd.DataFrame({"Date": D.Date, **S})
    pnls = pd.DataFrame({"Date": D.Date, **{name: pnl*size for name,size in S.items()}})
    best = R[R.Beats_both].iloc[0] if R.Beats_both.any() else R[R.Candidate.eq("Flat size 1")].iloc[0]
    notes = [
        "Latest owner instruction supersedes the previous no-rescaling rule: normalize arithmetic mean size to EXACTLY 1 across all recorded traded days (including breakeven entries). No-trade days excluded.",
        f"Complete history {D.Date.min()} to {D.Date.max()}: {len(D)} days, {traded.sum()} entries; frozen reference agrees on {reference_days} dates. No 60/20/20 split.",
        "Full-history fit, coefficient optimization AND normalization: explicitly IN SAMPLE. Exact full-period normalization needs the full future score distribution; this is not a causal live sizing policy or a deployment decision.",
        "Size floor 0.75, cap 1.15. CW 09:16 upper bound remains 1. All entries are taken; there is no 0/1 trade filter. Flat is feasible.",
        "Normalization is a box-and-sum Euclidean projection: size=clip(raw+offset, lower, upper), offset solved by bisection. Mean stays 1 after limits; dividing then clipping would not guarantee this.",
        "Historical rule templates are projected under the same constraints. Normalization can raise formerly cut-only rules and change their NW size; source rule names describe the original template, not the final exposure scope.",
        "Optimized policy: size=clip(1+sum(beta*feature)+offset, lower, upper); numeric features median-filled, standardized and clipped [-3,3]. Indicator features use entry-known stage/DTE/calendar/leg. S1/S2 are full-history-fitted predictors.",
        "Technique: seeded multi-start bounded coordinate pattern search (beta [-1,1]), coarse-to-fine steps, exact mean projection at every evaluation. Original starts retained alongside cross-objective warm starts; each objective reports its best feasible policy seen anywhere in the search. Global optimality is not claimed.",
        "Objectives: maximum net; maximum net with MDD <= flat; maximum net/MDD with net >= flat. Hard constraints are tested during search. Tiny coefficient penalty 1e-5*mean(beta^2) discourages unnecessarily large coefficients.",
        "Mean 1 is an arithmetic mean of multipliers, not risk parity or identical rupee exposure. Existing event reductions remain in base PnL. Event_weighted_exposure_ratio reports event-adjusted notional proxy; it need not equal 1.",
        "Saved production net PnL and existing costs are scaled linearly. No quantity-specific execution replay, nonlinear market impact, lot rounding or recalculated fixed fees. Units: normalized Rs for 25000 notional.",
        "Full-history mean is 1; annual and CW/NW subgroup means can differ. Drawdown is recomputed over the whole daily sequence, not summed by year.",
        "All historical split reports and unnormalized full-history report remain preserved. Prior live operating conclusion is not changed by this descriptive optimization.",
    ]
    with pd.ExcelWriter(OUT/"normalized_sizing.xlsx") as w:
        for sheet, data in {"Read_me":pd.DataFrame({"Method":notes}), "Candidates":R,
                            "By_year":pd.DataFrame(yearly), "By_leg":pd.DataFrame(attribution),
                            "Coefficients":coefficients, "Feature_transforms":transforms,
                            "Feature_coverage":coverage, "Optimization_trace":trace,
                            "Daily":daily, "Daily_sizes":sizes, "Daily_PnL":pnls}.items():
            data.to_excel(w,sheet_name=sheet,index=False)
        plotted = ["Flat size 1"]+[name for name in S if name.startswith("Optimized:")]
        curves = {"Equity_curves":{"Date":pd.to_datetime(D.Date)},
                  "Drawdown_curves":{"Date":pd.to_datetime(D.Date)}}
        for name in plotted:
            equity = np.cumsum(pnl*S[name])
            curves["Equity_curves"][name] = equity
            curves["Drawdown_curves"][name] = np.maximum.accumulate(np.r_[0.,equity])[1:]-equity
        for sheet, columns in curves.items():
            pd.DataFrame(columns).to_excel(w,sheet_name=sheet,index=False)
            ws = w.sheets[sheet]
            chart = LineChart()
            chart.title = sheet.replace('_',' ')
            chart.y_axis.title = "Normalized Rs"
            chart.x_axis.title = "Date"
            chart.width, chart.height = 26, 13
            chart.add_data(Reference(ws,min_col=2,max_col=len(columns),min_row=1,max_row=len(D)+1),titles_from_data=True)
            chart.set_categories(Reference(ws,min_col=1,min_row=2,max_row=len(D)+1))
            ws.add_chart(chart,"G2")
    old_manifest = json.loads((HERE/"output/full_history_sizing/manifest.json").read_text())
    inputs = old_manifest["Input_sha256"]
    inputs[str(Path(__file__).resolve())] = hashlib.sha256(Path(__file__).read_bytes()).hexdigest()
    for path in list(inputs):
        inputs[path] = hashlib.sha256(Path(path).read_bytes()).hexdigest()
    h = hashlib.sha256()
    for path in sorted((HERE/"output/backtest_relax/run_027bf0249b/days").glob("*.pkl")):
        h.update(path.name.encode()); h.update(path.read_bytes())
    manifest = dict(Mode="full-history exact trade-mean normalized optimization", Floor=FLOOR, Cap=CAP,
                    Seed=SEED, Entries=int(traded.sum()), Days=len(D), Evaluations=evaluations,
                    Features=names, Input_sha256=inputs, Daily_pickles_sha256=h.hexdigest())
    (OUT/"manifest.json").write_text(json.dumps(manifest,indent=2)+"\n")
    cols=["Candidate","Net","MDD","Net_per_MDD","Mean_size","Extra_net"]
    highlights=R[R.Candidate.str.startswith("Optimized:") | R.Candidate.eq("Flat size 1")]
    lines=["# Mean-one GammaShort sizing optimization", "", *["- "+n for n in notes], "",
           "| Candidate | Net | MDD | Net/MDD | Mean size | Extra net |", "|---|---:|---:|---:|---:|---:|"]
    for _,row in highlights[cols].iterrows():
        lines.append("| "+row.Candidate+" | "+" | ".join(f"{row[c]:.6f}" for c in cols[1:])+" |")
    lines += ["",f"{len(R)} candidates; {int(R.Beats_both.sum())} beat flat on both full-history net and net/MDD.",
              f"Highest historical ratio among candidates beating both: {best.Candidate}; net {best.Net:.2f}, MDD {best.MDD:.2f}, net/MDD {best.Net_per_MDD:.2f}."]
    (OUT/"SUMMARY.md").write_text("\n".join(lines)+"\n")
    print(highlights[cols].round(6).to_string(index=False))
    print(f"Wrote {OUT}; {len(R)} candidates; {int(R.Beats_both.sum())} beat both.")


if __name__ == "__main__":
    main()
