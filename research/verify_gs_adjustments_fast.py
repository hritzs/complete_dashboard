"""Independent check of the adjustment study: re-price every added leg from raw ticks.

Own code only (no engine, no study helpers): expiries from the raw file,
quotes per exact second (last row in that second, bid/ask > 0), the spec's
execution windows, units, fees and the 4.24 prefix cash for the roll.
"""
import hashlib,json,pickle,traceback
from concurrent.futures import ThreadPoolExecutor,as_completed
from pathlib import Path
import numpy as np
import pandas as pd
import pyarrow.parquet as pq

ROOT=Path(__file__).resolve().parent
OUT=ROOT/'output/gs_adjustments';STOPS=ROOT/'output/gs_intraday_stops';SOURCE=ROOT/'output/logic_comparison_20261021'
FEE=.25;UNITS={'Wings':(1.,1.),'BuyTested':(.5,),'SellUntested':(.5,),'NWLongStraddle':(.5,.5),'SellMore':(.5,.5)}


def secs(text):
    h,m,s=map(int,text.split(':'));return h*3600+m*60+s


def quotes(table,expiry,K,T):
    q=table[(table.Expiry==expiry)&(table.Strike==K)&(table.Type==T)&(table.Bid>0)&(table.Ask>0)]
    q=q.groupby('S').last();return q.index.to_numpy(np.int64),q.Bid.to_numpy(float),q.Ask.to_numpy(float)


def wanted_legs(name,K,up):
    return {'Wings':[('cw',float(round(K*1.02/50)*50),'CE',1,1.),('cw',float(round(K*.98/50)*50),'PE',1,1.)],
        'NWLongStraddle':[('nw',K,'CE',1,.5),('nw',K,'PE',1,.5)],
        'BuyTested':[('cw',K,'CE' if up else 'PE',1,.5)],'SellUntested':[('cw',K,'PE' if up else 'CE',-1,.5)],
        'SellMore':[('cw',K,'CE',-1,.5),('cw',K,'PE',-1,.5)]}[name]


def verify_date(date):
    checked=legs_checked=rolls=days=0;mismatch=[];deviations=[]
    rec=json.loads((OUT/'days'/(date+'.json')).read_text());assert not rec['Error'];days+=1
    saved=pickle.loads((SOURCE/'days'/(date+'.pkl')).read_bytes());base=saved['New']
    rows={r['Policy']:r for r in rec['Rows']}
    assert abs(rows['Baseline']['Net_PnL']-float(base.get('Net_PnL',0.)))<1e-8
    adjusted=[r for r in rec['Rows'] if r.get('Adjusted')]
    if not adjusted:
        for r in rec['Rows']:assert abs(r['Net_PnL']-float(base.get('Net_PnL',0.)))<1e-8
        return checked,legs_checked,rolls,days,mismatch,deviations
    path=pd.read_parquet(STOPS/'paths'/(date+'.parquet'));sig=pd.read_parquet(STOPS/'signals'/(date+'.parquet'))
    grid=path.Second.to_numpy(np.int64);U=path.U.to_numpy(float);first=sig[sig.Confirmed].iloc[0]
    t_star=int(first.Second);exit_s=int(grid[-1]);assert t_star<exit_s
    ev=1/3 if base['Event'] else 1.;scale=25000./float(base['Entry_U'])*ev
    tick_path=Path('/mnt/shared/TickDataIV')/pd.Timestamp(date).strftime('%d%m%Y')/'NIFTY.parquet'
    before=tick_path.stat();pf=pq.ParquetFile(tick_path)
    assert (before.st_size,str(before.st_mtime_ns))==(saved['Tick_size'],saved['Tick_mtime_ns']),'Source tick vintage changed'
    time_col='Time_dt' if 'Time_dt' in pf.schema_arrow.names else 'Time'
    col=pf.schema_arrow.names.index(time_col)
    anchor=pd.Timestamp(min(pf.metadata.row_group(i).column(col).statistics.min for i in range(pf.metadata.num_row_groups))).normalize()
    ex=pq.read_table(tick_path,columns=['Expiry'],filters=[('Type','in',['CE','PE'])]).column(0).unique().to_pylist()
    exps=sorted(pd.Timestamp(e).normalize() for e in ex if e is not None and pd.Timestamp(e).normalize()>=pd.Timestamp(date))
    filters=[]
    for lo,hi in ((t_star+2,t_star+5),(exit_s-60,exit_s+5)):
        filters.append([(time_col,'>=',anchor+pd.Timedelta(seconds=lo)),(time_col,'<=',anchor+pd.Timedelta(seconds=hi)),('Type','in',['CE','PE'])])
    tick=pq.read_table(tick_path,columns=[time_col,'Expiry','Strike','Type','Bid','Ask'],filters=filters).to_pandas()
    t=pd.to_datetime(tick[time_col]);tick['S']=t.dt.hour*3600+t.dt.minute*60+t.dt.second
    tick['Type']=tick.Type.astype(str);tick['Expiry']=pd.to_datetime(tick.Expiry).dt.normalize()
    for r in adjusted:
        name=r['Policy'];checked+=1
        if name=='Roll':
            rolls+=1;s0=int(np.flatnonzero(grid==t_star)[0])
            if abs(r['Prefix_cash']-float(path.Close_net.iloc[s0]))>1e-8 or abs(r['Net_PnL']-r['Prefix_cash']-r['Roll_net'])>1e-8:mismatch.append((date,name,'roll'))
            continue
        legs=json.loads(r['Legs']);entry=secs(r['Adjust_entry_time'])
        K=float(round(U[np.searchsorted(grid,entry)]/50)*50);assert r['ATM']==K
        # Reconcile actual implementation AND separately retain strict spec failures.
        up=int(first.Direction)>0;rg=str(first.Regime)
        pick=next(s for s in range(t_star+2,t_star+6) if np.any(grid==s))
        selected_K=float(round(U[np.searchsorted(grid,pick)]/50)*50)
        actual=[tuple(l) for l in legs]
        assert actual==wanted_legs(name,selected_K,up),(date,name,'actual implementation contract mismatch')
        if actual!=wanted_legs(name,K,up):
            deviations.append({'Date':date,'Policy':name,'Selection_time':f'{pick//3600:02d}:{pick%3600//60:02d}:{pick%60:02d}',
                'Fill_time':r['Adjust_entry_time'],'Selected_ATM':selected_K,'Fill_ATM':K,
                'Actual_legs':json.dumps(legs),'Specification_status':'FAIL: strike fixed before actual fill ATM'})
        assert rg=='Directional_expansion' or name not in ('BuyTested','SellUntested')
        assert rg=='Volatile_whipsaw' or name!='SellMore'
        qs=[quotes(tick,exps[0 if s=='cw' else 1],k,T) for s,k,T,_,_ in legs]
        first_ok=next(s for s in range(t_star+2,t_star+6) if all(np.any(q[0]==s) for q in qs))
        assert first_ok==entry,(date,name)
        def px(q,s,side):j=int(np.flatnonzero(q[0]==s)[0]);return q[2][j] if side>0 else q[1][j]
        epx=[px(q,entry,sg) for q,(_,_,_,sg,_) in zip(qs,legs)]
        close=next((s for s in range(exit_s,exit_s+6) if all(np.any(q[0]==s) for q in qs)),None)
        if close is not None:xpx=[px(q,close,-sg) for q,(_,_,_,sg,_) in zip(qs,legs)]
        else:
            xpx=[]
            for q,(_,_,_,sg,_) in zip(qs,legs):
                j=int(np.searchsorted(q[0],exit_s,side='right'))-1;assert j>=0 and exit_s-q[0][j]<=60
                xpx.append(q[1][j] if sg>0 else q[2][j])
        units=sum(u for *_,u in legs)
        adj=sum(sg*u*(x-e) for (_,_,_,sg,u),e,x in zip(legs,epx,xpx))*scale-FEE*units*ev
        legs_checked+=len(legs)
        if abs(adj-r['Adjust_PnL'])>1e-8 or abs(r['Net_PnL']-r['Base_PnL']-adj)>1e-8 or abs(r['Base_PnL']-base['Net_PnL'])>1e-8:
            mismatch.append((date,name,adj,r['Adjust_PnL']))
    after=tick_path.stat()
    assert (before.st_size,before.st_mtime_ns)==(after.st_size,after.st_mtime_ns),'Tick file changed during verification'
    return checked,legs_checked,rolls,days,mismatch,deviations


