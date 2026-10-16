"""Replay saved annual models without refitting or normalizing scored trades."""
import hashlib
import json
from pathlib import Path
import numpy as np
import pandas as pd

ROOT=Path(__file__).resolve().parent
OUT=ROOT/'output/walkforward_sizing'


def percentile(reference, values):
    reference=np.sort(reference)
    left=np.searchsorted(reference,values,'left');right=np.searchsorted(reference,values,'right')
    return np.where(left==right,left,(left+right+1)/2)/len(reference)


def transform(values, model):
    filled=np.where(np.isfinite(values),values,np.array(model['Median']))
    return np.clip((filled-model['Mean'])/model['Std'],-3,3)


def replay(td, model):
    base=model['Base_model'];score=base['Profit_scores'];cw=td.Leg.eq('CW').to_numpy()
    values=td[score['Core']].to_numpy(float)
    values=np.where(np.isfinite(values),values,score['Median']);s1=np.zeros(len(td))
    for j,bucket in enumerate(score['Buckets']):
        bins=values[:,j] if bucket['Feature'] in ('Table','DTE') else np.searchsorted(bucket['Edges'],values[:,j],'right')
        for level,effect,count in bucket['Effects']:s1+=np.where(bins==level,effect,0.)
    numeric=[j for j,name in enumerate(score['Core']) if name!='Table']
    s2x=np.column_stack([values[:,numeric],*((values[:,0]==i).astype(float) for i in (1,2,3))])
    s2=(s2x-score['S2_mean'])/score['S2_std']@np.array(score['S2_beta'])
    data=td.copy();data['S1_score']=s1;data['S2_score']=s2
    numeric=data[base['Numeric_features']].to_numpy(float)
    numeric[~cw,7:]=np.nan
    x=transform(numeric,base);names=list(base['Numeric_features'])
    for field,levels in [('Stage',['09:17','09:18','09:19','09:20+']),('DTE',[2,3,4,5]),('Weekday',[1,2,3,4,5,6])]:
        vals=pd.to_datetime(td.Date).dt.weekday.to_numpy() if field=='Weekday' else td[field].to_numpy()
        for level in levels:
            x=np.column_stack([x,(cw&(vals==level)).astype(float)]);names.append(f'CW {field}={level}')
    x=np.column_stack([x,(cw&td.Event.fillna(False).to_numpy(bool)).astype(float),(~cw).astype(float)])
    names+=['CW Event','NW'];base_x=x.copy();base_names=names.copy()
    x=np.column_stack([x,np.clip(td.Prior_flat_DD.to_numpy()/75,0,3),np.clip(td.Prior_flat_loss_streak.to_numpy()/3,0,3)])
    names+=['Prev_flat_DD','Prev_flat_loss_streak'];rm=model['Risk_model']
    risk_x=x[:,[names.index(n) for n in rm['Features']]]
    loss=1/(1+np.exp(-np.clip(rm['Logistic_beta'][0]+risk_x@np.array(rm['Logistic_beta'][1:]),-25,25)))
    down=np.maximum(0,rm['Downside_beta'][0]+risk_x@np.array(rm['Downside_beta'][1:]))
    parts=[];missing=np.zeros(len(td),bool)
    for col,reference in rm['Instability_references'].items():
        val=td[col].to_numpy(float);known=cw&np.isfinite(val);missing|=cw&~known
        parts.append(np.where(known,percentile(reference,np.nan_to_num(val)),.5))
    risk=.4*percentile(rm['Loss_reference'],loss)+.4*percentile(rm['Downside_reference'],down)+.2*np.mean(parts,axis=0)
    dicey=(risk>=rm['Threshold'])|missing
    x=np.column_stack([x,transform(np.column_stack([loss,down]),rm['Allocation_risk_transform'])])
    names+=['Risk_Loss_probability','Risk_Expected_downside']
    out={};features={}
    for key,xx,nn in [('WF10',base_x,base_names),('WF11',x,names)]:
        policy=model['Policies'][key];assert policy['Features']==nn
        cap=np.where(cw&td.Stage.eq('09:16').to_numpy(),1.,1.15)
        if key=='WF11':cap=np.where(dicey,np.minimum(cap,.85),cap)
        out[key]=np.clip(1+xx@np.array(policy['Beta'])+policy['Offset'],.75,cap)
        features[key]=xx
    return out,features,loss,down,risk,dicey


