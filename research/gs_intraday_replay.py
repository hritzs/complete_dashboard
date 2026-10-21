"""Instrument frozen GS in memory; reconcile intraday source-model paths.

This preserves legacy fills. It does NOT certify causal/live execution.
"""
import os
for key in ('NUMBA_NUM_THREADS','OMP_NUM_THREADS','OPENBLAS_NUM_THREADS','MKL_NUM_THREADS'):os.environ[key]='1'
import argparse,ast,contextlib,gc,hashlib,io,json,math,multiprocessing,pickle,sys,time,traceback,types
from concurrent.futures import ProcessPoolExecutor,as_completed
from pathlib import Path
import numpy as np
import pandas as pd
import pyarrow as pa

ROOT=Path(__file__).resolve().parent
SOURCE=ROOT/'output/logic_comparison_20261021'
OUT=ROOT/'output/gs_intraday_stops'
SRC=SOURCE/'sources/comparison_new_engine.py'
ENGINE=None;CURRENT=None;CAPTURE=None;DAILY=None;LOOKUP=None
POLICIES=('Baseline','Stop075','Stop0875','RegimeExit','Stop0875_Regime')


def clock(second):
    return f'{int(second)//3600:02d}:{int(second)%3600//60:02d}:{int(second)%60:02d}'


def seconds(stamps):
    return (stamps.dt.hour*3600+stamps.dt.minute*60+stamps.dt.second).to_numpy(np.int32)


def regime_signal(times,underlying,decision):
    """Only source labels at least2seconds before each completed minute boundary."""
    boundary=int(decision)//60*60
    targets=np.arange(boundary-20*60,boundary+1,60)-2
    pos=np.searchsorted(times,targets,side='right')-1
    if (pos<0).any():return 'Unavailable',0,np.nan,np.nan
    if (targets-times[pos]>3).any():return 'Unavailable',0,np.nan,np.nan
    u=underlying[pos]
    if not np.isfinite(u).all() or (u<=0).any():return 'Unavailable',0,np.nan,np.nan
    returns=np.diff(np.log(u));rv5=float(np.sum(returns[-5:]**2));prior=float(np.sum(returns[:-5]**2)/3)
    travel=float(np.abs(np.diff(u[-6:])).sum());eff=float(abs(u[-1]-u[-6])/travel) if travel else 0.
    ratio=rv5/prior if prior>0 else np.nan
    direction=1 if u[-1]>max(u[-6:-1]) and u[-1]>u[-6] else -1 if u[-1]<min(u[-6:-1]) and u[-1]<u[-6] else 0
    if np.isfinite(ratio) and ratio>1.5 and eff>=.60 and direction:return 'Directional_expansion',direction,eff,ratio
    if np.isfinite(ratio) and ratio>2. and eff<=.30:return 'Volatile_whipsaw',0,eff,ratio
    return 'Calm',0,eff,ratio


