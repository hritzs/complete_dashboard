"""Read-only preflight of data for directional long-convexity research."""
import hashlib
import json
from pathlib import Path

import numpy as np
import pandas as pd
import pyarrow.parquet as pq

ROOT=Path(__file__).resolve().parent
OUT=ROOT/'output/complement_strategy'
SAMPLES=('2021-01-04','2024-02-29','2026-10-13')


def audit(date):
    path=Path('/mnt/shared/TickDataIV')/pd.Timestamp(date).strftime('%d%m%Y')/'NIFTY.parquet'
    before=path.stat();pf=pq.ParquetFile(path);schema=pf.schema_arrow.names
    stamp=pf.metadata.row_group(0).column(schema.index('Time_dt')).statistics.min
    anchor=pd.Timestamp(stamp).normalize();start=anchor+pd.Timedelta(hours=9,minutes=15);end=start+pd.Timedelta(minutes=10)
    wanted=['Time_dt','Expiry','Strike','Type','UnderlyingLTP','UnderlyingBid','UnderlyingAsk',
        'Bid','Ask','BidQty','AskQty','Volume','OI','IV','BidIV','AskIV']
    d=pd.read_parquet(path,columns=[c for c in wanted if c in schema],filters=[('Time_dt','>=',start),('Time_dt','<',end)])
    d=d[d.Type.astype(str).isin(['CE','PE'])].copy()
    expiries=sorted(pd.to_datetime(d.Expiry).unique());nearest=pd.Timestamp(expiries[0])
    cw=d[pd.to_datetime(d.Expiry).eq(nearest)].copy();u=cw.dropna(subset=['UnderlyingLTP']).sort_values('Time_dt')
    u=u.drop_duplicates('Time_dt',keep='last')
    valid=(cw.Bid>0)&(cw.Ask>=cw.Bid)&np.isfinite(cw.Bid)&np.isfinite(cw.Ask)
    after=path.stat();assert (before.st_size,before.st_mtime_ns)==(after.st_size,after.st_mtime_ns)
    row={'Date':date,'Source_size':before.st_size,'Source_mtime_ns':str(before.st_mtime_ns),
        'Full_file_rows':pf.metadata.num_rows,'Prefix_rows_all_expiries':len(d),'CW_prefix_rows':len(cw),
        'Timestamp_anchor':str(anchor),'Nearest_expiry':str(nearest),'Expiries_in_prefix':len(expiries),
        'CW_unique_timestamps':int(cw.Time_dt.nunique()),
        'CW_subsecond_rows':int((cw.Time_dt.astype('int64')%1_000_000_000!=0).sum()) if str(cw.Time_dt.dtype)=='datetime64[ns]' else int(((cw.Time_dt.dt.microsecond!=0)|(cw.Time_dt.dt.nanosecond!=0)).sum()),
        'CW_valid_top_quote_fraction':float(valid.mean()),
        'Conflicting_underlying_at_same_timestamp':int(cw.groupby('Time_dt').UnderlyingLTP.nunique().gt(1).sum()),
        'Has_aggressor_side_field':any(c.lower() in ('aggressorside','aggressor_side','trade_side') for c in schema),
        'Has_full_order_book_depth':False,'Schema':', '.join(schema)}
    if len(u)>1:
        gaps=u.Time_dt.diff().dt.total_seconds().dropna()
        row.update(Underlying_median_timestamp_gap_seconds=float(gaps.median()),Underlying_max_timestamp_gap_seconds=float(gaps.max()))
    for col in ('IV','BidIV','AskIV'):
        if col in cw:
            vals=pd.to_numeric(cw[col],errors='coerce');vals=vals[np.isfinite(vals)&(vals>0)]
            row[col+'_positive_rows']=len(vals)
            for p in (.1,.5,.9):row[col+f'_q{int(100*p)}']=float(vals.quantile(p)) if len(vals) else np.nan
    return row


def main():
    OUT.mkdir(parents=True,exist_ok=True);rows=[]
    for date in SAMPLES:
        row=audit(date);rows.append(row)
        print(date,'prefix rows',row['Prefix_rows_all_expiries'],'CW timestamps',row['CW_unique_timestamps'],
            'subsecond rows',row['CW_subsecond_rows'],flush=True)
    frame=pd.DataFrame(rows);frame.to_csv(OUT/'data_audit.csv',index=False)
    frame.to_excel(OUT/'data_audit.xlsx',index=False,sheet_name='Sampled_data_audit')
    manifest={'Status':'preflight_only','Strategy_PnL_tested':False,'Sample_dates':SAMPLES,
        'Code_SHA256':hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
        'Outputs':{p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in OUT.glob('data_audit.*')},
        'Limits':'Three sampled 09:15–09:25 prefixes; top quotes, not certified order-event provenance or executability; no live changes'}
    (OUT/'audit_manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')


if __name__=='__main__':main()
