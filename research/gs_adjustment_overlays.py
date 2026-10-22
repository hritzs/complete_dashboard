"""Position adjustments instead of stopping out (GS_ADJUSTMENT_OVERLAY_SPEC.md).

On each CW trade of the frozen latest-GS book the FIRST confirmed 4.24 regime
signal triggers one fixed adjustment: wings, roll, option hedges, a next-week
long straddle or selling more. Added legs are priced from the day's raw tick
quotes (buy at ask, sell at bid); the roll is a new trade through the unchanged
engine. The base trade, its hedging and exits are untouched otherwise.
Source-model limitations of 4.24 remain; this is not causal execution.
"""
import os
for key in ('NUMBA_NUM_THREADS','OMP_NUM_THREADS','OPENBLAS_NUM_THREADS','MKL_NUM_THREADS'):os.environ[key]='1'
import argparse,contextlib,gc,hashlib,io,json,multiprocessing,pickle,time,traceback
from concurrent.futures import ProcessPoolExecutor,as_completed
from pathlib import Path
import numpy as np
import pandas as pd
import gs_intraday_replay as base

ROOT=Path(__file__).resolve().parent
STOPS=ROOT/'output/gs_intraday_stops'
OUT=ROOT/'output/gs_adjustments'
CANDIDATES=('Baseline','Wings','Roll','BuyTested','SellUntested','NWLongStraddle','SellMore')
FEE=0.25          # normalized units per added option unit, round trip
WING_PCT=0.02
ROLL_T=None;ROLLING=False;ROLL_CAPTURE=None;ROLL_RESULT=None


def run_selected(local):
    """Baseline as 4.24 (re-verified), then the roll trade if one is due."""
    global ROLLING,ROLL_RESULT
    out=_base_run_selected(local)
    if ROLL_T is not None:
        idx=local['idx_base'];cache=local['strike_data_cache'];cur=base.CURRENT
        secs=base.seconds(idx.Time);entry=int(np.searchsorted(secs,ROLL_T,side='left'))
        if entry<len(idx)-1:
            ROLLING=True
            try:
                ROLL_RESULT=base.ENGINE.simulate_trade(idx,entry,len(idx)-1,cache,float(cur['SL_bps']),float(cur['TP_bps']),
                    base.ENGINE.TV_THRESH,base.ENGINE.TV4_THRESH,base.ENGINE.IV_DROP_THRESH,cur['Date'],int(cur['DTE_idx']))
            finally:ROLLING=False
    return out


def capture_cw(local):
    """The roll trade's own source-model path (base capture otherwise)."""
    global ROLL_CAPTURE
    if not ROLLING:return _base_capture_cw(local)
    x=local;cut=x['cut'];a=x['sec_abs'][:cut+1];arrays=x['arrays'];cur=base.CURRENT
    ev=1/3 if cur['Event'] else 1.;E=base.ENGINE
    scale=E.PNL_SCALE/x['entry_underlying']*ev
    times=base.seconds(x['idx_base'].Time.iloc[a])
    mark=(x['ev']-arrays[5][a]-arrays[6][a]+x['fut_sec'][:cut+1])*scale
    force=(x['ev']-arrays[9][a]-arrays[10][a]+x['fut_sec'][:cut+1])*scale
    dte=int(cur['DTE_idx']);rate=E.HEDGE_SLIP_BPS.get(dte,E.HEDGE_SLIP_DEF)/10000
    cost=E.ENTRY_SLIP.get(dte,E.ENTRY_SLIP_DEF)+rate*x['hn_sec'][:cut+1]*scale
    frame=pd.DataFrame({'Second':times,'Mark_net':mark-cost,'Close_net':force-cost})
    frame.loc[len(frame)-1,'Mark_net']=frame.Close_net.iloc[-1]
    ROLL_CAPTURE={'Path':frame,'Strike':float(x['idx_base'].ATMRef.iloc[a[0]])}


_base_run_selected=base.run_selected;_base_capture_cw=base.capture_cw


def initialize():
    base.run_selected=run_selected;base.capture_cw=capture_cw
    base.initialize()


