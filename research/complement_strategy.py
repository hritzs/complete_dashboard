"""Fixed, causal long-option screen; independent of GammaShort entry selection."""
import os
for key in ('OMP_NUM_THREADS','OPENBLAS_NUM_THREADS','MKL_NUM_THREADS'):os.environ[key]='1'
import argparse,hashlib,json,math,pickle,time,traceback
from concurrent.futures import ThreadPoolExecutor,as_completed
from pathlib import Path
import numpy as np
import pandas as pd
import pyarrow as pa
import pyarrow.parquet as pq

ROOT=Path(__file__).resolve().parent
OUT=ROOT/'output/complement_strategy'
ARCHIVE=ROOT/'output/backtest_relax/run_027bf0249b/days'
GRID=np.arange(9*3600+15*60,11*3600+15*60+1)
POLICIES=tuple((s,g) for g in ('Breakout','LowerIV') for s in ('Directional','Tilted','Balanced'))
BUDGET=100.;FEE=1.;HAIRCUT=1e-4


def clock(second):
    return f'{second//3600:02d}:{second%3600//60:02d}:{second%60:02d}'


def availability(stamps):
    # Source dates are placeholders; each labelled second is available one later.
    return stamps.dt.hour*3600+stamps.dt.minute*60+stamps.dt.second+1


def carry(values):
    return pd.Series(values,index=GRID).ffill(limit=3).to_numpy(float)


def direction_signal(underlying,decision,high,low):
    p=decision-GRID[0];current=underlying[p-1]
    hist=underlying[max(0,p-300):p];valid=hist[np.isfinite(hist)]
    if len(valid)<240 or not np.isfinite(current) or not high>low:return 0,np.nan
    travel=np.abs(np.diff(valid)).sum();er=abs(valid[-1]-valid[0])/travel if travel else 0.
    if er<.30:return 0,er
    span=high-low
    if current>high+.1*span and valid[-1]>valid[0]:return 1,er
    if current<low-.1*span and valid[-1]<valid[0]:return -1,er
    return 0,er


def lower_iv(book,decision):
    p=decision-GRID[0];iv=(book['CE']['iv']+book['PE']['iv'])/2
    current=iv[p-1];ref=iv[max(0,p-300):p-1];ref=ref[np.isfinite(ref)]
    if not np.isfinite(current) or len(ref)<60:return False,np.nan
    rank=float(((ref<current).sum()+.5*(ref==current).sum())/len(ref))
    return rank<=.5,rank


def leg_quantities(structure,direction):
    if structure=='Directional':return {'CE':1.} if direction>0 else {'PE':1.}
    if structure=='Tilted':return {'CE':2. if direction>0 else 1.,'PE':1. if direction>0 else 2.}
    return {'CE':1.,'PE':1.}


def greek_values(S,K,days,ivs,quantities,scale):
    total=np.zeros(3);T=max(days/365,1e-8)
    for side,q in quantities.items():
        sig=ivs[side]
        if not np.isfinite(sig):return [np.nan]*3
        d1=(math.log(S/K)+.5*sig*sig*T)/(sig*math.sqrt(T));phi=math.exp(-.5*d1*d1)/math.sqrt(2*math.pi)
        delta=.5*(1+math.erf(d1/math.sqrt(2)))-(side=='PE')
        total+=q*np.array([delta,phi/(S*sig*math.sqrt(T)),S*phi*math.sqrt(T)*.01])
    return (total*scale).tolist()


def quote_book(frame,K):
    out={}
    for side in ('CE','PE'):
        d=frame[frame.Type.eq(side)].sort_values('available').drop_duplicates('available',keep='last').set_index('available').reindex(GRID)
        bid=d.Bid.to_numpy(float);ask=d.Ask.to_numpy(float)
        good=np.isfinite(bid)&np.isfinite(ask)&(bid>0)&(ask>=bid)&(d.BidQty.to_numpy(float)>0)&(d.AskQty.to_numpy(float)>0)
        bid=np.where(good,bid,np.nan);ask=np.where(good,ask,np.nan)
        iv=pd.to_numeric(d.AskIV,errors='coerce').to_numpy(float);iv=np.where(good&(iv>=.01)&(iv<=3),iv,np.nan)
        out[side]={'raw_bid':bid,'raw_ask':ask,'bid':carry(bid),'ask':carry(ask),'iv':carry(iv),
            'days':carry(pd.to_numeric(d.DTE,errors='coerce').to_numpy(float))}
    return out


