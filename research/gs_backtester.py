"""GS parameter backtester: give settings, get the whole backtest.

    research/.venv/bin/python research/gs_backtester.py --config research/backtests/example.json

Runs every listed setup on the REAL tick data through the UNCHANGED frozen
latest-GS engine (same entries as the frozen book: the LUT entry decision
does not depend on these settings), re-simulating each current-week trade
with the setup's exits / hedging, optional adjustment and sizing. NW trades
keep their frozen result. A setup with no overrides must reproduce the frozen
book exactly; that is asserted on every day it runs.

Config (JSON):
{
  "name": "my_test",                       # output folder research/output/backtests/<name>
  "from": "2021-01-01", "to": "2026-10-12",
  "workers": 12,
  "setups": [
    {"name": "Current"},                    # defaults = frozen engine
    {"name": "SL x1.25", "sl_mult": 1.25},
    {"name": "Tight band", "hedge": {"hedge_div": 70, "str_div": 5}},
    {"name": "Wings", "adjustment": "Wings"},
    {"name": "Half size DTE0", "sizing": {"by_dte": {"0": 0.5}}}
  ]
}

Setup keys (all optional):
  sl_bps / sl_mult            stop (absolute bps of entry underlying, or x the trade's own)
  tp_bps / tp_mult / tp_off   take profit
  tv_thresh, tv4_thresh, iv_drop_thresh   the engine's theta-value / IV-drop exits (0 = off)
  exit_time "HH:MM"           timed exit (default: the engine's grid end)
  hedge: hedge_div, str_div, pts_floor, hedge_frac, intra_mult (0 = no intra-minute),
         intra_frac, delta_thresh {"0": .25, "1": .2}
  adjustment: Wings | Roll | BuyTested | SellUntested | NWLongStraddle | SellMore
              (study-22 rules, at the first confirmed regime signal of THIS setup's path)
  sizing: {"fixed": 1.0, "by_dte": {"0": .5}, "by_stage": {"09:16": 1.0},
           "event": 1.0, "file": "dates.csv"}   (file: Date,Multiplier; factors multiply)
Results are research: the engine's legacy fill model (30-s / 3-s execution
averages, backfills) applies, as in every study; not causal execution.
"""
import os
for key in ('NUMBA_NUM_THREADS','OMP_NUM_THREADS','OPENBLAS_NUM_THREADS','MKL_NUM_THREADS'):os.environ[key]='1'
import argparse,contextlib,gc,hashlib,io,json,math,multiprocessing,pickle,time,traceback
from concurrent.futures import ProcessPoolExecutor,as_completed
from pathlib import Path
import numpy as np
import pandas as pd
import gs_intraday_replay as base
import gs_adjustment_overlays as adj

ROOT=Path(__file__).resolve().parent
TICKS=Path('/mnt/shared/TickDataIV')
CFG=None;OUT=None;MODE='verify';CAP=None;DAYRES=None
HEDGE_KEYS={'hedge_div':'PARAM_HEDGE_DIV','str_div':'PARAM_STR_D','pts_floor':'PARAM_PTS_FLOOR',
    'hedge_frac':'PARAM_HEDGE_FRAC','intra_mult':'PARAM_INTRA_MULT','intra_frac':'PARAM_INTRA_FRAC'}
ADJUSTMENTS=('Wings','Roll','BuyTested','SellUntested','NWLongStraddle','SellMore')


def is_default(s):
    return not any(k in s for k in ('sl_bps','sl_mult','tp_bps','tp_mult','tp_off','tv_thresh','tv4_thresh','iv_drop_thresh',
        'exit_time','hedge','adjustment')) and not s.get('sizing')


