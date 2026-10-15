"""Strictly pre-decision bid/ask entry features; share is read-only."""
import argparse
from concurrent.futures import ProcessPoolExecutor, as_completed
import hashlib
import json
import pickle
from pathlib import Path
import time

import numpy as np
import pandas as pd
import pyarrow.parquet as pq

HERE=Path(__file__).resolve().parent
OUT=HERE/'output/tick_entry_study'
TICKS=Path('/mnt/shared/TickDataIV')


def mid_percentile(reference,value):
    x=np.asarray(reference,float); x=x[np.isfinite(x)]
    if not len(x): return np.nan
    return float(((x<value).sum()+.5*(x==value).sum())/len(x))


def asof_book(frame,grid):
    if frame.empty:
        return pd.DataFrame(index=range(len(grid)))
    source=frame.sort_values('Time_dt').drop_duplicates('Time_dt',keep='last').rename(columns={'Time_dt':'Quote_time'})
    return pd.merge_asof(pd.DataFrame({'Snapshot':grid}),source,left_on='Snapshot',right_on='Quote_time',direction='backward')


def compute_features(underlying,quotes,decision):
    decision=pd.Timestamp(decision)
    opening=decision.normalize()+pd.Timedelta(hours=9,minutes=15)
    start=max(opening,decision-pd.Timedelta(seconds=300))
    underlying=underlying[(underlying.Time_dt<decision)&(underlying.Time_dt>=start-pd.Timedelta(seconds=3))].copy()
    underlying=underlying[np.isfinite(underlying.UnderlyingLTP)&(underlying.UnderlyingLTP>0)].sort_values('Time_dt')
    quotes=quotes[(quotes.Time_dt<decision)&(quotes.Time_dt>=start-pd.Timedelta(seconds=3))].copy()
    out={'Valid':False,'Feature_error':'no_underlying','Decision_time':str(decision)}
    if underlying.empty: return out
    u0=float(underlying.UnderlyingLTP.iloc[-1]); strike=round(u0/50)*50
    out.update(Feature_strike=strike,Underlying_at_decision=u0)
    grid=pd.date_range(start,decision,freq='1s',inclusive='left').append(pd.DatetimeIndex([decision-pd.Timedelta(nanoseconds=1)]))
    u=asof_book(underlying[['Time_dt','UnderlyingLTP']],grid)
    u_age=(u.Snapshot-u.Quote_time).dt.total_seconds().to_numpy()
    uval=u.UnderlyingLTP.to_numpy(float)
    books={}
    for k in (strike-50,strike,strike+50):
        for typ in ('CE','PE'):
            part=quotes[(quotes.Strike==k)&quotes.Type.eq(typ)]
            a=asof_book(part,grid)
            if a.empty or 'Quote_time' not in a: continue
            age=(a.Snapshot-a.Quote_time).dt.total_seconds().to_numpy()
            bid=a.Bid.to_numpy(float); ask=a.Ask.to_numpy(float)
            valid=np.isfinite(bid)&np.isfinite(ask)&(bid>0)&(ask>=bid)&(age<=3)
            books[k,typ]=(a,valid,age)
    if (strike,'CE') not in books or (strike,'PE') not in books:
        out['Feature_error']='missing_ATM_legs'; return out
    ce,cv,ca=books[strike,'CE']; pe,pv,pa=books[strike,'PE']
    valid=cv&pv&np.isfinite(uval)&(u_age<=3)
    out['Quote_coverage']=float(valid.mean())
    if not valid[-1]:
        out['Feature_error']='stale_or_invalid_current_quote'; return out
    bid=(ce.Bid+pe.Bid).to_numpy(float); ask=(ce.Ask+pe.Ask).to_numpy(float); mid=(bid+ask)/2
    seconds=(grid-decision).total_seconds().to_numpy()
    recent=valid&(seconds>=-60)
    ref=recent.copy(); ref[-1]=False
    percentile=mid_percentile(bid[ref],bid[-1]) if ref.sum()>=20 else np.nan
    indices=np.flatnonzero(recent)
    if len(indices)<2:
        out['Feature_error']='insufficient_recent_quotes'; return out
    first=indices[0]
    spread=ask[-1]-bid[-1]
    out.update(Valid=True,Feature_error='',Premium_percentile60=percentile,
        Straddle_bid=float(bid[-1]),Straddle_ask=float(ask[-1]),Spread_points=float(spread),
        Spread_pct=float(100*spread/mid[-1]),U_range_bps60=float(np.ptp(uval[recent])/u0*1e4),
        Str_change_pct60=float((mid[-1]/mid[first]-1)*100),Residual_z=np.nan,
        Max_feature_tick=str(max(underlying.Time_dt.max(),quotes.Time_dt.max())) if len(quotes) else str(underlying.Time_dt.max()))
    train=valid&(seconds<-5)
    if train.sum()>=25:
        s=(uval-u0)/u0*1e3
        X=np.column_stack([np.ones(len(grid)),s,s*s,seconds/60])
        beta=np.linalg.lstsq(X[train],mid[train],rcond=None)[0]
        errors=mid[train]-X[train]@beta
        scale=max(1.4826*np.median(np.abs(errors-np.median(errors))),np.std(errors),.05)
        fair=float(X[-1]@beta)
        out.update(Residual_z=float((bid[-1]-fair)/scale),Residual_bid_points=float(bid[-1]-fair),
                   Fitted_fair_mid=fair,Residual_scale=float(scale),Regression_points=int(train.sum()))
    for typ in ('CE','PE'):
        keys=[(strike-50,typ),(strike,typ),(strike+50,typ)]
        credit=np.nan
        if all(k in books for k in keys):
            left,center,right=[books[k] for k in keys]
            frames=[v[0] for v in (left,center,right)]
            fresh=all(v[1][-1] and v[2][-1]<=1 for v in (left,center,right))
            times=[v.Quote_time.iloc[-1] for v in frames]
            quantity=(frames[1].BidQty.iloc[-1]>=2 and frames[0].AskQty.iloc[-1]>0 and frames[2].AskQty.iloc[-1]>0)
            if fresh and quantity and (max(times)-min(times)).total_seconds()<=1:
                credit=float(2*frames[1].Bid.iloc[-1]-frames[0].Ask.iloc[-1]-frames[2].Ask.iloc[-1])
        out['Butterfly_credit_'+typ]=credit
    return out