def execute(book,quantities,decision,underlying,K):
    # Both legs use the same source second; no future average or backfill.
    entry=None
    for second in range(decision+2,decision+6):
        p=second-GRID[0]
        if all(np.isfinite(book[s]['raw_ask'][p]) for s in quantities) and np.isfinite(underlying[p]):entry=second;break
    if entry is None:return None
    ep=entry-GRID[0];S=underlying[ep];norm=25000./S
    premium=sum(q*book[s]['raw_ask'][ep] for s,q in quantities.items())*(1+HAIRCUT)
    size=BUDGET/(premium*norm);fee=FEE*sum(quantities.values())*size
    deadline=entry+900;trigger=None;reason=None
    for second in range(entry+1,deadline+1):
        p=second-GRID[0]
        if second==deadline:trigger=second;reason='Time15m';break
        bids=[book[s]['bid'][p] for s in quantities]
        if not all(np.isfinite(bids)):continue
        liquidation=sum(q*book[s]['bid'][p] for s,q in quantities.items())*(1-HAIRCUT)
        if liquidation<=.7*premium:trigger=second;reason='Premium_stop30';break
    for second in range(trigger+1,int(GRID[-1])+1):
        p=second-GRID[0]
        if all(np.isfinite(book[s]['raw_bid'][p]) for s in quantities):
            liquidation=sum(q*book[s]['raw_bid'][p] for s,q in quantities.items())*(1-HAIRCUT)
            gross=(liquidation-premium)*norm*size
            ivs={s:book[s]['iv'][ep] for s in quantities};days=float(np.nanmean([book[s]['days'][ep] for s in quantities]))
            greeks=greek_values(S,K,days,ivs,quantities,norm*size)
            return {'Traded':True,'Entry_Time':clock(entry),'Entry_quote_label':clock(entry-1),'Exit_Time':clock(second),
                'Decision_Time':clock(decision),'Exit_trigger_time':clock(trigger),'Exit_delay_seconds':second-trigger,
                'Exit_Reason':reason,'Strike':K,'Entry_U':S,'DTE_calendar':days,'Premium_budget':BUDGET,
                'Entry_debit_points':premium,'Exit_credit_points':liquidation,'Normalized_quantity':size,
                'CE_contract_units':quantities.get('CE',0)*norm*size,'PE_contract_units':quantities.get('PE',0)*norm*size,
                'Gross_PnL':gross,'Fee':fee,'Net_PnL':gross-fee,'Double_fee_net':gross-2*fee,
                'Delta_at_entry':greeks[0],'Gamma_at_entry':greeks[1],'Vega_1pct_at_entry':greeks[2],
                'Continuous_quantity_research':True,'Reason':''}
    raise RuntimeError(f'Entered at {clock(entry)} but no synchronized executable exit before {clock(int(GRID[-1]))}')


def empty_policy_day(date,structure,gate):
    return {'Date':date,'Policy':structure+' '+gate,'Traded':False,'Net_PnL':0.,'Double_fee_net':0.,
        'Reason':'No filled qualifying entry','Breakout_minutes':0,'Missing_strike_minutes':0,'IV_blocked_minutes':0,'Unfilled_attempts':0}


def simulate_day(date,no_signal_fast=True):
    began=time.monotonic();path=Path('/mnt/shared/TickDataIV')/pd.Timestamp(date).strftime('%d%m%Y')/'NIFTY.parquet'
    try:
        before=path.stat();pf=pq.ParquetFile(path);idx=pf.schema_arrow.names.index('Time_dt')
        anchor=pd.Timestamp(pf.metadata.row_group(0).column(idx).statistics.min).normalize()
        start=anchor+pd.Timedelta(seconds=int(GRID[0])-1);end=anchor+pd.Timedelta(seconds=int(GRID[-1]))
        filters=[('Time_dt','>=',start),('Time_dt','<',end)]
        u=pd.read_parquet(path,columns=['Time_dt','Expiry','UnderlyingLTP'],filters=filters)
        u['available']=availability(u.Time_dt)
        opening=u[u.available.lt(9*3600+30*60)]
        expiries=sorted(e for e in pd.to_datetime(opening.Expiry).unique() if pd.Timestamp(e).normalize()>=pd.Timestamp(date)+pd.Timedelta(days=2))
        if not expiries:raise ValueError('No expiry observed before09:30 at least2calendar days ahead')
        expiry=pd.Timestamp(expiries[0]);u=u[pd.to_datetime(u.Expiry).eq(expiry)]
        u=u[np.isfinite(u.UnderlyingLTP)&u.UnderlyingLTP.gt(0)].sort_values('available').drop_duplicates('available',keep='last')
        raw=u.set_index('available').UnderlyingLTP.reindex(GRID).to_numpy(float);underlying=carry(raw)
        op=underlying[GRID<9*3600+30*60];op=op[np.isfinite(op)]
        if len(op)<600:raise ValueError('Insufficient opening underlying coverage')
        high=float(op.max());low=float(op.min());del u,opening
        signals=[direction_signal(underlying,t,high,low)[0] for t in range(9*3600+31*60,10*3600+30*60+1,60)]
        if no_signal_fast and not any(signals):
            after=path.stat();assert (before.st_size,before.st_mtime_ns)==(after.st_size,after.st_mtime_ns),'Tick file changed during read'
            return {'Date':date,'Rows':[empty_policy_day(date,s,g) for s,g in POLICIES],
                'Tick_size':before.st_size,'Tick_mtime_ns':str(before.st_mtime_ns),'Seconds':time.monotonic()-began,'Error':'','No_signal_fast_path':True}
        columns=['Time_dt','Strike','Type','Bid','Ask','BidQty','AskQty','AskIV','DTE']
        q=pd.read_parquet(path,columns=columns,filters=filters+[('Expiry','==',expiry)])
        q['Type']=q.Type.astype(str);q['available']=availability(q.Time_dt)
        groups={float(k):v for k,v in q.groupby('Strike')};cache={};rows=[]
        for structure,gate in POLICIES:
            row=empty_policy_day(date,structure,gate)
            for decision in range(9*3600+31*60,10*3600+30*60+1,60):
                direction,er=direction_signal(underlying,decision,high,low)
                if direction==0:continue
                row['Breakout_minutes']+=1
                K=float(round(underlying[decision-GRID[0]-1]/50)*50)
                if K not in groups:row['Missing_strike_minutes']+=1;continue
                if K not in cache:cache[K]=quote_book(groups[K],K)
                book=cache[K];admitted,rank=lower_iv(book,decision)
                if gate=='LowerIV' and not admitted:row['IV_blocked_minutes']+=1;continue
                result=execute(book,leg_quantities(structure,direction),decision,underlying,K)
                if result is None:row['Unfilled_attempts']+=1;continue
                row.update(result,Direction=direction,Efficiency_ratio=er,AskIV_percentile=rank,Expiry=str(expiry.date()));break
            rows.append(row)
        after=path.stat();assert (before.st_size,before.st_mtime_ns)==(after.st_size,after.st_mtime_ns),'Tick file changed during read'
        return {'Date':date,'Rows':rows,'Tick_size':before.st_size,'Tick_mtime_ns':str(before.st_mtime_ns),'Seconds':time.monotonic()-began,'Error':''}
    except Exception:return {'Date':date,'Rows':[],'Seconds':time.monotonic()-began,'Error':traceback.format_exc()}


