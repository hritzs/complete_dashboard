"""Read-only audit of whether chain OI/volume can support a live regime signal.

No strategy PnL, model fitting or signal thresholds are selected here.
"""
import hashlib
import json
from pathlib import Path
from concurrent.futures import ThreadPoolExecutor
import numpy as np
import pandas as pd
import pyarrow.parquet as pq

ROOT=Path(__file__).resolve().parent
OUT=ROOT/'output/gs_regime_inputs'
# Three existing data-audit samples plus an explicitly ex-post worst-GS stress case.
DATES=('2021-01-04','2024-02-29','2026-10-13','2021-03-25')


def audit(date):
    path=Path('/mnt/shared/TickDataIV')/pd.Timestamp(date).strftime('%d%m%Y')/'NIFTY.parquet'
    before=path.stat();pf=pq.ParquetFile(path)
    i=pf.schema_arrow.names.index('Time_dt')
    anchor=pd.Timestamp(pf.metadata.row_group(0).column(i).statistics.min).normalize()
    start=anchor+pd.Timedelta(hours=9,minutes=15);decision=anchor+pd.Timedelta(hours=9,minutes=20)
    columns=['Time_dt','Expiry','Strike','Type','UnderlyingLTP','Bid','Ask','BidQty','AskQty','OI','Volume','IV','BidIV','AskIV']
    data=pd.read_parquet(path,columns=columns,filters=[('Time_dt','>=',start),('Time_dt','<',anchor+pd.Timedelta(hours=10,minutes=30))])
    data=data[data.Type.isin(['CE','PE'])].copy()
    data['Available_at']=data.Time_dt+pd.Timedelta(seconds=1)
    prefix=data[data.Available_at<decision]
    expiry=min(e for e in pd.to_datetime(prefix.Expiry).unique() if pd.Timestamp(e)>=pd.Timestamp(date))
    prefix=prefix[pd.to_datetime(prefix.Expiry).eq(expiry)]
    latest=prefix.sort_values('Available_at').dropna(subset=['UnderlyingLTP']).iloc[-1]
    u=float(latest.UnderlyingLTP)
    strikes=sorted(prefix.Strike.unique(),key=lambda k:(abs(float(k)-u),float(k)))[:9]
    data=data[pd.to_datetime(data.Expiry).eq(expiry)&data.Strike.isin(strikes)]
    contracts=[];changes=[]
    for (strike,side),group in data.groupby(['Strike','Type']):
        group=group.sort_values('Available_at').drop_duplicates('Available_at',keep='last').copy()
        for c in ['OI','Volume','Bid','Ask','BidQty','AskQty','IV']:group[c]=pd.to_numeric(group[c],errors='coerce')
        oi=group[group.OI.notna()&group.OI.ge(0)]
        changed=oi.OI.diff().ne(0);changed.iloc[0]=False
        events=oi[changed];gaps=events.Available_at.diff().dt.total_seconds().dropna()
        v=group.Volume.dropna();vd=v.diff().dropna()
        valid=group.Bid.gt(0)&group.Ask.ge(group.Bid)&group.BidQty.gt(0)&group.AskQty.gt(0)
        q=group.loc[valid];spread=(q.Ask-q.Bid)/((q.Ask+q.Bid)/2)
        contracts.append({'Date':date,'Expiry':str(pd.Timestamp(expiry).date()),'Strike':strike,'Type':side,
            'Rows':len(group),'OI_valid_rows':len(oi),'OI_distinct_values':oi.OI.nunique(),'OI_change_events':int(changed.sum()),
            'OI_unchanged_fraction':float(oi.OI.diff().eq(0).iloc[1:].mean()),
            'OI_change_gap_median_seconds':float(gaps.median()) if len(gaps) else np.nan,
            'OI_change_gap_min_seconds':float(gaps.min()) if len(gaps) else np.nan,
            'Volume_decrease_events':int(vd.lt(0).sum()),'Volume_change_events':int(vd.ne(0).sum()),
            'Quote_valid_fraction':float(valid.mean()),'Median_relative_spread':float(spread.median()),
            'Fractional_second_rows':int((group.Time_dt.dt.microsecond.ne(0)|group.Time_dt.dt.nanosecond.ne(0)).sum())})
        for _,row in events.iterrows():
            changes.append({'Date':date,'Expiry':str(pd.Timestamp(expiry).date()),'Strike':strike,'Type':side,
                'Available_at':str(row.Available_at.time()),'OI':row.OI})
    after=path.stat();assert (before.st_size,before.st_mtime_ns)==(after.st_size,after.st_mtime_ns)
    frame=pd.DataFrame(contracts)
    summary={'Date':date,'Selection':'Ex-post worst-GS stress diagnostic' if date=='2021-03-25' else 'Previously fixed data-audit sample',
        'Window':'09:15–10:30 IST','Expiry':str(pd.Timestamp(expiry).date()),'Contracts':len(frame),
        'ATM_reference_U_before_0920':u,'Median_OI_change_gap_seconds':frame.OI_change_gap_median_seconds.median(),
        'Median_OI_unchanged_fraction':frame.OI_unchanged_fraction.median(),
        'OI_change_events':int(frame.OI_change_events.sum()),'Volume_decrease_events':int(frame.Volume_decrease_events.sum()),
        'Fractional_second_rows':int(frame.Fractional_second_rows.sum()),'Tick_size':before.st_size,'Tick_mtime_ns':str(before.st_mtime_ns)}
    return summary,contracts,changes