def prepare(df):
    """The engine's own time / strike columns (worker_v_curve_evaluator), as seconds since midnight."""
    if df is None or df.empty:return df
    df=df.copy()
    tc=next(c for c in ['Time_dt','Time','timestamp','datetime','Date'] if c in df.columns)
    t=pd.to_datetime(df[tc])
    if t.dt.tz is not None:t=t.dt.tz_localize(None)
    df['_S']=(t.dt.hour*3600+t.dt.minute*60+t.dt.second).astype(np.int64)
    df['_K']=pd.to_numeric(df['Strike' if 'Strike' in df.columns else 'StrikePrice'],errors='coerce')
    df['Type']=df['Type'].astype(str)
    return df


def contract(df,K,T):
    c=df[(df['_K']==float(K))&(df['Type']==T)][['_S','Bid','Ask']].rename(columns={'_S':'Second'}).dropna()
    c=c[(c.Bid>0)&(c.Ask>0)].groupby('Second',sort=True).last()
    return c.index.to_numpy(np.int64),c.Bid.to_numpy(float),c.Ask.to_numpy(float)


def legs_for(name,regime,direction,K,K_up,K_dn):
    """(set, strike, type, sign +1 long / -1 short, units) per fixed candidate."""
    up=direction>0
    if name=='Wings':return [('cw',K_up,'CE',1,1.),('cw',K_dn,'PE',1,1.)]
    if name=='NWLongStraddle':return [('nw',K,'CE',1,.5),('nw',K,'PE',1,.5)]
    if regime=='Directional_expansion' and name=='BuyTested':return [('cw',K,'CE' if up else 'PE',1,.5)]
    if regime=='Directional_expansion' and name=='SellUntested':return [('cw',K,'PE' if up else 'CE',-1,.5)]
    if regime=='Volatile_whipsaw' and name=='SellMore':return [('cw',K,'CE',-1,.5),('cw',K,'PE',-1,.5)]
    return None


def price_overlay(legs,books,t_star,exit_second,grid,U,scale):
    """Execute, mark each second and close one candidate's added legs."""
    quotes=[books[(s,k,t)] for s,k,t,_,_ in legs]
    entry=None
    for s in range(t_star+2,t_star+6):
        if s>=exit_second:break
        if all(np.any(q[0]==s) for q in quotes):entry=s;break
    if entry is None:return {'Status':'no_entry_quote'}
    def at(q,s):j=int(np.flatnonzero(q[0]==s)[0]);return q[1][j],q[2][j]
    epx=[(at(q,entry)[1] if sign>0 else at(q,entry)[0]) for q,(_,_,_,sign,_) in zip(quotes,legs)]
    stale=False;close=None
    for s in range(exit_second,exit_second+6):
        if all(np.any(q[0]==s) for q in quotes):close=[at(q,s) for q in quotes];break
    if close is None:
        close=[]
        for q in quotes:
            j=int(np.searchsorted(q[0],exit_second,side='right'))-1
            if j<0 or exit_second-q[0][j]>60:return {'Status':'error','Error':'no exit quote within 60 s'}
            close.append((q[1][j],q[2][j]))
        stale=True
    xpx=[(c[0] if sign>0 else c[1]) for c,(_,_,_,sign,_) in zip(close,legs)]
    units=sum(u for *_,u in legs);fee=FEE*units*(1/3 if base.CURRENT['Event'] else 1.)  # normalized, event-scaled like the base
    gross=sum(sign*u*(x-e) for (_,_,_,sign,u),e,x in zip(legs,epx,xpx))*scale
    # Per-second liquidation marks from entry to exit on the base path grid.
    mark=np.zeros(len(grid))
    live=(grid>=entry)&(grid<=exit_second)
    for q,(_,_,_,sign,u),e in zip(quotes,legs,epx):
        j=np.searchsorted(q[0],grid,side='right')-1;ok=j>=0
        liq=np.where(ok,np.where(sign>0,q[1][np.clip(j,0,None)],q[2][np.clip(j,0,None)]),e)
        mark+=np.where(live,sign*u*(liq-e),0.)
    mark=mark*scale-np.where(live,fee,0.)
    net=gross-fee
    mark[grid>=exit_second]=net
    return {'Status':'stale_exit' if stale else 'ok','Entry_second':entry,'Entry_px':epx,'Exit_px':xpx,
        'Gross':gross,'Fee':fee,'Net':net,'Mark':mark,'Strike_atm':float(round(U[np.searchsorted(grid,entry)]/50)*50)}


