"""Independent check of the adjustment study: re-price every added leg from raw ticks.

Own code only (no engine, no study helpers): expiries from the raw file,
quotes per exact second (last row in that second, bid/ask > 0), the spec's
execution windows, units, fees and the 4.24 prefix cash for the roll.
"""
import hashlib,json,pickle
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


def main():
    run=json.loads((OUT/'run_manifest.json').read_text());assert run['Status']=='complete'
    checked=legs_checked=rolls=days=0;mismatch=[]
    for date in run['Dates']:
        rec=json.loads((OUT/'days'/(date+'.json')).read_text());assert not rec['Error'];days+=1
        base=pickle.loads((SOURCE/'days'/(date+'.pkl')).read_bytes())['New']
        rows={r['Policy']:r for r in rec['Rows']}
        assert abs(rows['Baseline']['Net_PnL']-float(base.get('Net_PnL',0.)))<1e-8
        adjusted=[r for r in rec['Rows'] if r.get('Adjusted')]
        if not adjusted:
            for r in rec['Rows']:assert abs(r['Net_PnL']-float(base.get('Net_PnL',0.)))<1e-8
            continue
        path=pd.read_parquet(STOPS/'paths'/(date+'.parquet'));sig=pd.read_parquet(STOPS/'signals'/(date+'.parquet'))
        grid=path.Second.to_numpy(np.int64);U=path.U.to_numpy(float);first=sig[sig.Confirmed].iloc[0]
        t_star=int(first.Second);exit_s=int(grid[-1]);assert t_star<exit_s
        ev=1/3 if base['Event'] else 1.;scale=25000./float(base['Entry_U'])*ev
        tick=pq.read_table(Path('/mnt/shared/TickDataIV')/pd.Timestamp(date).strftime('%d%m%Y')/'NIFTY.parquet',
            columns=['Time_dt' if 'Time_dt' in pq.read_schema(Path('/mnt/shared/TickDataIV')/pd.Timestamp(date).strftime('%d%m%Y')/'NIFTY.parquet').names else 'Time','Expiry','Strike','Type','Bid','Ask']).to_pandas()
        t=pd.to_datetime(tick.iloc[:,0]);tick['S']=t.dt.hour*3600+t.dt.minute*60+t.dt.second
        tick['Type']=tick.Type.astype(str);tick['Expiry']=pd.to_datetime(tick.Expiry).dt.normalize()
        exps=sorted(e for e in tick.Expiry.unique() if e>=pd.Timestamp(date))
        for r in adjusted:
            name=r['Policy'];checked+=1
            if name=='Roll':
                rolls+=1;s0=int(np.flatnonzero(grid==t_star)[0])
                if abs(r['Prefix_cash']-float(path.Close_net.iloc[s0]))>1e-8 or abs(r['Net_PnL']-r['Prefix_cash']-r['Roll_net'])>1e-8:mismatch.append((date,name,'roll'))
                continue
            legs=json.loads(r['Legs']);entry=secs(r['Adjust_entry_time'])
            K=float(round(U[np.searchsorted(grid,entry)]/50)*50);assert r['ATM']==K
            # Rule checks: strikes, sides and sizes per the frozen spec.
            up=int(first.Direction)>0;rg=str(first.Regime)
            want={'Wings':[('cw',float(round(K*1.02/50)*50),'CE',1,1.),('cw',float(round(K*.98/50)*50),'PE',1,1.)],
                'NWLongStraddle':[('nw',K,'CE',1,.5),('nw',K,'PE',1,.5)],
                'BuyTested':[('cw',K,'CE' if up else 'PE',1,.5)],'SellUntested':[('cw',K,'PE' if up else 'CE',-1,.5)],
                'SellMore':[('cw',K,'CE',-1,.5),('cw',K,'PE',-1,.5)]}[name]
            assert [tuple(l) for l in legs]==want,(date,name)
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
    status='PASS' if not mismatch else 'FAIL'
    out={'Status':status,'Dates':days,'Adjusted_rows_checked':checked,'Added_legs_repriced':legs_checked,'Rolls_checked':rolls,
        'Mismatches':mismatch[:20],'Limits':'Re-prices added legs and roll prefix cash; the roll trade itself is the unchanged engine (asserted in the run); not causal execution',
        'Verifier_SHA256':hashlib.sha256(Path(__file__).read_bytes()).hexdigest()}
    (OUT/'verification.json').write_text(json.dumps(out,indent=2,default=str)+'\n');print(json.dumps(out,indent=2,default=str))
    if mismatch:raise SystemExit(1)


if __name__=='__main__':main()