def capture(local):
    """Frozen-book check (verify) or this setup's own per-second path."""
    global CAP
    if MODE=='verify':return _orig_capture(local)
    x=local;cut=x['cut'];a=x['sec_abs'][:cut+1];arrays=x['arrays'];cur=base.CURRENT;E=base.ENGINE
    ev=1/3 if cur['Event'] else 1.;scale=E.PNL_SCALE/x['entry_underlying']*ev;dte=int(cur['DTE_idx'])
    rate=E.HEDGE_SLIP_BPS.get(dte,E.HEDGE_SLIP_DEF)/10000
    mark=(x['ev']-arrays[5][a]-arrays[6][a]+x['fut_sec'][:cut+1])*scale
    force=(x['ev']-arrays[9][a]-arrays[10][a]+x['fut_sec'][:cut+1])*scale
    cost=E.ENTRY_SLIP.get(dte,E.ENTRY_SLIP_DEF)+rate*x['hn_sec'][:cut+1]*scale
    frame=pd.DataFrame({'Second':base.seconds(x['idx_base'].Time.iloc[a]),'U':x['S'][:cut+1],'Mark_net':mark-cost,'Close_net':force-cost})
    frame.loc[len(frame)-1,'Mark_net']=frame.Close_net.iloc[-1]
    CAP={'Path':frame,'Feature_seconds':base.seconds(x['idx_base'].Time),'Feature_U':x['idx_base'].Underlying.to_numpy(float),
        'Strike':float(x['idx_base'].ATMRef.iloc[a[0]])}


def run_selected(local):
    """Inside the engine worker: verify the frozen trade, then simulate every setup."""
    global MODE,DAYRES
    out=_orig_run_selected(local)
    idx=local['idx_base'];cache=local['strike_data_cache'];cur=base.CURRENT;E=base.ENGINE
    entry=int(np.flatnonzero(idx.Time.dt.strftime('%H:%M').eq(cur['Entry_Time']).to_numpy())[0])
    saved={v:getattr(E,v) for v in HEDGE_KEYS.values()};saved['DELTA_THRESH']=dict(E.DELTA_THRESH)
    res={}
    try:
        for s in CFG['setups']:
            for k,v in (s.get('hedge') or {}).items():
                if k=='delta_thresh':E.DELTA_THRESH={int(a):float(b) for a,b in v.items()}
                elif k in HEDGE_KEYS:setattr(E,HEDGE_KEYS[k],float(v))
                else:raise ValueError(f'unknown hedge key {k}')
            sl=float(s.get('sl_bps',cur['SL_bps']*s.get('sl_mult',1.)));tp=float(s.get('tp_bps',cur['TP_bps']*s.get('tp_mult',1.)))
            if s.get('tp_off'):tp=1e9
            exit_idx=len(idx)-1
            if s.get('exit_time'):
                h,m=map(int,s['exit_time'].split(':'));t=base.seconds(idx.Time);exit_idx=min(exit_idx,int(np.searchsorted(t,h*3600+m*60,side='right'))-1)
            MODE='setup'
            r=E.simulate_trade(idx,entry,exit_idx,cache,sl,tp,float(s.get('tv_thresh',E.TV_THRESH)),float(s.get('tv4_thresh',E.TV4_THRESH)),
                float(s.get('iv_drop_thresh',E.IV_DROP_THRESH)),cur['Date'],int(cur['DTE_idx']))
            MODE='verify'
            for k,v in saved.items():setattr(E,k,v if k!='DELTA_THRESH' else dict(v))
            if r is None:raise RuntimeError(f"{s['name']}: engine returned no trade")
            item={'r':r,'cap':CAP,'sl':sl,'tp':tp}
            if s.get('adjustment')=='Roll':
                sig=signal(CAP)
                if sig is not None and sig[0]<int(CAP['Path'].Second.iloc[-1]):
                    t2=int(np.searchsorted(base.seconds(idx.Time),sig[0],side='left'))
                    if t2<len(idx)-1:
                        MODE='setup';base_cap=CAP
                        r2=E.simulate_trade(idx,t2,len(idx)-1,cache,sl,tp,E.TV_THRESH,E.TV4_THRESH,E.IV_DROP_THRESH,cur['Date'],int(cur['DTE_idx']))
                        MODE='verify';item.update(roll=r2,roll_cap=CAP,roll_t=sig[0]);CAP=base_cap
            res[s['name']]=item
    finally:
        MODE='verify'
        for k,v in saved.items():setattr(E,k,v if k!='DELTA_THRESH' else dict(v))
    DAYRES=res
    return out


