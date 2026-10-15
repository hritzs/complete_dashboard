"""Independent checks of saved entry-study artifacts, without evaluator imports."""
import hashlib
import json
import pickle
from pathlib import Path
import numpy as np
import pandas as pd

ROOT=Path(__file__).resolve().parent
OUT=ROOT/'output/tick_entry_study'


def main():
    d=pd.read_csv(OUT/'daily_comparison.csv',keep_default_na=False)
    f=pd.read_csv(OUT/'features.csv')
    s=pd.read_csv(OUT/'summary.csv')
    raw=pd.DataFrame([pickle.load(p.open('rb')) for p in sorted((ROOT/'output/backtest_relax/run_027bf0249b/days').glob('*.pkl'))]).sort_values('Date')
    assert len(d)==len(raw)==1430 and len(f)==720
    assert d.Date.tolist()==raw.Date.tolist()
    base=raw.Net_PnL.fillna(0.).to_numpy(float)
    np.testing.assert_allclose(d.Flat_PnL,base,rtol=0,atol=1e-10)
    assert len(d[d.Leg.isin(['CW','NW'])])==799
    valid=f.Valid.fillna(False).astype(bool)
    assert (pd.to_datetime(f.loc[valid,'Max_feature_tick'])<pd.to_datetime(f.loc[valid,'Decision_time'])).all()
    # All models must use only prior years; reproduce recorded quantile values.
    th=pd.read_csv(OUT/'training_thresholds.csv')
    features=raw.merge(f.drop(columns='Entry_Time'),on='Date',how='left')
    for r in th.to_dict('records'):
        hist=features[features.Date.lt(f"{r['Year']}-01-01")&features.Leg.eq('CW')&features.Valid.fillna(False).astype(bool)]
        if r['Fit_scope']=='stage_DTE': hist=hist[hist.Stage.eq(r['Stage'])&hist.DTE_idx.eq(r['DTE_idx'])]
        x=hist[r['Feature']].dropna().to_numpy(float)
        assert len(x)==r['Fit_n'] and r['Fit_max_date']<f"{r['Year']}-01-01"
        if len(x): np.testing.assert_allclose(np.quantile(x,.9),r['Threshold90'],atol=1e-10)
    rules=s.Policy.unique().tolist()
    for name in rules:
        p=d[name+'_PnL'].to_numpy(float)
        delta=d[name+'_Delta'].to_numpy(float)
        np.testing.assert_allclose(p-base,delta,atol=1e-10)
        action=d[name+'_Action']
        skip=action.eq('skip').to_numpy(); delay=action.eq('delay').to_numpy()
        assert (p[skip]==0).all()
        untouched=(~d.Leg.eq('CW')|d.Date.lt('2022-01-01')).to_numpy()
        np.testing.assert_allclose(p[untouched],base[untouched],atol=1e-10)
        np.testing.assert_allclose(p[action.eq('keep')],base[action.eq('keep')],atol=1e-10)
        if name!='Delaycheap': assert not delay.any()
        for i in np.flatnonzero(delay):
            original=d.Entry_Time.iloc[i]
            tag={'09:16':'0917','09:17':'0918'}[original]
            assert d['Signal_'+tag].iloc[i]=='LUT_ON'
            assert d[name+'_Entry_Time'].iloc[i]==tag[:2]+':'+tag[2:]
            np.testing.assert_allclose(p[i],float(d['PnL_'+tag].iloc[i]),atol=1e-10)
        for period,mask in [('All_history',np.ones(len(d),bool)),('Scored_2022plus',d.Date.ge('2022-01-01').to_numpy())]:
            q=p[mask]; running=peak=0.; mdd=0.
            for value in q:
                running+=value; peak=max(peak,running); mdd=max(mdd,peak-running)
            row=s[s.Policy.eq(name)&s.Period.eq(period)].iloc[0]
            np.testing.assert_allclose([q.sum(),mdd,q.min()],[row.Net,row.MDD,row.Worst_day],atol=1e-8)
            assert row.Entries==int(action[mask].isin(['keep','delay']).sum())
    manifest=json.loads((OUT/'manifest.json').read_text())
    for path,digest in manifest['Inputs_sha256'].items():
        assert hashlib.sha256((ROOT/path).read_bytes()).hexdigest()==digest,path
    for name,digest in manifest['Outputs_sha256'].items():
        assert hashlib.sha256((OUT/name).read_bytes()).hexdigest()==digest,name
    result={'Status':'PASS','Days':len(d),'Base_entries':799,'CW_features':len(f),'Valid_CW_features':int(valid.sum()),
        'Policies':len(rules),'Threshold_rows_verified':len(th),
        'Checks':['raw base replay','strict tick cutoffs','prior-only quantiles','NW and warmup invariance',
            'skip and delay gates/PnL','independent sequential DD','counts and attribution','input/output SHA256']}
    (OUT/'audit_result.json').write_text(json.dumps(result,indent=2)+'\n')
    print(json.dumps(result,indent=2))


if __name__=='__main__': main()
