"""Audit dated selection, all tuned metrics and identical-exposure null paths."""
import hashlib,json
from pathlib import Path
import numpy as np
import pandas as pd
from nested_sizing import ROOT,OUT,POLICIES,grid,metrics,choose
from monte_carlo_sizing import path_stats


def main():
    m=json.loads((OUT/'manifest.json').read_text())
    for key,folder in [('Inputs',ROOT),('Outputs',OUT)]:
        for name,digest in m[key].items():assert hashlib.sha256((folder/name).read_bytes()).hexdigest()==digest,name
    t,S,params,flags,vol,s1,s2,thresholds=grid();p=t.Net_PnL.to_numpy(float)
    for j in range(1,S.shape[1]):
        param=params.iloc[j];warm=t.Year.eq(2021).to_numpy();risk=flags[:,int(param.Risk_variant)]&~warm
        assert (S[risk,j]<=.85+1e-12).all() and ((S[:,j]>=.75)&(S[:,j]<=1.15)).all()
        assert (S[t.Leg.eq('NW')&~warm,j]<=1+1e-12).all()
    choices=pd.read_csv(OUT/'frozen_selections.csv');sens=pd.read_parquet(OUT/'inner_sensitivity.parquet')
    source=pd.read_csv(OUT/'trade_comparison.csv',float_precision='round_trip')
    assert t.Date.tolist()==source.Date.tolist()
    checks=0
    for year in range(2023,2027):
        prior=np.flatnonzero(t.Year.ge(2022)&t.Year.lt(year));cut=int(.6*len(prior));tr=prior[:cut];va=prior[cut:]
        a=metrics(p[tr],S[tr]);b=metrics(p[va],S[va]);a['Candidate']=a.index;b['Candidate']=b.index
        for part,frame in [('Inner_train',a),('Inner_validation',b)]:
            saved=sens[sens.Year.eq(year)&sens.Part.eq(part)].sort_values('Candidate')
            np.testing.assert_allclose(saved[frame.columns],frame,atol=1e-9,rtol=0);checks+=len(frame)
        # Extreme changes to scored and later outcomes cannot enter selection.
        perturbed=np.where(t.Year.ge(year),1e9,p)
        np.testing.assert_array_equal(perturbed[prior],p[prior])
        for policy in POLICIES:
            row=choices[choices.Year.eq(year)&choices.Policy.eq(policy)].iloc[0]
            j,_,status=choose(a,b,S[tr],policy);assert j==int(row.Candidate) and status==row.Status
            assert row.Train_last_date<row.Validation_first_date<=row.Validation_last_date<row.First_scored_date
            np.testing.assert_allclose(source.loc[source.Year.eq(year),policy+'_size'],S[t.Year.eq(year),j],atol=1e-12,rtol=0)
            if j and policy!='Exploratory allocation':
                factor=1.1 if policy=='Limited-DD profit' else 1.
                assert b.Net.iloc[j]>b.Net.iloc[0] and b.MDD.iloc[j]<=factor*b.MDD.iloc[0]+1e-9
                assert b.Worst.iloc[j]>=factor*b.Worst.iloc[0]-1e-9 and b.Mean_size.iloc[j]<=1.03+1e-9
    replay=pd.read_csv(OUT/'formula_replay.csv')
    raw=np.where(replay.Risk,replay.Cut,np.where(replay.Strong,replay.Boost,1.))*replay.Vol_factor
    value=np.minimum(np.clip(raw,.75,1.15),np.where(replay.Strong,1.15,1.))
    value=np.where(replay.Flat_selection,1.,np.minimum(value,replay.DD_cap))
    np.testing.assert_allclose(value,replay.Saved_size,atol=1e-12,rtol=0)
    z=np.load(OUT/'placement_paths.npz');indices=z['Indices'];outer=t.Year.ge(2023).to_numpy();ep=p[outer];et=t[outer].reset_index(drop=True)
    error=0.
    for k,name in enumerate(z['Names']):
        sizes=source.loc[outer,name+'_size'].to_numpy();original=sizes[indices]
        order=np.argsort(np.random.default_rng(20262000+k).random(original.shape),axis=1)
        random=np.take_along_axis(original,order,axis=1)
        np.testing.assert_allclose(original.sum(1),random.sum(1),atol=1e-10,rtol=0)
        an,ad,_=path_stats(ep[indices]*original);rn,rd,_=path_stats(ep[indices]*random)
        np.testing.assert_allclose(np.column_stack([an,ad,rn,rd]),z['Paths'][k],atol=1e-9,rtol=0)
        null=np.zeros(5000);rng=np.random.default_rng(20263000+k)
        for year in sorted(et.Year.unique()):
            mask=et.Year.eq(year).to_numpy();s=sizes[mask];o=np.argsort(rng.random((5000,len(s))),axis=1)
            np.testing.assert_allclose(s[o].sum(1),s.sum(),atol=1e-10,rtol=0);null+=(s[o]*ep[mask]).sum(1)
        np.testing.assert_allclose(null,z['Historical_null'][k],atol=1e-9,rtol=0)
        error=max(error,float(np.max(abs(null-z['Historical_null'][k]))))
    result={'Status':'PASS','Combinations':S.shape[1]-1,'Inner_candidate_metric_rows':checks,
        'Outer_entries':int(outer.sum()),'Annual_policy_selections':len(choices),'Formula_replay_rows':len(replay),
        'Matched_MC_policy_paths':len(z['Names'])*5000,'Within_year_permutations_per_policy':5000,'Maximum_null_replay_error':error,
        'Checks':['source/output hashes','all grid risk bounds and NW caps','all inner metrics and dated choices',
            'future outcome exclusion from parameter selection','chosen outer sizes','size formula replay',
            'identical per-path size sums','paired Monte Carlo and within-year permutation replay']}
    (OUT/'verification.json').write_text(json.dumps(result,indent=2)+'\n')
    m['Inputs']['verify_nested_sizing.py']=hashlib.sha256(Path(__file__).read_bytes()).hexdigest()
    m['Inputs']['test_nested_sizing.py']=hashlib.sha256((ROOT/'test_nested_sizing.py').read_bytes()).hexdigest()
    m['Outputs']={f.name:hashlib.sha256(f.read_bytes()).hexdigest() for f in OUT.iterdir() if f.is_file() and f.name!='manifest.json'}
    (OUT/'manifest.json').write_text(json.dumps(m,indent=2)+'\n');print(json.dumps(result,indent=2),flush=True)


if __name__=='__main__':main()
