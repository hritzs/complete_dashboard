"""Matched old/latest flat-book comparison; refuses partial or failed replay."""
import ast,hashlib,json,os,pickle
from pathlib import Path
os.environ.setdefault('MPLCONFIGDIR','/tmp/gs-logic-comparison-mpl')
import numpy as np
import pandas as pd
import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt
from compare_latest_logic import ROOT,OUT,ARCHIVE

LABELS={'Archived':'Archived research vintage','Old':'Old rules / matched current inputs','New':'Latest rules / matched current inputs'}


def source_settings():
    """Literal source configuration, with engine-specific names left visible."""
    settings={}
    for key,name in [('Old','Archived_GammaShort_Backtest_Final.py'),('New','7DResume_0.75_1.2.py')]:
        values={}
        for node in ast.parse((OUT/'sources'/name).read_text()).body:
            if not isinstance(node,ast.Assign):continue
            for target in node.targets:
                if not isinstance(target,ast.Name) or not target.id.isupper():continue
                try:value=ast.literal_eval(node.value)
                except (ValueError,TypeError):continue
                if 'PATH' in target.id or target.id.endswith(('DIR','BASE')) or target.id.startswith(('_','DERIVED_','LG_')):continue
                if target.id in ('CW_COLS','NW_COLS','TICK_COLS','LOG_COLS','EXIT_REASONS','RELAX_VARIANTS','SAVE_DAILY_FILES'):continue
                values[target.id]=repr(value)
        settings[key]=values
    return pd.DataFrame([{'Setting':name,'Old_source':settings['Old'].get(name,''),'Latest_source':settings['New'].get(name,'')}
        for name in sorted(set(settings['Old'])|set(settings['New']))])


def effective_settings():
    values=json.loads((OUT/'effective_settings.json').read_text())
    return pd.DataFrame([{'Setting':key,'Old_initialized':values['Old'].get(key,''),'New_initialized':values['New'].get(key,'')}
        for key in sorted(set(values['Old'])|set(values['New']))])


def stats(frame):
    p=frame.Net_PnL.to_numpy(float);eq=np.cumsum(p);dd=np.maximum.accumulate(np.r_[0.,eq])[1:]-eq
    active=frame.Leg.isin(['CW','NW']);cw=frame.Leg.eq('CW');nw=frame.Leg.eq('NW')
    return {'Net':float(p.sum()),'MDD':float(dd.max()),'Worst_day':float(p.min()),'Net_per_MDD':float(p.sum()/dd.max()) if dd.max() else np.nan,
        'Entries':int(active.sum()),'CW_entries':int(cw.sum()),'NW_entries':int(nw.sum()),
        'Wins':int(((frame.Net_PnL>0)&active).sum()),'Losses':int(((frame.Net_PnL<0)&active).sum()),
        'Mean_net_per_entry':float(p.sum()/active.sum()) if active.sum() else np.nan,
        'Event_weighted_trade_units':float(pd.to_numeric(frame.Trades,errors='raise').fillna(0).sum()),
        'CW_net':float(frame.loc[cw,'Net_PnL'].sum()),'NW_net':float(frame.loc[nw,'Net_PnL'].sum()),
        'CW_entry_cost':float(frame.loc[cw,'Entry_Cost'].sum()),'CW_hedge_cost':float(frame.loc[cw,'Hedge_Cost'].sum())}


def drawdown_episodes(books):
    episodes=[]
    for key,frame in books.items():
        equity=0.;peak=0.;peak_date='Initial zero';episode=None
        for i,row in enumerate(frame.itertuples(index=False)):
            equity+=row.Net_PnL;draw=peak-equity
            if draw<=1e-9:
                if episode is not None:
                    episode.update(Recovery_date=row.Date,Underwater_calendar_rows=i-episode.pop('_start'))
                    episodes.append(episode);episode=None
                peak=max(peak,equity);peak_date=row.Date
            else:
                if episode is None:episode={'Code':key,'Book':LABELS[key],'Peak_date':peak_date,'Peak_equity':peak,
                    'First_underwater_date':row.Date,'Maximum_DD':0.,'Trough_date':row.Date,'_start':i}
                if draw>episode['Maximum_DD']:episode.update(Maximum_DD=draw,Trough_date=row.Date)
        if episode is not None:
            episode.update(Recovery_date='Unrecovered at end',Underwater_calendar_rows=len(frame)-episode.pop('_start'))
            episodes.append(episode)
    return pd.DataFrame(episodes)