def one_day(date):
    global ROLL_T,ROLL_CAPTURE,ROLL_RESULT
    began=time.monotonic();ROLL_T=None;ROLL_CAPTURE=None;ROLL_RESULT=None
    original=pickle.loads((base.SOURCE/'days'/(date+'.pkl')).read_bytes());cur=original['New'];base.CURRENT=cur
    tick=Path('/mnt/shared/TickDataIV')/pd.Timestamp(date).strftime('%d%m%Y')/'NIFTY.parquet'
    rows=[]
    try:
        st=tick.stat();assert (st.st_size,str(st.st_mtime_ns))==(original['Tick_size'],original['Tick_mtime_ns'])
        if cur.get('Leg')!='CW':
            for name in CANDIDATES:
                rows.append({'Date':date,'Policy':name,'Leg':cur.get('Leg',''),'Traded':cur.get('Leg') in ('CW','NW'),
                    'Net_PnL':float(cur.get('Net_PnL',0.)),'Base_PnL':float(cur.get('Net_PnL',0.)),'Adjusted':False,'Status':'no_cw_trade'})
            return {'Date':date,'Error':'','Rows':rows,'Seconds':time.monotonic()-began}
        path=pd.read_parquet(STOPS/'paths'/(date+'.parquet'));sig=pd.read_parquet(STOPS/'signals'/(date+'.parquet'))
        grid=path.Second.to_numpy(np.int64);U=path.U.to_numpy(float);base_mark=path.Mark_net.to_numpy(float)
        base_net=float(path.Close_net.iloc[-1]);exit_second=int(grid[-1])
        np.testing.assert_allclose(base_net,cur['Net_PnL'],atol=1e-8,rtol=0)
        first=sig[sig.Confirmed]
        first=first.iloc[0] if len(first) else None
        t_star=int(first.Second) if first is not None else None
        if t_star is not None and t_star>=exit_second:t_star=None
        scale=25000./float(cur['Entry_U'])*(1/3 if cur['Event'] else 1.)
        marks={'Baseline':base_mark.copy()};extra={}
        if t_star is not None:
            # Engine run: re-verifies the baseline, then the roll trade.
            ROLL_T=t_star;log=io.StringIO()
            with contextlib.redirect_stdout(log):
                events={pd.Timestamp(date).date()} if cur['Event'] else set()
                returned=base.ENGINE.worker_v_curve_evaluator(([str(tick)],base.DAILY,base.LOOKUP,events,0,str(OUT/'runtime')))
                if not isinstance(returned,dict):raise RuntimeError(log.getvalue()+str(returned))
            cw,nw=base.ENGINE.load_day_ticks(str(tick),pd.Timestamp(date));cw,nw=prepare(cw),prepare(nw)
            s0=int(np.flatnonzero(grid==t_star)[0]) if np.any(grid==t_star) else None
            K=None
            books={}
            for name in CANDIDATES[1:]:
                if name=='Roll':continue
                pick=None
                for s in range(t_star+2,t_star+6):
                    if np.any(grid==s):pick=s;break
                if pick is None:extra[name]={'Status':'no_entry_quote'};continue
                K=float(round(U[np.searchsorted(grid,pick)]/50)*50)
                K_up=float(round(K*(1+WING_PCT)/50)*50);K_dn=float(round(K*(1-WING_PCT)/50)*50)
                legs=legs_for(name,str(first.Regime),int(first.Direction),K,K_up,K_dn)
                if legs is None:extra[name]={'Status':'not_applicable'};continue
                if any(s=='nw' for s,*_ in legs) and (nw is None or nw.empty):extra[name]={'Status':'no_next_week'};continue
                for s,k,t,_,_ in legs:
                    if (s,k,t) not in books:books[(s,k,t)]=contract(cw if s=='cw' else nw,k,t)
                res=price_overlay(legs,books,t_star,exit_second,grid,U,scale)
                if res['Status']=='error':raise RuntimeError(f'{name}: {res["Error"]}')
                res['Legs']=[(s,k,t,sign,u) for s,k,t,sign,u in legs];extra[name]=res
            # Roll: base prefix cash at t*, then the new engine trade.
            if s0 is not None and ROLL_CAPTURE is not None and ROLL_RESULT is not None:
                rp=ROLL_CAPTURE['Path'];prefix=float(path.Close_net.iloc[s0])
                ev=1/3 if cur['Event'] else 1.;E=base.ENGINE;dte=int(cur['DTE_idx'])
                expect=ROLL_RESULT['gross']*ev-E.ENTRY_SLIP.get(dte,E.ENTRY_SLIP_DEF)-E.HEDGE_SLIP_BPS.get(dte,E.HEDGE_SLIP_DEF)/10000*ROLL_RESULT['hedge_notional']*ev
                np.testing.assert_allclose(rp.Close_net.iloc[-1],expect,atol=1e-8,rtol=0)
                extra['Roll']={'Status':'ok','Prefix_cash':prefix,'Roll_net':float(rp.Close_net.iloc[-1]),
                    'Net':prefix+float(rp.Close_net.iloc[-1]),'Roll_strike':ROLL_CAPTURE['Strike'],
                    'Roll_exit_time':ROLL_RESULT['exit_time'],'Roll_exit_reason':ROLL_RESULT['exit_reason'],
                    'Path':(np.concatenate([grid[:s0+1],rp.Second.to_numpy(np.int64)]),
                            np.concatenate([base_mark[:s0],[prefix],prefix+rp.Mark_net.to_numpy(float)]))}
            else:extra['Roll']={'Status':'no_roll_entry'}
        for name in CANDIDATES:
            r={'Date':date,'Policy':name,'Leg':'CW','Traded':True,'Base_PnL':base_net,'Entry_Time':cur['Entry_Time'],
               'Base_exit_time':base.clock(exit_second),'Trigger_regime':'' if first is None else str(first.Regime),
               'Trigger_direction':0 if first is None else int(first.Direction),'Trigger_time':'' if t_star is None else base.clock(t_star)}
            e=extra.get(name)
            if name=='Baseline' or e is None or e.get('Status') not in ('ok','stale_exit'):
                r.update(Net_PnL=base_net,Adjusted=False,Status='baseline' if name=='Baseline' else (e or {'Status':'no_trigger'})['Status'])
                marks[name]=base_mark
            elif name=='Roll':
                r.update(Net_PnL=e['Net'],Adjusted=True,Status='ok',Adjust_PnL=e['Net']-base_net,Prefix_cash=e['Prefix_cash'],
                    Roll_net=e['Roll_net'],Roll_strike=e['Roll_strike'],Roll_exit_time=e['Roll_exit_time'],Roll_exit_reason=e['Roll_exit_reason'])
                marks[name]=e['Path']
            else:
                r.update(Net_PnL=base_net+e['Net'],Adjusted=True,Status=e['Status'],Adjust_PnL=e['Net'],Adjust_gross=e['Gross'],
                    Adjust_fee=e['Fee'],Adjust_entry_time=base.clock(e['Entry_second']),ATM=e['Strike_atm'],
                    Legs=json.dumps(e['Legs']),Entry_px=json.dumps(e['Entry_px']),Exit_px=json.dumps(e['Exit_px']))
                marks[name]=base_mark+e['Mark']
            rows.append(r)
        # Intraday marks per candidate (grid, mark) for the global DD.
        arrays={}
        for name in CANDIDATES:
            m=marks[name]
            if isinstance(m,tuple):arrays[name+'_t'],arrays[name+'_m']=m[0],m[1].astype(np.float64)
            else:arrays[name+'_t'],arrays[name+'_m']=grid,np.asarray(m,np.float64)
        dest=OUT/'marks'/(date+'.npz');tmp=OUT/'marks'/(date+'.tmp.npz');np.savez_compressed(tmp,**arrays);tmp.replace(dest)
        after=tick.stat();assert (st.st_size,st.st_mtime_ns)==(after.st_size,after.st_mtime_ns)
        return {'Date':date,'Error':'','Rows':rows,'Seconds':time.monotonic()-began,'Marks_SHA256':hashlib.sha256(dest.read_bytes()).hexdigest()}
    except Exception:return {'Date':date,'Error':traceback.format_exc(),'Seconds':time.monotonic()-began}
    finally:base.CURRENT=None;ROLL_T=None;ROLL_CAPTURE=None;ROLL_RESULT=None;gc.collect()


