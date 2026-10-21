"""Independent prefix-cash and global-DD verification of complete comparison."""
import hashlib,json,pickle
from pathlib import Path
from concurrent.futures import ThreadPoolExecutor
import numpy as np
import pandas as pd

ROOT=Path(__file__).resolve().parent;OUT=ROOT/'output/gs_intraday_stops';SOURCE=ROOT/'output/logic_comparison_20261021'


def path_stats(net,minimum,maximum,inside):
    cash=peak=worst=0.
    for p,lo,hi,local in zip(net,minimum,maximum,inside):
        worst=max(worst,local,peak-cash-lo);peak=max(peak,cash+hi);cash+=p
    closes=np.cumsum(np.asarray(net,float));dd=np.maximum.accumulate(np.r_[0.,closes])[1:]-closes
    return float(cash),float(dd.max()),float(worst)


def verify_day(date):
    rec=json.loads((OUT/'days'/(date+'.json')).read_text());assert not rec['Error']
    base=pickle.loads((SOURCE/'days'/(date+'.pkl')).read_bytes())
    assert (rec['Tick_size'],rec['Tick_mtime_ns'])==(base['Tick_size'],base['Tick_mtime_ns'])
    if not rec['Traded']:
        assert all(row['Net_PnL']==0 for row in rec['Policies'])
        return 0,0,0,0
    frame=pd.read_parquet(OUT/'paths'/(date+'.parquet'));signals=pd.read_parquet(OUT/'signals'/(date+'.parquet'))
    assert frame.Second.is_monotonic_increasing and frame.Second.is_unique
    assert np.isfinite(frame[['Mark_net','Close_net','U','Hedge_cost']].to_numpy()).all()
    np.testing.assert_allclose(frame.Close_net.iloc[-1],base['New']['Net_PnL'],atol=1e-8,rtol=0)
    checks=frame[frame.Is_check]
    # Independently recompute windows fully covered by the stored holding path.
    # Pre-entry windows remain covered by source hashes and future-perturbation tests.
    signal_checks=0;t=frame.Second.to_numpy(int);u=frame.U.to_numpy(float)
    for s in signals.itertuples(index=False):
        if base['New']['Leg']!='CW':break  # NW marks upsample source observations; no NW overlay is applied.
        targets=np.arange(int(s.Second)//60*60-1200,int(s.Second)//60*60+1,60)-2
        if targets[0]<t[0]:continue
        pos=np.searchsorted(t,targets,side='right')-1
        label='Unavailable';direction=0;eff=ratio=np.nan
        if (pos>=0).all() and not (targets-t[pos]>3).any() and (u[pos]>0).all():
            v=u[pos];r=np.diff(np.log(v));r5=np.sum(r[-5:]**2);r15=np.sum(r[:-5]**2)/3
            travel=np.abs(np.diff(v[-6:])).sum();eff=abs(v[-1]-v[-6])/travel if travel else 0.
            ratio=r5/r15 if r15>0 else np.nan
            direction=1 if v[-1]>max(v[-6:-1]) and v[-1]>v[-6] else -1 if v[-1]<min(v[-6:-1]) and v[-1]<v[-6] else 0
            label='Calm'
            if np.isfinite(ratio) and ratio>1.5 and eff>=.60 and direction:label='Directional_expansion'
            elif np.isfinite(ratio) and ratio>2 and eff<=.30:label='Volatile_whipsaw';direction=0
            else:direction=0
        assert label==s.Regime and direction==s.Direction
        np.testing.assert_allclose([eff,ratio],[s.Efficiency,s.RV_ratio],atol=1e-10,rtol=1e-10,equal_nan=True)
        signal_checks+=1
    for row in rec['Policies']:
        cut=row['Path_cut_index'];np.testing.assert_allclose(row['Net_PnL'],frame.Close_net.iloc[cut],atol=1e-10,rtol=0)
        np.testing.assert_allclose(row['Hedge_cost'],frame.Hedge_cost.iloc[cut],atol=1e-10,rtol=0)
        assert row['Post_entry_hedge_count']==int(frame.Fut_trade.iloc[1:cut+1].ne(0).sum())
        if row['Policy']=='Baseline' or base['New']['Leg']=='NW':assert cut==len(frame)-1
        else:
            candidates=[];name=row['Policy'];b=base['New']
            if name!='RegimeExit':
                multiplier=.75 if name=='Stop075' else .875
                budget=2.5*b['SL_bps']*(1/3 if b['Event'] else 1.)
                hits=checks[checks.Trigger_gross<=-budget*multiplier]
                if len(hits):candidates.append(int(hits.index[0]))
            if 'Regime' in name:
                sig=signals[signals.Confirmed]
                if len(sig):candidates.append(int(np.flatnonzero(frame.Second.to_numpy()==sig.Second.iloc[0])[0]))
            assert cut==min([len(frame)-1]+candidates)
    # Directly check several independently reconstructed two-day shape identities
    # elsewhere in unit tests; here complete holding path supplies each day's extrema.
    return len(frame),len(rec['Policies']),int(sum(r['Changed_exit'] for r in rec['Policies'])),signal_checks


def main():
    manifest=json.loads((OUT/'manifest.json').read_text())
    for name,digest in manifest['Inputs'].items():assert hashlib.sha256((ROOT/name).read_bytes()).hexdigest()==digest,name
    for name,digest in manifest['Outputs'].items():assert hashlib.sha256((OUT/name).read_bytes()).hexdigest()==digest,name
    run=json.loads((OUT/'run_manifest.json').read_text());assert run['Status']=='complete' and not run['Failed_dates']
    with ThreadPoolExecutor(max_workers=8) as pool:checked=list(pool.map(verify_day,run['Dates']))
    book=pd.read_excel(OUT/'gs_intraday_stop_comparison.xlsx',sheet_name=['Summary','Comparison','Daily_PnL','All_policy_days','Trades','Period_risk'])
    days=book['All_policy_days'];assert len(days)==7150 and len(book['Trades'])==3970
    assert not days.duplicated(['Date','Policy']).any()
    csv=pd.read_csv(OUT/'daily_policy_paths.csv');pd.testing.assert_frame_equal(days,csv,check_dtype=False,atol=1e-8,rtol=1e-10)
    summary=book['Summary'];periods=book['Period_risk'];period_count=0
    for name,g in days.groupby('Policy',sort=False):
        assert g.Date.tolist()==run['Dates'];dt=pd.to_datetime(g.Date)
        p=g.Net_PnL.to_numpy(float);equity=p.cumsum();dd=np.maximum.accumulate(np.r_[0.,equity])[1:]-equity
        np.testing.assert_allclose(g.Daily_DD,dd,atol=1e-8,rtol=0)
        np.testing.assert_allclose(book['Daily_PnL'][name],p,atol=1e-8,rtol=0)
        scopes={'All_dates':np.ones(len(g),bool),'2022plus':dt.dt.year>=2022,'2023plus':dt.dt.year>=2023,'2025plus':dt.dt.year>=2025}
        for scope,mask in scopes.items():
            f=g.loc[mask];value=path_stats(f.Net_PnL,f.Path_low,f.Path_high,f.Day_local_MDD)
            row=summary[summary.Policy.eq(name)&summary.Scope.eq(scope)].iloc[0]
            np.testing.assert_allclose([row.Net,row.MDD,row.Source_model_intraday_MDD],value,atol=1e-8,rtol=0)
        labels={'Year':dt.dt.year.astype(str),'Quarter':dt.dt.to_period('Q').astype(str),'Month':dt.dt.to_period('M').astype(str),
            'FY':np.array([f'FY{d.year if d.month>=4 else d.year-1}-{d.year+1 if d.month>=4 else d.year}' for d in dt])}
        for grouping,keys in labels.items():
            for row in periods[periods.Policy.eq(name)&periods.Grouping.eq(grouping)].itertuples(index=False):
                f=g.loc[keys==str(row.Period)];value=path_stats(f.Net_PnL,f.Path_low,f.Path_high,f.Day_local_MDD)
                np.testing.assert_allclose([row.Net,row.MDD,row.Source_model_intraday_MDD],value,atol=1e-8,rtol=0);period_count+=1
    result={'Status':'PASS','Dates':len(run['Dates']),'Policies':5,'Policy_days':len(days),'Traded_policy_days':len(book['Trades']),
        'Second_source_model_marks':sum(x[0] for x in checked),'Prefix_cash_exits_checked':sum(x[1] for x in checked),
        'Changed_policy_exits':sum(x[2] for x in checked),'Period_rows_checked':period_count,
        'Regime_windows_independently_recomputed_after_entry':sum(x[3] for x in checked),
        'Limits':'Reconciled legacy source-model paths; not a causal fill certification or independent alpha validation',
        'Verifier_SHA256':hashlib.sha256(Path(__file__).read_bytes()).hexdigest()}
    (OUT/'verification.json').write_text(json.dumps(result,indent=2)+'\n');print(json.dumps(result,indent=2))


if __name__=='__main__':main()
