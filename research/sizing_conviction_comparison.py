"""Compare every trade and enforce modest, risk-gated conviction boosts.

See CONVICTION_SIZING_SPEC.md. Reuses the frozen DD-risk model and inputs.
"""
import hashlib
import json
import pickle
from pathlib import Path

import numpy as np
import pandas as pd
from openpyxl.chart import LineChart, Reference

from sizing_drawdown_risk import measure, qualifies, search
from sizing_v5_capped import dd_stats

HERE = Path(__file__).resolve().parent
SOURCE = HERE/'output/drawdown_risk_sizing'
OUT = HERE/'output/conviction_sizing'
PROFILES = {'Gentle':(1.05,1.10,1.05), 'Modest priority':(1.10,1.15,1.10)}


def conviction_flags(td, X):
    cw = td.Leg.eq('CW').to_numpy()
    dicey = td.Dicey.to_numpy(bool)
    early = cw & td.Stage.eq('09:16').to_numpy()
    cuts = dict(S1_top_tercile=float(X.loc[cw,'S1_fullfit'].quantile(2/3)),
                S2_median=float(X.loc[cw,'S2_fullfit'].median()),
                Loss_probability_median=float(td.Loss_probability.median()))
    strong = (cw & ~dicey & (X.S1_fullfit>=cuts['S1_top_tercile']).to_numpy()
              & (X.S2_fullfit>=cuts['S2_median']).to_numpy()
              & (td.Loss_probability<=cuts['Loss_probability_median']).to_numpy())
    eligible = strong & ~early
    group = np.select([dicey, eligible, strong & early, early],
                      ['Dicey risk cut','Strong edge boost','Strong edge 09:16 blocked','Other 09:16'],
                      default='Other entry')
    return strong, eligible, early, group, cuts


def allocation_bounds(td, eligible, early, profile):
    minimum, maximum, other_max = PROFILES[profile]
    lower = np.where(eligible,minimum,.75)
    upper = np.where(eligible,maximum,other_max)
    upper[early] = np.minimum(upper[early],1.)
    dicey = td.Dicey.to_numpy(bool)
    upper[dicey] = np.minimum(upper[dicey],.85)
    feasible = bool((lower<=upper).all() and lower.sum()<=len(td)<=upper.sum())
    return lower, upper, feasible


def action(size):
    return np.where(size>1+1e-8,'Boost',np.where(size<1-1e-8,'Cut','Unchanged'))