def main():
    parser=argparse.ArgumentParser();parser.add_argument('--pilot',action='store_true');parser.add_argument('--workers',type=int,default=12)
    args=parser.parse_args()
    for name in ('days','pilot_days','marks','runtime'):(OUT/name).mkdir(parents=True,exist_ok=True)
    stops=json.loads((STOPS/'run_manifest.json').read_text());assert stops['Status']=='complete' and not stops['Failed_dates']
    inputs=[Path(__file__),ROOT/'gs_intraday_replay.py',ROOT/'GS_ADJUSTMENT_OVERLAY_SPEC.md',STOPS/'run_manifest.json']
    hashes={str(p.relative_to(ROOT)):hashlib.sha256(p.read_bytes()).hexdigest() for p in inputs}
    dates=stops['Dates']
    if args.pilot:
        rows=pd.DataFrame([pickle.loads((base.SOURCE/'days'/(d+'.pkl')).read_bytes())['New'] for d in dates])
        cw=rows[rows.Leg.eq('CW')]
        dates=list(dict.fromkeys([cw[cw.Stage.eq('09:16')].Date.iloc[0],cw[cw.Stage.eq('09:20+')].Date.iloc[0],
            cw[cw.Event.eq(True)].Date.iloc[0],cw[cw.DTE_idx.eq(0)].Date.iloc[0],cw[cw.DTE_idx.eq(1)].Date.iloc[0],
            rows[rows.Leg.eq('NW')].Date.iloc[0],cw.sort_values('Net_PnL').Date.iloc[0],'2021-01-01']))
    dest=OUT/('pilot_days' if args.pilot else 'days');manifest=OUT/('pilot_manifest.json' if args.pilot else 'run_manifest.json')
    if manifest.exists():assert json.loads(manifest.read_text())['Inputs']==hashes,'Inputs changed; use a new output directory'
    data={'Status':'running','Inputs':hashes,'Dates':dates,'Workers':args.workers,'Scope':'Fixed adjustment overlays on source-model CW paths; not causal execution'}
    manifest.write_text(json.dumps(data,indent=2)+'\n')
    todo=[d for d in dates if not (dest/(d+'.json')).exists() or json.loads((dest/(d+'.json')).read_text()).get('Error')]
    print('Adjustments',len(dates),'dates; remaining',len(todo),flush=True)
    with ProcessPoolExecutor(max_workers=args.workers,mp_context=multiprocessing.get_context('fork'),initializer=initialize) as pool:
        futures={pool.submit(one_day,date):date for date in todo}
        for i,future in enumerate(as_completed(futures),1):
            row=future.result();p=dest/(row['Date']+'.json');tmp=p.with_suffix('.tmp');tmp.write_text(json.dumps(row,indent=2,default=float)+'\n');tmp.replace(p)
            if args.pilot or i%25==0 or row['Error']:print(i,'/',len(todo),row['Date'],'seconds',round(row['Seconds'],1),'error',row['Error'][-300:],flush=True)
    failed=[d for d in dates if json.loads((dest/(d+'.json')).read_text()).get('Error')]
    data.update(Status='errors' if failed else 'complete',Failed_dates=failed);manifest.write_text(json.dumps(data,indent=2)+'\n')
    print('Finished; failures:',failed,flush=True)
    if failed:raise SystemExit(1)


if __name__=='__main__':main()
