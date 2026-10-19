"""Predeclared paired Monte Carlo scenarios; no future normalization or live writes."""
import hashlib,json,os,pickle
from pathlib import Path
os.environ.setdefault('MPLCONFIGDIR','/tmp/gs-monte-carlo-matplotlib')
import numpy as np
import pandas as pd
import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt
from sizing_full_history import B

ROOT=Path(__file__).resolve().parent
OUT=ROOT/'output/monte_carlo_sizing'
WF=ROOT/'output/walkforward_sizing'
HIGH=ROOT/'output/higher_tax_sizing'
SEED=20261019
PATHS,HORIZON=5000,150
SCENARIOS=[('IID',1,'base'),('Block5',5,'base'),('Block10',10,'base'),
           ('Block20',20,'base'),('Edge_cost_stress',10,'erosion'),('Adverse_blocks',10,'adverse')]


def draw_indices(pnl,rng,paths,horizon,block,adverse=False):
    """Non-wrapping moving blocks. Adverse weights alter the scenario, not sizing."""
    n=len(pnl)
    if block<1 or block>n:raise ValueError('Invalid block length')
    weights=None
    if adverse:
        totals=np.convolve(pnl,np.ones(block),mode='valid')
        weights=np.where(totals<=np.quantile(totals,.25),3.,1.);weights/=weights.sum()
    starts=rng.choice(n-block+1,size=(paths,int(np.ceil(horizon/block))),p=weights)
    return (starts[:,:,None]+np.arange(block)).reshape(paths,-1)[:,:horizon].astype(np.int32)


def path_stats(pnl):
    equity=np.cumsum(pnl,axis=1)
    peak=np.maximum.accumulate(np.column_stack([np.zeros(len(equity)),equity]),axis=1)[:,1:]
    return equity[:,-1],(peak-equity).max(axis=1),equity


def assign(dicey,strong,cut,boost):
    return np.where(dicey,cut,np.where(strong,boost,1.))


def prepare():
    t=pd.read_csv(WF/'trade_comparison.csv',dtype={'Source_mtime_ns':str},float_precision='round_trip')
    assert len(t)==799 and not t.Date.duplicated().any()
    mask_paths=[];folds=pd.read_csv(HIGH/'training_cutoffs.csv')
    for row in folds.itertuples():
        assert str(row.Last_cache_day)<=str(row.Train_until)<f'{int(row.Year)}-01-01'
    for delta in (2,4):
        strong=np.zeros(len(t),bool)
        for year in sorted(t.Year.unique()):
            if year==2021:continue
            path=HIGH/f'higher_tax_luts_{year}_plus{delta}.npz';mask_paths.append(path)
            masks=np.load(path)
            for i,row in t[t.Year.eq(year)&t.Leg.eq('CW')&t.Valid.fillna(False)].iterrows():
                vals=[row.DTE_idx,row.IV_Ratio,row.Str_Ratio,row.Build_IV,row.Norm_OG,row.Adj_IV_Chg]
                if not np.isfinite(vals).all():continue
                coord=(int(vals[0]),int(np.searchsorted(B.IV_THRESHOLDS,vals[1],side='right')),
                    int(np.searchsorted(B.STR_THRESHOLDS,vals[2],side='right')),
                    int(np.searchsorted(B.BLD_THRESHOLDS,vals[3],side='right')),
                    int(np.searchsorted(B.OG_THRESHOLDS,vals[4],side='right')),
                    int(np.searchsorted([B.ADJ_LOWER,B.ADJ_HIGHER],vals[5],side='right')))
                assert all(0<=x<n for x,n in zip(coord,B.DIMS))
                strong[i]=bool(masks[f'stage{B.STAGE_NAMES.index(row.Stage)}'][coord])
        t[f'Strong_tax_plus{delta}']=strong
    sizes={};params=[];warm=t.Year.eq(2021).to_numpy();dicey=t.Dicey.to_numpy(bool)
    for amount in (1.,.75,.85,1.05,1.10,1.15):
        name='Flat size 1' if amount==1 else f'Fixed {amount:g}'
        sizes[name]=np.where(warm,1.,amount)
        params.append({'Candidate':name,'Family':'Exposure benchmark','Risk_cut':np.nan,'Boost':amount,'Extra_tax':np.nan})
    for cut in (.75,.80,.85):
        name=f'Risk cut{cut:g}';sizes[name]=np.where(warm,1.,assign(dicey,np.zeros(len(t),bool),cut,1.))
        params.append({'Candidate':name,'Family':'Risk only','Risk_cut':cut,'Boost':1.,'Extra_tax':np.nan})
        for delta in (2,4):
            for boost in (1.05,1.10,1.15):
                name=f'Tax+{delta} cut{cut:g} boost{boost:g}'
                sizes[name]=np.where(warm,1.,assign(dicey,t[f'Strong_tax_plus{delta}'].to_numpy(bool),cut,boost))
                params.append({'Candidate':name,'Family':'Adaptive','Risk_cut':cut,'Boost':boost,'Extra_tax':delta})
    assert len(sizes)==27
    assert t.loc[~t.Leg.eq('CW'),'Strong_tax_plus2'].eq(False).all()
    return t,sizes,pd.DataFrame(params),mask_paths,folds


