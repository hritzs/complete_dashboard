"""Replay an adapter-only empty-expiry failure through both native engines."""
import hashlib,json,pickle,shutil,time
from pathlib import Path
import numpy as np
import pandas as pd
import compare_latest_logic as replay


def main():
    replay.quiet_initialize();proofs=[]
    for checkpoint in sorted((replay.OUT/'days').glob('*.pkl')):
        record=pickle.load(checkpoint.open('rb'));error=record.get('Error','')
        if not error:continue
        if 'cw=options[expiry==exps[0]]' not in error or not error.rstrip().endswith('IndexError: list index out of range'):
            raise RuntimeError('Unexpected replay error requires separate investigation: '+checkpoint.stem)
        date=record['Date'];base=pd.Timestamp(date);path=Path('/mnt/shared/TickDataIV')/base.strftime('%d%m%Y')/'NIFTY.parquet'
        began=time.monotonic();before=path.stat()
        cw,ce,ne=replay.CTX['old_native_cw'](str(path),base)
        assert cw.empty and ce is None and ne is None,'Not an empty eligible-expiry day'
        old=replay.OLD.process_day(str(path));new=replay.run_new(path,date,True)
        assert old.get('Leg','')==new.get('Leg','')=='' and old.get('Note')=='empty_ticks'
        np.testing.assert_allclose([old.get('Net_PnL',0.),new['Net_PnL']],[0.,0.],atol=0,rtol=0)
        after=path.stat();assert (before.st_size,before.st_mtime_ns)==(after.st_size,after.st_mtime_ns)
        raw=replay.OUT/'empty_expiry_raw_errors';raw.mkdir(exist_ok=True);saved=raw/checkpoint.name
        assert not saved.exists(),'Do not overwrite an original error checkpoint';shutil.copy2(checkpoint,saved)
        proof={'Date':date,'Mode':'Both original native loaders; eligible expiry set empty',
            'Fallback_code_SHA256':hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
            'Original_error_SHA256':hashlib.sha256(saved.read_bytes()).hexdigest()}
        fixed={'Date':date,'Old':old,'New':new,'Seconds':time.monotonic()-began,
            'Tick_size':before.st_size,'Tick_mtime_ns':str(before.st_mtime_ns),'Pilot_full_parity':False,
            'Error':'','Native_empty_expiry_fallback':True,**{k:v for k,v in proof.items() if k!='Date'}}
        tmp=checkpoint.with_suffix('.tmp')
        with tmp.open('wb') as stream:pickle.dump(fixed,stream)
        tmp.replace(checkpoint);proofs.append(proof);print(date,old.get('Note'),new.get('Note'),flush=True)
    if proofs:
        path=replay.OUT/'native_fallbacks.json';prior=json.loads(path.read_text()) if path.exists() else []
        path.write_text(json.dumps(prior+proofs,indent=2)+'\n')


if __name__=='__main__':main()
