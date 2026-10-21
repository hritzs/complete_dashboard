"""Save exact daily GS drawdown/risk baseline; never invent intraday marks."""
import os
os.environ.setdefault('MPLCONFIGDIR','/tmp/gs-stop-risk-mpl')
import hashlib,json,pickle
from pathlib import Path
import numpy as np
import pandas as pd
import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt

ROOT=Path(__file__).resolve().parent
SOURCE=ROOT/'output/logic_comparison_20261021'
OUT=ROOT/'output/gs_stop_risk'


def equity_path(values):
    p=np.asarray(values,dtype=float)
    if p.ndim!=1 or not np.isfinite(p).all():raise ValueError('Missing or invalid PnL blocks risk reporting')
    e=p.cumsum();h=np.maximum.accumulate(np.r_[0.,e])[1:]
    return e,h,h-e


def episodes(dates,values):
    eq,peak,dd=equity_path(values);rows=[];episode=None;peak_date='Initial_equity_0'
    for i,date in enumerate(dates):
        if dd[i]>1e-9:
            if episode is None:episode={'Peak_date':peak_date,'First_underwater_date':date,'Peak_equity':peak[i],
                'Trough_date':date,'Depth':0.,'Underwater_observations':0}
            episode['Underwater_observations']+=1
            if dd[i]>episode['Depth']:episode.update(Depth=dd[i],Trough_date=date)
        else:
            if episode is not None:
                episode.update(Recovery_date=date,Status='Recovered',Last_observed_date=date)
                rows.append(episode);episode=None
            peak_date=date
    if episode is not None:
        episode.update(Recovery_date='',Status='Unrecovered',Last_observed_date=dates[-1]);rows.append(episode)
    for row in rows:
        anchor=row['First_underwater_date'] if row['Peak_date']=='Initial_equity_0' else row['Peak_date']
        row['Elapsed_calendar_days']=(pd.Timestamp(row['Last_observed_date'])-pd.Timestamp(anchor)).days
    return pd.DataFrame(rows)


