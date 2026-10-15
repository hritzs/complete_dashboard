"""Evaluate preregistered tick-entry overlays; legacy entry list remains fixed."""
import hashlib
import json
import pickle
from pathlib import Path
import numpy as np
import pandas as pd

HERE=Path(__file__).resolve().parent
OUT=HERE/'output/tick_entry_study'
RULES=('P20','P10','Rcheap','Rrich','Lspread','Mstress','Combined','Delaycheap')
THRESHOLD_FEATURES=('Spread_pct','U_range_bps60','Str_change_pct60')


def metrics(p):
    p=np.asarray(p,float); equity=np.cumsum(p)
    dd=np.maximum.accumulate(np.r_[0.,equity])[1:]-equity
    mdd=float(dd.max()) if len(dd) else 0.
    return {'Net':float(p.sum()),'MDD':mdd,'Net_per_MDD':float(p.sum()/mdd) if mdd else np.nan,
            'Worst_day':float(p.min()) if len(p) else np.nan}


def prior_thresholds(d):
    """Fit once per calendar year; no current/future-year observations."""
    thresholds=pd.DataFrame(np.nan,index=d.index,columns=THRESHOLD_FEATURES)
    audit=[]
    for year in sorted(d.Year.unique()):
        if year<2022: continue
        hist=d[d.Year.lt(year)&d.Leg.eq('CW')&d.Valid.fillna(False).astype(bool)]
        current=d[d.Year.eq(year)&d.Leg.eq('CW')]
        for (stage,dte),group in current.groupby(['Stage','DTE_idx']):
            same=hist[hist.Stage.eq(stage)&hist.DTE_idx.eq(dte)]
            for col in THRESHOLD_FEATURES:
                local=same.loc[np.isfinite(same[col]),col]
                pool=hist.loc[np.isfinite(hist[col]),col]
                values=local if len(local)>=30 else pool if len(pool)>=60 else pd.Series(dtype=float)
                threshold=float(values.quantile(.9)) if len(values) else np.nan
                thresholds.loc[group.index,col]=threshold
                audit.append({'Year':int(year),'Stage':stage,'DTE_idx':int(dte),'Feature':col,
                    'Threshold90':threshold,'Fit_scope':'stage_DTE' if len(local)>=30 else 'pooled_CW',
                    'Fit_n':len(values),'Fit_max_date':str(hist.Date.max()) if len(hist) else '',
                    'Cutoff':f'{year-1}-12-31'})
    return thresholds,pd.DataFrame(audit)


def decisions(d,t):
    eligible=d.Leg.eq('CW')&d.Year.ge(2022)&d.Valid.fillna(False).astype(bool)
    p=d.Premium_percentile60; r=d.Residual_z
    p20=p.lt(.2)&p.notna(); cheap=r.lt(-1)&r.notna()
    spread=d.Spread_pct.gt(t.Spread_pct)&t.Spread_pct.notna()
    stress=d.U_range_bps60.gt(t.U_range_bps60)&d.Str_change_pct60.gt(t.Str_change_pct60)&t.U_range_bps60.notna()&t.Str_change_pct60.notna()
    raw={'P20':p20,'P10':p.lt(.1)&p.notna(),'Rcheap':cheap,'Rrich':r.lt(1)&r.notna(),
        'Lspread':spread,'Mstress':stress,'Combined':(p20|cheap|spread)&p.notna()&r.notna()&d.Spread_pct.notna()&t.Spread_pct.notna(),
        'Delaycheap':(p20|cheap)&d.Entry_Time.isin(['09:16','09:17'])}
    return {k:v&eligible for k,v in raw.items()}


