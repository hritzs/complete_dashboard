"""Past-only parameter selection and exposure-matched placement diagnostics."""
import hashlib,itertools,json
from pathlib import Path
import numpy as np
import pandas as pd
from monte_carlo_sizing import ROOT,WF,OUT as MC,draw_indices,path_stats,plt
from audit_walkforward_sizing import replay,percentile

OUT=ROOT/'output/nested_sizing'
POLICIES=['Strict profit','Strict ratio','Limited-DD profit','Exploratory allocation']


def metrics(p,sizes):
    x=np.asarray(p)[:,None]*sizes;eq=np.cumsum(x,axis=0)
    peak=np.maximum.accumulate(np.vstack([np.zeros(sizes.shape[1]),eq]),axis=0)[1:]
    net=x.sum(0);dd=(peak-eq).max(0);mean=sizes.mean(0)
    return pd.DataFrame({'Net':net,'MDD':dd,'Worst':x.min(0),'Mean_size':mean,
        'Ratio':net/np.maximum(dd,1e-9),'Allocation_excess':net-np.sum(p)*mean})


def grid():
    t=pd.read_csv(MC/'trade_comparison.csv',float_precision='round_trip')
    inputs=pd.read_csv(WF/'prepared_inputs.csv',dtype={'Source_mtime_ns':str},float_precision='round_trip')
    assert t.Date.tolist()==inputs.Date.tolist()
    features=pd.read_excel(WF/'walkforward_comparison.xlsx',sheet_name='Model_inputs')
    f=features[features.Policy.eq('WF10 maximum profit')].set_index('Date')
    s1=t.Date.map(f.S1_score).fillna(0).to_numpy();s2=t.Date.map(f.S2_score).fillna(0).to_numpy()
    flags=np.zeros((len(t),9),bool);vol=np.ones(len(t));thresholds=[]
    weights=[(.4,.4,.2),(.5,.3,.2),(.3,.5,.2)]
    for year in range(2022,2027):
        model=json.loads((WF/f'model_{year}.json').read_text());train=inputs.Year.lt(year).to_numpy();apply=inputs.Year.eq(year).to_numpy()
        _,_,loss,down,risk,_=replay(inputs,model);rm=model['Risk_model']
        lr=percentile(rm['Loss_reference'],loss);dr=percentile(rm['Downside_reference'],down)
        inst=(risk-.4*lr-.4*dr)/.2
        for wi,w in enumerate(weights):
            score=w[0]*lr+w[1]*dr+w[2]*inst
            for qi,q in enumerate((.6,.7,.75)):
                threshold=float(np.quantile(score[train],q));col=wi*3+qi
                flags[apply,col]=(score[apply]>=threshold)|t.loc[apply,'Dicey'].to_numpy(bool)|t.loc[apply,'Missing_CW'].fillna(False).to_numpy(bool)
                thresholds.append({'Year':year,'Risk_variant':col,'Loss_weight':w[0],'Down_weight':w[1],
                    'Instability_weight':w[2],'Quantile':q,'Threshold':threshold,'Last_training_date':model['Last_training_entry']})
        median=model['Base_model']['Median'][model['Base_model']['Numeric_features'].index('IV')]
        iv=t.IV.to_numpy(float);factor=np.divide(median,iv,out=np.ones(len(t)),where=np.isfinite(iv)&(iv>0))
        vol[apply]=np.clip(factor[apply],.85,1.)
    params=[{'Candidate':0,'Risk_variant':-1,'Cut':1.,'Extra_tax':0,'Boost':1.,'Score_guard':-99.,'Vol_damp':False,'DD_guard':False}]
    size=[np.ones(len(t))];strongs=[]
    for variant,cut,delta,boost,guard,v,d in itertools.product(range(9),(.75,.80,.85),(2,4),(1.05,1.10,1.15),(-99.,0.,.5),(False,True),(False,True)):
        strong=t[f'Strong_tax_plus{delta}'].to_numpy(bool)&(s1>=guard)&(s2>=guard)&t.Leg.eq('CW').to_numpy()
        raw=np.where(flags[:,variant],cut,np.where(strong,boost,1.))
        s=np.clip(raw*(vol if v else 1.),.75,1.15);s=np.minimum(s,np.where(strong,1.15,1.))
        if d:s=np.minimum(s,np.where(t.Prior_flat_DD.to_numpy()>75,.85,1.15))
        s[t.Year.eq(2021).to_numpy()]=1.
        params.append({'Candidate':len(size),'Risk_variant':variant,'Cut':cut,'Extra_tax':delta,'Boost':boost,
            'Score_guard':guard,'Vol_damp':v,'DD_guard':d});size.append(s)
    assert len(size)==1945
    return t,np.array(size).T,pd.DataFrame(params),flags,vol,s1,s2,pd.DataFrame(thresholds)