def main():
    source_file=SOURCE/'drawdown_risk_sizing.xlsx'
    D=pd.read_excel(source_file,sheet_name='Daily_risk')
    model=pd.read_excel(source_file,sheet_name='Model_inputs')
    previous=pd.read_excel(source_file,sheet_name='Daily_sizes')
    traded=D.Leg.isin(['CW','NW']).to_numpy()
    td=D[traded].reset_index(drop=True)
    if td.Date.tolist()!=model.Date.tolist() or D.Date.tolist()!=previous.Date.tolist():
        raise ValueError('Frozen source dates differ')
    names=[c for c in model if c!='Date']
    X=model[names].to_numpy(float)
    strong,eligible,early,group,cuts=conviction_flags(td,model)
    p=td.Net_PnL.to_numpy(float); full_pnl=D.Net_PnL.to_numpy(float); flat=measure(p)
    prior='Dicey<=0.85: Minimum DD'
    S={'Flat size 1':np.ones(len(D)), 'Previous DD-first':previous[prior].to_numpy()}
    prior_profit='Dicey<=0.85: Maximum profit'
    S['Previous risk-cut profit']=previous[prior_profit].to_numpy()
    old=pd.read_csv(SOURCE/'coefficients.csv')
    warm=[]
    for _, data in old.groupby('Candidate'):
        weights=data.set_index('Feature').Coefficient
        warm.append(weights.loc[names].to_numpy())
    rows, bounds, coefs, statuses, traces = [], {}, [], [], []
    evaluations=0
    print(f"{len(td)} entries: dicey {td.Dicey.sum()}, strong {strong.sum()}, boost eligible {eligible.sum()}, 09:16-blocked {(strong & early).sum()}",flush=True)
    for profile in PROFILES:
        lower,upper,feasible=allocation_bounds(td,eligible,early,profile)
        statuses.append(dict(Profile=profile,Feasible_budget=feasible,Lower_sum=lower.sum(),Upper_sum=upper.sum(),Target=len(td)))
        print(f'{profile}: feasible={feasible}, maximum budget {upper.sum():.2f} vs {len(td)} required',flush=True)
        if not feasible:
            continue
        results,trace,count=search(X,p,lower,upper,flat,warm)
        trace['Profile']=profile; traces.append(trace); evaluations+=count
        for objective,r in results.items():
            name=f'{profile}: {objective}'
            s=np.ones(len(D)); s[traded]=r['size']; S[name]=s
            bounds[name]=(lower,upper,profile)
            coefs += [dict(Candidate=name,Feature=k,Coefficient=float(v),Offset=r['offset']) for k,v in zip(names,r['beta'])]
    for name,s in S.items():
        v=s[traded]; vals=full_pnl*s
        if abs(v.mean()-1)>1e-10:
            raise AssertionError('Exposure budget differs from one')
        if name in bounds:
            lo,hi,profile=bounds[name]
            if (v<lo-1e-12).any() or (v>hi+1e-12).any():
                raise AssertionError('Conviction/risk bounds broken')
        st=dd_stats(vals)
        st.update(Candidate=name,Benchmark=name not in bounds,
                  Qualified=name in bounds and qualifies(measure(vals),flat),Mean_size=v.mean(),
                  Cuts=int((v<1-1e-8).sum()),Boosts=int((v>1+1e-8).sum()),
                  Unchanged=int((np.abs(v-1)<=1e-8).sum()),
                  Dicey_mean=v[td.Dicey.to_numpy(bool)].mean(),Dicey_max=v[td.Dicey.to_numpy(bool)].max(),
                  Conviction_entries=int(eligible.sum()),Conviction_mean=v[eligible].mean() if eligible.any() else np.nan,
                  Conviction_min=v[eligible].min() if eligible.any() else np.nan,
                  Conviction_max=v[eligible].max() if eligible.any() else np.nan,
                  Net_change_pct=100*(st['Net']/flat[0]-1),DD_change_pct=100*(st['MDD']/flat[1]-1))
        rows.append(st)
    R=pd.DataFrame(rows).sort_values(['Qualified','MDD','Net'],ascending=[False,True,False])
    candidates=R[R.Qualified]
    OUT.mkdir(parents=True,exist_ok=True)
    R.to_csv(OUT/'candidates.csv',index=False)
    pd.DataFrame(statuses).to_csv(OUT/'profile_feasibility.csv',index=False)
    if candidates.empty:
        (OUT/'SUMMARY.md').write_text('# Conviction sizing\n\nNo qualifying new policy found. See candidates.csv and profile_feasibility.csv.\n')
        print('No qualifying new conviction policy; source reports preserved.')
        return
    selected=candidates.iloc[0].Candidate
    # Pull instrument details from the identical local production records.
    records=pd.DataFrame([pickle.load(open(f,'rb')) for f in sorted((HERE/'output/backtest_relax/run_027bf0249b/days').glob('*.pkl'))])
    records=records.sort_values('Date').reset_index(drop=True)
    if records.Date.tolist()!=D.Date.tolist() or not np.allclose(pd.to_numeric(records.Net_PnL,errors='coerce').fillna(0),D.Net_PnL,atol=1e-8,rtol=0):
        raise ValueError('Raw instrument data no longer matches frozen source')
    details=[c for c in ['Date','Entry_Time','Exit_Time','Exit_Reason','Strike','CW_Expiry','NW_Expiry','Event'] if c in records]
    trades=td.merge(records[details],on='Date',how='left',validate='one_to_one')
    trades['Group']=group; trades['Strong_estimated_edge']=strong; trades['Boost_eligible']=eligible
    trades['S1_standardized']=model.S1_fullfit; trades['S2_standardized']=model.S2_fullfit
    trades['Expiry']=np.where(td.Leg.eq('NW'),trades.NW_Expiry,trades.CW_Expiry)
    trades['Flat_size']=1.; trades['Previous_size']=S['Previous DD-first'][traded]
    trades['New_size']=S[selected][traded]
    trades['Flat_PnL']=p; trades['Previous_PnL']=p*trades.Previous_size; trades['New_PnL']=p*trades.New_size
    trades['Delta_vs_flat']=trades.New_PnL-p; trades['Delta_vs_previous']=trades.New_PnL-trades.Previous_PnL
    trades['Previous_action']=action(trades.Previous_size.to_numpy()); trades['New_action']=action(trades.New_size.to_numpy())
    trades['Reason']=np.select([td.Dicey.to_numpy(bool),eligible,strong & early,early],
        ['Mandatory risk cut <=0.85','S1 strong + S2 support + low estimated loss risk: modest boost',
         'Strong estimated edge; 09:16 boost forbidden','09:16 boost forbidden'],
        default='Ordinary entry: capped reallocation to balance average size1')
    trades.to_csv(OUT/'trade_comparison.csv',index=False)
    attribution=[]; action_rows=[]; yearly=[]; legs=[]
    years=pd.to_datetime(D.Date).dt.year.to_numpy()
    for name,s in S.items():
        v=s[traded]; pnl=p*v
        for label in sorted(set(group)):
            mask=group==label
            attribution.append(dict(Candidate=name,Group=label,Entries=int(mask.sum()),Mean_size=v[mask].mean(),
                Min_size=v[mask].min(),Max_size=v[mask].max(),Flat_PnL=p[mask].sum(),Sized_PnL=pnl[mask].sum(),
                Delta_vs_flat=(pnl-p)[mask].sum()))
        a=action(v)
        for label in ('Cut','Unchanged','Boost'):
            mask=a==label
            action_rows.append(dict(Candidate=name,Action=label,Entries=int(mask.sum()),Mean_size=v[mask].mean() if mask.any() else np.nan,
                Flat_PnL=p[mask].sum(),Sized_PnL=pnl[mask].sum(),Delta_vs_flat=(pnl-p)[mask].sum()))
        for year in sorted(set(years)):
            yearly.append(dict(Candidate=name,Year=year,**dd_stats((full_pnl*s)[years==year])))
        for leg in ('CW','NW'):
            mask=td.Leg.eq(leg).to_numpy()
            legs.append(dict(Candidate=name,Leg=leg,Entries=int(mask.sum()),Net=pnl[mask].sum(),Mean_size=v[mask].mean()))
    examples=pd.concat([trades[trades.Group.eq(label)].head(2) for label in ['Dicey risk cut','Strong edge boost','Strong edge 09:16 blocked']],ignore_index=True)
    examples.to_csv(OUT/'example_trades.csv',index=False)
    notes=[
        'Specification fixed in CONVICTION_SIZING_SPEC.md before evaluating new policies. Same frozen trades/risk model and full-history input matrix as previous DD study.',
        'Strong CW edge = S1 upper tercile and S2 >= CW median, estimated loss probability <= book median, no dicey flag. 09:16 strong trades are explicitly blocked from boost. NW lacks this two-score confirmation.',
        'Gentle profile: strong 1.05-1.10, ordinary <=1.05. Modest priority: strong1.10-1.15, ordinary <=1.10. Dicey <=0.85; 09:16<=1; floor0.75; arithmetic mean1 over799 entries. Infeasible budgets are reported without relaxing constraints.',
        'Modest priority ensures eligible strong trades are never sized below ordinary trades. Normalization preserves strong-entry lower bounds and risk upper bounds. Other increases can be budget reallocation, not a high-conviction label.',
        'New numerical optima must preserve flat PnL, DD and worst-day limits. Benchmark rows may violate new conviction lower bounds/ordinary caps and are marked Benchmark.',
        'Trade comparisons include all799 entries with actual date/leg/stage/available strike/expiry and flat/previous/new size and PnL. Example trades are first two chronologically in each chosen group, not selected on profit.',
        'Full-history scores, risk estimates, thresholds, optimization and normalization are IN SAMPLE, not measured future edge. No live changes. LUT tax vector13/5/2/2/6 unchanged.',
        'Existing saved PnL/costs scale linearly, without new execution replay, fixed-fee recalculation or lot rounding. Arithmetic mean size1 does not imply identical event-adjusted cash exposure.',
    ]
    # Event is metadata from the original records, rather than a risk-model input export.
    base_exposure=np.where(trades.Leg.eq('CW') & trades.Event.astype(bool),1/3,1.)
    for i,row in R.iterrows():
        R.loc[i,'Event_weighted_exposure_ratio']=np.dot(S[row.Candidate][traded],base_exposure)/base_exposure.sum()
    R.to_csv(OUT/'candidates.csv',index=False)
    with pd.ExcelWriter(OUT/'conviction_trade_comparison.xlsx') as w:
        tables={'Read_me':pd.DataFrame({'Method':notes}), 'Candidates':R,'Trade_comparison':trades,
                'Examples':examples,'Risk_conviction_groups':pd.DataFrame(attribution),'Actions':pd.DataFrame(action_rows),
                'By_year':pd.DataFrame(yearly),'By_leg':pd.DataFrame(legs),'Profile_feasibility':pd.DataFrame(statuses),
                'Gate_thresholds':pd.DataFrame([cuts]),'Coefficients':pd.DataFrame(coefs),'Model_inputs':model,
                'Search_trace':pd.concat(traces,ignore_index=True) if traces else pd.DataFrame(),
                'Daily_sizes':pd.DataFrame({'Date':D.Date,**S}),
                'Daily_PnL':pd.DataFrame({'Date':D.Date,**{n:full_pnl*s for n,s in S.items()}})}
        for sheet,table in tables.items():
            table.to_excel(w,sheet_name=sheet,index=False)
        for sheet in ('Equity','Drawdown'):
            columns={'Date':pd.to_datetime(D.Date)}
            for name in ['Flat size 1','Previous DD-first',selected]:
                eq=np.cumsum(full_pnl*S[name]); columns[name]=eq if sheet=='Equity' else np.maximum.accumulate(np.r_[0.,eq])[1:]-eq
            pd.DataFrame(columns).to_excel(w,sheet_name=sheet,index=False)
            ws=w.sheets[sheet]; chart=LineChart(); chart.title=sheet; chart.width=26; chart.height=13
            chart.y_axis.title='Normalized Rs'; chart.add_data(Reference(ws,min_col=2,max_col=len(columns),min_row=1,max_row=len(D)+1),titles_from_data=True)
            chart.set_categories(Reference(ws,min_col=1,min_row=2,max_row=len(D)+1)); ws.add_chart(chart,'G2')
    paths=[Path(__file__).resolve(),HERE/'CONVICTION_SIZING_SPEC.md',HERE/'sizing_drawdown_risk.py',
           HERE/'sizing_normalized_optimization.py',source_file,SOURCE/'coefficients.csv',SOURCE/'manifest.json']
    h=hashlib.sha256()
    for f in sorted((HERE/'output/backtest_relax/run_027bf0249b/days').glob('*.pkl')):
        h.update(f.name.encode()); h.update(f.read_bytes())
    manifest=dict(Input_sha256={str(f):hashlib.sha256(f.read_bytes()).hexdigest() for f in paths},Daily_pickles_sha256=h.hexdigest(),
                  Profiles=PROFILES,Thresholds=cuts,Selected_policy=selected,Evaluations=evaluations,
                  Strong_entries=int(strong.sum()),Boost_eligible=int(eligible.sum()),Blocked_0916=int((strong & early).sum()))
    (OUT/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
    selected_row=R[R.Candidate.eq(selected)].iloc[0]
    lines=['# Trade comparison and conviction boosts','',*['- '+n for n in notes],'',
           f'Strong estimated edge {strong.sum()}; eligible boosts {eligible.sum()}; 09:16 blocked {(strong & early).sum()}.',
           f'Selected by lowest qualifying DD: {selected}; net {selected_row.Net:.2f}, MDD {selected_row.MDD:.2f}, ratio {selected_row.Net_per_MDD:.2f}, worst day {selected_row.Worst_day:.2f}.',
           f'Size actions: cut {selected_row.Cuts}, unchanged {selected_row.Unchanged}, boosted {selected_row.Boosts}; mean {selected_row.Mean_size:.12f}.',
           f'Eligible strong size min/mean/max {selected_row.Conviction_min:.4f}/{selected_row.Conviction_mean:.4f}/{selected_row.Conviction_max:.4f}.',
           '', 'All799 trades: trade_comparison.csv. First chronological examples: example_trades.csv. Full workbook: conviction_trade_comparison.xlsx.']
    lines += ['', '## Policy comparison', '',
              '| Policy | Net | MDD | Net/MDD | Cuts | Unchanged | Boosts | Mean size |',
              '|---|---:|---:|---:|---:|---:|---:|---:|']
    for name in ['Flat size 1','Previous DD-first',selected]:
        row=R[R.Candidate.eq(name)].iloc[0]
        lines.append(f'| {name} | {row.Net:.2f} | {row.MDD:.2f} | {row.Net_per_MDD:.2f} | {row.Cuts} | {row.Unchanged} | {row.Boosts} | {row.Mean_size:.9f} |')
    lines += ['', '## Trade groups for the selected policy', '',
              '| Group | Entries | Previous mean size | New mean size | New minus previous PnL |',
              '|---|---:|---:|---:|---:|']
    for label in sorted(set(group)):
        m=trades.Group.eq(label)
        lines.append(f'| {label} | {m.sum()} | {trades.loc[m,"Previous_size"].mean():.4f} | {trades.loc[m,"New_size"].mean():.4f} | {trades.loc[m,"Delta_vs_previous"].sum():.2f} |')
    lines += ['', '## First chronological trade examples', '',
              '| Date | Leg | Stage | Group | Flat size | Previous size | New size | Flat PnL | New PnL |',
              '|---|---|---|---|---:|---:|---:|---:|---:|']
    for _,row in examples.iterrows():
        lines.append(f'| {row.Date} | {row.Leg} | {row.Stage} | {row.Group} | 1 | {row.Previous_size:.4f} | {row.New_size:.4f} | {row.Flat_PnL:.2f} | {row.New_PnL:.2f} |')
    lines += ['', '## Budget feasibility', '']
    for status in statuses:
        lines.append(f'- {status["Profile"]}: lower budget {status["Lower_sum"]:.2f}, upper budget {status["Upper_sum"]:.2f}, required {status["Target"]}; feasible={status["Feasible_budget"]}.')
    (OUT/'SUMMARY.md').write_text('\n'.join(lines)+'\n')
    print(R[['Candidate','Qualified','Net','MDD','Net_per_MDD','Worst_day','Cuts','Unchanged','Boosts','Conviction_mean']].to_string(index=False))
    print(pd.DataFrame(attribution).query('Candidate == @selected').to_string(index=False))
    print('Selected',selected,'; wrote',OUT)


if __name__=='__main__':
    main()