def evaluate(d):
    t,audit=prior_thresholds(d); flags=decisions(d,t)
    daily=d.copy(); summaries=[]; attribution=[]
    traded=d.Leg.isin(['CW','NW']); base=d.Net_PnL.to_numpy(float)
    for name in ('Flat',)+RULES:
        p=base.copy(); executed=traded.to_numpy().copy()
        action=np.where(traded,'keep','no_trade').astype(object)
        times=d.Entry_Time.fillna('').to_numpy().copy()
        if name!='Flat':
            affected=flags[name].to_numpy()
            p[affected]=0.; executed[affected]=False; action[affected]='skip'; times[affected]=''
            if name=='Delaycheap':
                for i in np.flatnonzero(affected):
                    nxt={'09:16':'0917','09:17':'0918'}[d.Entry_Time.iloc[i]]
                    value=d['PnL_'+nxt].iloc[i]
                    if d['Signal_'+nxt].iloc[i]=='LUT_ON' and pd.notna(value) and np.isfinite(value):
                        p[i]=float(value); executed[i]=True; action[i]='delay'
                        times[i]=nxt[:2]+':'+nxt[2:]
        daily[name+'_PnL']=p; daily[name+'_Action']=action; daily[name+'_Entry_Time']=times
        daily[name+'_Delta']=p-base
        for scope,mask in [('All_history',np.ones(len(d),bool)),('Scored_2022plus',d.Year.ge(2022).to_numpy())]:
            stat=metrics(p[mask]); flat=metrics(base[mask]); n=int((traded.to_numpy()&mask).sum())
            summaries.append({'Policy':name,'Period':scope,**stat,'Entries':int(executed[mask].sum()),
                'Avg_PnL_per_entry':float(p[mask].sum()/executed[mask].sum()) if executed[mask].any() else np.nan,
                'Win_rate':float((p[mask&executed]>0).mean()) if executed[mask].any() else np.nan,
                'Skips':int((action[mask]=='skip').sum()),'Delays':int((action[mask]=='delay').sum()),
                'Entry_count_fraction':float(executed[mask].sum()/n),'Delta_net':stat['Net']-flat['Net'],
                'Qualifies':bool(name!='Flat' and stat['Net']>flat['Net'] and stat['Net_per_MDD']>flat['Net_per_MDD'] and stat['Worst_day']>=flat['Worst_day']),
                **{'Same_entry_count_flat_'+k:v for k,v in metrics(base[mask]*(executed[mask].sum()/n)).items()}})
        for year,group in d[d.Year.ge(2022)].groupby('Year'):
            ix=group.index.to_numpy()
            attribution.append({'Policy':name,'Year':int(year),**metrics(p[ix]),'Delta_net':float((p[ix]-base[ix]).sum()),
                'Entries':int(executed[ix].sum()),'Skips':int((action[ix]=='skip').sum()),'Delays':int((action[ix]=='delay').sum())})
    return daily,pd.DataFrame(summaries),pd.DataFrame(attribution),audit


def placebo_checks(d,daily,repetitions=1000):
    """Match activity counts within year, original stage and DTE."""
    thresholds,_=prior_thresholds(d)
    eligible=d.Leg.eq('CW')&d.Year.ge(2022)&d.Valid.fillna(False).astype(bool)
    p=d.Premium_percentile60.notna(); r=d.Residual_z.notna()
    spread=d.Spread_pct.notna()&thresholds.Spread_pct.notna()
    stress=d.U_range_bps60.notna()&d.Str_change_pct60.notna()&thresholds.U_range_bps60.notna()&thresholds.Str_change_pct60.notna()
    pools={'P20':p,'P10':p,'Rcheap':r,'Rrich':r,'Lspread':spread,'Mstress':stress,
        'Combined':p&r&spread,'Delaycheap':(p|r)&d.Entry_Time.isin(['09:16','09:17'])}
    scored=d.Year.ge(2022).to_numpy(); base=d.Net_PnL.to_numpy(float)
    rng=np.random.default_rng(42); rows=[]
    for rule in RULES:
        triggered=daily[rule+'_Action'].isin(['skip','delay']).to_numpy()
        groups=[]
        for indices in d[eligible&pools[rule]].groupby(['Year','Stage','DTE_idx']).groups.values():
            indices=np.asarray(indices,int); count=int(triggered[indices].sum())
            if count: groups.append((indices,count))
        alternate=np.zeros(len(d))
        if rule=='Delaycheap':
            for i in np.flatnonzero((eligible&pools[rule]).to_numpy()):
                tag={'09:16':'0917','09:17':'0918'}[d.Entry_Time.iloc[i]]
                value=d['PnL_'+tag].iloc[i]
                if d['Signal_'+tag].iloc[i]=='LUT_ON' and pd.notna(value) and np.isfinite(value): alternate[i]=value
        null_net=[]; null_ratio=[]
        for _ in range(repetitions):
            sample=base.copy()
            for indices,count in groups:
                selected=rng.choice(indices,size=count,replace=False)
                sample[selected]=alternate[selected]
            stat=metrics(sample[scored]); null_net.append(stat['Net']); null_ratio.append(stat['Net_per_MDD'])
        observed=metrics(daily.loc[scored,rule+'_PnL'])
        pnet=float((1+np.count_nonzero(np.asarray(null_net)>=observed['Net']))/(1+repetitions))
        rows.append({'Policy':rule,'Repetitions':repetitions,'Seed':42,'Observed_net':observed['Net'],
            'Observed_net_per_MDD':observed['Net_per_MDD'],'Null_median_net':float(np.median(null_net)),
            'Null_net_025':float(np.quantile(null_net,.025)),'Null_net_975':float(np.quantile(null_net,.975)),
            'P_net_one_sided':pnet,'P_net_Bonferroni8':min(1.,8*pnet),
            'P_ratio_one_sided':float((1+np.count_nonzero(np.asarray(null_ratio)>=observed['Net_per_MDD']))/(1+repetitions))})
    return pd.DataFrame(rows)


