"""Replay immutable old/new GammaShort implementations on identical inputs."""
import os
for key in ('NUMBA_NUM_THREADS','OMP_NUM_THREADS','OPENBLAS_NUM_THREADS','MKL_NUM_THREADS'):os.environ[key]='1'
import argparse,ast,contextlib,gc,hashlib,importlib,io,json,math,pickle,sys,time,traceback
from concurrent.futures import ProcessPoolExecutor,as_completed
from pathlib import Path
import numpy as np
import pandas as pd
import pyarrow as pa

ROOT=Path(__file__).resolve().parent
OUT=ROOT/'output/logic_comparison_20261021'
SRC=OUT/'sources';INPUT=OUT/'inputs'
ARCHIVE=ROOT/'output/backtest_relax/run_027bf0249b/days'
OLD=NEW=None
CTX={}


def build_module():
    tree=ast.parse((SRC/'7DResume_0.75_1.2.py').read_text())
    values={'INPUT_DIR':'/mnt/shared/TickDataIV','OUTPUT_BASE':str(OUT/'runtime'),
        'IDV_PATH':str(INPUT/'daily.csv'),'PREV_STR_PATH':str(INPUT/'daily.csv'),
        'EVENT_PATH':str(INPUT/'events.xlsx'),'LUT_DIR':str(INPUT/'luts_new'),'SAVE_DAILY_FILES':False}
    for node in tree.body:
        if isinstance(node,ast.Assign):
            for target in node.targets:
                if isinstance(target,ast.Name) and target.id in values:node.value=ast.Constant(values[target.id])
        if isinstance(node,ast.FunctionDef) and node.name=='worker_v_curve_evaluator':
            for part in ast.walk(node):
                if isinstance(part,ast.For) and isinstance(part.target,ast.Tuple) and any(isinstance(v,ast.Name) and v.id=='row' for v in part.target.elts):
                    body=[];inserted=False;stopped=False
                    for statement in part.body:
                        body.append(statement)
                        if isinstance(statement,ast.Assign) and any(isinstance(x,ast.Name) and x.id=='t_idx' for x in statement.targets):
                            body.extend(ast.parse('if _COMPARE_FAST and not _COMPARE_LUTS[t_idx][coord]:\n    continue').body);inserted=True
                        if isinstance(statement,ast.Expr) and isinstance(statement.value,ast.Call) and isinstance(statement.value.func,ast.Attribute) and statement.value.func.attr=='append' and any(isinstance(x,ast.Name) and x.id=='data_point' for x in statement.value.args):
                            body.extend(ast.parse('if _COMPARE_FAST:\n    break').body);stopped=True
                    if inserted:
                        assert stopped;part.body=body
    tree.body.insert(0,ast.Assign(targets=[ast.Name(id='_COMPARE_FAST',ctx=ast.Store())],value=ast.Constant(True)))
    ast.fix_missing_locations(tree)
    target=SRC/'comparison_new_engine.py';target.write_text(ast.unparse(tree)+'\n')
    return target