def initialize():
    global ENGINE,DAILY,LOOKUP
    pa.set_cpu_count(1);pa.set_io_thread_count(1)
    tree=ast.parse(SRC.read_text());changed_worker=changed_cw=changed_nw=False
    for node in tree.body:
        if isinstance(node,ast.Assign) and any(isinstance(t,ast.Name) and t.id=='OUTPUT_BASE' for t in node.targets):
            node.value=ast.Constant(str(OUT/'runtime'))
        if not isinstance(node,ast.FunctionDef):continue
        for decorator in node.decorator_list:
            if isinstance(decorator,ast.Call):
                for kw in decorator.keywords:
                    if kw.arg=='cache':kw.value=ast.Constant(False)
        if node.name=='simulate_trade':
            for i,stmt in enumerate(node.body):
                if isinstance(stmt,ast.Assign) and any(isinstance(t,ast.Name) and t.id=='sc' for t in stmt.targets):
                    node.body[i:i]=ast.parse('_capture_cw(locals())').body;changed_cw=True;break
        if node.name=='nw_day':
            for i,stmt in enumerate(node.body):
                if isinstance(stmt,ast.For) and isinstance(stmt.target,ast.Name) and stmt.target.id=='r':
                    node.body[i:i]=ast.parse('_nw_steps=[]').body
                    for sub in ast.walk(stmt):
                        if isinstance(sub,ast.If) and any(isinstance(x,ast.Break) for x in sub.body):
                            j=next(j for j,x in enumerate(sub.body) if isinstance(x,ast.Break))
                            sub.body[j:j]=ast.parse('_nw_steps.append((r[0],pos,fut,hn))').body
                    stmt.body.extend(ast.parse('_nw_steps.append((r[0],pos,fut,hn))').body)
                    break
            assert isinstance(node.body[-1],ast.Return)
            node.body[-1:-1]=ast.parse('_capture_nw(locals())').body;changed_nw=True
        if node.name=='worker_v_curve_evaluator':
            for sub in ast.walk(node):
                if not hasattr(sub,'body') or not isinstance(sub.body,list):continue
                for i,stmt in enumerate(sub.body):
                    if isinstance(stmt,ast.Assign) and any(isinstance(t,ast.Subscript) and isinstance(t.value,ast.Name) and t.value.id=='idx_base' and isinstance(t.slice,ast.Constant) and t.slice.value=='Minute' for t in stmt.targets):
                        sub.body[i:i]=ast.parse('return _run_selected(locals())').body;changed_worker=True;break
    assert changed_worker and changed_cw and changed_nw
    ast.fix_missing_locations(tree)
    module=types.ModuleType('_gs_trace_engine');module.__file__=str(SRC)
    module.__dict__.update(_capture_cw=capture_cw,_capture_nw=capture_nw,_run_selected=run_selected)
    sys.modules[module.__name__]=module
    exec(compile(tree,str(SRC),'exec'),module.__dict__);ENGINE=module
    DAILY=pd.read_csv(SOURCE/'inputs/daily.csv');DAILY.Date=pd.to_datetime(DAILY.Date);DAILY=DAILY.sort_values('Date').reset_index(drop=True)
    prev=DAILY.copy();prev.PrevDate=pd.to_datetime(prev.PrevDate);prev.Prev2Date=pd.to_datetime(prev.Prev2Date)
    gap=(prev.PrevDate-prev.Prev2Date).dt.days>1
    target=np.where(gap,prev.Adj_IV_Chg,prev.IV_Chg);denom=np.where(gap,prev.Prev2_Adj_IV,prev.Prev2_IV)
    normalized=target*.1/np.where(denom==0,np.nan,denom)
    LOOKUP={row.Date.strftime('%Y-%m-%d'):{'Straddle':float(row.Straddle),'Normalized_Adj_Chg':float(normalized[i]),
        'Yesterday_IV':float(row.Adj_IV),'Future':float(row.Future)} for i,row in prev.iterrows()}


def run_selected(local):
    idx=local['idx_base'];cache=local['strike_data_cache'];date=CURRENT['Date']
    match=idx.Time.dt.strftime('%H:%M').eq(CURRENT['Entry_Time']).to_numpy()
    assert match.any(),'Frozen entry minute absent'
    entry=int(np.flatnonzero(match)[0]);assert idx.ATMRef.iloc[entry]==CURRENT['Strike']
    result=ENGINE.simulate_trade(idx,entry,len(idx)-1,cache,float(CURRENT['SL_bps']),float(CURRENT['TP_bps']),
        ENGINE.TV_THRESH,ENGINE.TV4_THRESH,ENGINE.IV_DROP_THRESH,date,int(CURRENT['DTE_idx']))
    assert result is not None
    scale=1/3 if CURRENT['Event'] else 1.
    np.testing.assert_allclose(result['gross']*scale,CURRENT['Gross_PnL'],atol=1e-8,rtol=0)
    np.testing.assert_allclose(result['hedge_notional']*scale,CURRENT['Hedge_Notional'],atol=1e-7,rtol=0)
    assert result['exit_time']==CURRENT['Exit_Time'] and result['exit_reason']==CURRENT['Exit_Reason']
    assert result['min_hedges']==CURRENT['Min_Hedges'] and result['intra_hedges']==CURRENT['Intra_Hedges']
    return {'Selected_native_result':result}