def main():
    files=sorted((HERE/'output/backtest_relax/run_027bf0249b/days').glob('*.pkl'))
    raw=pd.DataFrame([pickle.load(f.open('rb')) for f in files]).sort_values('Date').reset_index(drop=True)
    raw['Net_PnL']=raw.Net_PnL.fillna(0.); raw['Year']=pd.to_datetime(raw.Date).dt.year
    extraction=json.loads((OUT/'features_manifest.json').read_text())
    if extraction.get('Code_sha256')!=hashlib.sha256((HERE/'tick_entry_features.py').read_bytes()).hexdigest():
        raise ValueError('Feature code differs from extracted cache')
    if extraction.get('Feature_sha256')!=hashlib.sha256((OUT/'features.csv').read_bytes()).hexdigest():
        raise ValueError('Feature payload differs from extraction manifest')
    features=pd.read_csv(OUT/'features.csv')
    if features.Date.duplicated().any(): raise ValueError('Duplicate feature dates')
    cw=raw[raw.Leg.eq('CW')]
    if set(cw.Date)!=set(features.Date): raise ValueError('Feature extraction is incomplete')
    joined=cw[['Date','Entry_Time']].merge(features,on='Date',validate='one_to_one')
    assert joined.Entry_Time_x.eq(joined.Entry_Time_y).all()
    valid=features.Valid.fillna(False).astype(bool)
    assert (pd.to_datetime(features.loc[valid,'Max_feature_tick'])<pd.to_datetime(features.loc[valid,'Decision_time'])).all()
    d=raw.merge(features.drop(columns=['Entry_Time']),on='Date',how='left',validate='one_to_one')
    daily,summary,yearly,thresholds=evaluate(d)
    paths={'daily_comparison.csv':daily,'trade_comparison.csv':daily[daily.Leg.isin(['CW','NW'])],
           'summary.csv':summary,'yearly.csv':yearly,'training_thresholds.csv':thresholds}
    paths['placebo_checks.csv']=placebo_checks(d,daily)
    scored=d.Year.ge(2022)&d.Leg.eq('CW')
    coverage=[]
    for col in ['Valid','Premium_percentile60','Residual_z',*THRESHOLD_FEATURES,'Butterfly_credit_CE','Butterfly_credit_PE']:
        available=(d[col].fillna(False).astype(bool) if col=='Valid' else np.isfinite(d[col]))&scored
        coverage.append({'Feature':col,'Scored_CW_days':int(scored.sum()),'Available':int(available.sum()),
            'Available_baseline_net':float(d.loc[available,'Net_PnL'].sum()),
            'Unavailable_baseline_net':float(d.loc[scored&~available,'Net_PnL'].sum()),
            'Positive_butterfly':int((d.loc[available,col]>0).sum()) if col.startswith('Butterfly') else np.nan})
    paths['coverage.csv']=pd.DataFrame(coverage)
    apparent=(features.Butterfly_credit_CE.gt(0)|features.Butterfly_credit_PE.gt(0))
    paths['butterfly_candidates.csv']=features.loc[apparent].copy()
    # Side-by-side attribution on identical feature-covered CW observations.
    subsets=[]
    for col in ['Premium_percentile60','Residual_z','Spread_pct']:
        mask=scored&np.isfinite(d[col])
        for name in ('Flat',)+RULES:
            subsets.append({'Subset':col,'Policy':name,'CW_days':int(mask.sum()),**metrics(daily.loc[mask,name+'_PnL'])})
    paths['matched_coverage.csv']=pd.DataFrame(subsets)
    stages=[]
    for stage,g in daily[scored].groupby('Stage'):
        for name in ('Flat',)+RULES:
            stages.append({'Stage':stage,'Policy':name,'Base_entries':len(g),'Net':float(g[name+'_PnL'].sum()),
                'Delta_net':float(g[name+'_Delta'].sum()),'Skips':int(g[name+'_Action'].eq('skip').sum()),
                'Delays':int(g[name+'_Action'].eq('delay').sum())})
    paths['stage_comparison.csv']=pd.DataFrame(stages)
    for name,frame in paths.items(): frame.to_csv(OUT/name,index=False)
    with pd.ExcelWriter(OUT/'tick_entry_comparison.xlsx',engine='openpyxl') as writer:
        for name,frame in paths.items():
            frame.to_excel(writer,sheet_name=name.removesuffix('.csv')[:31],index=False)
        for sheet in writer.book:
            sheet.freeze_panes='B2'; sheet.auto_filter.ref=sheet.dimensions
        from openpyxl.chart import LineChart, Reference
        curve=pd.DataFrame({'Date':daily.Date})
        for name in ('Flat',)+RULES:
            equity=np.cumsum(daily[name+'_PnL'].to_numpy())
            curve[name+'_Equity']=equity
            curve[name+'_DD']=np.maximum.accumulate(np.r_[0.,equity])[1:]-equity
        curve.to_excel(writer,sheet_name='Equity_DD',index=False)
        sheet=writer.book['Equity_DD']; sheet.freeze_panes='B2'
        for title,offset in [('Equity',2),('Drawdown',3)]:
            chart=LineChart(); chart.title=title; chart.height=13; chart.width=25
            chart.y_axis.title='Normalized PnL'; chart.x_axis.title='Trading day'
            for col in range(offset,20,2):
                chart.add_data(Reference(sheet,min_col=col,max_col=col,min_row=1,max_row=len(curve)+1),titles_from_data=True)
            sheet.add_chart(chart,'U2' if title=='Equity' else 'U29')
    forward=summary[summary.Period.eq('Scored_2022plus')]
    strike_mismatch=int((d.Leg.eq('CW')&d.Valid.fillna(False).astype(bool)&d.Feature_strike.ne(d.Strike)).sum())
    table='| Policy | Net | MDD | Net/DD | Worst day | Skips | Delays |\n|---|---:|---:|---:|---:|---:|---:|\n'
    for r in forward.to_dict('records'):
        table+=f"| {r['Policy']} | {r['Net']:.2f} | {r['MDD']:.2f} | {r['Net_per_MDD']:.2f} | {r['Worst_day']:.2f} | {r['Skips']} | {r['Delays']} |\n"
    winners=forward.loc[forward.Qualifies,'Policy'].tolist()
    delay=forward[forward.Policy.eq('Delaycheap')].iloc[0]
    baseline=forward[forward.Policy.eq('Flat')].iloc[0]
    placebo=paths['placebo_checks.csv'].set_index('Policy').loc['Delaycheap']
    positive_ce=int((scored&d.Butterfly_credit_CE.gt(0)).sum())
    positive_pe=int((scored&d.Butterfly_credit_PE.gt(0)).sum())
    report=f'''# Tick-entry research results

Conditional entry-overlay study; all original LUT taxes (13,5,2,2,6), production
NW trades, size multiplier1 (including original event weights) and saved
exits/costs remain unchanged. Features use strictly
earlier ticks, fixed pre-decision ATM strike and backward as-of snapshots.
2021 is warmup; across-day thresholds fit completed prior years only.

{table}

Qualification (net and net/DD above flat, worst day no worse): {', '.join(winners) or 'none'}.
The one-minute delay cuts net by {baseline.Net-delay.Net:.2f}
({100*(1-delay.Net/baseline.Net):.2f}%) and MDD by {baseline.MDD-delay.MDD:.2f}
({100*(1-delay.MDD/baseline.MDD):.2f}%). It delays128 entries and skips4;
it is a profit/risk tradeoff, not a higher-profit entry rule. Its randomized
net tail fraction is {placebo.P_net_one_sided:.4f}, or
{placebo.P_net_Bonferroni8:.4f} after correcting these eight comparisons.
There is no robust new profit edge established by this comparison.
This is an exploratory comparison of eight fixed hypotheses, not an untouched
final holdout or permission to deploy. Values are the backtest's normalized PnL
units, not account-level rupees. Calendar daily equity includes no-trade days.

P20/P10 reject low within-minute premium percentiles. Rcheap rejects bid residual
z<-1, Rrich requires z>=1 for valid observations. Lspread rejects prior-year top
decile spread; Mstress requires both range and premium change above prior-year
90th percentiles. Combined unions P20, Rcheap and Lspread. Delaycheap defers
adverse original 09:16/09:17 signals one minute only if the saved next-minute
LUT gate passes, otherwise skips. No later rescan or NW fallback is simulated.
Missing features keep the original trade. Coverage and matched-subset attribution
are explicit. Constant-size flat comparisons use the retained entry-count
fraction, an ex-post activity proxy rather than measured capital exposure.

Butterfly credits are quote diagnostics only, sampled at existing CW entries.
Scored-period positive quote credits: {positive_ce} call and {positive_pe} put
observations. See butterfly_candidates.csv; these are not completed trades.
Positive credit does not establish executable arbitrage: quote simultaneity,
fee schedules, contract lots and all-leg fills require separate verification.
This does not test a continuously scanned or delta-hedged statistical-arbitrage
strategy. Residual richness is a short-window price model, not demonstrated
mean reversion or a reliable expected-profit estimate.

Legacy base LUTs were fitted on broader history; saved fill/counterfactual
assumptions have not been certified causal. Although the new tick features and
annual threshold fitting are past-only, this is NOT an end-to-end leakage-free
strategy backtest. Filtering evaluates existing entries only, at fixed size;
it does not yet test different sizing or live order placement.
The past-only ATM differs from the saved fill strike on {strike_mismatch} valid
CW days; features deliberately use the strike knowable before the entry instant.
Stored tick timestamps are assumed to represent observation availability;
exchange/feed provenance and the legacy fill pipeline are not certified here.

See tick_entry_comparison.xlsx, all {int(raw.Leg.isin(['CW','NW']).sum())} trades
in trade_comparison.csv, daily_comparison.csv, coverage.csv, matched_coverage.csv,
yearly.csv, stage_comparison.csv, training_thresholds.csv and features.csv.
Coverage: {int(valid.sum())}/720 CW days have usable features; scored-period
coverage is {int((scored&d.Valid.fillna(False).astype(bool)).sum())}/550.
placebo_checks.csv reports 1,000 fixed-seed randomized trigger allocations per
candidate, matched by year/original stage/DTE and feature availability. Tail
fractions are exploratory; Bonferroni8 covers these eight net comparisons only,
not all earlier research. It cannot establish causal end-to-end alpha.
'''
    (OUT/'REPORT.md').write_text(report)
    manifests=[HERE/'tick_entry_features.py',HERE/'tick_entry_eval.py',HERE/'tick_entry_audit.py',HERE/'test_tick_entry_features.py',HERE/'test_tick_entry_eval.py',HERE/'TICK_ENTRY_SPEC.md',OUT/'features.csv',*files]
    manifest={'Inputs_sha256':{str(p.relative_to(HERE)):hashlib.sha256(p.read_bytes()).hexdigest() for p in manifests},
        'Days':len(raw),'Entries':int(raw.Leg.isin(['CW','NW']).sum()),'CW':len(features),
        'Tick_payloads':'Not hashed; feature rows retain source size, mtime and row count',
        'Outputs_sha256':{p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in OUT.iterdir() if p.is_file() and p.name not in ('manifest.json','audit_result.json','pilot_features.csv','pilot_features_manifest.json')}}
    (OUT/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
    print(forward[['Policy','Net','MDD','Net_per_MDD','Worst_day','Skips','Delays','Qualifies']].to_string(index=False))


if __name__=='__main__': main()
