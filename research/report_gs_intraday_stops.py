"""Complete-book stop comparison, explicitly retaining legacy source-model limits."""
import os
os.environ.setdefault('MPLCONFIGDIR','/tmp/gs-intraday-report-mpl')
import hashlib,json,pickle
from pathlib import Path
import numpy as np
import pandas as pd
import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt
from gs_intraday_replay import ROOT,OUT,SOURCE,POLICIES,clock
from gs_stop_risk_baseline import equity_path,episodes


def intraday_series(net,low,high,local_dd):
    """Exact global DD from each day's ordered-path extrema and local max DD."""
    equity=peak=maximum=0.;rows=[]
    for p,lo,hi,dd in zip(net,low,high,local_dd):
        day_max=max(float(dd),peak-(equity+float(lo)))
        peak=max(peak,equity+float(hi));equity+=float(p);maximum=max(maximum,day_max)
        rows.append((equity,peak,peak-equity,day_max,maximum))
    return np.asarray(rows,float)


def update_episodes(state,date,times,marks):
    """Vectorize marks; iterate only changes between underwater and recovered."""
    cash=state.get('cash',0.);peak=state.get('peak',0.)
    values=cash+np.asarray(marks,float)
    highs=np.maximum.accumulate(np.r_[peak,values])[1:];dd=highs-values
    underwater=dd>1e-9
    cuts=np.r_[0,np.flatnonzero(underwater[1:]!=underwater[:-1])+1,len(values)]
    for left,right in zip(cuts[:-1],cuts[1:]):
        if underwater[left]:
            if state.get('episode') is None:
                state['episode']={'Policy':state['Policy'],'Peak_time':state.get('peak_time','Initial_equity_0'),
                    'First_underwater_time':date+' '+clock(times[left]),'Peak_equity':float(highs[left]),'Depth':0.,
                    'Marked_observations_underwater':0}
            ep=state['episode'];ep['Marked_observations_underwater']+=int(right-left)
            i=int(left+np.argmax(dd[left:right]))
            if dd[i]>ep['Depth']:ep.update(Depth=float(dd[i]),Trough_time=date+' '+clock(times[i]))
        else:
            if state.get('episode') is not None:
                ep=state.pop('episode');ep.update(Recovery_time=date+' '+clock(times[left]),Status='Recovered')
                state.setdefault('episodes',[]).append(ep)
            state['peak_time']=date+' '+clock(times[right-1])
    state.update(cash=float(values[-1]),peak=float(highs[-1]),last_time=date+' '+clock(times[-1]))


def metrics(frame):
    p=frame.Net_PnL.to_numpy(float);e,h,dd=equity_path(p)
    proxy=intraday_series(p,frame.Path_low,frame.Path_high,frame.Day_local_MDD)
    traded=p[frame.Traded.to_numpy(bool)];n=max(1,int(np.ceil(.05*len(traded))))
    return {'Net':float(p.sum()),'MDD':float(dd.max()),'Source_model_intraday_MDD':float(proxy[:,-1].max()),
        'Worst_day':float(p.min()),'Trades':int(frame.Traded.sum()),'Changed_exits':int(frame.Changed_exit.sum()),
        'Win_pct':float(100*(traded>0).mean()) if len(traded) else np.nan,
        'Average_traded_PnL':float(traded.mean()) if len(traded) else np.nan,
        'Post_entry_hedge_count':int(frame.Post_entry_hedge_count.fillna(0).sum()),
        'Hedge_cost':float(frame.Hedge_cost.sum()),'Worst5pct_trade_mean':float(np.sort(traded)[:n].mean()) if len(traded) else np.nan,
        'Net_per_MDD':float(p.sum()/dd.max()) if dd.max()>0 else np.nan}