def main():
    manifest=json.loads((OUT/'manifest.json').read_text())
    for name,digest in manifest['Inputs'].items():
        path=Path(name) if Path(name).is_absolute() else ROOT/name
        assert hashlib.sha256(path.read_bytes()).hexdigest()==digest,name
    for name,digest in manifest['Outputs'].items():
        assert hashlib.sha256((OUT/name).read_bytes()).hexdigest()==digest,name
    td=pd.read_csv(OUT/'prepared_inputs.csv',dtype={'Source_mtime_ns':str},float_precision='round_trip')
    saved=pd.read_csv(OUT/'trade_comparison.csv',float_precision='round_trip')
    inputs=pd.read_excel(OUT/'walkforward_comparison.xlsx',sheet_name='Model_inputs')
    valid=td.Valid.fillna(False).astype(bool)
    assert (pd.to_datetime(td.loc[valid,'Max_feature_tick'])<pd.to_datetime(td.loc[valid,'Decision_time'])).all()
    for col in ('Reference_prev_date','Reference_prev2_date'):assert (td[col]<td.Date).all()
    for row in td.loc[valid].itertuples():
        source=Path('/mnt/shared/TickDataIV')/pd.Timestamp(row.Date).strftime('%d%m%Y')/'NIFTY.parquet'
        stat=source.stat()
        assert stat.st_size==int(row.Source_size),row.Date
        assert str(stat.st_mtime_ns)==str(row.Source_mtime_ns),row.Date
    # Replay realized-history inputs as a sequential calculation independent of fitting.
    daily=pd.read_csv(OUT/'daily_pnl.csv');baseline=daily['Flat size 1'].to_numpy(float)
    running=peak=streak=0.;history={}
    for day,pnl in zip(daily.Date,baseline):
        history[day]=(peak-running,streak)
        running+=pnl;peak=max(peak,running);streak=streak+1 if pnl<0 else (0 if pnl>0 else streak)
    np.testing.assert_allclose(td.Prior_flat_DD,[history[d][0] for d in td.Date],atol=1e-9)
    np.testing.assert_allclose(td.Prior_flat_loss_streak,[history[d][1] for d in td.Date])
    rows=[];max_error=0.
    for path in sorted(OUT.glob('model_*.json')):
        m=json.loads(path.read_text());year=m['Year'];train=td.Year.lt(year).to_numpy();apply=td.Year.eq(year).to_numpy()
        assert td.loc[train,'Date'].max()==m['Last_training_entry']<m['First_scored_entry']==td.loc[apply,'Date'].min()
        assert int(train.sum())==m['Training_entries'] and int(apply.sum())==m['Scored_entries']
        pred,features,loss,down,risk,dicey=replay(td,m)
        for values,col in [(loss,'Loss_probability'),(down,'Expected_downside'),(risk,'Risk_score')]:
            np.testing.assert_allclose(values[apply],saved.loc[apply,col],atol=1e-10,rtol=0)
        np.testing.assert_array_equal(dicey[apply],saved.loc[apply,'Dicey'])
        # References and cutoff must be those of training entries, never scored entries.
        np.testing.assert_allclose(np.sort(loss[train]),m['Risk_model']['Loss_reference'],atol=1e-10,rtol=0)
        np.testing.assert_allclose(np.sort(down[train]),m['Risk_model']['Downside_reference'],atol=1e-10,rtol=0)
        np.testing.assert_allclose(np.quantile(risk[train],.75),m['Risk_model']['Threshold'],atol=1e-10,rtol=0)
        for key,name in [('WF10','WF10 maximum profit'),('WF11','WF11 dicey0.85 maximum profit')]:
            policy=m['Policies'][key];actual=saved.loc[apply,name+'_size'].to_numpy(float)
            np.testing.assert_allclose(pred[key][apply],actual,atol=1e-10,rtol=0)
            selected=inputs[inputs.Year.eq(year)&inputs.Policy.eq(name)]
            assert selected.Date.tolist()==td.loc[apply,'Date'].tolist()
            np.testing.assert_allclose(selected[policy['Features']],features[key][apply],atol=1e-10,rtol=0)
            if policy['Status']=='Optimized on prior years':np.testing.assert_allclose(pred[key][train].mean(),1.,atol=1e-10)
            np.testing.assert_allclose(saved.loc[apply,name+'_PnL'],td.loc[apply,'Net_PnL']*actual,atol=1e-9)
            max_error=max(max_error,float(np.max(abs(pred[key][apply]-actual))))
            rows.append({'Year':year,'Policy':key,'Status':policy['Status'],'Training_mean':float(pred[key][train].mean()),
                         'Scored_mean':float(actual.mean()),'Entries':int(apply.sum())})
    for key in ('WF10 maximum profit','WF11 dicey0.85 maximum profit'):
        assert saved.loc[saved.Year.eq(2021),key+'_size'].eq(1).all()
    report={'Status':'PASS','Strict_valid_inputs':int(valid.sum()),'Entries':len(td),'Scored_entries':int(td.Year.ge(2022).sum()),
            'Maximum_replay_error':max_error,'Folds':rows,'Checks':['SHA256 inputs and outputs','strict tick and daily reference cutoffs',
            'valid source size and mtime fingerprints','sequential prior DD and streak','training/scoring separation','saved percentile references and Q75',
            'frozen coefficients, scalers, offset and bounds replay','no scoring-period normalization','saved standardized inputs and PnL']}
    (OUT/'verification.json').write_text(json.dumps(report,indent=2)+'\n')
    manifest['Inputs']['audit_walkforward_sizing.py']=hashlib.sha256(Path(__file__).read_bytes()).hexdigest()
    manifest['Outputs']={p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in OUT.iterdir() if p.is_file() and p.name!='manifest.json' and not p.name.startswith('pilot')}
    (OUT/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
    print(json.dumps(report,indent=2),flush=True)


if __name__=='__main__':main()