def capture_cw(local):
    global CAPTURE
    x=local;cut=x['cut'];a=x['sec_abs'][:cut+1];arrays=x['arrays']
    scale=ENGINE.PNL_SCALE/x['entry_underlying']*(1/3 if CURRENT['Event'] else 1.)
    index=x['idx_base'];times=seconds(index.Time.iloc[a])
    mark=(x['ev']-arrays[5][a]-arrays[6][a]+x['fut_sec'][:cut+1])*scale
    force=(x['ev']-arrays[9][a]-arrays[10][a]+x['fut_sec'][:cut+1])*scale
    notional=x['hn_sec'][:cut+1]*scale
    rate=ENGINE.HEDGE_SLIP_BPS.get(int(CURRENT['DTE_idx']),ENGINE.HEDGE_SLIP_DEF)/10000
    cost=float(CURRENT['Entry_Cost'])+rate*notional
    frame=pd.DataFrame({'Second':times,'U':x['S'][:cut+1],'Mark_net':mark-cost,'Close_net':force-cost,
        'Hedge_cost':rate*notional,'Fut_position':x['fut_tot'][:cut+1],'Fut_trade':x['fut_trade'][:cut+1],
        'Is_check':x['is_min'][:cut+1],'Trigger_gross':np.nan})
    rp=x['row_pos'][:x['exit_row']+1]
    frame.loc[rp,'Trigger_gross']=x['tot_pnl'][:x['exit_row']+1]*scale
    frame.loc[len(frame)-1,'Mark_net']=frame.Close_net.iloc[-1]
    np.testing.assert_allclose([frame.Close_net.iloc[-1],frame.Hedge_cost.iloc[-1]],
        [CURRENT['Net_PnL'],CURRENT['Hedge_Cost']],atol=1e-8,rtol=0)
    CAPTURE={'Path':frame,'Feature_seconds':seconds(index.Time),'Feature_U':index.Underlying.to_numpy(float),
        'Budget':2.5*CURRENT['SL_bps']*(1/3 if CURRENT['Event'] else 1.),'Trace_kind':'CW_second_source_model'}


def capture_nw(local):
    global CAPTURE
    x=local;steps=x['_nw_steps'];exit_hhmm=x['ex'][0]
    end=exit_hhmm//100*3600+exit_hhmm%100*60;start=ENGINE.NW_ENTRY_T//100*3600+ENGINE.NW_ENTRY_T%100*60
    grid=np.arange(start,end+1,dtype=np.int32);secs=x['secs'];p=np.searchsorted(secs,grid,side='right')-1
    assert (p>=0).all()
    u=x['S'][p];bid=x['ce']['Bid'].to_numpy(float)[p]+x['pe']['Bid'].to_numpy(float)[p]
    ask=x['ce']['Ask'].to_numpy(float)[p]+x['pe']['Ask'].to_numpy(float)[p]
    pos=np.zeros(len(grid));fut=np.zeros(len(grid));hn=np.zeros(len(grid));trades=np.zeros(len(grid))
    previous_pos=0.
    for hhmm,position,profit,notional in steps:
        t=hhmm//100*3600+hhmm%100*60;j=t-start
        assert j>=0
        ref=x['S'][int(np.searchsorted(secs,t,side='right')-1)]
        pos[j:]=position;fut[j:]=profit+position*(u[j:]-ref);hn[j:]=notional
        trades[j]=position-previous_pos;previous_pos=position
    scale=ENGINE.PNL_SCALE/x['u0'];cost=ENGINE.NW_HEDGE_COST_BPS/10000*hn*scale
    mark=(x['credit']-ask+fut)*scale-cost
    result=x['out'];np.testing.assert_allclose(result['pnl'],CURRENT['Net_PnL'],atol=1e-8,rtol=0)
    assert result['exit_time']==CURRENT['Exit_Time'] and result['exit_reason']==CURRENT['Exit_Reason']
    # Minute stop uses source mid; intervening proxy uses liquidation ask.
    close=mark.copy();close[-1]=result['pnl'];mark[-1]=result['pnl']
    frame=pd.DataFrame({'Second':grid,'U':u,'Mark_net':mark,'Close_net':close,'Hedge_cost':cost,
        'Fut_position':pos,'Fut_trade':trades,'Is_check':grid%60==0,'Trigger_gross':np.nan})
    assert np.isfinite(frame[['Mark_net','Close_net','U']].to_numpy()).all()
    CAPTURE={'Path':frame,'Feature_seconds':secs,'Feature_U':x['S'],'Budget':np.nan,'Trace_kind':'NW_minute_hedges_second_mark_proxy'}


