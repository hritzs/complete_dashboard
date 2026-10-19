"""Audit saved Monte Carlo paths, selections, bounds and report reconciliations."""
import hashlib,json
from pathlib import Path
import numpy as np
import pandas as pd
from PIL import Image
from monte_carlo_sizing import OUT,ROOT,WF,PATHS,HORIZON,SCENARIOS,path_stats,draw_indices,SEED


def main():
    manifest=json.loads((OUT/'manifest.json').read_text())
    for key,folder in [('Inputs',ROOT),('Outputs',OUT)]:
        for name,digest in manifest[key].items():assert hashlib.sha256((folder/name).read_bytes()).hexdigest()==digest,name
    t=pd.read_csv(OUT/'trade_comparison.csv',float_precision='round_trip');params=pd.read_csv(OUT/'policy_parameters.csv')
    train=t.Year.between(2022,2024);test=t.Year.ge(2025);source=t[test].reset_index(drop=True)
    results=np.load(OUT/'simulated_outcomes.npz');draws=np.load(OUT/'sampled_indices.npz')
    names=results['Names'].tolist();summary=pd.read_csv(OUT/'mc_evaluation.csv')
    errors=[];rows=0
    for name in names:
        rule=params[params.Candidate.eq(name)].iloc[0];sz=t[name+'_size'].to_numpy();warm=t.Year.eq(2021).to_numpy()
        if rule.Family=='Exposure benchmark':expected=np.full(len(t),rule.Boost)
        else:
            strong=t[f'Strong_tax_plus{int(rule.Extra_tax)}'].to_numpy(bool) if pd.notna(rule.Extra_tax) else np.zeros(len(t),bool)
            expected=np.where(t.Dicey.to_numpy(bool),rule.Risk_cut,np.where(strong,rule.Boost,1.))
        expected[warm]=1.;np.testing.assert_array_equal(expected,sz)
        assert ((sz>=.75)&(sz<=1.15)).all()
        np.testing.assert_allclose(t[name+'_PnL'],t.Net_PnL*sz,atol=1e-10,rtol=0)
    # Independent replays using saved common draws, including the training selection draw.
    cases=[('Training',t[train].reset_index(drop=True),'base',results['Training_Net'],results['Training_MDD'])]
    cases +=[(label,source,shock,results['Net'][j],results['MDD'][j]) for j,(label,_,shock) in enumerate(SCENARIOS)]
    for label,pool,shock,net_saved,dd_saved in cases:
        indices=draws[label];assert indices.shape==(PATHS,HORIZON) and (indices>=0).all() and (indices<len(pool)).all()
        block=10 if label=='Training' else next(b for s,b,_ in SCENARIOS if s==label)
        for start in range(0,HORIZON,block):
            length=min(block,HORIZON-start)
            if length>1:assert (np.diff(indices[:,start:start+length],axis=1)==1).all()
        p=pool.Net_PnL.to_numpy()
        if shock=='erosion':p=np.where(p>0,p*.85,p*1.25)-.5
        for k,name in enumerate(names):
            x=p[indices]*pool[name+'_size'].to_numpy()[indices]
            # DD includes the initial zero peak; a first-trade loss must count.
            eq=np.cumsum(x,axis=1);peaks=np.maximum.accumulate(np.column_stack([np.zeros(PATHS),eq]),axis=1)[:,1:]
            net=eq[:,-1];dd=(peaks-eq).max(axis=1)
            np.testing.assert_allclose(net,net_saved[k],atol=1e-9,rtol=0);np.testing.assert_allclose(dd,dd_saved[k],atol=1e-9,rtol=0)
            errors.append(float(max(np.max(abs(net-net_saved[k])),np.max(abs(dd-dd_saved[k])))))
            if label!='Training':
                row=summary[summary.Scenario.eq(label)&summary.Candidate.eq(name)].iloc[0]
                np.testing.assert_allclose([net.mean(),np.quantile(net,.05),np.quantile(dd,.95)],
                    [row.Mean_net,row.Net_p05,row.MDD_p95],atol=1e-9,rtol=0)
            rows+=PATHS
        k=names.index('Fixed 1.15')
        np.testing.assert_allclose(net_saved[k],net_saved[0]*1.15,atol=1e-9,rtol=0)
        np.testing.assert_allclose(dd_saved[k],dd_saved[0]*1.15,atol=1e-9,rtol=0)
    training=pd.read_csv(OUT/'mc_training.csv');budget=training[training.Candidate.eq('Flat size 1')].MDD_p95.iloc[0]
    adaptive=set(params[params.Family.eq('Adaptive')].Candidate)
    selections=json.loads((OUT/'selection.json').read_text())['Policies']
    for objective,group in [('Maximum simulated mean profit',training),('Maximum adaptive mean profit',training[training.Candidate.isin(adaptive)]),
        ('Maximum mean profit at flat tail-DD budget',training[training.MDD_p95.le(budget+1e-9)])]:
        expected=group.sort_values(['Mean_net','MDD_p95'],ascending=[False,True]).iloc[0].Candidate
        assert selections[objective]==expected
    for chart in OUT.glob('*.png'):
        with Image.open(chart) as image:
            image.verify()
        with Image.open(chart) as image:
            assert min(image.size)>400 and np.asarray(image.convert('L')).std()>5
    daily=pd.read_csv(OUT/'daily_pnl.csv');hist=pd.read_csv(OUT/'historical_summary.csv')
    for name in names:
        row=hist[hist.Candidate.eq(name)&hist.Period.eq('Evaluation_2025plus')].iloc[0]
        p=daily.loc[daily.Date.ge('2025-01-01'),name].to_numpy();net,dd,_=path_stats(p[None,:])
        np.testing.assert_allclose([net[0],dd[0]],[row.Net,row.MDD],atol=1e-9,rtol=0)
    result={'Status':'PASS','Policies':len(names),'Evaluation_scenarios':len(SCENARIOS),'Paths_per_scenario':PATHS,
        'Horizon_trades':HORIZON,'Policy_path_replays_including_training':rows,'Maximum_replay_error':max(errors),
        'PNG_charts':len(list(OUT.glob('*.png'))),'Checks':['source and output hashes','all27 size rules and hard bounds',
        'all training/evaluation paths replay','non-wrapping contiguous blocks','paired summaries and constant-size scaling',
        'training-only selections','historical evaluation reconciliation','PNG decoding and nonblank contents']}
    (OUT/'verification.json').write_text(json.dumps(result,indent=2)+'\n')
    manifest['Inputs']['verify_monte_carlo_sizing.py']=hashlib.sha256(Path(__file__).read_bytes()).hexdigest()
    manifest['Inputs']['test_monte_carlo_sizing.py']=hashlib.sha256((ROOT/'test_monte_carlo_sizing.py').read_bytes()).hexdigest()
    manifest['Outputs']={p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in OUT.iterdir() if p.is_file() and p.name!='manifest.json'}
    (OUT/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n');print(json.dumps(result,indent=2),flush=True)


if __name__=='__main__':main()