_orig_run_selected=base.run_selected;_orig_capture=base.capture_cw


def initialize(config):
    global CFG
    CFG=config;base.run_selected=run_selected;base.capture_cw=capture;base.initialize()


def signal(cap):
    """First confirmed regime signal on this setup's own path (4.24 detector)."""
    frame=cap['Path'];prev=None
    for sec in frame.Second[frame.Second%60==0]:
        label,direction,_,_=base.regime_signal(cap['Feature_seconds'],cap['Feature_U'],sec)
        minute=int(sec)//60
        if label in ('Directional_expansion','Volatile_whipsaw') and prev==(label,direction,minute-1):return int(sec),label,direction
        prev=(label,direction,minute)
    return None


def size_of(s,cur,date):
    z=s.get('sizing') or {};m=float(z.get('fixed',1.))
    m*=float((z.get('by_dte') or {}).get(str(int(cur['DTE_idx'])),1.))
    m*=float((z.get('by_stage') or {}).get(str(cur.get('Stage','')),1.))
    if cur.get('Event'):m*=float(z.get('event',1.))
    if z.get('file'):
        t=pd.read_csv(ROOT/z['file'] if not Path(z['file']).is_absolute() else z['file'])
        hit=t[t.Date.astype(str).str[:10]==date];m*=float(hit.Multiplier.iloc[0]) if len(hit) else 1.
    return m