def unique_top(order,sizes,k=10):
    used=set();out=[]
    for j in order:
        key=np.round(sizes[:,j],12).tobytes()
        if key in used:continue
        used.add(key);out.append(int(j))
        if len(out)==k:break
    return out


def choose(a,b,sizes,policy):
    flat=a.iloc[0];vflat=b.iloc[0]
    eligible=a.Net.gt(flat.Net+1e-9)&a.MDD.le(flat.MDD*1.1+1e-9)&a.Mean_size.le(1.03+1e-9)&a.Allocation_excess.gt(1e-9)
    column='Ratio' if policy=='Strict ratio' else 'Net'
    if policy=='Exploratory allocation':
        order=a.iloc[1:].sort_values('Allocation_excess',ascending=False,kind='stable').index
        top=unique_top(order,sizes)
        return int(b.loc[top].sort_values(['Allocation_excess','Candidate'],ascending=[False,True]).index[0]),top,'Exploratory: no validation pass claimed'
    order=a[eligible].sort_values(column,ascending=False,kind='stable').index
    top=unique_top(order,sizes)
    factor=1.1 if policy=='Limited-DD profit' else 1.
    okay=b.Net.gt(vflat.Net+1e-9)&b.MDD.le(vflat.MDD*factor+1e-9)&b.Worst.ge(vflat.Worst*factor-1e-9)&b.Mean_size.le(1.03+1e-9)&b.Allocation_excess.gt(1e-9)
    finalists=[j for j in top if okay.iloc[j]]
    if not finalists:return 0,top,'Flat fallback: no qualifying shortlisted validation policy'
    return int(b.loc[finalists].sort_values([column,'Candidate'],ascending=[False,True]).index[0]),top,'Passed past inner validation'


def placement(p,s,indices,rng):
    outcomes=p[indices];actual=s[indices];order=np.argsort(rng.random(actual.shape),axis=1)
    shuffled=np.take_along_axis(actual,order,axis=1)
    assert np.allclose(actual.sum(1),shuffled.sum(1),atol=1e-10,rtol=0)
    an,ad,_=path_stats(outcomes*actual);rn,rd,_=path_stats(outcomes*shuffled)
    return an,ad,rn,rd


