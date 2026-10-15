"""Drawdown-first research sizing with mandatory cuts on estimated dicey days.

Specification: DRAWDOWN_SIZING_SPEC.md. Full-history, in-sample research.
"""
import hashlib
import json
from pathlib import Path

import numpy as np
import pandas as pd
from openpyxl.chart import LineChart, Reference

from sizing_full_history import build_candidates, load
from sizing_normalized_optimization import design, project
from sizing_v5_capped import dd_stats

HERE = Path(__file__).resolve().parent
OUT = HERE / "output/drawdown_risk_sizing"
CEILINGS, SEED = (.90, .85, .75), 73


def stress_history(pnl):
    dd, streaks = [], []
    cum = peak = 0.
    streak = 0
    for p in pnl:
        dd.append(peak-cum); streaks.append(streak)
        cum += p; peak = max(peak, cum)
        streak = streak+1 if p < 0 else (0 if p > 0 else streak)
    return np.array(dd), np.array(streaks)


def risk_fit(X, pnl, names, td):
    keep = np.array([not name.endswith('_fullfit') for name in names])
    A = X[:, keep]
    labels = (pnl < 0).astype(float)
    Z = np.column_stack([np.ones(len(A)), A])
    beta = np.zeros(Z.shape[1])
    prob = np.clip(labels.mean(), 1e-6, 1-1e-6)
    beta[0] = np.log(prob/(1-prob))
    regularization = np.diag(np.r_[0., np.full(A.shape[1], 20.)])
    for _ in range(60):
        p = 1/(1+np.exp(-np.clip(Z@beta, -25, 25)))
        curvature = np.maximum(p*(1-p), 1e-6)
        step = np.linalg.solve(Z.T@(curvature[:, None]*Z)+regularization,
                               Z.T@(p-labels)+regularization@beta)
        beta -= step
        if np.linalg.norm(step) < 1e-8:
            break
    loss_probability = 1/(1+np.exp(-np.clip(Z@beta, -25, 25)))
    target = np.maximum(-pnl, 0)
    # Explicit unpenalized intercept; indicator features are not centered.
    mean = A.mean(0)
    centered = A-mean
    downside_beta = np.linalg.solve(centered.T@centered+50*np.eye(A.shape[1]),
                                     centered.T@(target-target.mean()))
    intercept = target.mean()-mean@downside_beta
    downside = np.maximum(intercept+A@downside_beta, 0)
    rank = lambda x: pd.Series(x).rank(pct=True).to_numpy()
    instability_readings = []
    missing = np.zeros(len(td), bool)
    cw = td.Leg.eq('CW').to_numpy()
    for col in ('u_range_bps', 'str_range_pct', 'spread_pct'):
        x = td[col].where(td.Leg.eq('CW'))
        instability_readings.append(x.rank(pct=True).fillna(.5).to_numpy())
        missing |= cw & x.isna().to_numpy()
    instability = np.mean(instability_readings, axis=0)
    loss_rank, downside_rank = rank(loss_probability), rank(downside)
    composite = .4*loss_rank+.4*downside_rank+.2*instability
    threshold = np.quantile(composite, .75)
    high = composite >= threshold
    dicey = high | missing
    reason = [';'.join(k for k, v in [('top_quartile_risk', h), ('missing_CW_entry_features', m)] if v)
              for h, m in zip(high, missing)]
    risk = pd.DataFrame(dict(Date=td.Date.to_numpy(), Loss_probability=loss_probability,
                             Expected_downside=downside, Loss_rank=loss_rank, Downside_rank=downside_rank,
                             Instability=instability, Risk_score=composite, Risk_threshold=threshold,
                             Dicey=dicey, Flag_reason=reason))
    model = pd.DataFrame(dict(Feature=['Intercept']+list(np.array(names)[keep]),
                              Logistic_beta=beta, Downside_beta=np.r_[intercept, downside_beta]))
    return risk, model


def measure(p):
    eq = np.cumsum(p)
    dd = np.maximum.accumulate(np.r_[0., eq])[1:]-eq
    return float(p.sum()), float(dd.max()), float(p.min())