def initialize():
    global OLD,NEW,CTX
    pa.set_cpu_count(1);pa.set_io_thread_count(1)
    sys.path.insert(0,str(SRC))
    OLD=importlib.import_module('Archived_GammaShort_Backtest_Final')
    NEW=importlib.import_module('comparison_new_engine')
    OLD.DAILY_CSV=str(INPUT/'daily.csv');OLD.DAY_INPUTS='daily_csv';OLD.LUT_DIR=str(INPUT/'luts_new')
    OLD.EVENT_PATH=str(INPUT/'events.xlsx');OLD.EVENT_PATH_FALLBACK=str(INPUT/'events.xlsx')
    OLD.RELAX=False;OLD.TICK_LAYOUT='combined'
    luts=OLD.load_cw_luts(str(INPUT/'luts_new'));nwl=OLD.load_nw_lut(str(INPUT/'luts_new'))
    latest=[NEW.load_lut_from_csv(str(INPUT/'luts_new'/name)) for name in OLD.STAGE_FILES]
    latest_nw=NEW.load_nw_lut_from_csv(str(INPUT/'luts_new/NW_LUT.csv'))
    for a,b in zip(luts,latest):np.testing.assert_array_equal(a,b)
    np.testing.assert_array_equal(nwl,latest_nw);NEW._COMPARE_LUTS=latest
    old_inputs=OLD.load_day_inputs();events=OLD.load_event_dates()
    OLD._init_worker(dict(luts=luts,nwl=nwl,inputs=old_inputs,events=events))
    daily=pd.read_csv(INPUT/'daily.csv');daily.Date=pd.to_datetime(daily.Date);daily=daily.sort_values('Date').reset_index(drop=True)
    prev=daily.copy();prev.PrevDate=pd.to_datetime(prev.PrevDate);prev.Prev2Date=pd.to_datetime(prev.Prev2Date)
    gap=(prev.PrevDate-prev.Prev2Date).dt.days>1
    target=np.where(gap,prev.Adj_IV_Chg,prev.IV_Chg);denom=np.where(gap,prev.Prev2_Adj_IV,prev.Prev2_IV)
    normalized=target*.1/np.where(denom==0,np.nan,denom)
    lookup={}
    for i,row in prev.iterrows():
        lookup[row.Date.strftime('%Y-%m-%d')]={'Straddle':float(row.Straddle),'Normalized_Adj_Chg':float(normalized[i]),
            'Yesterday_IV':float(row.Adj_IV),'Future':float(row.Future),
            'NW_Adj_IV':float(row.get('NW_Adj_IV',np.nan)),'NW_Expiry':str(row.NW_Expiry) if pd.notna(row.get('NW_Expiry')) else None}
    CTX={'luts':latest,'nwl':latest_nw,'daily':daily,'lookup':lookup,'events':events,
         'old_native_cw':OLD.load_cw_ticks,'old_native_nw':OLD.load_nw_ticks,'new_native':NEW._read_ticks}


def quiet_initialize():
    with contextlib.redirect_stdout(io.StringIO()):initialize()


def new_result(day,date):
    base={'Date':date,'Leg':'','Net_PnL':0.,'Note':'no_trade'}
    if day is None:return base
    comfort=day.get('dte_idx') is not None and day['dte_idx']<=1
    hit=None
    for scan in day['scans']:
        if scan['time_hhmm']>NEW.NW_AFTER_HHMM:break
        if CTX['luts'][scan['t_idx']][scan['coord']]:hit=('CW',scan);break
    if hit is None and NEW.NW_ENABLED and comfort:
        nw=day.get('nw')
        if nw and nw.get('cell') is not None and CTX['nwl'][nw['cell']] and np.isfinite(nw.get('pnl',np.nan)):hit=('NW',nw)
    if hit is None:
        for scan in day['scans']:
            if scan['time_hhmm']<=NEW.NW_AFTER_HHMM:continue
            if CTX['luts'][scan['t_idx']][scan['coord']]:hit=('CW',scan);break
    if hit is None:return {**base,'Event':day['is_event'],'DTE_idx':day['dte_idx']}
    leg,obj=hit
    if leg=='CW':
        pc=day['pnl_cache'][obj['entry_idx']]
        return {'Date':date,'Event':day['is_event'],'DTE_idx':day['dte_idx'],'Leg':'CW','Entry_Time':obj['time_str'],
            'Stage':OLD.STAGE_NAMES[obj['t_idx']],'Cell':str(obj['coord']),'Net_PnL':obj['pnl'],'Gross_PnL':pc['gross'],
            'Entry_Cost':pc['entry_cost'],'Hedge_Cost':pc['hedge_cost'],'Hedge_Notional':pc['hedge_notional'],
            'Strike':pc['strike'],'Entry_U':pc['entry_u'],'Exit_Time':pc['exit_time'],'Exit_Reason':pc['exit_reason'],
            'Trades':obj['trades'],'SL_bps':pc['sl_bps'],'TP_bps':pc['tp_bps'],
            'Min_Hedges':pc['min_hedges'],'Intra_Hedges':pc['intra_hedges'],'Note':''}
    return {'Date':date,'Event':day['is_event'],'DTE_idx':day['dte_idx'],'Leg':'NW','Entry_Time':'09:21','Stage':'NW',
        'Cell':str(obj['cell']),'Net_PnL':obj['pnl'],'Gross_PnL':obj['pnl'],'Entry_Cost':0.,'Hedge_Cost':0.,
        'Hedge_Notional':np.nan,'Strike':obj.get('strike'),'Entry_U':obj.get('entry_u'),
        'Exit_Time':obj.get('exit_time'),'Exit_Reason':obj.get('exit_reason'),'Trades':1.,'Note':''}