def main():
    OUT.mkdir(parents=True,exist_ok=True);t,S,params,flags,vol,s1,s2,thresholds=grid()
    p=t.Net_PnL.to_numpy(float);sizes={'Flat size 1':np.ones(len(t)),**{k:np.ones(len(t)) for k in POLICIES}}
    selected=[];sensitivity=[];replay_rows=[]
    for year in range(2023,2027):
        prior=np.flatnonzero(t.Year.ge(2022)&t.Year.lt(year));cut=int(len(prior)*.6);tr=prior[:cut];va=prior[cut:]
        assert len(tr)>=50 and len(va)>=30 and t.Date.iloc[va[-1]]<f'{year}-01-01'
        a=metrics(p[tr],S[tr]);b=metrics(p[va],S[va]);a['Candidate']=a.index;b['Candidate']=b.index
        for frame,part in [(a,'Inner_train'),(b,'Inner_validation')]:
            saved=frame.copy();saved['Year']=year;saved['Part']=part;sensitivity.append(saved)
        apply=t.Year.eq(year).to_numpy()
        for policy in POLICIES:
            j,top,status=choose(a,b,S[tr],policy);sizes[policy][apply]=S[apply,j]
            row={'Year':year,'Policy':policy,**params.iloc[j].to_dict(),'Status':status,'Shortlist':json.dumps(top),
                'Train_last_date':t.Date.iloc[tr[-1]],'Validation_first_date':t.Date.iloc[va[0]],
                'Validation_last_date':t.Date.iloc[va[-1]],'First_scored_date':t.loc[apply,'Date'].min(),
                'Training_entries':len(tr),'Validation_entries':len(va),'Scored_entries':int(apply.sum()),
                **{'Train_'+k:float(a.loc[j,k]) for k in ['Net','MDD','Allocation_excess','Mean_size']},
                **{'Validation_'+k:float(b.loc[j,k]) for k in ['Net','MDD','Allocation_excess','Mean_size']}}
            selected.append(row)
            for i in np.flatnonzero(apply):
                r=params.iloc[j];risk=bool(flags[i,int(r.Risk_variant)]) if j else False
                strong=bool(t.iloc[i][f'Strong_tax_plus{int(r.Extra_tax)}'] and s1[i]>=r.Score_guard and s2[i]>=r.Score_guard) if j else False
                replay_rows.append({'Date':t.Date.iloc[i],'Policy':policy,'Year':year,'Candidate':j,'Flat_selection':j==0,
                    'Risk':risk,'Strong':strong,'Cut':r.Cut,'Boost':r.Boost,'Vol_factor':vol[i] if r.Vol_damp else 1.,
                    'DD_cap':.85 if r.DD_guard and t.Prior_flat_DD.iloc[i]>75 else 1.15,'Saved_size':S[i,j]})
        print('Year',year,{r['Policy']:(int(r['Candidate']),r['Status']) for r in selected if r['Year']==year},flush=True)
    choices=pd.DataFrame(selected);sens=pd.concat(sensitivity,ignore_index=True).merge(params,on='Candidate',validate='many_to_one')
    choices.to_csv(OUT/'frozen_selections.csv',index=False);sens.to_parquet(OUT/'inner_sensitivity.parquet',index=False)
    # All scored policy parameters are frozen before any outer performance is assessed.
    outer=t.Year.ge(2023).to_numpy();et=t[outer].reset_index(drop=True);ep=p[outer];names=list(sizes)
    indices=draw_indices(ep,np.random.default_rng(20261020),5000,150,10)
    paths={};permutation_rows=[];historical_null={}
    for k,name in enumerate(names):
        s=sizes[name][outer];an,ad,rn,rd=placement(ep,s,indices,np.random.default_rng(20262000+k))
        paths[name]=np.column_stack([an,ad,rn,rd])
        random_net=np.zeros(5000);rng=np.random.default_rng(20263000+k)
        for year in sorted(et.Year.unique()):
            mask=et.Year.eq(year).to_numpy();ss=s[mask];pp=ep[mask]
            orders=np.argsort(rng.random((5000,len(ss))),axis=1);random_net+=(ss[orders]*pp).sum(1)
        historical_null[name]=random_net;actual=float(np.dot(ep,s));tail=float((1+np.sum(random_net>=actual-1e-9))/5001)
        permutation_rows.append({'Policy':name,'Actual_net':actual,'Same_size_random_mean_net':float(random_net.mean()),
            'Actual_minus_random_mean':float(actual-random_net.mean()),'Placement_upper_tail_frequency':tail,
            'Bonferroni_four_streams':min(1.,tail*4),'MC_mean_actual_net':float(an.mean()),'MC_mean_random_net':float(rn.mean()),
            'MC_mean_placement_excess':float((an-rn).mean()),'MC_excess_p05':np.quantile(an-rn,.05),'MC_excess_p95':np.quantile(an-rn,.95),
            'MC_P_actual_gt_random':float(np.mean(an>rn+1e-9)),'MC_actual_MDD_p95':np.quantile(ad,.95),
            'MC_random_MDD_p95':np.quantile(rd,.95),'Mean_size':float(s.mean())})
    np.savez_compressed(OUT/'placement_paths.npz',Indices=indices,Names=np.array(names),Paths=np.array([paths[n] for n in names]),
        Historical_null=np.array([historical_null[n] for n in names]))
    pd.DataFrame(permutation_rows).to_csv(OUT/'placement_summary.csv',index=False)
    daily_source=pd.read_csv(WF/'daily_pnl.csv');d=pd.DataFrame({'Date':daily_source.Date,'Net_PnL':daily_source['Flat size 1']})
    active=d.Date.isin(t.Date).to_numpy();ds=pd.DataFrame({'Date':d.Date});dp=ds.copy();summaries=[];yearly=[];quarterly=[]
    for name,s in sizes.items():
        full=np.ones(len(d));full[active]=s;ds[name]=full;dp[name]=d.Net_PnL.to_numpy()*full
        t[name+'_size']=s;t[name+'_PnL']=p*s
        for period,mask in [('All_history',np.ones(len(d),bool)),('Warmup_2021_2022',d.Date.lt('2023-01-01').to_numpy()),('Outer_2023plus',d.Date.ge('2023-01-01').to_numpy())]:
            stat=metrics(d.Net_PnL.to_numpy()[mask],full[mask,None]).iloc[0].to_dict();stat['Mean_size']=float(full[mask&active].mean())
            stat['Allocation_excess']=stat['Net']-float(d.Net_PnL.to_numpy()[mask].sum())*stat['Mean_size']
            summaries.append({'Candidate':name,'Period':period,**stat})
        for target,periods,col in [(yearly,pd.to_datetime(d.Date).dt.year,'Year'),(quarterly,pd.to_datetime(d.Date).dt.to_period('Q').astype(str),'Quarter')]:
            for value in sorted(set(periods)):
                mask=np.asarray(periods==value);stat=metrics(d.Net_PnL.to_numpy()[mask],full[mask,None]).iloc[0].to_dict();stat['Mean_size']=float(full[mask&active].mean())
                stat['Allocation_excess']=stat['Net']-float(d.Net_PnL.to_numpy()[mask].sum())*stat['Mean_size'];target.append({'Candidate':name,col:value,**stat})
    report=pd.DataFrame(summaries);placement_summary=pd.DataFrame(permutation_rows)
    files=make_charts(et,sizes,outer,sens,placement_summary,paths)
    tables={'Read_me':pd.DataFrame({'Line':(ROOT/'NESTED_SIZING_SPEC.md').read_text().splitlines()}),'Candidates':report,
        'Daily_sizes':ds,'Daily_PnL':dp,'Trade_comparison':t,'By_year':pd.DataFrame(yearly),'By_quarter':pd.DataFrame(quarterly),
        'Frozen_parameters':choices,'Parameter_grid':params,'Inner_sensitivity':sens,'Annual_thresholds':thresholds,
        'Placement_comparison':placement_summary,'Formula_replay':pd.DataFrame(replay_rows)}
    with pd.ExcelWriter(OUT/'nested_sizing.xlsx',engine='openpyxl') as writer:
        for sheet,frame in tables.items():frame.to_excel(writer,sheet_name=sheet,index=False);writer.sheets[sheet].freeze_panes='B2'
        from openpyxl.drawing.image import Image
        ws=writer.book.create_sheet('Charts')
        for j,file in enumerate(files):
            pic=Image(OUT/file);pic.width=1000;pic.height=500;ws.add_image(pic,f'A{1+j*29}')
    ds.to_csv(OUT/'daily_sizes.csv',index=False);dp.to_csv(OUT/'daily_pnl.csv',index=False);report.to_csv(OUT/'historical_summary.csv',index=False)
    t.to_csv(OUT/'trade_comparison.csv',index=False);pd.DataFrame(replay_rows).to_csv(OUT/'formula_replay.csv',index=False)
    lines=['# Nested sizing and equal-exposure placement test','',
        '1944 fixed combinations; yearly past-only selection;442 outer2023+ trades. No normalization. Conditional legacy entry book, not an untouched holdout.',
        'Random placements preserve exact sizes and size sums; they are diagnostic counterfactuals, not tradable risk-gated policies.','',
        '| Policy | Outer net | Max DD | Mean size | Placement excess vs random |',
        '|---|---:|---:|---:|---:|']
    for r in report[report.Period.eq('Outer_2023plus')].itertuples():
        excess=placement_summary.set_index('Policy').loc[r.Candidate,'Actual_minus_random_mean']
        lines.append(f'| {r.Candidate} | {r.Net:,.2f} | {r.MDD:.2f} | {r.Mean_size:.4f} | {excess:+.2f} |')
    lines+=['','No future normalization or post-score grid winner. Strict policies fall back to flat when past validation fails.',
        'Exploratory allocation is shown even when validation fails; it is not an adopted alpha model.',
        'Permutation frequencies and bootstrap intervals condition on this historical sample and do not establish future profitability.','']
    for file in files:lines.append(f'![{file}]({file})\n')
    (OUT/'REPORT.md').write_text('\n'.join(lines)+'\n')
    inputs=[Path(__file__),ROOT/'NESTED_SIZING_SPEC.md',ROOT/'audit_walkforward_sizing.py',ROOT/'monte_carlo_sizing.py',
        MC/'trade_comparison.csv',WF/'prepared_inputs.csv',WF/'daily_pnl.csv',WF/'walkforward_comparison.xlsx',*sorted(WF.glob('model_*.json'))]
    manifest={'Combinations':1944,'Outer_entries':int(outer.sum()),'Inputs':{str(f.relative_to(ROOT)):hashlib.sha256(f.read_bytes()).hexdigest() for f in inputs},
        'Outputs':{f.name:hashlib.sha256(f.read_bytes()).hexdigest() for f in OUT.iterdir() if f.is_file() and f.name!='manifest.json'}}
    (OUT/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
    print(report[report.Period.eq('Outer_2023plus')].to_string(index=False),flush=True);print(placement_summary.to_string(index=False),flush=True)


def make_charts(t,sizes,outer,sensitivity,summary,paths):
    names=list(sizes);colors=['#333333','#176ea4','#26956a','#a97605','#c55b5b'];files=[]
    def save(fig,name):
        fig.savefig(OUT/(name+'.png'),dpi=170,bbox_inches='tight');fig.savefig(OUT/(name+'.svg'),bbox_inches='tight');plt.close(fig);files.append(name+'.png')
    fig,axes=plt.subplots(2,1,figsize=(12,7),sharex=True,layout='constrained')
    for name,color in zip(names,colors):
        p=t.Net_PnL.to_numpy()*sizes[name][outer];_,_,eq=path_stats(p[None,:]);e=eq[0]
        axes[0].plot(pd.to_datetime(t.Date),e,label=name,color=color);axes[1].plot(pd.to_datetime(t.Date),np.maximum.accumulate(np.r_[0.,e])[1:]-e,color=color)
    axes[0].set(title='Chronological outer results: parameters chosen before each year',ylabel='Net (normalized units)');axes[0].legend(fontsize=8)
    axes[1].set(ylabel='Drawdown',xlabel='Trade date');save(fig,'outer_equity_drawdown')
    fig,axes=plt.subplots(1,2,figsize=(12,5),layout='constrained')
    for name,color in zip(names[1:],colors[1:]):
        z=paths[name];delta=z[:,0]-z[:,2]
        if np.ptp(delta)>1e-10:axes[0].hist(delta,bins=50,histtype='step',density=True,label=name,color=color)
        axes[1].scatter(np.quantile(z[:,1],.95),z[:,0].mean(),label=name,color=color)
    axes[0].axvline(0,color='black',lw=.8);axes[0].set(title='Actual placement minus random placement',xlabel='Paired net difference; same sizes, same trades',ylabel='Density');axes[0].legend(fontsize=8)
    axes[0].text(.02,.97,'Flat fallback streams coincide at zero; omitted',transform=axes[0].transAxes,va='top',fontsize=8)
    axes[1].set(title='5000 Block10 paths of150 trades',xlabel='95th-percentile maximum DD',ylabel='Mean net');axes[1].legend(fontsize=8)
    save(fig,'equal_exposure_placement')
    v=sensitivity[sensitivity.Part.eq('Inner_validation')&sensitivity.Candidate.ne(0)]
    fig,axes=plt.subplots(1,2,figsize=(11,4.5),layout='constrained')
    for ax,column,title in [(axes[0],'Allocation_excess','Mean inner-validation allocation excess'),(axes[1],'MDD','Mean inner-validation maximum DD')]:
        table=v.groupby(['Cut','Boost'])[column].mean().unstack('Boost');im=ax.imshow(table.to_numpy(),aspect='auto',cmap='RdYlGn' if column=='Allocation_excess' else 'YlOrRd')
        ax.set_xticks(range(len(table.columns)),[f'{x:.2f}' for x in table.columns]);ax.set_yticks(range(len(table.index)),[f'{x:.2f}' for x in table.index]);ax.set(title=title,xlabel='Strong boost',ylabel='Dicey cut')
        for i in range(len(table)):
            for j in range(len(table.columns)):ax.text(j,i,f'{table.iloc[i,j]:.1f}',ha='center',va='center')
        fig.colorbar(im,ax=ax,shrink=.8)
    fig.suptitle('Sensitivity summarizes past inner validation, not an outer winner search');save(fig,'parameter_sensitivity')
    return files


if __name__=='__main__':main()