def qualifies(stats, flat, tol=1e-8):
    return stats[0] >= flat[0]-tol and stats[1] <= flat[1]+tol and stats[2] >= flat[2]-tol


def search(X, p, lower, upper, flat, warm):
    rng = np.random.default_rng(SEED)
    objectives = ('Minimum DD', 'Maximum ratio', 'Maximum profit')
    seen, trace = {}, []
    evaluations = 0

    def value(r, objective):
        net, dd, worst = r['stats']
        regularizer = 1e-5*np.mean(r['beta']**2)
        if not qualifies(r['stats'], flat):
            violation = max(0, 1-net/flat[0])+max(0, dd/flat[1]-1)+max(0, (flat[2]-worst)/abs(flat[2]))
            return -1e6-violation-regularizer
        if objective == 'Minimum DD':
            benefit = -dd/flat[1]
        elif objective == 'Maximum ratio':
            benefit = (net/dd)/(flat[0]/flat[1])
        else:
            benefit = net/flat[0]
        return benefit-regularizer

    def evaluate(beta):
        nonlocal evaluations
        evaluations += 1
        size, offset = project(1+np.einsum('ij,j->i', X, beta), lower, upper)
        r = dict(beta=beta.copy(), size=size, offset=offset, stats=measure(p*size))
        for obj in objectives:
            if obj not in seen or value(r, obj) > value(seen[obj], obj):
                seen[obj] = r
        return r

    seeds = [evaluate(np.zeros(X.shape[1]))]+[evaluate(b) for b in warm]
    for scale in (.04, .12, .25):
        seeds += [evaluate(np.clip(rng.normal(0, scale, X.shape[1]), -1, 1)) for _ in range(20)]
    for k in range(X.shape[1]):
        for direction in (-1, 1):
            beta = np.zeros(X.shape[1]); beta[k] = direction*.08
            seeds.append(evaluate(beta))
    local = {}
    for objective in objectives:
        starts = sorted(seeds, key=lambda r: value(r, objective), reverse=True)[:2]+list(local.values())
        for start_no, current in enumerate(starts):
            for step in (.12, .06, .03, .015, .0075):
                for _ in range(3):
                    changed = False
                    for k in range(X.shape[1]):
                        best = current
                        for direction in (-1, 1):
                            b = current['beta'].copy(); b[k] = np.clip(b[k]+direction*step, -1, 1)
                            trial = evaluate(b)
                            if value(trial, objective) > value(best, objective)+1e-10:
                                best = trial
                        if best is not current:
                            current = best; changed = True
                    if not changed:
                        break
                trace.append(dict(Objective=objective, Start=start_no, Step=step,
                                  Net=current['stats'][0], MDD=current['stats'][1], Worst=current['stats'][2],
                                  Qualified=qualifies(current['stats'], flat), Evaluations=evaluations))
            if objective not in local or value(current, objective) > value(local[objective], objective):
                local[objective] = current
        print(f"  {objective}: {seen[objective]['stats']}; qualified={qualifies(seen[objective]['stats'], flat)}", flush=True)
    return seen, pd.DataFrame(trace), evaluations


