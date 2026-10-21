"""Report the fixed screen and actual combined daily paths, never partial runs."""
import hashlib,json,os,pickle
from pathlib import Path
os.environ.setdefault('MPLCONFIGDIR','/tmp/gs-complement-mpl')
import numpy as np
import pandas as pd
import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt
from complement_strategy import ROOT,OUT,POLICIES


def stats(pnl):
    p=np.asarray(pnl,float);eq=np.cumsum(p);dd=np.maximum.accumulate(np.r_[0.,eq])[1:]-eq
    maximum=float(dd.max())
    return {'Net':float(p.sum()),'MDD':maximum,'Net_per_MDD':float(p.sum()/maximum) if maximum else np.nan,
        'Worst_day':float(p.min()),'Best_day':float(p.max()),'Winning_days':int((p>0).sum()),'Losing_days':int((p<0).sum())}


def load():
    run=json.loads((OUT/'run_manifest.json').read_text());assert run['Status']=='complete' and not run['Failed_dates']
    for name,digest in run['Inputs'].items():assert hashlib.sha256((ROOT/name).read_bytes()).hexdigest()==digest,name
    if run.get('Raw_failed_dates'):
        assert hashlib.sha256((ROOT/'resolve_complement_eligibility.py').read_bytes()).hexdigest()==run['Eligibility_auditor_SHA256']
        raw=json.loads((OUT/'raw_run_manifest.json').read_text());assert raw['Status']=='errors' and raw['Failed_dates']==run['Raw_failed_dates']
    dates=run['Dates'];assert len(dates)==1430 and sorted(p.stem for p in (OUT/'days').glob('*.pkl'))==dates
    replay=[pickle.load((OUT/'days'/(date+'.pkl')).open('rb')) for date in dates]
    assert all(not r['Error'] for r in replay)
    for date in run.get('Raw_failed_dates',[]):
        record=replay[dates.index(date)];original=OUT/'raw_ineligible_days'/(date+'.pkl')
        assert record['Raw_exception_SHA256']==hashlib.sha256(original.read_bytes()).hexdigest()
        assert record['Eligibility_resolution'] and all(not row['Traded'] for row in record['Rows'])
    trades=pd.DataFrame([row for record in replay for row in record['Rows']]);assert len(trades)==6*1430
    for col in ('Gross_PnL','Fee','Premium_budget'):
        if col not in trades:trades[col]=0.
        trades[col]=trades[col].fillna(0.)
    for col in ('Expiry','Strike','Entry_Time','Exit_Time','Exit_Reason','Delta_at_entry','Gamma_at_entry','Vega_1pct_at_entry',
        'Normalized_quantity','CE_contract_units','PE_contract_units','Entry_debit_points','Exit_credit_points','Entry_U',
        'Decision_Time','Entry_quote_label','Exit_trigger_time','Exit_delay_seconds'):
        if col not in trades:trades[col]=np.nan
    assert not trades.duplicated(['Date','Policy']).any()
    active=trades.Traded.astype(bool)
    assert trades.loc[~active,'Net_PnL'].eq(0).all()
    np.testing.assert_allclose(trades.loc[active,'Gross_PnL']-trades.loc[active,'Fee'],trades.loc[active,'Net_PnL'],atol=1e-9,rtol=0)
    assert trades.loc[active,'Gross_PnL'].ge(-100.-1e-8).all()
    pilot=json.loads((OUT/'pilot_manifest.json').read_text());assert pilot['Status']=='complete' and pilot['Inputs']==run['Inputs']
    for date in pilot['Dates']:
        a=pickle.load((OUT/'pilot_days'/(date+'.pkl')).open('rb'));b=replay[dates.index(date)]
        pd.testing.assert_frame_equal(pd.DataFrame(a['Rows']),pd.DataFrame(b['Rows']),check_dtype=False,atol=1e-10,rtol=0)
    gsout=ROOT/'output/logic_comparison_20261021';gsrun=json.loads((gsout/'run_manifest.json').read_text())
    assert gsrun['Status']=='complete' and not gsrun['Failed_dates'] and gsrun['Dates']==dates
    for name,digest in gsrun['Inputs'].items():assert hashlib.sha256((ROOT/name).read_bytes()).hexdigest()==digest,name
    gs_records=[pickle.load((gsout/'days'/(date+'.pkl')).open('rb')) for date in dates]
    assert all(not r['Error'] for r in gs_records)
    gs=pd.DataFrame([r['New'] for r in gs_records]);gs['Net_PnL']=gs.Net_PnL.fillna(0.)
    return dates,trades,replay,gs,gs_records,run


