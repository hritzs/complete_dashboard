"""Calibrate the tick Monte Carlo path model (GS_TICK_MONTECARLO_SPEC.md section 2).

Stage 1 (extract): per real trading day, the engine's own CW tick rows ->
1-s underlying 09:15-15:30, per-minute ATM OTM IV and straddle, smile and
bid/ask-spread samples, DTE row. Stage 2 (fit): model parameters from
calibration years only (2021-2024); 2025+ days are extracted for the realism
gate but never used in the fit.
"""
import os
for key in ('NUMBA_NUM_THREADS','OMP_NUM_THREADS','OPENBLAS_NUM_THREADS','MKL_NUM_THREADS'):os.environ[key]='1'
import argparse,contextlib,io,json,math,multiprocessing,traceback
from concurrent.futures import ProcessPoolExecutor,as_completed
from pathlib import Path
import numpy as np
import pandas as pd
import gs_intraday_replay as base

ROOT=Path(__file__).resolve().parent
OUT=ROOT/'output/tick_montecarlo/calibration'
TICKS=Path('/mnt/shared/TickDataIV')
T0,T1=9*3600+15*60,15*3600+30*60          # 09:15:00 .. 15:30:00
MIN0,MIN1=556,930                          # 09:16 .. 15:30 (minute of day)
SAMPLE_MIN=(560,660,780,900)               # 09:20, 11:00, 13:00, 15:00
CAL_END='2024-12-31'


def days():
    out=[]
    for p in TICKS.iterdir():
        try:d=pd.to_datetime(p.name,format='%d%m%Y')
        except ValueError:continue
        if (p/'NIFTY.parquet').exists() and d>=pd.Timestamp('2021-01-01'):out.append(d.strftime('%Y-%m-%d'))
    return sorted(out)


def series(c):
    """Per contract: sorted seconds with last LTP / bid / ask in each second."""
    g=c.groupby('_S').last();return g.index.to_numpy(np.int64),g.LTP.to_numpy(float),g.Bid.to_numpy(float),g.Ask.to_numpy(float)


def asof(sec,vals,t):
    j=np.searchsorted(sec,t,side='right')-1
    return np.where(j>=0,vals[np.clip(j,0,None)],np.nan)