def run_new(path,date,fast=True):
    scratch=OUT/'runtime'/str(os.getpid());scratch.mkdir(parents=True,exist_ok=True)
    NEW._COMPARE_FAST=fast
    log=io.StringIO()
    with contextlib.redirect_stdout(log):
        _,error=NEW.worker_v_curve_evaluator(([str(path)],CTX['daily'],CTX['lookup'],CTX['events'],0,str(scratch)))
    if error:raise RuntimeError(error)
    if '[WARN]' in log.getvalue():raise RuntimeError(log.getvalue())
    dest=scratch/'temp_cache_0.pkl';cache=pickle.load(dest.open('rb'));dest.unlink()
    return new_result(cache.get(date),date)


def one_day(date,pilot=False):
    started=time.monotonic();base=pd.Timestamp(date);path=Path('/mnt/shared/TickDataIV')/base.strftime('%d%m%Y')/'NIFTY.parquet'
    if not path.exists():path=path.with_suffix('.feather')
    try:
        before=path.stat();raw=CTX['new_native'](str(path));raw.Type=raw.Type.astype(str)
        options=raw[raw.Type.isin(['CE','PE'])];expiry=pd.to_datetime(options.Expiry)
        exps=sorted(e for e in expiry.unique() if pd.Timestamp(e).normalize()>=base.normalize())
        cw=options[expiry==exps[0]].reset_index(drop=True)
        nw=options[expiry==exps[1]].reset_index(drop=True) if len(exps)>1 else None
        if pilot:
            native,ce,ne=CTX['old_native_cw'](str(path),base)
            pd.testing.assert_frame_equal(native,cw[native.columns],check_dtype=True)
            assert ce==pd.Timestamp(exps[0])
            if ne is not None:
                nt=CTX['old_native_nw'](base.strftime('%d%m%Y'),str(path),ne)
                pd.testing.assert_frame_equal(nt,nw[nt.columns],check_dtype=True)
        OLD.load_cw_ticks=lambda *_:(cw.copy(),pd.Timestamp(exps[0]),pd.Timestamp(exps[1]) if len(exps)>1 else None)
        OLD.load_nw_ticks=lambda *_:nw.copy() if nw is not None else None
        NEW._read_ticks=lambda *_:raw.copy()
        old=OLD.process_day(str(path));new=run_new(path,date,True)
        if old.get('Note','').startswith(('ERROR','exception:')) or old.get('Error'):raise RuntimeError(str(old))
        if pilot:
            full=run_new(path,date,False)
            for key in ('Leg','Entry_Time','Strike','Exit_Time','Exit_Reason','Cell'):
                assert full.get(key)==new.get(key),(date,key,full.get(key),new.get(key))
            np.testing.assert_allclose(full['Net_PnL'],new['Net_PnL'],atol=1e-9,rtol=0)
        after=path.stat();assert (before.st_size,before.st_mtime_ns)==(after.st_size,after.st_mtime_ns),'Tick file changed during replay'
        result={'Date':date,'Old':old,'New':new,'Seconds':time.monotonic()-started,'Tick_size':before.st_size,
            'Tick_mtime_ns':str(before.st_mtime_ns),'Pilot_full_parity':pilot,'Error':''}
        return result
    except Exception:
        return {'Date':date,'Error':traceback.format_exc(),'Seconds':time.monotonic()-started}
    finally:
        OLD.load_cw_ticks=CTX['old_native_cw'];OLD.load_nw_ticks=CTX['old_native_nw'];NEW._read_ticks=CTX['new_native'];gc.collect()