def policies(capture):
    frame=capture['Path'];check=frame[frame.Is_check];signals=[];previous=None
    for row in check.itertuples():
        label,direction,eff,ratio=regime_signal(capture['Feature_seconds'],capture['Feature_U'],row.Second)
        minute=int(row.Second)//60
        confirmed=label in ('Directional_expansion','Volatile_whipsaw') and previous==(label,direction,minute-1)
        signals.append({'Second':row.Second,'Regime':label,'Direction':direction,'Efficiency':eff,'RV_ratio':ratio,'Confirmed':confirmed})
        previous=(label,direction,minute)
    first=next((s for s in signals if s['Confirmed']),None)
    result=[]
    for name in POLICIES:
        cut=len(frame)-1;reason=CURRENT['Exit_Reason'];decision_regime=''
        if CURRENT['Leg']=='CW' and name!='Baseline':
            mult=.75 if name=='Stop075' else .875 if name in ('Stop0875','Stop0875_Regime') else 1.
            candidates=[]
            if name!='RegimeExit':
                hits=check[check.Trigger_gross.le(-capture['Budget']*mult)]
                if len(hits):candidates.append((int(hits.index[0]),'Tighter_SL',''))
            if 'Regime' in name and first is not None:
                loc=int(np.flatnonzero(frame.Second.to_numpy()==first['Second'])[0]);candidates.append((loc,'Regime_exit',first['Regime']))
            if candidates:
                proposed,why,regime=min(candidates,key=lambda q:q[0])
                if proposed<cut:cut=proposed;reason=why;decision_regime=regime
        net=float(frame.Close_net.iloc[cut]);holding=frame.Mark_net.iloc[:cut+1].to_numpy(float).copy();holding[-1]=net
        result.append({'Date':CURRENT['Date'],'Policy':name,'Leg':CURRENT['Leg'],'Entry_Time':CURRENT['Entry_Time'],
            'Exit_Time':clock(frame.Second.iloc[cut]),'Baseline_Exit_Time':CURRENT['Exit_Time'],'Exit_Reason':reason,
            'Decision_regime':decision_regime,'Net_PnL':net,'Baseline_Net_PnL':CURRENT['Net_PnL'],
            'Hedge_cost':float(frame.Hedge_cost.iloc[cut]),'Source_mark_MAE':float(max(0,-holding.min())),
            'Source_mark_MFE':float(max(0,holding.max())),'Path_cut_index':cut,'Changed_exit':cut<len(frame)-1,
            'Post_entry_hedge_count':int(frame.Fut_trade.iloc[1:cut+1].ne(0).sum())})
    return result,pd.DataFrame(signals)


def one_day(date):
    global CURRENT,CAPTURE
    began=time.monotonic();CAPTURE=None
    original=pickle.loads((SOURCE/'days'/(date+'.pkl')).read_bytes());CURRENT=original['New']
    path=Path('/mnt/shared/TickDataIV')/pd.Timestamp(date).strftime('%d%m%Y')/'NIFTY.parquet'
    try:
        before=path.stat();assert (before.st_size,str(before.st_mtime_ns))==(original['Tick_size'],original['Tick_mtime_ns'])
        if CURRENT.get('Leg') not in ('CW','NW'):
            result={'Date':date,'Traded':False,'Policies':[{'Date':date,'Policy':p,'Net_PnL':0.,'Changed_exit':False} for p in POLICIES]}
        else:
            log=io.StringIO()
            with contextlib.redirect_stdout(log):
                if CURRENT['Leg']=='CW':
                    events={pd.Timestamp(date).date()} if CURRENT['Event'] else set()
                    returned=ENGINE.worker_v_curve_evaluator(([str(path)],DAILY,LOOKUP,events,0,str(OUT/'runtime')))
                    if not isinstance(returned,dict):raise RuntimeError(log.getvalue()+str(returned))
                else:
                    _,nw=ENGINE.load_day_ticks(str(path),pd.Timestamp(date));out,why=ENGINE.nw_day(nw)
                    if why or out is None:raise RuntimeError(str(why))
            assert CAPTURE is not None,'Native simulation failed to expose a path'
            values=CAPTURE['Path'][['Mark_net','Close_net','U']].to_numpy(float);assert np.isfinite(values).all()
            policy_rows,signals=policies(CAPTURE)
            np.testing.assert_allclose(policy_rows[0]['Net_PnL'],CURRENT['Net_PnL'],atol=1e-8,rtol=0)
            dest=OUT/'paths'/(date+'.parquet');tmp=dest.with_suffix('.tmp')
            CAPTURE['Path'].to_parquet(tmp,index=False,compression='zstd');tmp.replace(dest)
            signal_path=OUT/'signals'/(date+'.parquet');tmp=signal_path.with_suffix('.tmp')
            signals.to_parquet(tmp,index=False,compression='zstd');tmp.replace(signal_path)
            result={'Date':date,'Traded':True,'Policies':policy_rows,'Trace_kind':CAPTURE['Trace_kind'],
                'Path_rows':len(CAPTURE['Path']),'Path_SHA256':hashlib.sha256(dest.read_bytes()).hexdigest(),
                'Signals_SHA256':hashlib.sha256(signal_path.read_bytes()).hexdigest()}
        after=path.stat();assert (before.st_size,before.st_mtime_ns)==(after.st_size,after.st_mtime_ns)
        return {**result,'Error':'','Seconds':time.monotonic()-began,'Tick_size':before.st_size,'Tick_mtime_ns':str(before.st_mtime_ns)}
    except Exception:return {'Date':date,'Error':traceback.format_exc(),'Seconds':time.monotonic()-began}
    finally:CAPTURE=None;gc.collect()