def load_books():
    manifest=json.loads((OUT/'run_manifest.json').read_text());assert manifest['Status']=='complete' and not manifest['Failed_dates']
    for name,digest in manifest['Inputs'].items():assert hashlib.sha256((ROOT/name).read_bytes()).hexdigest()==digest,name
    dates=manifest['Dates'];assert sorted(p.stem for p in (OUT/'days').glob('*.pkl'))==dates
    rows=[pickle.load((OUT/'days'/(date+'.pkl')).open('rb')) for date in dates]
    assert len(rows)==1430 and all(not row.get('Error') for row in rows)
    archived=pd.DataFrame([pickle.load((ARCHIVE/(day+'.pkl')).open('rb')) for day in dates])
    books={'Archived':archived,'Old':pd.DataFrame([r['Old'] for r in rows]),'New':pd.DataFrame([r['New'] for r in rows])}
    for key,d in books.items():
        assert d.Date.tolist()==dates and not d.Date.duplicated().any()
        d['Net_PnL']=pd.to_numeric(d.Net_PnL,errors='raise').fillna(0.);d.Leg=d.Leg.fillna('')
        for c in ['Entry_Cost','Hedge_Cost','Gross_PnL','Hedge_Notional']:
            if c not in d:d[c]=np.nan
            d[c]=pd.to_numeric(d[c],errors='raise').fillna(0.)
        for c in ['Entry_Time','Stage','Cell','Strike','Entry_U','Exit_Time','Exit_Reason','Trades','SL_bps','TP_bps','Note','DTE_idx','Event','Min_Hedges','Intra_Hedges']:
            if c not in d:d[c]=np.nan
        assert np.isfinite(d.Net_PnL).all()
        assert (d.loc[~d.Leg.isin(['CW','NW']),'Net_PnL']==0).all()
        cw=d.Leg.eq('CW')
        np.testing.assert_allclose(d.loc[cw,'Net_PnL'],d.loc[cw,'Gross_PnL']-d.loc[cw,'Entry_Cost']-d.loc[cw,'Hedge_Cost'],atol=1e-9,rtol=0)
    return books,rows,manifest


