"""Whole-book comparison of the fixed position adjustments (GS_ADJUSTMENT_OVERLAY_SPEC.md)."""
import os
os.environ.setdefault('MPLCONFIGDIR','/tmp/gs-adjust-report-mpl')
import hashlib,json,pickle
from pathlib import Path
import numpy as np
import pandas as pd
import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt
from gs_adjustment_overlays import ROOT,OUT,STOPS,CANDIDATES
from gs_intraday_replay import SOURCE
from gs_stop_risk_baseline import equity_path
from report_gs_intraday_stops import intraday_series


def day_shape(marks):
    """Ordered-path extremes and local DD of one day (initial equity 0)."""
    eq=np.r_[0.,np.asarray(marks,float)];draw=np.maximum.accumulate(eq)-eq
    return float(eq.min()),float(eq.max()),float(draw.max())


def metrics(frame):
    p=frame.Net_PnL.to_numpy(float);_,_,dd=equity_path(p)
    proxy=intraday_series(p,frame.Path_low,frame.Path_high,frame.Day_local_MDD)
    adj=frame[frame.Adjusted.astype(bool)]
    return {'Net':float(p.sum()),'MDD':float(dd.max()),'Source_model_intraday_MDD':float(proxy[:,-1].max()),
        'Worst_day':float(p.min()),'Trades':int(frame.Traded.sum()),'Adjusted_trades':int(len(adj)),
        'Adjustment_PnL':float(adj.Adjust_PnL.sum()) if len(adj) else 0.,'Adjustment_fees':float(adj.Adjust_fee.fillna(0).sum()) if len(adj) else 0.,
        'Net_double_fee':float(p.sum()-adj.Adjust_fee.fillna(0).sum()) if len(adj) else float(p.sum()),
        'Adjusted_win_pct':float(100*(adj.Adjust_PnL>0).mean()) if len(adj) else np.nan,
        'Net_per_MDD':float(p.sum()/dd.max()) if dd.max()>0 else np.nan}