def main():
    run=json.loads((OUT/'run_manifest.json').read_text());assert run['Status']=='complete' and not run['Failed_dates']
    for name,digest in run['Inputs'].items():assert hashlib.sha256((ROOT/name).read_bytes()).hexdigest()==digest,name
    checked=legs=rolls=days=0;bad=[];deviations=[]
    with ThreadPoolExecutor(max_workers=12) as pool:
        futures={pool.submit(verify_date,date):date for date in run['Dates']}
        for i,future in enumerate(as_completed(futures),1):
            try:
                c,l,r,d,m,v=future.result();checked+=c;legs+=l;rolls+=r;days+=d;bad+=m;deviations+=v
            except Exception:bad.append((futures[future],traceback.format_exc()))
            if i%50==0:print(i,'/',len(run['Dates']),'failures',len(bad),flush=True)
    result={'Status':'FAIL' if bad else 'PASS_ACCOUNTING_SPEC_DEVIATIONS' if deviations else 'PASS_ACCOUNTING',
        'Accounting_status':'FAIL' if bad else 'PASS','Specification_status':'FAIL' if bad or deviations else 'PASS_DECLARED_RULES_ONLY',
        'Execution_certified':False,'Dates':days,'Adjusted_rows_checked':checked,'Added_legs_repriced':legs,
        'Rolls_checked':rolls,'Mismatches':bad,'Spec_deviations':sorted(deviations,key=lambda x:(x['Date'],x['Policy'])),
        'Verifier_SHA256':hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
        'Limits':'Exact raw entry/exit quote-window repricing and roll prefix identity; legacy base/roll fills, stale-exit convention, added-leg timing and portfolio-hedge limitations remain. No live validation.'}
    (OUT/'verification.json').write_text(json.dumps(result,indent=2,default=str)+'\n')
    pd.DataFrame(result['Spec_deviations']).to_csv(OUT/'specification_deviations.csv',index=False)
    print(json.dumps(result,indent=2,default=str),flush=True)
    if bad:raise SystemExit(1)


if __name__=='__main__':main()