def pilot_day(date):
    fast=simulate_day(date);full=simulate_day(date,False)
    assert not fast['Error'] and not full['Error'],(fast['Error'],full['Error'])
    pd.testing.assert_frame_equal(pd.DataFrame(fast['Rows']),pd.DataFrame(full['Rows']),check_dtype=False,atol=1e-10,rtol=0)
    fast['Full_no_signal_parity']=True
    return fast


def main():
    parser=argparse.ArgumentParser();parser.add_argument('--pilot',action='store_true');parser.add_argument('--workers',type=int,default=6);args=parser.parse_args()
    pa.set_cpu_count(max(1,min(8,args.workers)));pa.set_io_thread_count(max(1,args.workers))
    dates=['2021-01-04','2024-02-29','2026-10-13'] if args.pilot else [p.stem for p in sorted(ARCHIVE.glob('*.pkl'))]
    dest=OUT/('pilot_days' if args.pilot else 'days');dest.mkdir(parents=True,exist_ok=True)
    manifest=OUT/('pilot_manifest.json' if args.pilot else 'run_manifest.json')
    inputs={p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in [Path(__file__),ROOT/'COMPLEMENT_STRATEGY_SPEC.md']}
    if manifest.exists():assert json.loads(manifest.read_text())['Inputs']==inputs,'Source changed: move pilot/run outputs before rerun'
    state={'Inputs':inputs,'Dates':dates,'Status':'running','Premium_budget':BUDGET,'Workers':args.workers}
    manifest.write_text(json.dumps(state,indent=2)+'\n')
    todo=[date for date in dates if not (dest/(date+'.pkl')).exists() or pickle.load((dest/(date+'.pkl')).open('rb')).get('Error')]
    def save(result):
        path=dest/(result['Date']+'.pkl');tmp=path.with_suffix('.tmp')
        with tmp.open('wb') as stream:pickle.dump(result,stream)
        tmp.replace(path)
    if args.workers==1:
        results=map(pilot_day if args.pilot else simulate_day,todo)
        for result in results:save(result);print(result['Date'],'error',bool(result['Error']),'seconds',round(result['Seconds'],1),flush=True)
    else:
        with ThreadPoolExecutor(max_workers=args.workers) as pool:
            futures=[pool.submit(pilot_day if args.pilot else simulate_day,date) for date in todo]
            for i,future in enumerate(as_completed(futures),1):
                result=future.result();save(result)
                if args.pilot or i%25==0 or result['Error']:print(i,'/',len(todo),result['Date'],'error',bool(result['Error']),'seconds',round(result['Seconds'],1),flush=True)
    errors=[date for date in dates if pickle.load((dest/(date+'.pkl')).open('rb')).get('Error')]
    state.update(Status='errors' if errors else 'complete',Failed_dates=errors);manifest.write_text(json.dumps(state,indent=2)+'\n')
    if errors:raise SystemExit('Replay errors: '+str(errors))


if __name__=='__main__':main()