def main():
    D, _ = load()
    _, _, coverage, scores = build_candidates(D)
    traded = D.Leg.isin(['CW', 'NW']).to_numpy()
    td = D[traded].reset_index(drop=True)
    pnl = D.Net_PnL.to_numpy(float); p = pnl[traded]
    flat = measure(p)
    X, names, transforms = design(D, traded, scores)
    dd, streak = stress_history(pnl)
    for name, values, scale in [('Prev_flat_DD', dd[traded], 75.), ('Prev_flat_loss_streak', streak[traded], 3.)]:
        X = np.column_stack([X, np.clip(values/scale, 0, 3)])
        names.append(name)
    risk, risk_model = risk_fit(X, p, names, td)
    risk['Prev_flat_DD'], risk['Prev_flat_loss_streak'] = dd[traded], streak[traded]
    extra_transforms = []
    for col in ('Loss_probability', 'Expected_downside'):
        x = risk[col].to_numpy(); mean, std = x.mean(), x.std()
        if std > 1e-12:
            name = 'Risk_'+col
            X = np.column_stack([X, np.clip((x-mean)/std, -3, 3)])
            names.append(name)
            extra_transforms.append(dict(Feature=name, Mean=mean, Std=std, Clip_standardized=3))
    transforms = pd.concat([transforms, pd.DataFrame(extra_transforms), pd.DataFrame([
        dict(Feature='Prev_flat_DD', Scaling='min(DD/75,3)'),
        dict(Feature='Prev_flat_loss_streak', Scaling='min(streak/3,3)')])], ignore_index=True)
    warm = []
    old = pd.read_csv(HERE/'output/normalized_sizing/optimized_coefficients.csv')
    for _, group in old.groupby('Objective'):
        weights = group.set_index('Feature').Coefficient
        warm.append(np.array([weights.get(name, 0.) for name in names]))
    S = {'Flat size 1':np.ones(len(D))}
    previous = pd.read_excel(HERE/'output/normalized_sizing/normalized_sizing.xlsx',sheet_name='Daily_sizes')
    if previous.Date.tolist() != D.Date.tolist():
        raise ValueError('Previous benchmark dates differ')
    for name in ('Optimized: Max profit', 'Optimized: Ratio profit floored'):
        S['Previous '+name] = previous[name].to_numpy()
    details, coefs, traces, bounds, searches = [], [], [], {}, 0
    mask = risk.Dicey.to_numpy(bool)
    print(f"{traded.sum()} entries; {mask.sum()} dicey; {len(names)} features; flat={flat}",flush=True)
    for ceiling in CEILINGS:
        upper = np.where(td.Leg.eq('CW') & td.Stage.eq('09:16'), 1., 1.15)
        upper[mask] = np.minimum(upper[mask], ceiling)
        if upper.sum() < len(td)-1e-10:
            details.append(dict(Ceiling=ceiling, Status='Infeasible exact mean budget', Max_total_size=upper.sum()))
            continue
        print(f"Dicey ceiling {ceiling}; maximum feasible budget {upper.sum():.2f}",flush=True)
        results, trace, count = search(X, p, np.full(len(td), .75), upper, flat, warm)
        trace['Ceiling'] = ceiling; traces.append(trace); searches += count
        for objective, r in results.items():
            name = f"Dicey<={ceiling:g}: {objective}"
            full = np.ones(len(D)); full[traded] = r['size']; S[name] = full
            bounds[name] = ceiling
            coefs += [dict(Candidate=name, Feature=k, Coefficient=float(v), Offset=r['offset']) for k,v in zip(names,r['beta'])]
            details.append(dict(Ceiling=ceiling, Candidate=name, Status='Qualified' if qualifies(r['stats'], flat) else 'No qualifying policy found',
                                Max_total_size=upper.sum(), Net=r['stats'][0], MDD=r['stats'][1], Worst=r['stats'][2]))
        warm += [r['beta'] for r in results.values()]
    rows, groups, annual, legs = [], [], [], []
    years = pd.to_datetime(D.Date).dt.year.to_numpy()
    exposure = np.where(td.Leg.eq('CW') & td.Event.astype(bool), 1/3, 1.)
    for name, size in S.items():
        v = size[traded]; net = pnl*size
        if abs(v.mean()-1)>1e-10 or (v<.75-1e-12).any() or (v>1.15+1e-12).any():
            raise AssertionError('Mean or size bounds failed')
        if name in bounds and (v[mask]>bounds[name]+1e-12).any():
            raise AssertionError('A dicey day was not cut')
        if (v[(td.Leg.eq('CW') & td.Stage.eq('09:16')).to_numpy()]>1+1e-12).any():
            raise AssertionError('09:16 boost')
        st = dd_stats(net)
        st.update(Candidate=name, Benchmark=name not in bounds,
                  Qualified=name in bounds and qualifies(measure(net),flat), Mean_size=v.mean(),
                  Dicey_ceiling=bounds.get(name,np.nan), Dicey_mean=v[mask].mean(), Dicey_max=v[mask].max(),
                  Other_mean=v[~mask].mean(), Min_size=v.min(), Max_size=v.max(),
                  Up_budget=np.maximum(v-1,0).sum(), Down_budget=np.maximum(1-v,0).sum(),
                  Net_change_pct=100*(st['Net']/flat[0]-1), DD_change_pct=100*(st['MDD']/flat[1]-1),
                  Event_weighted_exposure_ratio=np.dot(v,exposure)/exposure.sum())
        rows.append(st)
        for label, sel in [('Dicey',mask),('Other',~mask)]:
            groups.append(dict(Candidate=name, Group=label, Entries=int(sel.sum()), Mean_size=v[sel].mean(),
                               Max_size=v[sel].max(), PnL=(p*v)[sel].sum(), Flat_PnL=p[sel].sum(),
                               Realized_loss_pct=100*np.mean(p[sel]<0)))
        for year in sorted(set(years)):
            annual.append(dict(Candidate=name,Year=year,**dd_stats(net[years==year])))
        for leg in ('CW', 'NW'):
            sel = D.Leg.eq(leg).to_numpy()
            legs.append(dict(Candidate=name, Leg=leg, Entries=int(sel.sum()),
                             Net=net[sel].sum(), Mean_size=size[sel].mean()))
    R = pd.DataFrame(rows).sort_values(['Qualified','MDD','Net'],ascending=[False,True,False])
    eligible = R[R.Qualified]
    choice = eligible.iloc[0] if len(eligible) else None
    OUT.mkdir(parents=True,exist_ok=True)
    R.to_csv(OUT/'candidates.csv',index=False)
    pd.DataFrame(coefs).to_csv(OUT/'coefficients.csv',index=False)
    risk.to_csv(OUT/'daily_risk.csv',index=False)
    notes = [
        'Drawdown-first full-history research: see DRAWDOWN_SIZING_SPEC.md, saved before evaluation. No 60/20/20 split.',
        'Full-history risk-model fitting, parameter search, quantiles and normalization are IN SAMPLE; not causal live performance or a claim that losing days are predictable.',
        'Dicey = top-quartile composite estimated loss/downside/instability risk OR missing CW entry features. Defined before evaluating the cut policies; never simply flags realized losing days.',
        'Loss classifier lambda20 and downside ridge lambda50 exclude fullfit S1/S2 risk inputs. Risk coefficients, entry feature transforms and daily risk scores are saved.',
        'Every new policy must cut ALL dicey entries to its fixed ceiling (0.90/0.85/0.75); normalization cannot undo the cut. Global floor0.75, ceiling1.15, 09:16 CW <=1, arithmetic mean exactly1 over799 entries.',
        'Three numerical search objectives per ceiling: minimum MDD, maximum net/MDD, maximum net. Qualifying policies require net>=flat, MDD<=flat and worst daily net>=flat worst. Feasibility-first search may explore violations; unqualified results are explicit.',
        'Reference stress features use flat-book results up to yesterday; they do not implement a halt based on the sized book equity. Annual/leg/risk-group means need not equal1.',
        'Previous optima and flat are BENCHMARKS and do not enforce the new dicey ceilings; Qualified refers only to new cut policies.',
        'Net/costs scale linearly from saved production PnL. Units normalized Rs for25000 notional; no quantity-specific tick replay/lot rounding. Event-weighted exposure proxy reported separately from arithmetic multiplier mean.',
        'Risk estimates use entry inputs; their historical fitting and the full-period normalization use future information relative to early dates. Full-history numerical local search has no global-optimum guarantee.',
    ]
    daily = D[['Date','Leg','Stage','Net_PnL']].merge(risk,on='Date',how='left',validate='one_to_one')
    daily['Dicey'] = daily.Dicey.fillna(False).astype(bool)
    with pd.ExcelWriter(OUT/'drawdown_risk_sizing.xlsx') as w:
        tables={'Read_me':pd.DataFrame({'Method':notes}), 'Candidates':R, 'Risk_groups':pd.DataFrame(groups),
                'By_year':pd.DataFrame(annual), 'By_leg':pd.DataFrame(legs), 'Daily_risk':daily, 'Coefficients':pd.DataFrame(coefs),
                'Risk_model':risk_model, 'Transforms':transforms, 'Coverage':coverage,
                'Search_status':pd.DataFrame(details), 'Search_trace':pd.concat(traces,ignore_index=True) if traces else pd.DataFrame(),
                'Model_inputs':pd.DataFrame({'Date':td.Date,**{n:X[:,i] for i,n in enumerate(names)}}),
                'Daily_sizes':pd.DataFrame({'Date':D.Date,**S}),
                'Daily_PnL':pd.DataFrame({'Date':D.Date,**{n:pnl*s for n,s in S.items()}})}
        for sheet, table in tables.items():
            table.to_excel(w,sheet_name=sheet,index=False)
        plotted=list(S)[:3]+([] if choice is None else [choice.Candidate])
        for sheet in ('Equity','Drawdown'):
            columns={'Date':pd.to_datetime(D.Date)}
            for name in plotted:
                eq=np.cumsum(pnl*S[name])
                columns[name]=eq if sheet=='Equity' else np.maximum.accumulate(np.r_[0.,eq])[1:]-eq
            pd.DataFrame(columns).to_excel(w,sheet_name=sheet,index=False)
            ws=w.sheets[sheet]; chart=LineChart(); chart.title=sheet; chart.width=26; chart.height=13
            chart.y_axis.title='Normalized Rs'
            chart.add_data(Reference(ws,min_col=2,max_col=len(columns),min_row=1,max_row=len(D)+1),titles_from_data=True)
            chart.set_categories(Reference(ws,min_col=1,min_row=2,max_row=len(D)+1)); ws.add_chart(chart,'G2')
    old_manifest=json.loads((HERE/'output/normalized_sizing/manifest.json').read_text())
    paths=list(old_manifest['Input_sha256'])+[str(Path(__file__).resolve()),str(HERE/'DRAWDOWN_SIZING_SPEC.md'),
          str(HERE/'output/normalized_sizing/optimized_coefficients.csv'),str(HERE/'output/normalized_sizing/normalized_sizing.xlsx')]
    digests={path:hashlib.sha256(Path(path).read_bytes()).hexdigest() for path in paths}
    h=hashlib.sha256()
    for path in sorted((HERE/'output/backtest_relax/run_027bf0249b/days').glob('*.pkl')):
        h.update(path.name.encode()); h.update(path.read_bytes())
    (OUT/'manifest.json').write_text(json.dumps(dict(Input_sha256=digests,Daily_pickles_sha256=h.hexdigest(),
        Seed=SEED,Evaluations=searches,Dicey_entries=int(mask.sum()),Features=names),indent=2)+'\n')
    lines=['# Drawdown-first sizing with mandatory dicey-day cuts','',*['- '+n for n in notes],'',
           f'Dicey entries: {mask.sum()} / {len(td)}. Qualified new policies: {len(eligible)}.']
    if choice is not None:
        lines += ['',f'Lowest observed qualifying MDD: {choice.Candidate}; net {choice.Net:.2f}, MDD {choice.MDD:.2f}, net/MDD {choice.Net_per_MDD:.2f}, worst day {choice.Worst_day:.2f}; mean size {choice.Mean_size:.12f}; dicey mean/max {choice.Dicey_mean:.4f}/{choice.Dicey_max:.4f}.']
    else:
        lines += ['','No qualifying dicey-cut policy was found; do not substitute a violating policy.']
    (OUT/'SUMMARY.md').write_text('\n'.join(lines)+'\n')
    print(R[['Candidate','Qualified','Net','MDD','Net_per_MDD','Worst_day','Mean_size','Dicey_mean','Dicey_max']].to_string(index=False))
    print('Wrote',OUT)


if __name__=='__main__':
    main()