def extract(date):
    try:
        path=TICKS/pd.Timestamp(date).strftime('%d%m%Y')/'NIFTY.parquet'
        with contextlib.redirect_stdout(io.StringIO()):cw,_=base.ENGINE.load_day_ticks(str(path),pd.Timestamp(date))
        if cw is None or cw.empty:return {'Date':date,'Error':'no cw rows'}
        t=pd.to_datetime(cw.Time_dt);cw=cw.assign(_S=(t.dt.hour*3600+t.dt.minute*60+t.dt.second).astype(np.int64),
            _K=pd.to_numeric(cw.Strike,errors='coerce'),Type=cw.Type.astype(str))
        g=cw.groupby('_S');grid=np.arange(T0,T1+1)
        u=pd.Series(g.UnderlyingLTP.first()).reindex(grid).ffill().bfill().to_numpy(float)
        dte=pd.Series(g.DTE.first()).reindex(grid).ffill().bfill().to_numpy(float)
        if not np.isfinite(u).all() or (u<=0).any():return {'Date':date,'Error':'underlying gaps'}
        covered=int(pd.Series(g.UnderlyingLTP.first()).reindex(grid).notna().sum())
        if covered<0.6*len(grid):return {'Date':date,'Error':f'thin session ({covered} s)'}
        ub=pd.Series(g.UnderlyingBid.first()).reindex(grid);ua=pd.Series(g.UnderlyingAsk.first()).reindex(grid)
        uhalf=float(np.nanmedian((ua-ub).to_numpy(float)/2)) if (ua-ub).notna().any() else np.nan
        raw916=float(dte[(MIN0*60)-T0]);d=int(round(raw916-(MIN1-MIN0)/375))
        bus=int(np.busday_count(np.datetime64(date),np.datetime64(date)+np.timedelta64(int(math.ceil(raw916)),'D')))
        wk=int(math.ceil(raw916))-bus;trading=max(0.,raw916-1.) if wk>0 else raw916
        dte_idx=int(np.clip(math.ceil(trading)-1,0,6));adj=math.sqrt(raw916/trading) if trading>0 and trading!=raw916 else 1.
        books={}
        def book(K,T):
            if (K,T) not in books:
                c=cw[(cw._K==K)&(cw.Type==T)]
                books[(K,T)]=series(c) if len(c) else (np.array([],np.int64),)+(np.array([]),)*3
            return books[(K,T)]
        mins=np.arange(MIN0,MIN1+1);atm_iv=np.full(len(mins),np.nan);strad=np.full(len(mins),np.nan);atmk=np.zeros(len(mins))
        for i,mn in enumerate(mins):
            s=mn*60;F=u[s-T0];K=round(F/50)*50;atmk[i]=K
            ce=book(K,'CE');pe=book(K,'PE')
            if not len(ce[0]) or not len(pe[0]):continue
            cl=float(asof(ce[0],ce[1],s));pl=float(asof(pe[0],pe[1],s))
            T=max(dte[s-T0]/365.,1e-5)
            atm_iv[i]=base.ENGINE.compute_iv_array(np.array([F]),float(K),np.array([T]),np.array([cl]),np.array([pl]))[0]
            strad[i]=cl+pl
        smile=[];spread=[]
        for mn in SAMPLE_MIN:
            s=mn*60;F=u[s-T0];K0=round(F/50)*50;T=max(dte[s-T0]/365.,1e-5);a=atm_iv[mn-MIN0]
            for j in range(-8,9):
                K=K0+50*j
                for typ in ('CE','PE'):
                    b=book(K,typ)
                    if not len(b[0]):continue
                    bid=float(asof(b[0],b[2],s));ask=float(asof(b[0],b[3],s))
                    if not (bid>0 and ask>=bid):continue
                    mid=(bid+ask)/2;spread.append((dte_idx,mid,(ask-bid)/2,math.log(K/F)/math.sqrt(T)))
                    otm=(typ=='CE' and K>F) or (typ=='PE' and K<F)
                    if otm and j!=0 and np.isfinite(a) and mid>0.5:
                        iv=base.ENGINE._iv_py(typ=='CE',mid,F,float(K),T)
                        if np.isfinite(iv):smile.append((dte_idx,math.log(K/F)/math.sqrt(T),iv/a))
        dest=OUT/'days'/(date+'.npz');tmp=OUT/'days'/(date+'.tmp.npz')
        np.savez_compressed(tmp,u=u.astype(np.float64),dte=dte,atm_iv=atm_iv,strad=strad,atmk=atmk,
            smile=np.array(smile,float).reshape(-1,3),spread=np.array(spread,float).reshape(-1,4))
        tmp.replace(dest)
        return {'Date':date,'Error':'','D':d,'Raw916':raw916,'Trading_DTE':trading,'DTE_idx':dte_idx,'Adj_factor':adj,
            'U916':float(u[MIN0*60-T0]),'IV916':float(atm_iv[0]),'Straddle916':float(strad[0]),'U_half_spread':uhalf,'Covered_s':covered}
    except Exception:return {'Date':date,'Error':traceback.format_exc()[-800:]}