def main():
    verification=json.loads((OUT/'verification.json').read_text())
    assert verification.get('Accounting_status')=='PASS','Independent accounting reconciliation required'
    assert hashlib.sha256((ROOT/'verify_gs_adjustments_fast.py').read_bytes()).hexdigest()==verification['Verifier_SHA256']
    run=json.loads((OUT/'run_manifest.json').read_text());assert run['Status']=='complete' and not run['Failed_dates']
    for name,digest in run['Inputs'].items():assert hashlib.sha256((ROOT/name).read_bytes()).hexdigest()==digest,name
    pilot=json.loads((OUT/'pilot_manifest.json').read_text());assert pilot['Status']=='complete' and pilot['Inputs']==run['Inputs']
    rows=[];baseline=[];inputs=[]
    for date in run['Dates']:
        p=OUT/'days'/(date+'.json');rec=json.loads(p.read_text());assert not rec['Error'];inputs.append(p)
        if date in pilot['Dates']:
            assert json.loads((OUT/'pilot_days'/(date+'.json')).read_text())['Rows']==rec['Rows'],date
        base=pickle.loads((SOURCE/'days'/(date+'.pkl')).read_bytes())['New'];baseline.append(base)
        marks={}
        if base.get('Leg')=='CW':
            m=OUT/'marks'/(date+'.npz');assert hashlib.sha256(m.read_bytes()).hexdigest()==rec['Marks_SHA256'];inputs.append(m)
            z=np.load(m);marks={c:z[c+'_m'] for c in CANDIDATES}
        elif base.get('Leg')=='NW':
            nw=pd.read_parquet(STOPS/'paths'/(date+'.parquet')).Mark_net.to_numpy(float);marks={c:nw for c in CANDIDATES}
        for r in rec['Rows']:
            r=dict(r);m=marks.get(r['Policy'])
            if m is not None:
                assert np.isfinite(m).all();np.testing.assert_allclose(m[-1],r['Net_PnL'],atol=1e-8,rtol=0)
                r['Path_low'],r['Path_high'],r['Day_local_MDD']=day_shape(m)
            else:r.update(Path_low=0.,Path_high=0.,Day_local_MDD=0.)
            r.setdefault('Entry_Time',base.get('Entry_Time',''))
            r['Exit_Time']=r.get('Roll_exit_time') if r.get('Policy')=='Roll' and r.get('Adjusted') else (r.get('Base_exit_time') or base.get('Exit_Time',''))
            r['Exit_Reason']=('Roll ' + str(r.get('Roll_exit_reason'))) if r.get('Policy')=='Roll' and r.get('Adjusted') else base.get('Exit_Reason','')
            rows.append(r)
    daily=pd.DataFrame(rows);baseline=pd.DataFrame(baseline);dates=pd.to_datetime(run['Dates'])
    for c in ('Adjust_PnL','Adjust_fee'):daily[c]=daily.get(c,np.nan)
    daily['Traded']=daily.Traded.astype(bool);daily['Adjusted']=daily.Adjusted.astype(bool)
    assert len(daily)==len(run['Dates'])*len(CANDIDATES)
    scopes={'All_dates':np.ones(len(dates),bool),'2022plus':dates.year>=2022,'2023plus':dates.year>=2023,'2025plus':dates.year>=2025}
    labels={'Year':dates.year.astype(str),'Quarter':dates.to_period('Q').astype(str),'Month':dates.to_period('M').astype(str),
        'FY':np.array([f'FY{d.year if d.month>=4 else d.year-1}-{d.year+1 if d.month>=4 else d.year}' for d in dates])}
    paths=[];summary=[];periods=[]
    for name in CANDIDATES:
        frame=daily[daily.Policy.eq(name)].set_index('Date').loc[run['Dates']].reset_index()
        e,h,dd=equity_path(frame.Net_PnL);proxy=intraday_series(frame.Net_PnL,frame.Path_low,frame.Path_high,frame.Day_local_MDD)
        frame['Equity']=e;frame['Daily_DD']=dd
        for i,col in enumerate(('Source_model_equity_close','Source_model_intraday_peak','Source_model_DD_at_close','Source_model_day_max_DD','Source_model_running_MDD')):frame[col]=proxy[:,i]
        paths.append(frame)
        for scope,mask in scopes.items():summary.append({'Policy':name,'Scope':scope,**metrics(frame.loc[mask])})
        for grouping,keys in labels.items():
            for label in sorted(set(keys)):periods.append({'Policy':name,'Grouping':grouping,'Period':label,**metrics(frame.loc[keys==label])})
    full=pd.concat(paths,ignore_index=True);summary=pd.DataFrame(summary);periods=pd.DataFrame(periods)
    b=full[full.Policy.eq('Baseline')]
    np.testing.assert_allclose(b.Net_PnL,baseline.Net_PnL.fillna(0),atol=1e-8,rtol=0)
    np.testing.assert_allclose(b.Daily_DD.max(),168.42425845197704,atol=1e-8,rtol=0)
    comparisons=[]
    for scope in scopes:
        group=summary[summary.Scope.eq(scope)];base=group[group.Policy.eq('Baseline')].iloc[0]
        for row in group.itertuples(index=False):
            comparisons.append({'Policy':row.Policy,'Scope':scope,'Delta_net':row.Net-base.Net,'Daily_DD_change':row.MDD-base.MDD,
                'Intraday_DD_change':row.Source_model_intraday_MDD-base.Source_model_intraday_MDD,
                'Better_net_and_daily_DD':(row.Net>base.Net+1e-8) and (row.MDD<base.MDD-1e-8),
                'Better_net_and_both_DD':(row.Net>base.Net+1e-8) and (row.MDD<base.MDD-1e-8) and (row.Source_model_intraday_MDD<base.Source_model_intraday_MDD-1e-8),
                'Profit_retention_pct':100*row.Net/base.Net if base.Net>0 else np.nan})
    comparison=pd.DataFrame(comparisons)
    cw=daily[daily.Leg.eq('CW')]
    status=cw.groupby(['Policy','Status']).size().rename('Trades').reset_index()
    by_regime=cw[cw.Adjusted].groupby(['Policy','Trigger_regime']).agg(Trades=('Adjust_PnL','size'),Adjust_PnL=('Adjust_PnL','sum'),
        Mean=('Adjust_PnL','mean'),Win_pct=('Adjust_PnL',lambda s:100*(s>0).mean())).reset_index()
    wide=full.pivot(index='Date',columns='Policy',values='Net_PnL').reindex(run['Dates']).reset_index()[['Date',*CANDIDATES]]
    notes=['Fixed position adjustments instead of stopping out, triggered by the first confirmed 4.24 regime signal; one per CW trade.',
        'Base trade, entries, size1, hedging and exits unchanged; added legs close at the base exit second. Roll replaces the base trade from t*.',
        'Added legs priced from raw tick quotes: buy at ask, sell at bid, 2-5 s after the decision; fee 0.25 per unit round trip (event x1/3).',
        'Added legs are not delta-hedged; Wings/BuyTested/NWLongStraddle add long options; SellUntested/SellMore add risk up to 1.5x per side.',
        'Retrospective fixed-candidate comparison over already-researched years: no winner selected, nothing adopted.',
        'Intraday DD is the 4.24 source-model mark proxy plus added legs marked at liquidation side; not executable account DD.',
        'NW trades and no-trade days unchanged in every candidate. Units: normalized additive (points x 25000 / entry underlying).',
        'Status not_applicable = candidate regime rule did not match the trigger; no_entry_quote / no_trigger / no_roll_entry = baseline kept.']
    notes.extend([
        'Verification: '+verification['Status']+'; accounting reconciles, but declared strike-at-fill specification deviations are retained. This study is not a validated implementation of its intended execution rule.',
        'Independent verification is quote/accounting reconciliation only, not causal execution certification. See GS_ADJUSTMENT_AUDIT_NOTES.md.',
        'Added-leg strike selection uses a post-trigger underlying observation; quotes do not check displayed quantity/crossing; five exits use up-to60second stale quotes.',
        'Added-leg cash is posted at base exit even if its quoted close is up to5seconds later. Roll uses a different legacy timing convention.',
        'The double-fee column stresses added option-leg model fees only, not all GS costs or roll execution costs; the1.5x short-unit cap is not a portfolio Greek/margin/currency-risk cap.'
    ])
    tables={'Read_me':pd.DataFrame({'Method':notes}),'Summary':summary,'Comparison':comparison,'Daily_PnL':wide,'All_policy_days':full,
        'Latest_GS_daily':baseline,'Adjusted_trades':cw[cw.Adjusted],'Status_counts':status,'By_regime':by_regime,'Period_risk':periods,
        'Specification':pd.DataFrame({'Line':(ROOT/'GS_ADJUSTMENT_OVERLAY_SPEC.md').read_text().splitlines()})}
    tables['Execution_audit_notes']=pd.DataFrame({'Line':(ROOT/'GS_ADJUSTMENT_AUDIT_NOTES.md').read_text().splitlines()})
    tables['Specification_deviations']=pd.DataFrame(verification['Spec_deviations'])
    tables['Verification']=pd.DataFrame([{k:v for k,v in verification.items() if not isinstance(v,(list,dict))}])
    fig,ax=plt.subplots(2,1,figsize=(13,8),sharex=True,layout='constrained')
    for name,frame in full.groupby('Policy',sort=False):
        ax[0].plot(dates,frame.Equity,label=name,lw=1);ax[1].plot(dates,frame.Daily_DD,lw=1)
    ax[0].set(title='GS position adjustments vs baseline (legacy source-model execution)',ylabel='Cumulative net units');ax[0].legend(fontsize=8,ncol=4)
    ax[1].set(ylabel='Closed-day DD',xlabel='Date')
    fig.savefig(OUT/'equity_drawdown_comparison.png',dpi=150);plt.close(fig)
    with pd.ExcelWriter(OUT/'gs_adjustment_comparison.xlsx') as writer:
        for name,frame in tables.items():frame.to_excel(writer,sheet_name=name,index=False);writer.sheets[name].freeze_panes='B2'
        from openpyxl.drawing.image import Image
        writer.book.create_sheet('Charts').add_image(Image(OUT/'equity_drawdown_comparison.png'),'A1')
    for name,frame in [('summary',summary),('comparison',comparison),('daily_policy_paths',full),('daily_pnl',wide),('period_risk',periods),('status_counts',status),('by_regime',by_regime)]:frame.to_csv(OUT/(name+'.csv'),index=False)
    report=['# GS position adjustments instead of stopping out','',*['- '+n for n in notes],'',
        '| Policy | Net | Daily DD | Intraday DD | Adjusted trades | Adjustment PnL |','|---|---:|---:|---:|---:|---:|']
    for row in summary[summary.Scope.eq('All_dates')].itertuples(index=False):
        report.append(f'| {row.Policy} | {row.Net:.2f} | {row.MDD:.2f} | {row.Source_model_intraday_MDD:.2f} | {row.Adjusted_trades} | {row.Adjustment_PnL:.2f} |')
    report+=['','No adjustment is adopted from this retrospective comparison.','','![Comparison](equity_drawdown_comparison.png)','']
    (OUT/'REPORT.md').write_text('\n'.join(report))
    inputs.extend([Path(__file__),ROOT/'gs_adjustment_overlays.py',ROOT/'GS_ADJUSTMENT_OVERLAY_SPEC.md',
        ROOT/'GS_ADJUSTMENT_AUDIT_NOTES.md',ROOT/'verify_gs_adjustments.py',ROOT/'verify_gs_adjustments_fast.py',
        STOPS/'manifest.json',SOURCE/'run_manifest.json',OUT/'verification.json',OUT/'run_manifest.json',OUT/'pilot_manifest.json'])
    manifest={'Inputs':{str(p.relative_to(ROOT)):hashlib.sha256(p.read_bytes()).hexdigest() for p in inputs},
        'Outputs':{p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in OUT.iterdir() if p.is_file() and p.name not in ('manifest.json','verification.json','full_run.log')}}
    (OUT/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
    print(summary.to_string(index=False),flush=True)


if __name__=='__main__':main()