def main():
    OUT.mkdir(parents=True,exist_ok=True)
    run=json.loads((SOURCE/'run_manifest.json').read_text())
    assert run['Status']=='complete' and not run['Failed_dates']
    for name,digest in run['Inputs'].items():assert hashlib.sha256((ROOT/name).read_bytes()).hexdigest()==digest,name
    paths=[SOURCE/'days'/(date+'.pkl') for date in run['Dates']]
    records=[pickle.loads(p.read_bytes()) for p in paths]
    assert all(not r.get('Error') for r in records)
    d=pd.DataFrame([r['New'] for r in records]);assert d.Date.tolist()==run['Dates']
    d['Traded']=d.Leg.isin(['CW','NW'])
    assert np.isfinite(d.Net_PnL.to_numpy(float)).all(),'Missing source PnL blocks the risk baseline'
    assert d.loc[~d.Traded,'Net_PnL'].eq(0).all()
    e,h,dd=equity_path(d.Net_PnL)
    daily=d.copy();daily['Equity']=e;daily['Global_peak']=h;daily['Global_DD']=dd
    for window in (20,60,252):
        daily[f'Rolling_{window}_observation_MDD']=[float(equity_path(d.Net_PnL.iloc[max(0,i-window+1):i+1])[2].max()) for i in range(len(d))]
    periods=[];dates=pd.to_datetime(d.Date)
    groups={'Year':dates.dt.year.astype(str),'Quarter':dates.dt.to_period('Q').astype(str),
        'Month':dates.dt.to_period('M').astype(str),
        'FY':pd.Series(np.where(dates.dt.month>=4,dates.dt.year,dates.dt.year-1)).map(lambda y:f'FY{y}-{y+1}')}
    for name,labels in groups.items():
        for label,g in daily.groupby(labels,sort=True):
            periods.append({'Grouping':name,'Period':label,'Net':g.Net_PnL.sum(),'Trades':int(g.Traded.sum()),
                'Reset_period_MDD':equity_path(g.Net_PnL)[2].max(),'Max_global_DD_observed':g.Global_DD.max()})
    stop=d[d.Traded].copy();cw=stop.Leg.eq('CW')
    stop['CW_gross_trigger_budget_normalized']=np.where(cw,2.5*stop.SL_bps*stop.Trades,np.nan)
    stop['Net_loss_exceeding_CW_gross_trigger']=np.where(cw,np.maximum(0,-stop.Net_PnL-stop.CW_gross_trigger_budget_normalized),np.nan)
    stop['Intratrade_MAE']=np.nan;stop['Intraday_MDD']=np.nan
    traded=d.loc[d.Traded,'Net_PnL'].to_numpy(float);tail_n=max(1,int(np.ceil(.05*len(traded))))
    streak=longest=0
    for value in traded:streak=streak+1 if value<0 else 0;longest=max(longest,streak)
    summary={'Status':'DAILY_BASELINE_ONLY','Dates':len(d),'Traded_dates':int(d.Traded.sum()),
        'Net':float(e[-1]),'Max_closed_day_DD':float(dd.max()),'Worst_traded_day':float(traded.min()),
        'Empirical_worst_5pct_traded_day_mean_PnL':float(np.sort(traded)[:tail_n].mean()),
        'Tail_observations':tail_n,'Longest_losing_trade_streak':longest,
        'Intraday_DD_available':False,'Model_fitted':False,'Stops_optimized':False,
        'PnL_units':'Normalized additive units; no account capital or percentage DD assumed'}
    epi=episodes(d.Date.tolist(),d.Net_PnL)
    np.testing.assert_allclose(summary['Max_closed_day_DD'],168.424258,atol=1e-6,rtol=0)
    np.testing.assert_allclose(epi.Depth.max(),summary['Max_closed_day_DD'],atol=1e-8,rtol=0)
    fig,ax=plt.subplots(2,1,figsize=(12,7),sharex=True,layout='constrained')
    ax[0].plot(dates,e);ax[0].set(title='Frozen latest GS: daily risk baseline',ylabel='Cumulative net units')
    ax[1].fill_between(dates,dd,0,alpha=.4);ax[1].set(ylabel='Closed-day drawdown units',xlabel='Date (intraday DD not available)')
    fig.savefig(OUT/'daily_equity_drawdown.png',dpi=160);plt.close(fig)
    tables={'Summary':pd.DataFrame([summary]),'Daily_DD':daily,'DD_episodes':epi,'Period_risk':pd.DataFrame(periods),
        'Trade_stop_audit':stop,'Research_contract':pd.DataFrame({'Line':(ROOT/'GS_STOP_REGIME_RISK_SPEC.md').read_text().splitlines()})}
    with pd.ExcelWriter(OUT/'gs_stop_risk_baseline.xlsx') as writer:
        for name,frame in tables.items():frame.to_excel(writer,sheet_name=name,index=False);writer.sheets[name].freeze_panes='B2'
        from openpyxl.drawing.image import Image
        writer.book.create_sheet('Charts').add_image(Image(OUT/'daily_equity_drawdown.png'),'A1')
    for name in ('Daily_DD','DD_episodes','Period_risk','Trade_stop_audit'):tables[name].to_csv(OUT/(name.lower()+'.csv'),index=False)
    (OUT/'summary.json').write_text(json.dumps(summary,indent=2)+'\n')
    (OUT/'REPORT.md').write_text('\n'.join([
        '# GS stop and regime risk: measured daily baseline','',
        'This is the frozen latest-GS daily baseline, not an optimized stop/model result.',
        f"- Dates: {len(d)}; traded dates: {int(d.Traded.sum())}.",
        f"- Net normalized PnL: {summary['Net']:.2f}; maximum closed-day DD: {summary['Max_closed_day_DD']:.2f}.",
        f"- Worst traded day: {summary['Worst_traded_day']:.2f}; mean of worst40 traded days: {summary['Empirical_worst_5pct_traded_day_mean_PnL']:.2f}.",
        '- Intraday DD and whole-trade MAE are unavailable from daily checkpoints and are not estimated here.',
        '- Net loss beyond the gross CW trigger is a diagnostic including fees and other exits; it is not a measured trigger-to-fill overshoot.',
        '- Global DD carries the equity peak across year boundaries. Period-reset and rolling-window DD are separately labeled.',
        '- No account capital is assumed; these are additive normalized units, not currency returns or percentage DD.',
        '- Next execution gates, event labels, regime controls and chronological comparison are defined in research/GS_STOP_REGIME_RISK_SPEC.md.',
        '', '![Daily equity and drawdown](daily_equity_drawdown.png)','']))
    inputs=[Path(__file__),ROOT/'test_gs_stop_risk.py',ROOT/'GS_STOP_REGIME_RISK_SPEC.md',ROOT/'GS_REGIME_COMPLEMENT_SPEC.md',SOURCE/'run_manifest.json',*paths]
    manifest={'Inputs':{str(p.relative_to(ROOT)):hashlib.sha256(p.read_bytes()).hexdigest() for p in inputs},
        'Outputs':{p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in OUT.iterdir() if p.name not in ('manifest.json','verification.json')}}
    (OUT/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n');print(json.dumps(summary,indent=2))


if __name__=='__main__':main()