def one_day(date):
    global DAYRES
    began=time.monotonic();DAYRES=None
    original=pickle.loads((base.SOURCE/'days'/(date+'.pkl')).read_bytes());cur=original['New'];base.CURRENT=cur
    tick=TICKS/pd.Timestamp(date).strftime('%d%m%Y')/'NIFTY.parquet'
    rows=[]
    try:
        if cur.get('Leg')!='CW':
            for s in CFG['setups']:
                net=float(cur.get('Net_PnL',0.))*(size_of(s,cur,date) if cur.get('Leg')=='NW' else 1.)
                rows.append({'Date':date,'Setup':s['name'],'Leg':cur.get('Leg',''),'Traded':cur.get('Leg')=='NW','Net_PnL':net,
                    'Frozen_PnL':float(cur.get('Net_PnL',0.)),'Exit_Reason':cur.get('Exit_Reason',''),'Exit_Time':cur.get('Exit_Time',''),
                    'Path_low':min(0.,net),'Path_high':max(0.,net),'Day_local_MDD':max(0.,-net)})
            return {'Date':date,'Error':'','Rows':rows,'Seconds':time.monotonic()-began}
        log=io.StringIO()
        with contextlib.redirect_stdout(log):
            events={pd.Timestamp(date).date()} if cur['Event'] else set()
            got=base.ENGINE.worker_v_curve_evaluator(([str(tick)],base.DAILY,base.LOOKUP,events,0,str(OUT/'runtime')))
            if not isinstance(got,dict):raise RuntimeError(log.getvalue()+str(got))
        assert DAYRES is not None
        books=None;E=base.ENGINE;ev=1/3 if cur['Event'] else 1.;dte=int(cur['DTE_idx'])
        for s in CFG['setups']:
            it=DAYRES[s['name']];r=it['r'];cap=it['cap'];path=cap['Path']
            net=r['gross']*ev-E.ENTRY_SLIP.get(dte,E.ENTRY_SLIP_DEF)-E.HEDGE_SLIP_BPS.get(dte,E.HEDGE_SLIP_DEF)/10000*r['hedge_notional']*ev
            np.testing.assert_allclose(path.Close_net.iloc[-1],net,atol=1e-8,rtol=0)
            if is_default(s):np.testing.assert_allclose(net,cur['Net_PnL'],atol=1e-8,rtol=0)   # reproduces the frozen book
            mark=path.Mark_net.to_numpy(float);row={'Date':date,'Setup':s['name'],'Leg':'CW','Traded':True,'Frozen_PnL':float(cur['Net_PnL']),
                'Entry_Time':cur['Entry_Time'],'Strike':r['strike'],'SL_bps':it['sl'],'TP_bps':it['tp'] if it['tp']<1e8 else np.nan,
                'Exit_Time':r['exit_time'],'Exit_Reason':r['exit_reason'],'Min_hedges':r['min_hedges'],'Intra_hedges':r['intra_hedges'],
                'Trade_PnL':net,'Adjustment':'','Adjust_PnL':0.}
            a=s.get('adjustment')
            if a:
                if a not in ADJUSTMENTS:raise ValueError(f'unknown adjustment {a}')
                sig=signal(cap);grid=path.Second.to_numpy(np.int64);exit_s=int(grid[-1])
                if sig is not None and sig[0]<exit_s:
                    t_star,label,direction=sig;row.update(Trigger_time=base.clock(t_star),Trigger_regime=label)
                    if a=='Roll' and 'roll' in it and it['roll'] is not None:
                        rp=it['roll_cap']['Path'];r2=it['roll'];s0=int(np.flatnonzero(grid==t_star)[0]);prefix=float(path.Close_net.iloc[s0])
                        roll_net=r2['gross']*ev-E.ENTRY_SLIP.get(dte,E.ENTRY_SLIP_DEF)-E.HEDGE_SLIP_BPS.get(dte,E.HEDGE_SLIP_DEF)/10000*r2['hedge_notional']*ev
                        np.testing.assert_allclose(rp.Close_net.iloc[-1],roll_net,atol=1e-8,rtol=0)
                        mark=np.concatenate([mark[:s0],[prefix],prefix+rp.Mark_net.to_numpy(float)])
                        row.update(Adjustment='Roll',Adjust_PnL=prefix+roll_net-net,Exit_Time=r2['exit_time'],Exit_Reason='Roll '+r2['exit_reason']);net=prefix+roll_net
                    elif a!='Roll':
                        if books is None:
                            with contextlib.redirect_stdout(io.StringIO()):cw,nw=E.load_day_ticks(str(tick),pd.Timestamp(date))
                            cw,nw=adj.prepare(cw),adj.prepare(nw);books={}
                        pick=next((x for x in range(t_star+2,t_star+6) if np.any(grid==x)),None)
                        if pick is not None:
                            U=path.U.to_numpy(float);K=float(round(U[np.searchsorted(grid,pick)]/50)*50)
                            legs=adj.legs_for(a,label,direction,K,float(round(K*1.02/50)*50),float(round(K*.98/50)*50))
                            if legs and not (any(x=='nw' for x,*_ in legs) and (nw is None or nw.empty)):
                                for x,k,t,_,_ in legs:
                                    if (x,k,t) not in books:books[(x,k,t)]=adj.contract(cw if x=='cw' else nw,k,t)
                                p=adj.price_overlay(legs,books,t_star,exit_s,grid,U,25000./float(cur['Entry_U'])*ev)
                                if p['Status']=='error':raise RuntimeError(p['Error'])
                                if p['Status'] in ('ok','stale_exit'):
                                    mark=mark+p['Mark'];row.update(Adjustment=a,Adjust_PnL=p['Net']);net+=p['Net']
            m=size_of(s,cur,date);net*=m;mark=mark*m
            eq=np.r_[0.,mark];eq[-1]=net;draw=np.maximum.accumulate(eq)-eq
            row.update(Size=m,Net_PnL=net,Path_low=float(eq.min()),Path_high=float(eq.max()),Day_local_MDD=float(draw.max()))
            rows.append(row)
        return {'Date':date,'Error':'','Rows':rows,'Seconds':time.monotonic()-began}
    except Exception:return {'Date':date,'Error':traceback.format_exc()[-1500:],'Seconds':time.monotonic()-began}
    finally:base.CURRENT=None;DAYRES=None;gc.collect()


