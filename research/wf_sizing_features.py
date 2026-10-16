"""Strict pre-entry features for the WF10/WF11 rerun; read-only tick share."""
import argparse
from concurrent.futures import ThreadPoolExecutor, as_completed
import hashlib,json,pickle,time
from pathlib import Path
import numpy as np
import pandas as pd
import pyarrow as pa
import pyarrow.parquet as pq
from tick_entry_features import asof_book

ROOT=Path(__file__).resolve().parent
OUT=ROOT/'output/walkforward_sizing'
DAILY=Path('/mnt/shared/Hardik/Project_Codes/GammaShortDailyProcess/GammaShortDailydata.csv')


def one_day(rec):
    started=time.monotonic(); day=pd.Timestamp(rec['Date']); path=Path('/mnt/shared/TickDataIV')/day.strftime('%d%m%Y')/'NIFTY.parquet'
    out={'Date':rec['Date'],'Entry_Time':rec['Entry_Time'],'Leg':rec['Leg'],'Valid':False}
    try:
        pf=pq.ParquetFile(path); idx=pf.schema_arrow.names.index('Time_dt')
        anchor=pd.Timestamp(pf.metadata.row_group(0).column(idx).statistics.min).normalize()
        h,m=map(int,rec['Entry_Time'].split(':')); decision=anchor+pd.Timedelta(hours=h,minutes=m)
        open_end=anchor+pd.Timedelta(hours=9,minutes=16)
        expiry=pd.Timestamp(rec['CW_Expiry'] if rec['Leg']=='CW' else rec['NW_Expiry'])
        common=[('Expiry','==',expiry)]
        filters=[common+[('Time_dt','>=',decision-pd.Timedelta(seconds=3)),('Time_dt','<',decision)],
                 common+[('Time_dt','>=',open_end-pd.Timedelta(seconds=3)),('Time_dt','<',min(open_end,decision))]]
        u=pd.read_parquet(path,columns=['Time_dt','UnderlyingLTP'],filters=filters,use_threads=False)
        u=u[np.isfinite(u.UnderlyingLTP)&u.UnderlyingLTP.gt(0)].sort_values('Time_dt')
        if u.empty: raise ValueError('no_fresh_underlying')
        now=u[u.Time_dt<decision]
        if (decision-now.Time_dt.iloc[-1]).total_seconds()>3:raise ValueError('stale_underlying')
        u0=float(now.UnderlyingLTP.iloc[-1]); k=round(u0/50)*50
        before_open=u[u.Time_dt<min(open_end,decision)]
        uopen=float(before_open.UnderlyingLTP.iloc[-1]) if len(before_open) else np.nan
        start=decision-pd.Timedelta(seconds=60)
        q=pd.read_parquet(path,columns=['Time_dt','Type','Bid','Ask','LTP','IV','DTE','UnderlyingLTP'],
            filters=common+[('Strike','==',k),('Time_dt','>=',start-pd.Timedelta(seconds=3)),('Time_dt','<',decision)],use_threads=False)
        result=calculate(q,decision,u0,k)
        out.update(result,Decision_time=str(decision),Opening_underlying=uopen,Feature_strike=k,
            Max_feature_tick=str(max(u.Time_dt.max(),q.Time_dt.max())),
            Source_size=path.stat().st_size,Source_mtime_ns=str(path.stat().st_mtime_ns))
    except Exception as e: out['Feature_error']=f'{type(e).__name__}: {e}'
    out['Seconds']=time.monotonic()-started
    return out


