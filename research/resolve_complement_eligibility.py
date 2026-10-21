"""Audit only known pre-entry data-admission failures; preserve raw exceptions.

No missing exit, execution failure or other unexpected error can become zero PnL.
"""
import hashlib,json,pickle,shutil
from pathlib import Path
import numpy as np
import pandas as pd
import pyarrow.parquet as pq
from complement_strategy import ROOT,OUT,GRID,POLICIES,availability,carry,empty_policy_day


def audit(date,error):
    last=error.strip().splitlines()[-1]
    permitted={'ValueError: No expiry observed before09:30 at least2calendar days ahead',
        'ValueError: Insufficient opening underlying coverage'}
    null_candidate=last.startswith("TypeError: ufunc 'isfinite' not supported") and 'np.isfinite(u.UnderlyingLTP)' in error
    if last not in permitted and not null_candidate:raise ValueError('Unexpected error is not eligible for resolution: '+last)
    path=Path('/mnt/shared/TickDataIV')/pd.Timestamp(date).strftime('%d%m%Y')/'NIFTY.parquet'
    before=path.stat();pf=pq.ParquetFile(path);j=pf.schema_arrow.names.index('Time_dt')
    anchor=pd.Timestamp(pf.metadata.row_group(0).column(j).statistics.min).normalize()
    d=pd.read_parquet(path,columns=['Time_dt','Expiry','UnderlyingLTP'],filters=[
        ('Time_dt','>=',anchor+pd.Timedelta(seconds=int(GRID[0])-1)),
        ('Time_dt','<',anchor+pd.Timedelta(hours=9,minutes=30))])
    d['available']=availability(d.Time_dt);d=d[d.available.lt(9*3600+30*60)]
    exp=sorted(e for e in pd.to_datetime(d.Expiry).unique() if pd.Timestamp(e).normalize()>=pd.Timestamp(date)+pd.Timedelta(days=2))
    valid_seconds=0
    if null_candidate:
        assert str(pf.schema_arrow.field('UnderlyingLTP').type)=='null','Non-null numeric schema errors require a real replay, not zero PnL'
        reason='UnderlyingLTP is Arrow null type for entire source file'
    elif 'No expiry observed' in last:
        assert not exp,'Admission failure did not reproduce'
        reason='No morning observations' if d.empty else 'No observed expiry meeting minimum2calendar days'
    else:
        assert exp
        u=d[pd.to_datetime(d.Expiry).eq(pd.Timestamp(exp[0]))]
        u=u[np.isfinite(u.UnderlyingLTP)&u.UnderlyingLTP.gt(0)].sort_values('available').drop_duplicates('available',keep='last')
        a=carry(u.set_index('available').UnderlyingLTP.reindex(GRID).to_numpy(float))
        valid_seconds=int(np.isfinite(a[GRID<9*3600+30*60]).sum());assert valid_seconds<600
        reason='Fewer than600 valid opening underlying seconds'
    after=path.stat();assert (before.st_size,before.st_mtime_ns)==(after.st_size,after.st_mtime_ns)
    return {'Date':date,'Eligibility_reason':reason,'Opening_rows':len(d),'Valid_opening_seconds_if_measured':valid_seconds,
        'Observed_expiries':len(exp),'Tick_size':before.st_size,'Tick_mtime_ns':str(before.st_mtime_ns),'Original_exception':last,
        'File_first_source_time':str(min(pf.metadata.row_group(i).column(j).statistics.min for i in range(pf.metadata.num_row_groups))),
        'File_last_source_time':str(max(pf.metadata.row_group(i).column(j).statistics.max for i in range(pf.metadata.num_row_groups)))}


def main():
    manifest=OUT/'run_manifest.json';run=json.loads(manifest.read_text())
    assert run['Status'] in ('errors','complete'),'Wait for the raw replay to finish'
    for name,digest in run['Inputs'].items():assert hashlib.sha256((ROOT/name).read_bytes()).hexdigest()==digest
    failures=run.get('Failed_dates',[])
    if not failures:print('No raw errors need eligibility resolution');return
    plan=[]
    for date in failures:
        path=OUT/'days'/(date+'.pkl');record=pickle.load(path.open('rb'))
        plan.append((date,record,audit(date,record['Error']),path.read_bytes()))
    original=OUT/'raw_run_manifest.json';assert not original.exists(),'Do not overwrite the original raw run manifest'
    shutil.copy2(manifest,original);raw=OUT/'raw_ineligible_days';raw.mkdir(exist_ok=True);rows=[]
    for date,record,resolution,original_bytes in plan:
        path=OUT/'days'/(date+'.pkl');assert path.read_bytes()==original_bytes,'Checkpoint changed during audit'
        shutil.copy2(path,raw/path.name)
        policy_rows=[]
        for structure,gate in POLICIES:
            row=empty_policy_day(date,structure,gate);row['Reason']='Ineligible: '+resolution['Eligibility_reason'];policy_rows.append(row)
        replacement={'Date':date,'Rows':policy_rows,'Seconds':record['Seconds'],'Error':'',
            'Tick_size':resolution['Tick_size'],'Tick_mtime_ns':resolution['Tick_mtime_ns'],
            'Eligibility_resolution':resolution['Eligibility_reason'],'Raw_exception_SHA256':hashlib.sha256(original_bytes).hexdigest()}
        tmp=path.with_suffix('.tmp')
        with tmp.open('wb') as stream:pickle.dump(replacement,stream)
        tmp.replace(path);rows.append(resolution)
    frame=pd.DataFrame(rows);frame.to_csv(OUT/'eligibility_audit.csv',index=False)
    frame.to_excel(OUT/'eligibility_audit.xlsx',index=False)
    run.update(Status='complete',Failed_dates=[],Raw_failed_dates=failures,
        Eligibility_resolution='Known pre-entry requirements independently rechecked; original exceptions preserved in raw_ineligible_days',
        Eligibility_auditor_SHA256=hashlib.sha256(Path(__file__).read_bytes()).hexdigest())
    manifest.write_text(json.dumps(run,indent=2)+'\n');print(frame.to_string(index=False))


if __name__=='__main__':main()