def drawdown(p):
    e=np.cumsum(p);h=np.maximum.accumulate(np.r_[0.,e])[1:];return e,h-e


def intraday_dd(net,lo,hi,local):
    equity=peak=worst=0.
    for p,l,h,d in zip(net,lo,hi,local):
        worst=max(worst,d,peak-(equity+l));peak=max(peak,equity+h);equity+=p
    return worst


def metrics(f):
    p=f.Net_PnL.to_numpy(float);e,dd=drawdown(p);tr=f[f.Traded];cw=tr[tr.Leg.eq('CW')]
    return {'Net':float(p.sum()),'Max_daily_DD':float(dd.max()) if len(dd) else 0.,
        'Max_intraday_DD':intraday_dd(p,f.Path_low,f.Path_high,f.Day_local_MDD),'Worst_day':float(p.min()) if len(p) else 0.,
        'Trades':int(len(tr)),'Win_pct':float(100*(tr.Net_PnL>0).mean()) if len(tr) else np.nan,
        'Avg_trade':float(tr.Net_PnL.mean()) if len(tr) else np.nan,'SL_exits':int(cw.Exit_Reason.astype(str).str.contains('SL').sum()),
        'Adjusted':int((cw.get('Adjustment',pd.Series(dtype=str)).fillna('')!='').sum()),
        'Net_per_DD':float(p.sum()/dd.max()) if len(dd) and dd.max()>0 else np.nan}


def report(cfg,rows,dates):
    df=pd.DataFrame(rows);df['Traded']=df.Traded.astype(bool);d=pd.to_datetime(df.Date)
    df['Year']=d.dt.year.astype(str);df['FY']=[f'FY{x.year if x.month>=4 else x.year-1}-{(x.year if x.month>=4 else x.year-1)+1}' for x in d]
    names=[s['name'] for s in cfg['setups']];summary=[];periods=[]
    for n in names:
        f=df[df.Setup.eq(n)].sort_values('Date');summary.append({'Setup':n,**metrics(f)})
        for g in ('Year','FY'):
            for lab,sub in f.groupby(g):periods.append({'Setup':n,'Grouping':g,'Period':lab,**metrics(sub)})
    summary=pd.DataFrame(summary);base_row=summary.iloc[0]
    summary['Delta_net_vs_first']=summary.Net-base_row.Net;summary['Delta_daily_DD_vs_first']=summary.Max_daily_DD-base_row.Max_daily_DD
    summary['More_profit_and_less_DD']=(summary.Net>base_row.Net)&(summary.Max_daily_DD<base_row.Max_daily_DD)
    daily=df.pivot(index='Date',columns='Setup',values='Net_PnL')[names].reset_index()
    with pd.ExcelWriter(OUT/'backtest.xlsx') as w:
        for name,t in (('Summary',summary),('Periods',pd.DataFrame(periods)),('Daily_PnL',daily),('Trades',df[df.Traded]),
                       ('Config',pd.DataFrame({'Config':[json.dumps(cfg,indent=1)]}))):
            t.to_excel(w,sheet_name=name,index=False)
    summary.to_csv(OUT/'summary.csv',index=False);df.to_csv(OUT/'all_days.csv',index=False)
    try:
        import matplotlib;matplotlib.use('Agg');import matplotlib.pyplot as plt
        fig,ax=plt.subplots(2,1,figsize=(13,8),sharex=True,layout='constrained')
        for n in names:
            f=df[df.Setup.eq(n)].sort_values('Date');e,dd=drawdown(f.Net_PnL.to_numpy(float));x=pd.to_datetime(f.Date)
            ax[0].plot(x,e,lw=1,label=n);ax[1].plot(x,dd,lw=1)
        ax[0].legend(fontsize=8,ncol=3);ax[0].set_ylabel('Cumulative net');ax[1].set_ylabel('Daily DD');fig.savefig(OUT/'equity_dd.png',dpi=130);plt.close(fig)
    except Exception as e:print('chart skipped:',e)
    lines=[f"# Backtest: {cfg['name']}",'',f"{cfg['from']} .. {cfg['to']}, {len(dates)} days. Real ticks, unchanged GS engine (legacy fill model). First setup is the reference.",'',
        '| Setup | Net | Daily DD | Intraday DD | Worst day | Trades | Win % | More profit & less DD |','|---|---:|---:|---:|---:|---:|---:|:---:|']
    for r in summary.itertuples():
        lines.append(f'| {r.Setup} | {r.Net:.1f} | {r.Max_daily_DD:.1f} | {r.Max_intraday_DD:.1f} | {r.Worst_day:.1f} | {r.Trades} | {r.Win_pct:.1f} | {"yes" if r.More_profit_and_less_DD else ""} |')
    lines+=['','![Equity and DD](equity_dd.png)','','Historical backtest on one path: choosing the best of many setups here overfits; confirm on later dates before use.']
    (OUT/'REPORT.md').write_text('\n'.join(lines)+'\n');print('\n'.join(lines[4:4+2+len(summary)]))