def main():
    OUT.mkdir(exist_ok=True,parents=True)
    with ThreadPoolExecutor(max_workers=4) as pool:results=list(pool.map(audit,DATES))
    tables={'Summary':pd.DataFrame([r[0] for r in results]),
        'Contracts':pd.DataFrame([x for r in results for x in r[1]]),
        'OI_changes':pd.DataFrame([x for r in results for x in r[2]]),
        'Research_spec':pd.DataFrame({'Line':(ROOT/'GS_REGIME_COMPLEMENT_SPEC.md').read_text().splitlines()})}
    notes=['# GS regime-complement input audit','',
        'Four sampled morning prefixes; feasibility audit only. No strategy trades or PnL tested.',
        'The2021-03-25 stress case was chosen after seeing worst GS PnL and is not an unbiased prediction test.',
        'Median OI value-change gaps are60–181seconds across these samples; OI is a slower confirmation candidate, not a second-by-second trigger.',
        'Volume decreases within contracts. Its field semantics must be verified before any cumulative-volume differencing.',
        'Existing latest-GS hedges use net-delta or delta/gamma-distance triggers, not an option-chain regime detector.',
        'The candidate combines completed-bar direction, realized variance and fixed-contract IV breadth. OI remains optional.',
        'See research/GS_REGIME_COMPLEMENT_SPEC.md and the Research_spec Excel sheet for definitions, provisional recipe and required execution study.',
        '',tables['Summary'].to_string(index=False),'']
    (OUT/'REPORT.md').write_text('\n'.join(notes))
    with pd.ExcelWriter(OUT/'regime_input_audit.xlsx') as writer:
        for name,frame in tables.items():frame.to_excel(writer,sheet_name=name,index=False);writer.sheets[name].freeze_panes='B2'
    for name in ('Summary','Contracts','OI_changes'):tables[name].to_csv(OUT/(name.lower()+'.csv'),index=False)
    manifest={'Status':'INPUT_AUDIT_ONLY','Strategy_PnL_tested':False,'Dates':DATES,
        'Inputs':{p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in [Path(__file__),ROOT/'GS_REGIME_COMPLEMENT_SPEC.md']},
        'Outputs':{p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in OUT.iterdir() if p.name!='manifest.json'},
        'Limits':'Four sampled morning prefixes, not all history. OI value-change cadence is not exchange publication latency. No order/aggressor inference; no validated trading rule.'}
    (OUT/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
    print(tables['Summary'].to_string(index=False))


if __name__=='__main__':main()