def main():
    parser=argparse.ArgumentParser();parser.add_argument('--pilot',action='store_true');parser.add_argument('--workers',type=int,default=20);args=parser.parse_args()
    OUT.mkdir(parents=True,exist_ok=True);build_module()
    archive=pd.DataFrame([pickle.load(f.open('rb')) for f in sorted(ARCHIVE.glob('*.pkl'))]).sort_values('Date')
    dates=archive.Date.tolist()
    if args.pilot:
        early=archive[archive.Leg.eq('CW')&archive.Stage.eq('09:16')].Date.iloc[0]
        late=archive[archive.Leg.eq('CW')&archive.Stage.eq('09:20+')].Date.iloc[0]
        nw=archive[archive.Leg.eq('NW')].Date.iloc[0]
        delta0=archive[archive.Leg.eq('CW')&archive.DTE_idx.eq(0)].sort_values('Net_PnL').Date.iloc[0]
        delta1=archive[archive.Leg.eq('CW')&archive.DTE_idx.eq(1)].sort_values('Net_PnL').Date.iloc[0]
        dates=list(dict.fromkeys([early,late,nw,archive[archive.Leg.fillna('').eq('')].Date.iloc[0],delta0,delta1]))
    dest=OUT/('pilot_days' if args.pilot else 'days');dest.mkdir(exist_ok=True)
    inputs=[Path(__file__),ROOT/'LOGIC_COMPARISON_SPEC.md',*SRC.glob('*.py'),*INPUT.rglob('*')]
    inputs=[p for p in inputs if p.is_file()]
    hashes={str(p.relative_to(ROOT)):hashlib.sha256(p.read_bytes()).hexdigest() for p in inputs}
    manifest=OUT/('pilot_manifest.json' if args.pilot else 'run_manifest.json')
    if manifest.exists():assert json.loads(manifest.read_text())['Inputs']==hashes,'Source/input change: use fresh replay output'
    manifest.write_text(json.dumps({'Inputs':hashes,'Dates':dates,'Workers':args.workers,'Status':'running'},indent=2)+'\n')
    todo=[]
    for day in dates:
        f=dest/(day+'.pkl')
        if not f.exists() or pickle.load(f.open('rb')).get('Error'):todo.append(day)
    print('Replay days',len(dates),'remaining',len(todo),flush=True)
    with ProcessPoolExecutor(max_workers=args.workers,initializer=quiet_initialize) as pool:
        futures={pool.submit(one_day,date,args.pilot):date for date in todo}
        for i,future in enumerate(as_completed(futures),1):
            result=future.result();date=result['Date'];f=dest/(date+'.pkl');tmp=f.with_suffix('.tmp')
            with tmp.open('wb') as stream:pickle.dump(result,stream)
            tmp.replace(f)
            if args.pilot or i%20==0 or result.get('Error'):
                print(i,'/',len(todo),date,'error',bool(result.get('Error')),'seconds',round(result['Seconds'],1),
                    'net',[(k,result.get(k,{}).get('Net_PnL')) for k in ('Old','New')],flush=True)
    failures=[day for day in dates if pickle.load((dest/(day+'.pkl')).open('rb')).get('Error')]
    data=json.loads(manifest.read_text());data.update(Status='errors' if failures else 'complete',Failed_dates=failures)
    manifest.write_text(json.dumps(data,indent=2)+'\n')
    print('Finished; errors',failures,flush=True)
    if failures:raise SystemExit(1)


if __name__=='__main__':main()