def main():
    books,records,run=load_books();dates=books['Old'].Date
    comp=pd.DataFrame({'Date':dates})
    columns=['Leg','Entry_Time','Stage','Cell','Strike','Entry_U','Exit_Time','Exit_Reason','Net_PnL','Gross_PnL',
        'Entry_Cost','Hedge_Cost','Hedge_Notional','Trades','SL_bps','TP_bps','DTE_idx','Event','Min_Hedges','Intra_Hedges','Note']
    for key,d in books.items():
        for col in columns:comp[key+'_'+col]=d[col].to_numpy()
    old,new=books['Old'],books['New'];oa=old.Leg.isin(['CW','NW']).to_numpy();na=new.Leg.isin(['CW','NW']).to_numpy()
    same=(oa&na&old.Leg.eq(new.Leg).to_numpy()&old.Entry_Time.fillna('').eq(new.Entry_Time.fillna('')).to_numpy()
        &np.isclose(pd.to_numeric(old.Strike,errors='coerce'),pd.to_numeric(new.Strike,errors='coerce'),atol=1e-9,rtol=0,equal_nan=False))
    comp['Entry_status']=np.select([~oa&~na,oa&~na,~oa&na,same],['No trade both','Old only','New only','Same entry'],default='Changed entry')
    comp['New_minus_old']=new.Net_PnL.to_numpy()-old.Net_PnL.to_numpy()
    comp['Old_minus_archive']=old.Net_PnL.to_numpy()-books['Archived'].Net_PnL.to_numpy()
    comp['Exit_changed']=same&(~old.Exit_Time.fillna('').eq(new.Exit_Time.fillna('')).to_numpy()|~old.Exit_Reason.fillna('').eq(new.Exit_Reason.fillna('')).to_numpy())
    summary=[];period_tables={};cohorts=[]
    for key,d in books.items():
        for period,mask in [('All_common_dates',np.ones(len(d),bool)),('Research_scored_2022plus',d.Date.ge('2022-01-01').to_numpy()),
            ('Nested_scored_2023plus',d.Date.ge('2023-01-01').to_numpy()),('MC_evaluation_2025plus',d.Date.ge('2025-01-01').to_numpy())]:
            summary.append({'Book':LABELS[key],'Code':key,'Period':period,**stats(d[mask])})
    dt=pd.to_datetime(dates);years=dt.dt.year
    for sheet,periods in [('By_year',years.astype(str)),('By_quarter',dt.dt.to_period('Q').astype(str)),
        ('By_month',dt.dt.to_period('M').astype(str)),('By_financial_year',pd.Series(np.where(dt.dt.month.ge(4),years,years-1)).map(lambda y:f'FY{y}-{y+1}'))]:
        rows=[]
        for key,d in books.items():
            for per in sorted(set(periods)):
                mask=np.asarray(periods==per);rows.append({'Book':LABELS[key],'Code':key,'Period':per,**stats(d[mask])})
        period_tables[sheet]=pd.DataFrame(rows)
    for status in comp.Entry_status.unique():
        mask=comp.Entry_status.eq(status).to_numpy()
        for key in ('Old','New'):cohorts.append({'Cohort':status,'Book':LABELS[key],'Dates':int(mask.sum()),**stats(books[key][mask])})
    transitions=pd.crosstab(old.Leg.replace('','No trade'),new.Leg.replace('','No trade')).reset_index()
    summary=pd.DataFrame(summary);daily_pnl=pd.DataFrame({'Date':dates,'Flat size 1':books['Archived'].Net_PnL,
        'Old rules matched':old.Net_PnL,'Latest rules matched':new.Net_PnL})
    paths={'Date':dates}
    for key,d in books.items():
        eq=d.Net_PnL.cumsum().to_numpy();paths[key+'_Equity']=eq;paths[key+'_DD']=np.maximum.accumulate(np.r_[0.,eq])[1:]-eq
    changes=[
        ('DTE1/2 minute-start residual','Opposite-side residual: negative net becomes positive and vice versa','Same-side residual: retains sign of pre-hedge net','Exact hedge-kernel regression tests; alters turnover, futures PnL and possible exits'),
        ('Intraminute hard delta hedge','1.2x threshold; neutral at intra fraction1','Same','Not a new sized position; source kernel agrees on this branch'),
        ('DTE3–7 points hedge','PointsAllowed,75% minute / full intra','Same one-strike kernel branch','Version differences can still arise from mark timing'),
        ('Post-entry minute grid','Entry second+k minutes, forward to first available tick','Clock minutes, backward to last available tick','Especially relevant when exact :00 tick is absent or entry is delayed'),
        ('Daily ADJ change','Uses Final_IV_Chg when present','Recomputes gap/non-gap Adj_IV_Chg or IV_Chg','Frozen same CSV, but version-specific feature definitions retained'),
        ('Grid end switch','2026-06-03','2026-08-03','Actual source-version settings; no silent calendar override'),
        ('CW tables','Frozen latest tables','Exactly same frozen latest tables','Five table files at old/new original locations are byte-identical'),
        ('NW table','Frozen latest23-YES table','Exactly same frozen latest23-YES table','Current old path has24 YES; do not silently compare those two table vintages'),
        ('Original CW tax hurdles','13/5/2/2/6 through frozen existing LUTs','Same frozen LUTs; no refit or extra tax deduction','This comparison changes execution versions, not LUT training or conviction tax'),
        ('Scale / costs / events','25000/U; source entry/hedge costs; events1/3','Same configured values','NW source Gross_PnL is already net of embedded NW hedge costs; cost columns are CW-only comparisons'),
        ('Sizing study applicability','Earlier sizing studies were trained/scored on archived old outcomes','Fresh flat baseline only','Do not call older sizing conclusions validated on the new engine')]
    notes=['Same1430 archived dates, identical frozen current daily/event inputs and latest table files in both fresh books.',
        'Archived reference retains its original input vintage; use New minus Old matched for implementation comparison.',
        'Flat multiplier1 throughout. This is not a new sizing fit or an old-PnL rescaling.',
        'Every date is included, including zero-PnL/no-trade dates. Maximum DD includes initial zero equity; per-period DD resets.',
        'DD is the closed-day equity drawdown, not a tick-by-tick intraday mark-to-market drawdown. Values are normalized backtest PnL units.',
        'Source option30-second/futures3-second averages and backfills remain; this is a version comparison, not a certification of causal fills.',
        'CW gross minus explicit costs equals net. NW source labels its already-net result as gross; NW cost columns are not a claim of zero execution cost.',
        'Same-entry cohort matches date, expiry leg, entry minute and strike. Cell/exit differences remain visible.',
        'Pruned versus full new source and native-loader parity passed the six-day pilot. Per-date errors must be zero.',
        'The old fixed-time diagnostic simulations are retained: removing their rows could alter DTE fallback on a no-production-row day.',
        'Source_settings lists literal uppercase assignments only; differently named or computed settings need the source and Rule_changes.',
        'Neither live code nor shared inputs were changed. Source snapshots, fingerprints and generated adapter are retained.']
    tables={'Read_me':pd.DataFrame({'Method':notes}),'Summary':summary,'Rule_changes':pd.DataFrame(changes,columns=['Rule','Archived_code','Latest_code','Interpretation']),
        'Source_settings':source_settings(),'Effective_settings':effective_settings(),
        'Daily_comparison':comp,'Trade_comparison':comp[oa|na|books['Archived'].Leg.isin(['CW','NW']).to_numpy()],
        'Daily_PnL':daily_pnl,'Equity_DD':pd.DataFrame(paths),'DD_episodes':drawdown_episodes(books),
        'Entry_cohorts':pd.DataFrame(cohorts),'Leg_transitions':transitions,
        **period_tables,'Old_daily':old,'New_daily':new,'Archived_daily':books['Archived'],
        'Replay_audit':pd.DataFrame([{k:v for k,v in r.items() if k not in ('Old','New')} for r in records])}
    tables['Hedge_equations']=pd.DataFrame([
        ('Net delta','net = short_option_delta + futures_position','Entry futures initialize to -short_option_delta; initial net0'),
        ('DTE1/2 minute trigger','abs(net) > delta_threshold','Threshold0.25 at DTE_idx0;0.20 at DTE_idx1; strict greater-than'),
        ('Minute residual magnitude','residual = (1 - 0.75) * delta_threshold','75% is used on the threshold, not simply75% of current net'),
        ('Old minute target','post_net = -sign(net) * residual','Futures adjustment = post_net - net; residual changes side'),
        ('Latest minute target','post_net = sign(net) * residual','Futures adjustment = post_net - net; residual retains side'),
        ('DTE1/2 intra trigger','abs(net) > 1.2 * delta_threshold','Fraction1.0 yields residual0; both versions target neutral'),
        ('DTE3–7 trigger','points_out = abs(net / option_gamma); trigger > PointsAllowed at minute / 1.2*PointsAllowed intra','One-strike points branch unchanged; actual marks still depend on version'),
        ('Daily net accounting','CW net = event_adjusted_gross - entry_cost - hedge_cost','NW source result already includes its hedge-cost handling'),
        ('Daily DD','equity[t] = sum(net[0:t]); DD[t] = max(0,equity[0:t]) - equity[t]','Maximum closed-day DD=max(DD); not intraday MtM'),
    ],columns=['Calculation','Equation','Meaning'])
    examples=[]
    for dte,threshold in [(1,.25),(2,.20)]:
        for sign in (-1,1):
            net=sign*1.2*threshold;residual=.25*threshold
            examples.append({'DTE_bucket':dte,'Pre_hedge_net':net,'Threshold':threshold,'Minute_fraction':.75,
                'Residual_magnitude':residual,'Old_post_net':-sign*residual,'New_post_net':sign*residual,
                'Old_futures_adjustment':-sign*residual-net,'New_futures_adjustment':sign*residual-net})
    tables['Hedge_examples']=pd.DataFrame(examples)
    bothcw=old.Leg.eq('CW')&new.Leg.eq('CW')
    attribution=[]
    for status in sorted(comp.loc[bothcw,'Entry_status'].unique()):
        mask=bothcw&comp.Entry_status.eq(status)
        gross=float((new.Gross_PnL-old.Gross_PnL)[mask].sum())
        entry=float((new.Entry_Cost-old.Entry_Cost)[mask].sum());hedge=float((new.Hedge_Cost-old.Hedge_Cost)[mask].sum())
        net=float(comp.loc[mask,'New_minus_old'].sum())
        np.testing.assert_allclose(net,gross-entry-hedge,atol=1e-8,rtol=0)
        attribution.append({'Entry_status':status,'CW_dates_both':int(mask.sum()),'Gross_delta':gross,
            'Entry_cost_delta':entry,'Hedge_cost_delta':hedge,'Net_delta':net,'Accounting_error':net-(gross-entry-hedge)})
    tables['CW_cost_attribution']=pd.DataFrame(attribution)
    if (OUT/'native_fallbacks.json').exists():
        tables['Native_fallbacks']=pd.DataFrame(json.loads((OUT/'native_fallbacks.json').read_text()))
        note='Empty-expiry adapter failures were re-run through both original native loaders; both returned no trade. Original adapter exceptions and fallback source are preserved.'
        notes.append(note);tables['Read_me']=pd.DataFrame({'Method':notes})
    for sheet,column in [('By_DTE','DTE_idx'),('By_stage','Stage'),('By_exit','Exit_Reason')]:
        grouped=[]
        for key,d in books.items():
            active=d.Leg.isin(['CW','NW'])
            for value,frame in d[active].groupby(column,dropna=False):
                grouped.append({'Book':LABELS[key],'Code':key,column:value,**stats(frame)})
        tables[sheet]=pd.DataFrame(grouped)
    chart_files=charts(comp,pd.DataFrame(paths))
    with pd.ExcelWriter(OUT/'latest_logic_comparison.xlsx',engine='openpyxl') as w:
        for sheet,frame in tables.items():frame.to_excel(w,sheet_name=sheet,index=False);w.sheets[sheet].freeze_panes='B2'
        from openpyxl.drawing.image import Image
        ws=w.book.create_sheet('Charts')
        for i,file in enumerate(chart_files):
            pic=Image(OUT/file);pic.width=1000;pic.height=500;ws.add_image(pic,f'A{1+i*29}')
    for name,frame in [('summary',summary),('daily_comparison',comp),('daily_pnl',daily_pnl),('old_daily',old),('new_daily',new),('entry_cohorts',tables['Entry_cohorts'])]:frame.to_csv(OUT/(name+'.csv'),index=False)
    values=summary[summary.Period.eq('All_common_dates')].set_index('Code');vold=values.loc['Old'];vnew=values.loc['New']
    lines=['# Latest GammaShort logic: matched replay','',*['- '+n for n in notes],'',
        '| Book | Net | Maximum DD | Worst day | CW trades | NW trades |','|---|---:|---:|---:|---:|---:|']
    for key,r in values.iterrows():lines.append(f'| {r.Book} | {r.Net:,.2f} | {r.MDD:.2f} | {r.Worst_day:.2f} | {r.CW_entries} | {r.NW_entries} |')
    lines+=['',f'Latest minus freshly replayed old: net {vnew.Net-vold.Net:+.6f}; MDD {vnew.MDD-vold.MDD:+.6f}.',
        f'Entry status: {comp.Entry_status.value_counts().to_dict()}. Same-entry exits changed: {int(comp.Exit_changed.sum())}.',
        'Earlier sizing results remain studies on the old book. They must not be described as new-logic sizing performance.','']
    for file in chart_files:lines.append(f'![{file}]({file})\n')
    (OUT/'REPORT.md').write_text('\n'.join(lines)+'\n')
    inputs=[Path(__file__),ROOT/'test_latest_logic.py',ROOT/'test_logic_ledger.py',ROOT/'verify_logic_comparison.py',ROOT/'replay_empty_expiry_native.py',
        *sorted((OUT/'empty_expiry_raw_errors').glob('*.pkl')),OUT/'run_manifest.json',OUT/'pilot_manifest.json',
        *sorted((OUT/'pilot_days').glob('*.pkl')),*sorted(ARCHIVE.glob('*.pkl')),*sorted((OUT/'days').glob('*.pkl'))]
    manifest={'Inputs':{str(p.relative_to(ROOT)):hashlib.sha256(p.read_bytes()).hexdigest() for p in inputs},
        'Outputs':{p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in OUT.iterdir() if p.is_file() and p.name not in ('manifest.json','verification.json')}}
    (OUT/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n');print(values.to_string(),flush=True)


def charts(comp,paths):
    files=[]
    def save(fig,name):
        fig.savefig(OUT/(name+'.png'),dpi=170,bbox_inches='tight');fig.savefig(OUT/(name+'.svg'),bbox_inches='tight');plt.close(fig);files.append(name+'.png')
    fig,axes=plt.subplots(2,1,figsize=(12,7),sharex=True,layout='constrained');date=pd.to_datetime(paths.Date)
    for key,color in [('Archived','#aaaaaa'),('Old','#444444'),('New','#176ea4')]:
        axes[0].plot(date,paths[key+'_Equity'],label=LABELS[key],color=color,lw=1.2)
        axes[1].plot(date,paths[key+'_DD'],color=color,lw=1.2)
    axes[0].set(title='Matched flat size1 books: same dates and fresh input snapshot',ylabel='Cumulative net (normalized units)');axes[0].legend(fontsize=8)
    axes[1].set(ylabel='Drawdown',xlabel='Date');save(fig,'matched_equity_drawdown')
    fig,axes=plt.subplots(1,2,figsize=(12,4.5),layout='constrained')
    for status,g in comp.groupby('Entry_status'):
        if status=='No trade both':continue
        axes[0].scatter(pd.to_datetime(g.Date),g.New_minus_old,label=status,s=9,alpha=.7)
    axes[0].axhline(0,color='black',lw=.6);axes[0].set(title='Daily implementation PnL differences',ylabel='Latest minus matched old');axes[0].legend(fontsize=8)
    monthly=comp.assign(Month=pd.to_datetime(comp.Date).dt.to_period('M').astype(str)).groupby('Month').New_minus_old.sum()
    axes[1].bar(np.arange(len(monthly)),monthly,color=np.where(monthly>=0,'#26956a','#c55b5b'))
    ticks=np.arange(0,len(monthly),6);axes[1].set_xticks(ticks,monthly.index[ticks],rotation=45,ha='right');axes[1].set(title='Monthly net difference',ylabel='Latest minus matched old')
    save(fig,'daily_monthly_differences');return files


if __name__=='__main__':main()