def fit():
    meta=pd.read_csv(OUT/'days.csv');meta=meta[meta.Error.isna()|meta.Error.eq('')]
    cal=meta[meta.Date<=CAL_END].copy()
    per_min=[];kurt=[];jumps=[];ivpairs=[];smile=[];spread=[];logrv=[];rows=[]
    secs_year=375*60*252.
    for r in cal.itertuples():
        z=np.load(OUT/'days'/(r.Date+'.npz'));u=z['u'];lr=np.diff(np.log(u))
        m=lr[:(len(lr)//60)*60].reshape(-1,60);mrv=(m**2).sum(1)                       # 1-min realized variance
        day_rv=mrv.sum()
        if day_rv<=0:continue
        per_min.append(mrv/day_rv*len(mrv))                                             # seasonality share
        ret1=m.sum(1);loc=np.sqrt(pd.Series(mrv).rolling(15,min_periods=5,center=True).median().to_numpy())
        zs=ret1/np.where(loc>0,loc,np.nan);zs=zs[np.isfinite(zs)]
        kurt.append(zs);big=np.abs(zs)>6;jumps.append((int(big.sum()),zs[big]))
        logrv.append(np.log(np.maximum(mrv,1e-12)))
        iv=z['atm_iv'];ok=np.isfinite(iv)
        ann=math.sqrt(day_rv*252)                                                      # day RV annualized
        rows.append({'Date':r.Date,'DTE_idx':r.DTE_idx,'IV916':r.IV916,'RV_ann':ann,'Ratio':ann/r.IV916 if r.IV916>0 else np.nan})
        if ok.sum()>30:
            li=np.log(np.where(ok,iv,np.nan));p=u[(np.arange(MIN0,MIN1+1)*60)-T0]
            dl=np.diff(li);dr=np.diff(np.log(p));g=np.isfinite(dl)&np.isfinite(dr)
            ivpairs.append(np.c_[dr[g],dl[g],np.arange(len(dl))[g]])
        if len(z['smile']):smile.append(z['smile'])
        if len(z['spread']):spread.append(z['spread'])
    season=np.nanmean(np.vstack([p[:min(len(q) for q in per_min)] for p in per_min]),0);season/=season.mean()
    # Stochastic variance: AR(1) of de-seasonalized log 1-min RV, demeaned per day.
    xs=[];ys=[]
    for lv in logrv:
        n=min(len(lv),len(season));x=lv[:n]-np.log(season[:n]);x=x-np.nanmean(x);xs.append(x[:-1]);ys.append(x[1:])
    X=np.concatenate(xs);Y=np.concatenate(ys);ok=np.isfinite(X)&np.isfinite(Y);phi=float(np.polyfit(X[ok],Y[ok],1)[0])
    resid=Y[ok]-phi*X[ok];xi=float(resid.std())
    allz=np.concatenate(kurt);allz=allz[np.abs(allz)<=6];k=float(((allz-allz.mean())**4).mean()/allz.var()**2)
    nu=float(max(4.1,6/max(k-3,1e-6)+4))                                               # Student-t dof from excess kurtosis
    njump=np.array([j[0] for j in jumps]);jsize=np.concatenate([j[1] for j in jumps if len(j[1])]) if any(len(j[1]) for j in jumps) else np.array([])
    P=np.vstack(ivpairs);beta=float(np.polyfit(P[:,0],P[:,1],1)[0]);ivres=float((P[:,1]-beta*P[:,0]).std())
    drift=pd.DataFrame(P,columns=['r','dl','m']).groupby('m').dl.mean().reindex(range(MIN1-MIN0)).fillna(0).to_numpy()
    S=np.vstack(smile);smile_fit={}
    for d in range(7):
        s=S[(S[:,0]==d)&np.isfinite(S[:,2])&(np.abs(S[:,1])<3)]
        smile_fit[str(d)]=list(np.polyfit(s[:,1],s[:,2]-1,2)[::-1][1:]) if len(s)>50 else [0.,0.]
    SP=np.vstack(spread);spread_fit={}
    for d in range(7):
        s=SP[SP[:,0]==d]
        spread_fit[str(d)]=list(np.polyfit(s[:,1],s[:,2],1)[::-1]) if len(s)>50 else [0.05,0.005]
    params={'Calibration_end':CAL_END,'Days':int(len(rows)),'Season_1min':season.tolist(),'Logvar_phi':phi,'Logvar_xi':xi,
        'Student_t_nu':nu,'Excess_kurtosis_1min':k-3,'Jump_rate_per_day':float(njump.mean()),'Jump_sizes_sigma':jsize.tolist(),
        'IV_spot_beta':beta,'IV_noise_1min':ivres,'IV_drift_by_minute':drift.tolist(),'Smile_ab_by_dte':smile_fit,
        'Spread_c0_c1_by_dte':spread_fit,'U_half_spread':float(cal.U_half_spread.median())}
    (OUT/'params.json').write_text(json.dumps(params,indent=1)+'\n')
    pd.DataFrame(rows).to_csv(OUT/'day_templates.csv',index=False)
    print(json.dumps({k:v for k,v in params.items() if not isinstance(v,(list,dict))},indent=1))


def main():
    ap=argparse.ArgumentParser();ap.add_argument('--stage',choices=('extract','fit'),default='extract');ap.add_argument('--workers',type=int,default=12)
    a=ap.parse_args()
    (OUT/'days').mkdir(parents=True,exist_ok=True)
    if a.stage=='fit':return fit()
    dates=days();done=set()
    if (OUT/'days.csv').exists():
        old=pd.read_csv(OUT/'days.csv');done=set(old.Date[old.Error.isna()|old.Error.eq('')])
    todo=[d for d in dates if d not in done];rows=[] if not done else pd.read_csv(OUT/'days.csv').to_dict('records')
    rows=[r for r in rows if r['Date'] in done]
    print('extract',len(todo),'of',len(dates),flush=True)
    with ProcessPoolExecutor(max_workers=a.workers,mp_context=multiprocessing.get_context('fork'),initializer=base.initialize) as pool:
        futs=[pool.submit(extract,d) for d in todo]
        for i,f in enumerate(as_completed(futs),1):
            rows.append(f.result())
            if i%100==0:print(i,'/',len(todo),flush=True);pd.DataFrame(rows).sort_values('Date').to_csv(OUT/'days.csv',index=False)
    df=pd.DataFrame(rows).sort_values('Date');df.to_csv(OUT/'days.csv',index=False)
    print('done; errors',int((df.Error.notna()&df.Error.ne('')).sum()),flush=True)


if __name__=='__main__':main()