def main():
    parser=argparse.ArgumentParser();parser.add_argument('--pilot',action='store_true');parser.add_argument('--workers',type=int,default=12)
    args=parser.parse_args()
    for name in ('days','pilot_days','paths','signals','runtime'):(OUT/name).mkdir(parents=True,exist_ok=True)
    run=json.loads((SOURCE/'run_manifest.json').read_text());assert run['Status']=='complete' and not run['Failed_dates']
    for name,digest in run['Inputs'].items():assert hashlib.sha256((ROOT/name).read_bytes()).hexdigest()==digest,name
    inputs=[Path(__file__),ROOT/'GS_INTRADAY_STOP_REPLAY_SPEC.md',SOURCE/'run_manifest.json',*sorted((SOURCE/'days').glob('*.pkl'))]
    hashes={str(p.relative_to(ROOT)):hashlib.sha256(p.read_bytes()).hexdigest() for p in inputs}
    dates=run['Dates']
    if args.pilot:
        rows=pd.DataFrame([pickle.loads((SOURCE/'days'/(d+'.pkl')).read_bytes())['New'] for d in dates])
        picks=[rows[rows.Leg.eq('CW')&rows.Stage.eq('09:16')].Date.iloc[0],rows[rows.Leg.eq('CW')&rows.Stage.eq('09:20+')].Date.iloc[0],
            rows[rows.Leg.eq('CW')&rows.Event.eq(True)].Date.iloc[0],rows[rows.Leg.eq('CW')&rows.DTE_idx.eq(0)].Date.iloc[0],
            rows[rows.Leg.eq('CW')&rows.DTE_idx.eq(1)].Date.iloc[0],rows[rows.Leg.eq('NW')].Date.iloc[0],
            rows[rows.Leg.eq('CW')].sort_values('Net_PnL').Date.iloc[0],'2021-11-04']
        dates=list(dict.fromkeys(picks))
    dest=OUT/('pilot_days' if args.pilot else 'days');manifest=OUT/('pilot_manifest.json' if args.pilot else 'run_manifest.json')
    if manifest.exists():assert json.loads(manifest.read_text())['Inputs']==hashes,'Inputs changed; use a new output directory'
    data={'Status':'running','Inputs':hashes,'Dates':dates,'Workers':args.workers,'Scope':'Source-model reconstruction and fixed CW overlays; not causal execution'}
    manifest.write_text(json.dumps(data,indent=2)+'\n')
    todo=[d for d in dates if not (dest/(d+'.json')).exists() or json.loads((dest/(d+'.json')).read_text()).get('Error')]
    print('Replay',len(dates),'dates; remaining',len(todo),flush=True)
    with ProcessPoolExecutor(max_workers=args.workers,mp_context=multiprocessing.get_context('fork'),initializer=initialize) as pool:
        futures={pool.submit(one_day,date):date for date in todo}
        for i,future in enumerate(as_completed(futures),1):
            row=future.result();p=dest/(row['Date']+'.json');tmp=p.with_suffix('.tmp');tmp.write_text(json.dumps(row,indent=2)+'\n');tmp.replace(p)
            if args.pilot or i%25==0 or row['Error']:print(i,'/',len(todo),row['Date'],'seconds',round(row['Seconds'],1),'error',row['Error'][-150:],flush=True)
    failed=[d for d in dates if json.loads((dest/(d+'.json')).read_text()).get('Error')]
    data.update(Status='errors' if failed else 'complete',Failed_dates=failed);manifest.write_text(json.dumps(data,indent=2)+'\n')
    print('Finished; failures:',failed,flush=True)
    if failed:raise SystemExit(1)


if __name__=='__main__':main()