def main():
    global OUT
    ap=argparse.ArgumentParser();ap.add_argument('--config',required=True);a=ap.parse_args()
    cfg=json.loads(Path(a.config).read_text());names=[s['name'] for s in cfg['setups']]
    assert cfg['setups'] and len(set(names))==len(names),'setups need unique names'
    OUT=ROOT/'output/backtests'/cfg['name'];(OUT/'days').mkdir(parents=True,exist_ok=True);(OUT/'runtime').mkdir(exist_ok=True)
    stamp=hashlib.sha256((json.dumps(cfg,sort_keys=True)+Path(__file__).read_text()).encode()).hexdigest()[:16]
    run=json.loads((base.SOURCE/'run_manifest.json').read_text())
    dates=[d for d in run['Dates'] if cfg.get('from','0000')<=d<=cfg.get('to','9999')]
    done={}
    for p in (OUT/'days').glob('*.json'):
        r=json.loads(p.read_text())
        if r.get('Stamp')==stamp and not r['Error']:done[r['Date']]=r
    todo=[d for d in dates if d not in done];print(f"{cfg['name']}: {len(dates)} days, {len(todo)} to run, {len(names)} setups",flush=True)
    globals()['OUT']=OUT
    with ProcessPoolExecutor(max_workers=int(cfg.get('workers',12)),mp_context=multiprocessing.get_context('fork'),initializer=initialize,initargs=(cfg,)) as pool:
        futs=[pool.submit(one_day,d) for d in todo]
        for i,f in enumerate(as_completed(futs),1):
            r=f.result();r['Stamp']=stamp;p=OUT/'days'/(r['Date']+'.json');tmp=p.with_suffix('.tmp');tmp.write_text(json.dumps(r,default=float));tmp.replace(p)
            done[r['Date']]=r
            if r['Error'] or i%50==0:print(i,'/',len(todo),r['Date'],round(r['Seconds'],1),'s',('ERROR '+r['Error'][-300:]) if r['Error'] else '',flush=True)
    failed=[d for d in dates if done[d]['Error']]
    if failed:print('failed days:',failed[:20]);raise SystemExit(1)
    report(cfg,[x for d in dates for x in done[d]['Rows']],dates)


if __name__=='__main__':main()