def main():
    run=json.loads((OUT/'run_manifest.json').read_text());assert run['Status']=='complete' and not run['Failed_dates']
    for name,digest in run['Inputs'].items():assert hashlib.sha256((ROOT/name).read_bytes()).hexdigest()==digest,name
    pilot=json.loads((OUT/'pilot_manifest.json').read_text());assert pilot['Status']=='complete' and pilot['Inputs']==run['Inputs']
    rows=[];baseline=[];checks=[];audits=[];inputs=[];coverage=[]
    episode_states={p:{'Policy':p,'cash':0.,'peak':0.} for p in POLICIES}
    for date in run['Dates']:
        checkpoint=OUT/'days'/(date+'.json');rec=json.loads(checkpoint.read_text());assert not rec['Error'];inputs.append(checkpoint)
        if date in pilot['Dates']:
            old=json.loads((OUT/'pilot_days'/(date+'.json')).read_text())
            assert old['Policies']==rec['Policies'] and old.get('Path_SHA256')==rec.get('Path_SHA256')
        original=pickle.loads((SOURCE/'days'/(date+'.pkl')).read_bytes());base=original['New'];baseline.append(base)
        assert (rec['Tick_size'],rec['Tick_mtime_ns'])==(original['Tick_size'],original['Tick_mtime_ns'])
        audits.append({k:v for k,v in rec.items() if k!='Policies'})
        if rec['Traded']:
            path=OUT/'paths'/(date+'.parquet');signal=OUT/'signals'/(date+'.parquet');inputs.extend([path,signal])
            assert hashlib.sha256(path.read_bytes()).hexdigest()==rec['Path_SHA256']
            assert hashlib.sha256(signal.read_bytes()).hexdigest()==rec['Signals_SHA256']
            frame=pd.read_parquet(path)
            sig=pd.read_parquet(signal);confirmed=sig[sig.Confirmed]
            coverage.append({'Date':date,'Leg':base['Leg'],'Checks':len(sig),'Unavailable_checks':int(sig.Regime.eq('Unavailable').sum()),
                'Directional_checks':int(sig.Regime.eq('Directional_expansion').sum()),'Whipsaw_checks':int(sig.Regime.eq('Volatile_whipsaw').sum()),
                'Confirmed_checks':int(sig.Confirmed.sum()),'First_confirmation_time':clock(confirmed.Second.iloc[0]) if len(confirmed) else '',
                'Overlay_permitted':base['Leg']=='CW'})
            np.testing.assert_allclose(frame.Close_net.iloc[-1],base['Net_PnL'],atol=1e-8,rtol=0)
        for item in rec['Policies']:
            row=dict(item,Traded=rec['Traded'])
            if rec['Traded']:
                cut=int(row['Path_cut_index']);mark=frame.Mark_net.iloc[:cut+1].to_numpy(float).copy();mark[-1]=row['Net_PnL']
                path_eq=np.r_[0.,mark];draw=np.maximum.accumulate(path_eq)-path_eq
                row.update(Path_low=float(path_eq.min()),Path_high=float(path_eq.max()),Day_local_MDD=float(draw.max()))
                if base['Leg']=='CW':
                    budget=2.5*base['SL_bps']*(1/3 if base['Event'] else 1.)
                    trigger=float(frame.Trigger_gross.iloc[cut])
                    trigger_net=trigger-float(base['Entry_Cost'])-float(frame.Hedge_cost.iloc[cut])
                    row.update(Original_CW_gross_budget=budget,Trigger_gross_at_exit=trigger,
                        Trigger_net_equivalent=trigger_net,Source_exit_cash_minus_trigger_mark=row['Net_PnL']-trigger_net)
                sample_times=np.r_[int(frame.Second.iloc[0])-1,frame.Second.iloc[:cut+1].to_numpy(int),86399]
                sample_marks=np.r_[0.,mark,row['Net_PnL']]
                update_episodes(episode_states[row['Policy']],date,sample_times,sample_marks)
                # Source minute checks and hedges are inspection points, not the full path.
                if row['Policy']=='Baseline':
                    sample=frame[frame.Is_check|frame.Fut_trade.ne(0)].copy()
                    for pos in {0,len(frame)-1,int(np.argmin(mark)),int(np.argmax(mark)),int(np.argmax(draw[1:]))}:
                        if pos not in sample.index:sample=pd.concat([sample,frame.iloc[[pos]]])
                    sample=sample.sort_index();sample.insert(0,'Date',date);sample['Time']=sample.Second.map(clock);checks.append(sample)
            else:
                row.update(Path_low=0.,Path_high=0.,Day_local_MDD=0.,Hedge_cost=0.,Leg='',Entry_Time='',Exit_Time='',Exit_Reason='')
                update_episodes(episode_states[row['Policy']],date,[86399],[0.])
            rows.append(row)
    daily=pd.DataFrame(rows);baseline=pd.DataFrame(baseline);dates=pd.to_datetime(run['Dates'])
    assert len(daily)==len(run['Dates'])*len(POLICIES)
    paths=[];summary=[];periods=[];episode_tables=[]
    scopes={'All_dates':np.ones(len(dates),bool),'2022plus':dates.year>=2022,'2023plus':dates.year>=2023,'2025plus':dates.year>=2025}
    labels={'Year':dates.year.astype(str),'Quarter':dates.to_period('Q').astype(str),'Month':dates.to_period('M').astype(str),
        'FY':np.array([f'FY{d.year if d.month>=4 else d.year-1}-{d.year+1 if d.month>=4 else d.year}' for d in dates])}
    for name in POLICIES:
        frame=daily[daily.Policy.eq(name)].set_index('Date').loc[run['Dates']].reset_index()
        e,h,dd=equity_path(frame.Net_PnL);proxy=intraday_series(frame.Net_PnL,frame.Path_low,frame.Path_high,frame.Day_local_MDD)
        frame['Equity']=e;frame['Daily_peak']=h;frame['Daily_DD']=dd
        for i,col in enumerate(('Source_model_equity_close','Source_model_intraday_peak','Source_model_DD_at_close','Source_model_day_max_DD','Source_model_running_MDD')):frame[col]=proxy[:,i]
        assert proxy[:,-1].max()+1e-8>=dd.max()
        paths.append(frame)
        ep=episodes(frame.Date.tolist(),frame.Net_PnL);ep.insert(0,'Policy',name);episode_tables.append(ep)
        for scope,mask in scopes.items():summary.append({'Policy':name,'Scope':scope,**metrics(frame.loc[mask])})
        for grouping,keys in labels.items():
            for label in sorted(set(keys)):
                subset=frame.loc[keys==label]
                periods.append({'Policy':name,'Grouping':grouping,'Period':label,**metrics(subset)})
    full=pd.concat(paths,ignore_index=True);summary=pd.DataFrame(summary);periods=pd.DataFrame(periods)
    baseline_rows=full[full.Policy.eq('Baseline')];np.testing.assert_allclose(baseline_rows.Net_PnL,baseline.Net_PnL,atol=1e-8,rtol=0)
    np.testing.assert_allclose(baseline_rows.Daily_DD.max(),168.42425845197704,atol=1e-8,rtol=0)
    comparisons=[]
    for scope in scopes:
        group=summary[summary.Scope.eq(scope)];base=group[group.Policy.eq('Baseline')].iloc[0]
        for row in group.itertuples(index=False):
            comparisons.append({'Policy':row.Policy,'Scope':scope,'Delta_net':row.Net-base.Net,
                'Daily_DD_change':row.MDD-base.MDD,'Source_model_intraday_DD_change':row.Source_model_intraday_MDD-base.Source_model_intraday_MDD,
                'Lower_daily_DD':row.MDD<base.MDD-1e-8,'Lower_source_model_intraday_DD':row.Source_model_intraday_MDD<base.Source_model_intraday_MDD-1e-8,
                'Profit_retention_pct':100*row.Net/base.Net if base.Net>0 else np.nan})
    comparison=pd.DataFrame(comparisons)
    intraday_episodes=[]
    for state in episode_states.values():
        if state.get('episode') is not None:
            ep=state.pop('episode');ep.update(Recovery_time='',Status='Unrecovered');state.setdefault('episodes',[]).append(ep)
        for ep in state.get('episodes',[]):
            end=ep['Recovery_time'] or state['last_time']
            start=ep['First_underwater_time'] if ep['Peak_time']=='Initial_equity_0' else ep['Peak_time']
            ep['Elapsed_calendar_seconds']=(pd.Timestamp(end)-pd.Timestamp(start)).total_seconds()
            intraday_episodes.append(ep)
    intraday_episodes=pd.DataFrame(intraday_episodes)
    for row in summary[summary.Scope.eq('All_dates')].itertuples(index=False):
        group=intraday_episodes[intraday_episodes.Policy.eq(row.Policy)]
        np.testing.assert_allclose(group.Depth.max() if len(group) else 0.,row.Source_model_intraday_MDD,atol=1e-8,rtol=0)
    wide=full.pivot(index='Date',columns='Policy',values='Net_PnL').reindex(run['Dates']).reset_index()
    notes=['Fixed CW stop/regime overlays on unchanged selected latest-GS trades; no size changes or new entries.',
        'Retrospective fixed-candidate comparison, no model fitted or threshold winner selected. Prior research already examined these years.',
        'Intraday DD is a SOURCE-MODEL mark proxy: legacy future option/futures averages and backfills remain; not certified executable account DD.',
        'CW hedges are second-based; NW retains its separate minute hedge/mark execution convention. NW unchanged by every overlay.',
        'Hedge_cost here includes reconstructed NW fees already embedded in native NW net PnL; saved legacy NW Gross_PnL embeds those fees and its separate Hedge_Cost field is0. They are not deducted twice.',
        'Source_exit_cash_minus_trigger_mark isolates source mark/averaged-exit differences after like-for-like fees; it is not measured exchange slippage.',
        'Every early exit uses current source exit credit/debit and hedge PnL/cost prefix. Later hedge cash flows are discarded; daily PnL is never clipped.',
        'Raw Fut_position is the native hedge position before a hypothetical close. Close_net liquidates it under source assumptions; the overlay is flat thereafter. No extra close fees beyond the source model are invented.',
        'All1430 original dates retained, including Diwali/Muhurat. Entries and LUT penalties unchanged, including09:16.',
        'Full second source-model paths are in paths/*.parquet. Baseline_checkpoints sheet samples minute checks/hedges/extrema and is not the full intraday path.',
        'Source_model_day_max_DD carries prior intraday peaks across days. Period statistics reset within each reported period; these are not added.',
        'Episode duration includes flat cash observations before entry/after exit and at23:59:59; these are accounting marks, not invented market quotes.',
        'PnL and DD are normalized additive units, not currency or percentage account returns. No account capital is invented.',
        'Nonfinite reconstructed marks, trace failures and PnL mismatches block reporting. Legacy fills can conceal missing/stale source quotes; this is not quote-quality certification. Causal-execution replay and forward paper evidence remain necessary.']
    tables={'Read_me':pd.DataFrame({'Method':notes}),'Summary':summary,'Comparison':comparison,'Daily_PnL':wide,'All_policy_days':full,
        'Trades':full[full.Traded],'Latest_GS_daily':baseline,'Daily_DD_episodes':pd.concat(episode_tables,ignore_index=True),
        'Intraday_proxy_episodes':intraday_episodes,
        'Period_risk':periods,'Baseline_checkpoints':pd.concat(checks,ignore_index=True),'Replay_audit':pd.DataFrame(audits),
        'Regime_coverage':pd.DataFrame(coverage),
        'Specification':pd.DataFrame({'Line':(ROOT/'GS_INTRADAY_STOP_REPLAY_SPEC.md').read_text().splitlines()})}
    fig,ax=plt.subplots(3,1,figsize=(13,11),sharex=True,layout='constrained')
    for name,frame in full.groupby('Policy',sort=False):
        ax[0].plot(dates,frame.Equity,label=name,lw=1)
        ax[1].plot(dates,frame.Daily_DD,lw=1)
        ax[2].plot(dates,frame.Source_model_day_max_DD,lw=1)
    ax[0].set(title='Fixed GS stop/regime comparisons: legacy execution assumptions',ylabel='Cumulative net units');ax[0].legend(fontsize=8,ncol=3)
    ax[1].set(ylabel='Closed-day DD');ax[2].set(ylabel='Daily max source-model intraday DD',xlabel='Date; proxy is not executable account DD')
    fig.savefig(OUT/'equity_drawdown_comparison.png',dpi=150);plt.close(fig)
    with pd.ExcelWriter(OUT/'gs_intraday_stop_comparison.xlsx') as writer:
        for name,frame in tables.items():frame.to_excel(writer,sheet_name=name,index=False);writer.sheets[name].freeze_panes='B2'
        from openpyxl.drawing.image import Image
        writer.book.create_sheet('Charts').add_image(Image(OUT/'equity_drawdown_comparison.png'),'A1')
    for name,frame in [('summary',summary),('comparison',comparison),('daily_policy_paths',full),('daily_pnl',wide),('period_risk',periods),('intraday_proxy_episodes',intraday_episodes)]:frame.to_csv(OUT/(name+'.csv'),index=False)
    report=['# GS intraday reconstruction and fixed stop comparisons','',*['- '+n for n in notes],'',
        '| Policy | Net | Daily DD | Source-model intraday DD | Changed exits |','|---|---:|---:|---:|---:|']
    for row in summary[summary.Scope.eq('All_dates')].itertuples(index=False):report.append(f'| {row.Policy} | {row.Net:.2f} | {row.MDD:.2f} | {row.Source_model_intraday_MDD:.2f} | {row.Changed_exits} |')
    report+=['','No policy is adopted or called an optimal stop from this retrospective comparison.','',
        '![Comparison](equity_drawdown_comparison.png)','']
    (OUT/'REPORT.md').write_text('\n'.join(report))
    inputs.extend([Path(__file__),ROOT/'gs_intraday_replay.py',ROOT/'test_gs_intraday_replay.py',ROOT/'gs_stop_risk_baseline.py',
        ROOT/'verify_gs_intraday_stops.py',ROOT/'GS_INTRADAY_STOP_REPLAY_SPEC.md',OUT/'run_manifest.json',OUT/'pilot_manifest.json',
        *sorted((OUT/'pilot_days').glob('*.json'))])
    manifest={'Inputs':{str(p.relative_to(ROOT)):hashlib.sha256(p.read_bytes()).hexdigest() for p in inputs},
        'Outputs':{p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in OUT.iterdir() if p.is_file() and p.name not in ('manifest.json','verification.json')}}
    (OUT/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
    print(summary.to_string(index=False),flush=True)


if __name__=='__main__':main()