def main():
    dates,trades,replay,gs,gs_records,run=load();policies=[s+' '+g for s,g in POLICIES]
    from annotate_complement_calendar import verify as verify_calendar
    verify_calendar()
    eligibility=pd.read_csv(OUT/'eligibility_audit.csv',dtype={'Tick_mtime_ns':str})
    for row in eligibility.itertuples(index=False):
        mask=trades.Date.eq(row.Date)
        assert not trades.loc[mask,'Traded'].any()
        trades.loc[mask,'Reason']='Ineligible: '+row.Eligibility_reason
    daily=trades.pivot(index='Date',columns='Policy',values='Net_PnL').reindex(dates).reset_index()
    stress=trades.pivot(index='Date',columns='Policy',values='Double_fee_net').reindex(dates).reset_index()
    gs_active=gs.Leg.fillna('').isin(['CW','NW']).to_numpy();gs_pnl=gs.Net_PnL.to_numpy(float)
    daily['Latest GS']=gs_pnl;stress['Latest GS']=gs_pnl
    meta={};summaries=[];period_tables={};paths={'Date':dates};dt=pd.to_datetime(dates)
    gs_bad=gs_pnl<0;cut=float(np.quantile(gs_pnl[gs_active],.1));gs_tail=gs_active&(gs_pnl<=cut)
    for name in policies:
        rows=trades[trades.Policy.eq(name)].set_index('Date').loc[dates].reset_index()
        active=rows.Traded.to_numpy(bool);p=rows.Net_PnL.to_numpy(float)
        meta[name]={'active':active,'positions':active.astype(int),'fees':rows.Fee.fillna(0).to_numpy(float),'premium':rows.Premium_budget.fillna(0).to_numpy(float)}
        combo='GS + '+name;daily[combo]=gs_pnl+p;stress[combo]=gs_pnl+rows.Double_fee_net.to_numpy(float)
        meta[combo]={'active':active|gs_active,'positions':active.astype(int)+gs_active.astype(int),
            'fees':meta[name]['fees'],'premium':meta[name]['premium']}
        overlap=active&gs_active
        for i in np.flatnonzero(overlap):
            gr=gs_records[i];new=gr['New'];old=gr['Old'];expiry=old.get('CW_Expiry' if new.get('Leg')=='CW' else 'NW_Expiry')
            index=trades.index[trades.Date.eq(dates[i])&trades.Policy.eq(name)][0]
            known=bool(expiry) and not pd.isna(expiry)
            trades.loc[index,'GS_expiry_inferred_from_same_input_old_loader']=str(expiry) if known else ''
            same=known and str(rows.Expiry.iloc[i])==str(expiry) and rows.Strike.iloc[i]==new.get('Strike')
            trades.loc[index,'Same_GS_expiry_and_strike']=bool(same)
    meta['Latest GS']={'active':gs_active,'positions':gs_active.astype(int),'fees':np.zeros(len(dates)),'premium':np.zeros(len(dates))}
    series=['Latest GS']+policies+['GS + '+name for name in policies]
    masks={'All_dates':np.ones(len(dates),bool),'2022plus':dt.year>=2022,'2023plus':dt.year>=2023,'2025plus':dt.year>=2025}
    for name in series:
        p=daily[name].to_numpy(float);eq=p.cumsum();dd=np.maximum.accumulate(np.r_[0.,eq])[1:]-eq
        paths[name+' Equity']=eq;paths[name+' DD']=dd
        for scope,mask in masks.items():
            summ={'Policy':name,'Scope':scope,**stats(p[mask]),'Active_days':int(meta[name]['active'][mask].sum()),
                'Underlying_strategy_entries':int(meta[name]['positions'][mask].sum()),
                'Addon_fees':float(meta[name]['fees'][mask].sum()),'Addon_cumulative_premium_outlay':float(meta[name]['premium'][mask].sum()),
                'Double_addon_fee_net':float(stress.loc[mask,name].sum()),
                'PnL_on_GS_losing_days':float(p[mask&gs_bad].sum()),'PnL_on_worst_GS_decile':float(p[mask&gs_tail].sum()),
                'Correlation_with_GS_all_days':float(np.corrcoef(p[mask],gs_pnl[mask])[0,1]) if np.std(p[mask])>0 else np.nan}
            summaries.append(summ)
    for sheet,labels in [('By_year',dt.year.astype(str)),('By_quarter',dt.to_period('Q').astype(str)),
        ('By_month',dt.to_period('M').astype(str)),('By_FY',pd.Series(np.where(dt.month>=4,dt.year,dt.year-1)).map(lambda y:f'FY{y}-{y+1}').to_numpy())]:
        rows=[]
        for name in series:
            for period in sorted(set(labels)):
                mask=np.asarray(labels==period);rows.append({'Policy':name,'Period':period,**stats(daily.loc[mask,name]),
                    'Active_days':int(meta[name]['active'][mask].sum()),'Underlying_strategy_entries':int(meta[name]['positions'][mask].sum())})
        period_tables[sheet]=pd.DataFrame(rows)
    summary=pd.DataFrame(summaries);paths=pd.DataFrame(paths)
    notes=[
        'Fixed six-policy directional long-option screen; parameters were specified before new strategy PnL; no model fitting or historical winner adoption.',
        'Parallel sleeve authorized by owner. Additional premium budget is additional capital; no equal-capital or free-hedge claim.',
        '100 normalized premium units per active position, determined at entry; fractional continuous quantities, not executable lot-rounded orders.',
        'Only second/minute data available in sampled files; source dates1900 are time placeholders; decision uses past availability, no backward fills.',
        'Buy at ask+1bp, sell at bid-1bp; fresh synchronized source-second fills with a decision delay. Multi-leg atomicity/capacity and source provenance remain modeled.',
        'Additional round-trip drag1 normalized unit per leg/standard25000-U quantity; double-drag stress included. This is not a verified actual statutory/broker fee schedule.',
        'Stop30% of premium, delayed fill may overshoot; long gross loss bounded by paid premium. No futures neutralization.',
        'Latest GS is from the matched fresh engine replay; it retains legacy LUT, fill and backfill limitations, unlike the new sleeve execution model.',
        'Combined DD is calculated from the combined daily PnL; strategy DDs are not added. DD excludes intraday MtM.',
        'GS losing-day/decile attribution is descriptive ex-post analysis, never a signal. All-calendar-day correlation includes zero/no-trade days.',
        'Same-contract offset is diagnostic; buying existing GS shorts can reduce net exposure without creating independent return alpha.',
        'Earlier research used these years; this screen is not an untouched holdout. No neural model, parameter optimization or alpha validation is claimed.',
        'Zero-error complete histories and exact serial/pool pilot parity are required before reporting.'
    ]
    tables={'Read_me':pd.DataFrame({'Method':notes}),'Summary':summary,'All_policy_days':trades,'Trades':trades[trades.Traded],
        'Daily_PnL':daily[['Date']+series],'Double_fee_daily':stress[['Date']+series],'Equity_DD':paths,
        **period_tables,'Latest_GS_daily':gs,'Data_audit':pd.read_csv(OUT/'data_audit.csv',dtype={'Source_mtime_ns':str}),
        'Specification':pd.DataFrame({'Line':(ROOT/'COMPLEMENT_STRATEGY_SPEC.md').read_text().splitlines()}),
        'Replay_audit':pd.DataFrame([{k:v for k,v in r.items() if k!='Rows'} for r in replay])}
    tables['Coverage']=pd.DataFrame([{'Policy':name,'Calendar_dates':len(g),'Entered_dates':int(g.Traded.sum()),
        'Direction_signal_dates':int(g.Breakout_minutes.gt(0).sum()),
        'No_direction_dates':int(g.Breakout_minutes.eq(0).sum()),
        'Direction_but_no_filled_entry_dates':int((g.Breakout_minutes.gt(0)&~g.Traded).sum()),
        'Audited_ineligible_dates':int(g.Reason.fillna('').str.startswith('Ineligible:').sum())} for name,g in trades.groupby('Policy')])
    tables['Formulas']=pd.DataFrame([
        ('Availability','available_second = source_label_second + 1','Only available seconds strictly before the decision enter features'),
        ('Opening breakout','up: U > high + 0.1*(high-low); down: U < low - 0.1*(high-low)','09:15–09:30 range; decisions09:31–10:30'),
        ('Efficiency','ER = abs(U_last-U_first) / sum(abs(diff(U)))','Prior300seconds; at least240 valid seconds; fixed gate>=0.30'),
        ('Purchased-IV percentile','rank = (count(reference < current) + 0.5*count(reference == current)) / count(reference)','Mean CE/PE AskIV; same strike; minimum60 past observations; LowerIV requires<=0.50'),
        ('Entry debit','debit = sum(integer_leg_quantity * entry_ask) * (1 + 0.0001)','Single1leg, tilted2:1, balanced1:1; all option quantities positive'),
        ('Fixed premium budget','norm = 25000/U_entry; size = 100/(debit*norm)','100 normalized premium units; no future averaging; fractional research units'),
        ('Actual contract units','contract_units_leg = integer_leg_quantity * norm * size','Not lot-rounded or capacity-certified'),
        ('Exit credit','credit = sum(integer_leg_quantity * exit_bid) * (1 - 0.0001)','Actual fresh synchronized delayed exit, not an LTP mark'),
        ('Gross / fees / net','gross = (credit-debit)*norm*size; fee = 1*sum(integer_leg_quantities)*size; net = gross-fee','Fee is modeled round-trip drag; stress net=gross-2*fee'),
        ('Risk bound','gross >= -100; net >= -100-fee','30% premium stop can overshoot on its next executable quote'),
        ('Greek model','T = DTE_calendar/365; d1 = (ln(S/K) + 0.5*sigma^2*T)/(sigma*sqrt(T))','Zero-rate model using entry AskIV; missing valid AskIV gives unmeasured greeks'),
        ('Delta','call_delta = N(d1); put_delta = N(d1)-1; position_delta = sum(q*delta)*norm*size','No futures neutralization'),
        ('Gamma / vega','gamma = phi(d1)/(S*sigma*sqrt(T)); vega_1pt = 0.01*S*phi(d1)*sqrt(T)','Aggregate each leg with q*norm*size; vega is per1percentage point of IV'),
        ('Portfolio','combined_daily = latest_GS_daily + sleeve_daily','Additional premium capital; closed-day portfolio DD recomputed from combined equity'),
    ],columns=['Calculation','Equation','Interpretation'])
    if (OUT/'eligibility_audit.csv').exists():
        tables['Eligibility_audit']=pd.read_csv(OUT/'eligibility_audit.csv',dtype={'Tick_mtime_ns':str})
        tables['Muhurat_calendar']=pd.read_csv(ROOT/'muhurat_sessions.csv')
        note='11 pre-entry admission failures audited: 5 verified Diwali/Muhurat sessions outside the fixed morning window, 5 missing-underlying files, 1 insufficient-opening-data date. Scheduled closures are not missing morning data. NSE F&O circulars and year-specific IST hours are recorded in Muhurat_calendar; all dates and original checkpoints/exceptions retained. Missing exits and unexpected execution failures remain fatal. No trading-hour shift, entry, PnL or DD changed.'
        notes.append(note);tables['Read_me']=pd.DataFrame({'Method':notes})
    if (OUT/'sample_signal_coverage.csv').exists():tables['Sample_signal_coverage']=pd.read_csv(OUT/'sample_signal_coverage.csv')
    files=charts(daily,paths,policies)
    with pd.ExcelWriter(OUT/'complement_strategy.xlsx',engine='openpyxl') as w:
        for sheet,frame in tables.items():frame.to_excel(w,sheet_name=sheet,index=False);w.sheets[sheet].freeze_panes='B2'
        from openpyxl.drawing.image import Image
        ws=w.book.create_sheet('Charts')
        for i,file in enumerate(files):
            pic=Image(OUT/file);pic.width=1000;pic.height=650;ws.add_image(pic,f'A{1+38*i}')
    for name,frame in [('summary',summary),('trades',trades),('daily_pnl',daily[['Date']+series])]:frame.to_csv(OUT/(name+'.csv'),index=False)
    full=summary[summary.Scope.eq('All_dates')]
    lines=['# Parallel long-option complement: fixed screen','',*['- '+note for note in notes],'',
        '| Policy | Net | Maximum DD | Worst day | Active days | Entries |','|---|---:|---:|---:|---:|---:|']
    for row in full.itertuples(index=False):lines.append(f'| {row.Policy} | {row.Net:,.2f} | {row.MDD:.2f} | {row.Worst_day:.2f} | {row.Active_days} | {row.Underlying_strategy_entries} |')
    entries=int(trades.Traded.sum())
    if entries==0:
        lines+=['','**Coverage failure: no long-option entries were executed.** All combined paths equal GS alone. This provides no evidence of return alpha, risk reduction or a working complement.',
            'The sampled signal audit diagnoses a restrictive1-second path-efficiency gate: do not interpret nonparticipation as low-risk strategy performance. Any revised signal needs a separately recorded prior-data-calibrated study.']
    else:
        independent=trades.loc[trades.Traded,'Date'].nunique()
        lines+=['',f'Total long entries across the six policy books: {entries}, on only {independent} distinct dates. Trades shared by policy variants are not independent observations. Every fixed policy is reported; none is adopted from a historical maximum.',
            'Report the participation count with PnL and correlation: sparse trading alone does not establish a reliable directional or diversification edge.']
    baseline=full[full.Policy.eq('Latest GS')].iloc[0]
    combined=full[full.Policy.str.startswith('GS + ')]
    if combined.MDD.ge(baseline.MDD-1e-8).all():
        lines+=['','None of the six combined paths reduced maximum daily drawdown below latest GS alone. No complementary policy is validated or adopted by this screen.']
    lines+=['',*['!['+file+']('+file+')' for file in files],'']
    (OUT/'REPORT.md').write_text('\n'.join(lines))
    inputs=[Path(__file__),ROOT/'complement_strategy.py',ROOT/'COMPLEMENT_STRATEGY_SPEC.md',ROOT/'test_complement_strategy.py',
        ROOT/'annotate_complement_calendar.py',ROOT/'test_complement_calendar.py',ROOT/'muhurat_sessions.csv',OUT/'calendar_annotation_manifest.json',
        ROOT/'verify_complement_strategy.py',ROOT/'resolve_complement_eligibility.py',ROOT/'complement_signal_audit.py',ROOT/'complement_data_audit.py',
        OUT/'data_audit.csv',OUT/'audit_manifest.json',*sorted((OUT/'raw_ineligible_days').glob('*.pkl')),
        OUT/'run_manifest.json',OUT/'pilot_manifest.json',*sorted((OUT/'days').glob('*.pkl')),*sorted((OUT/'pilot_days').glob('*.pkl')),
        ROOT/'output/logic_comparison_20261021/run_manifest.json',*sorted((ROOT/'output/logic_comparison_20261021/days').glob('*.pkl'))]
    manifest={'Inputs':{str(p.relative_to(ROOT)):hashlib.sha256(p.read_bytes()).hexdigest() for p in inputs},
        'Outputs':{p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in OUT.iterdir() if p.is_file() and p.name not in ('manifest.json','verification.json')}}
    (OUT/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n');print(full.to_string(index=False),flush=True)


def charts(daily,paths,policies):
    dates=pd.to_datetime(daily.Date);files=[]
    fig,axes=plt.subplots(2,1,figsize=(12,9),sharex=True,layout='constrained')
    axes[0].plot(dates,paths['Latest GS Equity'],color='black',lw=1.8,label='Latest GS')
    axes[1].plot(dates,paths['Latest GS DD'],color='black',lw=1.8,label='Latest GS')
    for name in policies:
        axes[0].plot(dates,paths['GS + '+name+' Equity'],lw=1,label=name)
        axes[1].plot(dates,paths['GS + '+name+' DD'],lw=1,label=name)
    axes[0].set(title='Latest GS plus independently budgeted long options',ylabel='Cumulative normalized net');axes[0].legend(fontsize=7,ncol=2)
    axes[1].set(ylabel='Daily equity drawdown',xlabel='Date')
    fig.savefig(OUT/'combined_equity_dd.png',dpi=170,bbox_inches='tight');fig.savefig(OUT/'combined_equity_dd.svg',bbox_inches='tight');plt.close(fig);files.append('combined_equity_dd.png')
    fig,axes=plt.subplots(2,1,figsize=(12,9),sharex=True,layout='constrained')
    for name in policies:
        axes[0].plot(dates,paths[name+' Equity'],label=name,lw=1)
        axes[1].plot(dates,paths[name+' DD'],lw=1)
    axes[0].set(title='Long-option sleeves alone: same fixed premium budget',ylabel='Cumulative normalized net');axes[0].legend(fontsize=7,ncol=2)
    axes[1].set(ylabel='Daily equity drawdown',xlabel='Date')
    fig.savefig(OUT/'complement_equity_dd.png',dpi=170,bbox_inches='tight');fig.savefig(OUT/'complement_equity_dd.svg',bbox_inches='tight');plt.close(fig);files.append('complement_equity_dd.png')
    return files


if __name__=='__main__':main()