def calculate(q,decision,u0,strike):
    q=q[q.Time_dt.lt(decision)].copy(); q['Time_dt']=q.Time_dt.astype('datetime64[ns]')
    grid=pd.date_range(decision-pd.Timedelta(seconds=60),decision,freq='s',inclusive='left').as_unit('ns')
    grid=grid.append(pd.DatetimeIndex([decision-pd.Timedelta(nanoseconds=1)]))
    books=[]; valid=np.ones(len(grid),bool)
    for typ in ('CE','PE'):
        b=asof_book(q[q.Type.eq(typ)][['Time_dt','Bid','Ask','LTP','IV','DTE']],grid)
        if 'Quote_time' not in b: raise ValueError('missing_ATM_leg')
        age=(b.Snapshot-b.Quote_time).dt.total_seconds().to_numpy()
        valid&=(age<=3)&b.Bid.gt(0).to_numpy()&b.Ask.ge(b.Bid).to_numpy()&b.LTP.gt(0).to_numpy()
        books.append(b)
    u=asof_book(q[['Time_dt','UnderlyingLTP']],grid)
    valid&=np.isfinite(u.UnderlyingLTP.to_numpy())&((u.Snapshot-u.Quote_time).dt.total_seconds().to_numpy()<=3)
    if valid.sum()<20 or not valid[-1]: raise ValueError('insufficient_or_stale_prefix')
    ce,pe=books; premium=(ce.LTP+pe.LTP).to_numpy(float); end=premium[-1]
    uv=u.UnderlyingLTP.to_numpy(float); first=np.flatnonzero(valid)[0]
    otm=ce if u0<strike else pe; iv=float(otm.IV.iloc[-1]); raw=float(otm.DTE.iloc[-1])
    if not (.01<=iv<=2 and raw>0): raise ValueError('invalid_prefix_IV_DTE')
    # DTE adjustment uses the actual calendar date separately in prepare().
    return {'Valid':True,'Feature_error':'','Underlying':u0,'Raw_IV':iv,'Raw_DTE':raw,
        'Straddle':end,'u_range_bps':float(np.ptp(uv[valid])/u0*1e4),
        'str_range_pct':float(np.ptp(premium[valid])/end*100),
        'str_chg_abs':float(abs(premium[-1]/premium[first]-1)*100),
        'spread_pct':float((ce.Ask.iloc[-1]-ce.Bid.iloc[-1]+pe.Ask.iloc[-1]-pe.Bid.iloc[-1])/end*100),
        'Coverage':float(valid.mean())}


def main():
    p=argparse.ArgumentParser();p.add_argument('--workers',type=int,default=8);p.add_argument('--limit',type=int);a=p.parse_args()
    pa.set_cpu_count(1);pa.set_io_thread_count(1);OUT.mkdir(parents=True,exist_ok=True)
    records=[pickle.load(p.open('rb')) for p in sorted((ROOT/'output/backtest_relax/run_027bf0249b/days').glob('*.pkl'))]
    records=[r for r in records if r.get('Leg') in ('CW','NW')]
    if a.limit:records=records[:a.limit]
    dest=OUT/('pilot.csv' if a.limit else 'strict_features.csv');meta=dest.with_suffix('.json')
    code=hashlib.sha256(Path(__file__).read_bytes()).hexdigest()
    if dest.exists() and (not meta.exists() or json.loads(meta.read_text()).get('Code_sha256')!=code):raise ValueError('Stale feature code; recompute generated cache')
    rows=pd.read_csv(dest,dtype={'Source_mtime_ns':str}).to_dict('records') if dest.exists() else []
    done={r['Date'] for r in rows};todo=[r for r in records if r['Date'] not in done]
    meta.write_text(json.dumps({'Code_sha256':code,'Status':'extracting'}))
    with ThreadPoolExecutor(max_workers=a.workers) as executor:
        futures=[executor.submit(one_day,r) for r in todo]
        for i,f in enumerate(as_completed(futures),1):
            rows.append(f.result())
            if i%25==0 or i==len(futures):
                pd.DataFrame(rows).sort_values('Date').to_csv(dest,index=False)
                print(f'{i}/{len(todo)}; rows{len(rows)}; valid{sum(bool(r.get("Valid")) for r in rows)}',flush=True)
    meta.write_text(json.dumps({'Code_sha256':code,'Status':'complete','Rows':len(rows),
        'CSV_sha256':hashlib.sha256(dest.read_bytes()).hexdigest()},indent=2)+'\n')


if __name__=='__main__':main()