def one_day(rec):
    began=time.monotonic(); day=rec['Date']; dt=pd.Timestamp(day)
    path=TICKS/dt.strftime('%d%m%Y')/'NIFTY.parquet'
    result={'Date':day,'Entry_Time':rec['Entry_Time']}
    try:
        pf=pq.ParquetFile(path); idx=pf.schema_arrow.names.index('Time_dt')
        stamp=pf.metadata.row_group(0).column(idx).statistics.min
        anchor=pd.Timestamp(stamp).normalize()
        hh,mm=map(int,rec['Entry_Time'].split(':'))
        decision=anchor+pd.Timedelta(hours=hh,minutes=mm)
        start=max(anchor+pd.Timedelta(hours=9,minutes=15),decision-pd.Timedelta(seconds=303))
        filters=[('Expiry','==',pd.Timestamp(rec['CW_Expiry'])),('Time_dt','>=',start),('Time_dt','<',decision)]
        u=pd.read_parquet(path,columns=['Time_dt','UnderlyingLTP'],filters=filters)
        good=u[np.isfinite(u.UnderlyingLTP)&(u.UnderlyingLTP>0)].sort_values('Time_dt')
        if good.empty:
            result.update(Valid=False,Feature_error='no_underlying'); return result
        k=round(float(good.UnderlyingLTP.iloc[-1])/50)*50
        columns=['Time_dt','Strike','Type','Bid','Ask','BidQty','AskQty']
        q=pd.read_parquet(path,columns=columns,filters=filters+[('Strike','in',[k-50,k,k+50])])
        result.update(compute_features(u,q,decision))
        result.update(Source_size=path.stat().st_size,Source_mtime_ns=path.stat().st_mtime_ns,
                      Source_rows=pf.metadata.num_rows,Extraction_seconds=time.monotonic()-began)
    except Exception as e:
        result.update(Valid=False,Feature_error=f'{type(e).__name__}: {str(e)[:180]}')
    return result


def main():
    ap=argparse.ArgumentParser(); ap.add_argument('--workers',type=int,default=8); ap.add_argument('--limit',type=int)
    args=ap.parse_args(); OUT.mkdir(parents=True,exist_ok=True)
    records=[pickle.load(f.open('rb')) for f in sorted((HERE/'output/backtest_relax/run_027bf0249b/days').glob('*.pkl'))]
    records=[r for r in records if r.get('Leg')=='CW']
    if args.limit: records=records[:args.limit]
    destination=OUT/('pilot_features.csv' if args.limit else 'features.csv')
    manifest_path=OUT/(destination.stem+'_manifest.json')
    code_hash=hashlib.sha256(Path(__file__).read_bytes()).hexdigest()
    if destination.exists():
        if not manifest_path.exists() or json.loads(manifest_path.read_text()).get('Code_sha256')!=code_hash:
            raise ValueError('Cached feature code differs or has no manifest; recompute this generated cache before resuming')
    existing=pd.read_csv(destination).to_dict('records') if destination.exists() else []
    wanted={r['Date']:r for r in records}
    for row in existing:
        if row['Date'] not in wanted or row['Entry_Time']!=wanted[row['Date']]['Entry_Time']:
            raise ValueError('Cached entry list differs; recompute this generated cache')
        path=TICKS/pd.Timestamp(row['Date']).strftime('%d%m%Y')/'NIFTY.parquet'
        if pd.notna(row.get('Source_size')) and (not path.exists() or path.stat().st_size!=int(row['Source_size']) or path.stat().st_mtime_ns!=int(row['Source_mtime_ns'])):
            raise ValueError('Cached tick-source fingerprint changed; recompute this generated cache')
    completed={r['Date'] for r in existing}
    todo=[r for r in records if r['Date'] not in completed]
    rows=existing
    manifest_path.write_text(json.dumps({'Status':'extracting','Code_sha256':code_hash},indent=2)+'\n')
    with ProcessPoolExecutor(max_workers=args.workers) as executor:
        futures=[executor.submit(one_day,r) for r in todo]
        for i,future in enumerate(as_completed(futures),1):
            rows.append(future.result())
            if i%25==0 or i==len(futures):
                pd.DataFrame(rows).sort_values('Date').to_csv(destination,index=False)
                print(f'{i}/{len(futures)} new; total {len(rows)}; valid {sum(bool(r.get("Valid")) for r in rows)}',flush=True)
    if not todo: print('All requested days already extracted',flush=True)
    if destination.exists():
        manifest={'Status':'complete','Rows':len(rows),'Feature_sha256':hashlib.sha256(destination.read_bytes()).hexdigest(),
                  'Code_sha256':hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                  'Source_fingerprints':'size/mtime/row count only; full tick payloads are not hashed',
                  'Decision_policy':'Time_dt strictly before decision; no backfill; as-of quote age<=3s'}
        manifest_path.write_text(json.dumps(manifest,indent=2)+'\n')


if __name__=='__main__': main()