def simulate(t,sizes,indices,shock,label):
    p=t.Net_PnL.to_numpy(float)
    if shock=='erosion':p=np.where(p>0,p*.85,p*1.25)-.5
    baseline=p[indices];flat_net,flat_dd,_=path_stats(baseline)
    nets=[];dds=[];rows=[];fans=[]
    for name,size in sizes.items():
        x=baseline*size[indices];net,dd,equity=path_stats(x);delta=net-flat_net
        q5,q50,q95=np.quantile(net,[.05,.5,.95]);dd95=np.quantile(dd,.95)
        rows.append({'Scenario':label,'Candidate':name,'Paths':len(indices),'Horizon_trades':indices.shape[1],
            'Mean_net':float(net.mean()),'Median_net':q50,'Net_p05':q5,'Net_p95':q95,
            'Worst5_mean_net':float(np.sort(net)[:max(1,len(net)//20)].mean()),
            'Mean_MDD':float(dd.mean()),'Median_MDD':float(np.median(dd)),'MDD_p95':dd95,'MDD_p99':np.quantile(dd,.99),
            'P_net_positive':float(np.mean(net>0)),'P_net_gt_flat':float(np.mean(net>flat_net+1e-9)),
            'P_net_up_DD_down':float(np.mean((net>flat_net+1e-9)&(dd<flat_dd-1e-9))),
            'Mean_delta_flat':float(delta.mean()),'Delta_p05':np.quantile(delta,.05),'Delta_p95':np.quantile(delta,.95),
            'Mean_sampled_size':float(size[indices].mean()),'Mean_size_pool':float(size.mean())})
        nets.append(net);dds.append(dd)
        if label=='Block10':fans.append(np.quantile(equity,[.05,.25,.5,.75,.95],axis=0))
    return pd.DataFrame(rows),np.array(nets),np.array(dds),np.array(fans)


def select(training,params):
    def best(frame):return str(frame.sort_values(['Mean_net','MDD_p95'],ascending=[False,True]).iloc[0].Candidate)
    baseline=float(training.loc[training.Candidate.eq('Flat size 1'),'MDD_p95'].iloc[0])
    adaptive=set(params.loc[params.Family.eq('Adaptive'),'Candidate'])
    return {'Maximum simulated mean profit':best(training),
        'Maximum adaptive mean profit':best(training[training.Candidate.isin(adaptive)]),
        'Maximum mean profit at flat tail-DD budget':best(training[training.MDD_p95.le(baseline+1e-9)])}


def hist_stats(p,size):
    net,dd,_=path_stats(np.asarray(p)[None,:])
    return {'Net':float(net[0]),'MDD':float(dd[0]),'Worst_day':float(np.min(p)),
        'Mean_size':float(np.mean(size)),'Cuts':int(np.sum(size<1-1e-9)),'Boosts':int(np.sum(size>1+1e-9))}


def charts(t,sizes,summary,training,selections,nets,dds,fans):
    plt.rcParams.update({'font.size':10,'axes.spines.top':False,'axes.spines.right':False,'figure.facecolor':'white'})
    names=list(sizes);selected=list(dict.fromkeys(['Flat size 1',*selections.values()]))
    colors=dict(zip(selected,['#444444','#d66a14','#176ea4','#26956a']))
    files=[]
    def save(fig,name):
        fig.savefig(OUT/(name+'.png'),dpi=170,bbox_inches='tight');fig.savefig(OUT/(name+'.svg'),bbox_inches='tight');plt.close(fig);files.append(name+'.png')
    # Evaluation history only; policies were selected on earlier years.
    fig,ax=plt.subplots(2,1,figsize=(12,7),sharex=True,layout='constrained')
    for name in selected:
        p=t.Net_PnL.to_numpy()*sizes[name];_,dd,e=path_stats(p[None,:]);eq=e[0];draw=np.maximum.accumulate(np.r_[0.,eq])[1:]-eq
        ax[0].plot(pd.to_datetime(t.Date),eq,label=name,color=colors[name]);ax[1].plot(pd.to_datetime(t.Date),draw,color=colors[name])
    ax[0].set(title='Subsequent historical evaluation: 2025–2026',ylabel='Cumulative net (normalized units)');ax[1].set(ylabel='Drawdown',xlabel='Trade date')
    ax[0].legend(fontsize=8);save(fig,'historical_equity_drawdown')
    # Main paired distributions and fan chart.
    idx=SCENARIOS.index(('Block10',10,'base'));sn=nets[idx];sd=dds[idx]
    fig,ax=plt.subplots(1,2,figsize=(12,4.5),layout='constrained')
    for name in selected:
        i=names.index(name);ax[0].hist(sn[i]-sn[0],bins=50,density=True,histtype='step',label=name,color=colors[name])
        sort=np.sort(sd[i]);ax[1].plot(sort,np.arange(1,len(sort)+1)/len(sort),color=colors[name],label=name)
    ax[0].axvline(0,color='black',lw=.7);ax[0].set(title='Paired profit difference versus flat1',xlabel='Net difference over150 trades',ylabel='Density')
    ax[1].set(title='Maximum-drawdown distribution',xlabel='Maximum drawdown',ylabel='Empirical cumulative probability');ax[1].legend(fontsize=8)
    save(fig,'paired_profit_and_drawdown')
    fig,axes=plt.subplots(1,len(selected),figsize=(4*len(selected),4),sharey=True,squeeze=False,layout='constrained')
    for ax,name in zip(axes[0],selected):
        q=fans[names.index(name)];x=np.arange(1,HORIZON+1);ax.fill_between(x,q[0],q[4],color=colors[name],alpha=.15,label='5–95%');ax.fill_between(x,q[1],q[3],color=colors[name],alpha=.25,label='25–75%');ax.plot(x,q[2],color=colors[name],label='Median');ax.set(title=name.replace(' ','\n',2),xlabel='Simulated trade');ax.legend(fontsize=8)
    axes[0,0].set_ylabel('Cumulative net (normalized units)');fig.suptitle('Block10 evaluation bootstrap:5000 paths,150 trades; pointwise bands')
    save(fig,'equity_fan_bands')
    main=summary[summary.Scenario.eq('Block10')]
    fig,ax=plt.subplots(figsize=(9,5),layout='constrained')
    ax.scatter(main.MDD_p95,main.Mean_net,c='#a8bccb',label='All27 fixed candidates')
    for name in selected:
        r=main[main.Candidate.eq(name)].iloc[0];ax.scatter([r.MDD_p95],[r.Mean_net],color=colors[name],s=65,label=name)
    fixed=main[main.Candidate.str.startswith('Fixed')|main.Candidate.eq('Flat size 1')].sort_values('MDD_p95')
    ax.plot(fixed.MDD_p95,fixed.Mean_net,'--',color='#999999',label='Constant-size exposure line')
    ax.set(title='Evaluation scenario frontier: profit versus tail drawdown',xlabel='95th-percentile maximum DD',ylabel='Mean net over150 trades');ax.legend(fontsize=8)
    save(fig,'profit_tail_risk_frontier')
    fig,ax=plt.subplots(1,2,figsize=(12,5),layout='constrained');x=np.arange(len(SCENARIOS));width=.8/len(selected)
    for k,name in enumerate(selected):
        r=summary[summary.Candidate.eq(name)].set_index('Scenario').loc[[s[0] for s in SCENARIOS]]
        pos=x+(k-(len(selected)-1)/2)*width
        ax[0].bar(pos,r.Mean_net,width,label=name,color=colors[name]);ax[1].bar(pos,r.MDD_p95,width,color=colors[name])
    for a in ax:a.set_xticks(x,[s[0].replace('_','\n') for s in SCENARIOS],rotation=20)
    ax[0].set(title='Profit across declared scenarios',ylabel='Mean terminal net');ax[1].set(title='Tail drawdown across scenarios',ylabel='95th-percentile MDD');ax[0].legend(fontsize=8)
    save(fig,'scenario_comparison')
    return files


def main():
    OUT.mkdir(parents=True,exist_ok=True);t,sizes,params,masks,folds=prepare();names=list(sizes)
    train=t.Year.between(2022,2024).to_numpy();test=t.Year.ge(2025).to_numpy()
    assert train.sum()==422 and test.sum()==195 and t.loc[train,'Date'].max()<t.loc[test,'Date'].min()
    rng=np.random.default_rng(SEED);index=draw_indices(t.loc[train,'Net_PnL'].to_numpy(),rng,PATHS,HORIZON,10)
    tr_sizes={name:s[train] for name,s in sizes.items()}
    training,tr_net,tr_dd,_=simulate(t[train].reset_index(drop=True),tr_sizes,index,'base','Training_Block10')
    selections=select(training,params)
    (OUT/'selection.json').write_text(json.dumps({'Selected_using':'2022–2024 only; before evaluation','Policies':selections},indent=2)+'\n')
    print('Frozen training selections:',selections,flush=True)
    eval_t=t[test].reset_index(drop=True);eval_sizes={name:s[test] for name,s in sizes.items()}
    summaries=[];net_arrays=[];dd_arrays=[];draws={};fans=None;samples=[]
    for j,(label,block,shock) in enumerate(SCENARIOS):
        rng=np.random.default_rng(SEED+100+j)
        indices=draw_indices(eval_t.Net_PnL.to_numpy(),rng,PATHS,HORIZON,block,shock=='adverse');draws[label]=indices
        result,net,dd,fan=simulate(eval_t,eval_sizes,indices,shock,label)
        summaries.append(result);net_arrays.append(net);dd_arrays.append(dd)
        if label=='Block10':fans=fan
        for name in dict.fromkeys(['Flat size 1',*selections.values()]):
            k=names.index(name)
            samples.extend({'Scenario':label,'Candidate':name,'Path':i+1,'Net':net[k,i],'MDD':dd[k,i],
                'Delta_flat':net[k,i]-net[0,i]} for i in range(100))
        print('Completed',label,flush=True)
    summary=pd.concat(summaries,ignore_index=True);nets=np.array(net_arrays);dds=np.array(dd_arrays)
    np.savez_compressed(OUT/'simulated_outcomes.npz',Names=np.array(names),Scenarios=np.array([s[0] for s in SCENARIOS]),Net=nets,MDD=dds,
        Training_Net=tr_net,Training_MDD=tr_dd)
    np.savez_compressed(OUT/'sampled_indices.npz',Training=index,**draws)
    np.savez_compressed(OUT/'fan_bands.npz',Names=np.array(names),Quantiles=np.array([.05,.25,.5,.75,.95]),Equity=fans)
    files=sorted((ROOT/'output/backtest_relax/run_027bf0249b/days').glob('*.pkl'))
    d=pd.DataFrame([pickle.load(p.open('rb')) for p in files]).sort_values('Date').reset_index(drop=True)
    d.Net_PnL=d.Net_PnL.fillna(0.);d.Leg=d.Leg.fillna('');active=d.Leg.isin(['CW','NW']).to_numpy()
    assert d.loc[active,'Date'].tolist()==t.Date.tolist()
    ds=pd.DataFrame({'Date':d.Date});dp=ds.copy();hist=[];yearly=[];quarterly=[]
    for name,s in sizes.items():
        t[name+'_size']=s;t[name+'_PnL']=t.Net_PnL.to_numpy()*s
        whole=np.ones(len(d));whole[active]=s;ds[name]=whole;dp[name]=d.Net_PnL.to_numpy()*whole
        for scope,mask in [('All_history',np.ones(len(d),bool)),('Warmup_2021',d.Date.lt('2022-01-01').to_numpy()),
            ('Selection_2022_2024',d.Date.between('2022-01-01','2024-12-31').to_numpy()),('Evaluation_2025plus',d.Date.ge('2025-01-01').to_numpy())]:
            hist.append({'Candidate':name,'Period':scope,**hist_stats(dp[name].to_numpy()[mask],whole[mask&active])})
        for target,periods,col in [(yearly,pd.to_datetime(d.Date).dt.year,'Year'),(quarterly,pd.to_datetime(d.Date).dt.to_period('Q').astype(str),'Quarter')]:
            for period in sorted(set(periods)):
                mask=np.asarray(periods==period);target.append({'Candidate':name,col:period,**hist_stats(dp[name].to_numpy()[mask],whole[mask&active])})
    selected_table=pd.DataFrame([{'Objective':objective,'Candidate':name} for objective,name in selections.items()])
    tables={'Read_me':pd.DataFrame({'Line':(ROOT/'MONTE_CARLO_SIZING_SPEC.md').read_text().splitlines()}),
        'Candidates':pd.DataFrame(hist),'By_year':pd.DataFrame(yearly),'By_quarter':pd.DataFrame(quarterly),
        'Daily_sizes':ds,'Daily_PnL':dp,'Daily_risk':d.merge(t,on='Date',how='left',suffixes=('_base','')),
        'Trade_comparison':t,'Policy_parameters':params,'MC_training':training,'MC_evaluation':summary,
        'Frozen_selection':selected_table,'MC_sample_paths':pd.DataFrame(samples),'Tax_training_cutoffs':folds}
    with pd.ExcelWriter(OUT/'monte_carlo_sizing.xlsx',engine='openpyxl') as writer:
        for sheet,frame in tables.items():
            frame.to_excel(writer,sheet_name=sheet,index=False);writer.sheets[sheet].freeze_panes='B2'
        from openpyxl.drawing.image import Image
        chart_files=charts(eval_t,eval_sizes,summary,training,selections,nets,dds,fans)
        ws=writer.book.create_sheet('Charts')
        for i,name in enumerate(chart_files):
            picture=Image(OUT/name);picture.width=1000;picture.height=500;ws.add_image(picture,f'A{1+i*29}')
    for name,frame in [('mc_training',training),('mc_evaluation',summary),('historical_summary',tables['Candidates']),
        ('daily_sizes',ds),('daily_pnl',dp),('trade_comparison',t),('policy_parameters',params)]:frame.to_csv(OUT/(name+'.csv'),index=False)
    main=summary[summary.Scenario.eq('Block10')].set_index('Candidate')
    lines=['# Monte Carlo sizing scenarios','',
        '5000 paired paths ×150 trade opportunities; outcomes in normalized backtest units, not account returns.',
        'Selection uses2022–2024; evaluation uses2025–2026. No future mean balancing. Previously researched years are not an untouched holdout.',
        'Conditional on legacy fixed entries/fills and higher-tax training cache; Monte Carlo does not remove those limitations.','',
        '| Training-selected objective | Policy | Evaluation MC mean net | Net p05 | DD p95 | Mean size |',
        '|---|---|---:|---:|---:|---:|']
    for objective,name in {'Baseline':'Flat size 1',**selections}.items():
        r=main.loc[name];lines.append(f'| {objective} | {name} | {r.Mean_net:,.2f} | {r.Net_p05:,.2f} | {r.MDD_p95:,.2f} | {r.Mean_size_pool:.4f} |')
    lines+=['','Full six-scenario and all27-policy comparisons: `monte_carlo_sizing.xlsx` and `mc_evaluation.csv`.',
        'Maximum means maximum among this declared bounded policy grid, not the maximum attainable future profit.',
        'Fixed1.15 is an exposure benchmark: it scales both profit and drawdown by15%; that alone is not allocation alpha.',
        'Scenario p05/p95 bands are conditional empirical bootstrap ranges, not guarantees or comprehensive model uncertainty.',
        'The drawdown budget is a flat1 training-tail reference, not a user-specified capital limit. No live policy adopted.','']
    for name in chart_files:lines.append(f'![{name}]({name})\n')
    (OUT/'REPORT.md').write_text('\n'.join(lines)+'\n')
    inputs=[Path(__file__),ROOT/'MONTE_CARLO_SIZING_SPEC.md',ROOT/'GammaShort_Backtest_Final.py',
        WF/'trade_comparison.csv',WF/'manifest.json',HIGH/'training_cutoffs.csv',HIGH/'manifest.json',*masks,*files]
    manifest={'Seed':SEED,'Paths':PATHS,'Horizon_trades':HORIZON,'Policies':names,'Scenarios':SCENARIOS,
        'Selection_entries':int(train.sum()),'Evaluation_entries':int(test.sum()),'Selections':selections,
        'Inputs':{str(p.relative_to(ROOT)):hashlib.sha256(p.read_bytes()).hexdigest() for p in inputs},
        'Outputs':{p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in OUT.iterdir() if p.is_file() and p.name!='manifest.json'}}
    (OUT/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
    print(main.loc[list(dict.fromkeys(['Flat size 1',*selections.values()]))].to_string(),flush=True)


if __name__=='__main__':main()
