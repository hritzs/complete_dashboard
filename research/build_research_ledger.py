"""Rebuild an iteration-indexed Excel ledger without refitting or editing sources."""
import hashlib
import json
import pickle
from pathlib import Path
import numpy as np
import pandas as pd
from openpyxl import Workbook, load_workbook
from openpyxl.cell import WriteOnlyCell
from openpyxl.styles import Font, PatternFill, Alignment
from openpyxl.utils import get_column_letter
from openpyxl.workbook.properties import CalcProperties
from sizing_algorithm_report import add_sheets

ROOT=Path(__file__).resolve().parent
OUT=ROOT/'output/research_ledger'
REGISTRY=ROOT/'research_iterations.json'
INPUTS=set()


def excel_rank(value,reference):
    # COUNTIF("<"&cell) converts to rounded text; compare numbers directly.
    less=f'SUMPRODUCT(--ISNUMBER({reference}),--({reference}<{value}))'
    equal=f'SUMPRODUCT(--ISNUMBER({reference}),--({reference}={value}))'
    return f'({less}+({equal}+1)/2)/COUNT({reference})'


def read(path,sheet=None):
    path=Path(path); INPUTS.add(path)
    return pd.read_excel(path,sheet_name=sheet) if path.suffix=='.xlsx' else pd.read_csv(path)


def scalar(v):
    if v is None or (not isinstance(v,(list,dict)) and pd.isna(v)): return None
    if isinstance(v,np.generic): return v.item()
    return v


def table(book,name,frame):
    ws=book.create_sheet(name); ws.freeze_panes='B2'
    ws.column_dimensions['A'].width=19
    for k in range(2,min(len(frame.columns),20)+1): ws.column_dimensions[get_column_letter(k)].width=19
    for k,name in enumerate(frame.columns,1):
        if name in ('Name','Policy_name','Protocol','Precision_coverage','Feature','Included','Line'):
            ws.column_dimensions[get_column_letter(k)].width=70 if name=='Line' else 42
    ws.row_dimensions[1].height=32
    header=[]
    for value in frame.columns:
        cell=WriteOnlyCell(ws,value=str(value)); cell.font=Font(bold=True,color='FFFFFF')
        cell.fill=PatternFill('solid',fgColor='24476A'); cell.alignment=Alignment(wrap_text=True); header.append(cell)
    ws.append(header)
    for row in frame.itertuples(index=False,name=None): ws.append([scalar(v) for v in row])
    if len(frame.columns): ws.auto_filter.ref=f'A1:{get_column_letter(len(frame.columns))}{len(frame)+1}'
    return ws


def stats(p,active,size,base):
    p=np.asarray(p,float); active=np.asarray(active,bool); size=np.asarray(size,float); base=np.asarray(base,float)
    eq=np.cumsum(p); dd=np.maximum.accumulate(np.r_[0.,eq])[1:]-eq
    pos=p[p>0].sum(); neg=-p[p<0].sum(); n=int(active.sum())
    draw=0; longest=0
    for value in dd:
        draw=draw+1 if value>1e-9 else 0; longest=max(longest,draw)
    mdd=float(dd.max()) if len(p) else 0.
    return dict(Net=float(p.sum()),Flat_net=float(base.sum()),Delta_net=float((p-base).sum()),
        MDD=mdd,Net_per_MDD=float(p.sum()/mdd) if mdd else np.nan,Worst_day=float(p.min()) if len(p) else np.nan,
        Best_day=float(p.max()) if len(p) else np.nan,Entries=n,Wins=int(((p>0)&active).sum()),
        Losses=int(((p<0)&active).sum()),Breakeven=int(((p==0)&active).sum()),
        Win_pct=100*float((p[active]>0).mean()) if n else np.nan,
        PnL_per_entry=float(p.sum()/n) if n else np.nan,Profit_factor=float(pos/neg) if neg else np.nan,
        Mean_executed_size=float(size[active].mean()) if n else np.nan,
        Min_executed_size=float(size[active].min()) if n else np.nan,Max_executed_size=float(size[active].max()) if n else np.nan,
        Longest_DD_days=longest,Days=len(p),From='',To='')


def policy(name,pnl,size,active,entry,leg,exit_info=None):
    return dict(name=name,pnl=np.asarray(pnl,float),size=np.asarray(size,float),active=np.asarray(active,bool),
                entry=np.asarray(entry,object),leg=np.asarray(leg,object),exit=exit_info)


def load_iteration(config,raw):
    path=ROOT/'output'/config['source']; x=read(path)
    kind=config['kind']; daily_sheet=config.get('daily','daily_comparison' if kind=='tick' else 'Daily_PnL' if kind=='wide' else 'Trades')
    source=x[daily_sheet].copy(); source['Date']=source.Date.astype(str).str[:10]
    if source.Date.duplicated().any(): raise ValueError('Duplicate dates: '+config['id'])
    # Tax source contains trades only: keep all production calendar days in range.
    if kind=='tax': dates=raw.Date
    else: dates=source.Date
    d=raw.set_index('Date').loc[list(dates)].reset_index()
    d=d.sort_values('Date').reset_index(drop=True)
    source=source.set_index('Date').reindex(d.Date).reset_index()
    base=d.Net_PnL.to_numpy(float); active=d.Leg.isin(['CW','NW']).to_numpy()
    entry=d.Entry_Time.fillna('').to_numpy(); leg=d.Leg.fillna('').to_numpy()
    policies=[policy('Flat size 1',base,np.ones(len(d)),active,entry,leg)]
    if kind=='wide':
        sizes=x['Daily_sizes'].set_index('Date').reindex(d.Date)
        for name in source.columns.drop('Date'):
            values=source[name].to_numpy(float); sz=sizes[name].to_numpy(float)
            np.testing.assert_allclose(values,base*sz,atol=1e-8)
            if name=='Flat size 1': continue
            policies.append(policy(name,values,sz,active&(sz>0),entry,leg))
    elif kind=='legacy':
        if 'Net_PnL' in source:
            np.testing.assert_allclose(source.Net_PnL,base,atol=1e-4,rtol=0)
        if config.get('size'):
            sz=source[config['size']].to_numpy(float)
            values=source[config['pnl']].to_numpy(float) if config.get('pnl') else source.Net_PnL.to_numpy(float)*sz
            policies.append(policy('Archived selected sizing',values,sz,active&(sz>0),entry,leg))
    elif kind=='tax':
        for col in source:
            if not col.endswith('_size'): continue
            name=col[:-5]; sz=source[col].fillna(1.).to_numpy(float)
            values=source[name+'_PnL'].fillna(0.).to_numpy(float)
            np.testing.assert_allclose(values,base*sz,atol=1e-8)
            policies.append(policy(name,values,sz,active&(sz>0),entry,leg))
    elif kind=='tick':
        for col in source:
            if not col.endswith('_Action'): continue
            name=col[:-7]
            if name=='Flat': continue
            act=source[col].isin(['keep','delay']).to_numpy()
            exits=[]
            for i in range(len(d)):
                if source[col].iloc[i]=='delay':
                    tag=str(source[name+'_Entry_Time'].iloc[i]).replace(':','')
                    exits.append(str(source.get('Exit_'+tag,pd.Series(index=source.index,dtype=str)).iloc[i]))
                elif act[i]: exits.append(str(d.Exit_Reason.iloc[i])+' '+str(d.Exit_Time.iloc[i]))
                else: exits.append('')
            policies.append(policy(name,source[name+'_PnL'],act.astype(float),act,
                source[name+'_Entry_Time'].fillna(''),leg,exits))
    elif kind=='relax':
        for variant in ('R1','R2','R3','R4'):
            counter=source[f'Relax_{variant}_PnL'].to_numpy(float); hit=np.isfinite(counter)
            for amount in (1.,.75):
                values=np.where(hit,counter*amount,base); sz=np.where(hit,amount,1.)
                tm=np.where(hit,source[f'Relax_{variant}_Time'].fillna(''),entry)
                exits=np.where(hit,source[f'Relax_{variant}_Exit'].fillna(''),d.Exit_Reason.fillna('')+' '+d.Exit_Time.fillna(''))
                policies.append(policy(f'{variant} relaxed trades x{amount:g}',values,sz,active|hit,tm,np.where(hit,'CW',leg),exits))
    for p in policies:
        if not np.isfinite(p['pnl']).all() or not np.isfinite(p['size']).all(): raise ValueError('Nonfinite daily policy')
        if (p['pnl'][~p['active']]!=0).any(): raise ValueError('PnL on a nonexecuted trade')
    return d,policies,x


def summaries(config,d,policies,grouping):
    dates=pd.to_datetime(d.Date); year=dates.dt.year
    labels={'Year':year.astype(str),'Quarter':dates.dt.to_period('Q').astype(str),
        'Month':dates.dt.to_period('M').astype(str),
        'FY':pd.Series(np.where(dates.dt.month.ge(4),year,year-1)).map(lambda y:f'FY{y}-{y+1}')}
    if grouping=='Overview':
        selections={'All_history':np.ones(len(d),bool)}
        if config['kind'] in ('tax','tick') or config.get('warmup_year')==2021:
            selections.update(Warmup_2021=year.eq(2021).to_numpy(),Scored_2022plus=year.ge(2022).to_numpy())
        elif 'Part' in d:
            selections.update({str(k):d.Part.eq(k).to_numpy() for k in d.Part.dropna().unique()})
    else:
        values=labels[grouping]; selections={str(k):np.asarray(values==k) for k in sorted(set(values))}
    out=[]
    for p in policies:
        for label,mask in selections.items():
            if not mask.any(): continue
            stat=stats(p['pnl'][mask],p['active'][mask],p['size'][mask],d.Net_PnL.to_numpy()[mask])
            stat.update(From=str(d.Date[mask].iloc[0]),To=str(d.Date[mask].iloc[-1]))
            out.append(dict(Iteration=config['id'],Policy=p['id'],Policy_name=p['name'],Period=label,**stat))
    return pd.DataFrame(out)


def risk_replay(book,raw):
    source=read(ROOT/'output/drawdown_risk_sizing/drawdown_risk_sizing.xlsx')
    model=source['Risk_model']; inputs=source['Model_inputs'].set_index('Date')
    risk=source['Daily_risk']; risk=risk[risk.Leg.isin(['CW','NW'])].reset_index(drop=True)
    names=model.Feature.tolist(); matrix=np.column_stack([np.ones(len(risk)),inputs.loc[risk.Date,names[1:]].to_numpy(float)])
    beta=model.Logistic_beta.to_numpy(float); gamma=model.Downside_beta.to_numpy(float)
    loss=1/(1+np.exp(-np.clip(matrix@beta,-25,25))); down=np.maximum(matrix@gamma,0)
    np.testing.assert_allclose(loss,risk.Loss_probability,atol=1e-10)
    np.testing.assert_allclose(down,risk.Expected_downside,atol=1e-10)
    f=read(ROOT/'output/entry_window_features.csv'); f=f[['Date','entry','u_range_bps','str_range_pct','spread_pct']]
    t=raw[raw.Leg.isin(['CW','NW'])].copy(); t['entry']=t.Entry_Time.str.replace(':','').astype(int)
    t=t.merge(f,on=['Date','entry'],how='left',validate='one_to_one').set_index('Date').loc[risk.Date].reset_index()
    cw=t.Leg.eq('CW'); parts=[]
    for c in ('u_range_bps','str_range_pct','spread_pct'):
        t[c]=t[c].where(cw); parts.append(t[c].rank(pct=True).fillna(.5).to_numpy())
    lr=pd.Series(loss).rank(pct=True).to_numpy(); dr=pd.Series(down).rank(pct=True).to_numpy()
    instability=np.mean(parts,axis=0); score=.4*lr+.4*dr+.2*instability
    np.testing.assert_allclose(score,risk.Risk_score,atol=1e-10)
    flag=(score>=np.quantile(score,.75))|(cw.to_numpy()&t[['u_range_bps','str_range_pct','spread_pct']].isna().any(axis=1).to_numpy())
    np.testing.assert_array_equal(flag,risk.Dicey.to_numpy(bool))
    table(book,'Risk_Inputs',pd.DataFrame({'Date':risk.Date,**{n:matrix[:,i] for i,n in enumerate(names)}}))
    table(book,'Risk_Coefs',pd.DataFrame([['Logistic',*beta],['Downside',*gamma]],columns=['Model',*names]))
    table(book,'Risk_Transforms',source['Transforms'])
    table(book,'Risk_Input_Dictionary',pd.DataFrame({'Feature':names,'Logistic_beta':beta,'Downside_beta':gamma,
        'Included':'Intercept or predictor listed here is used by both risk models; S1/S2 fullfit columns are excluded'}))
    result=risk.copy(); result['Actual_loss_label']=(result.Net_PnL<0).astype(int)
    result['Actual_negative_part']=np.maximum(-result.Net_PnL,0)
    for c in ('u_range_bps','str_range_pct','spread_pct'): result[c]=t[c]
    for i,name in enumerate(('U_rank','Straddle_rank','Spread_rank')): result[name]=parts[i]
    result['Replayed_loss']=loss; result['Replayed_downside']=down; result['Replayed_risk']=score
    result['Replay_error']=score-risk.Risk_score.to_numpy()
    table(book,'Risk_Trade_Replay',result)
    key_columns={'Date':risk.Date}
    for label,col in [('U','u_range_bps'),('Straddle','str_range_pct'),('Spread','spread_pct')]:
        key_columns[label+'_order_key']=t[col].rank(method='dense')
    for label,col in [('U','u_range_bps'),('Straddle','str_range_pct'),('Spread','spread_pct')]:
        key_columns[label+'_raw_17digits']=t[col].map(lambda v:format(v,'.17g') if pd.notna(v) else '')
    table(book,'Risk_Rank_Keys',pd.DataFrame(key_columns))
    # Explicit executable Excel formula replay; static saved/replayed values are in the preceding sheet.
    headers=['Date','Leg','Net_PnL','Logit_formula','Probability_formula','Downside_raw_formula','Downside_formula',
        'Loss_rank_formula','Downside_rank_formula','U_range','Str_range','Spread','U_rank_formula',
        'Str_rank_formula','Spread_rank_formula','Instability_formula','Risk_formula','Threshold_formula',
        'Dicey_formula','Saved_probability','Saved_downside','Saved_risk','Risk_error_formula']
    ws=book.create_sheet('Risk_Excel_Formulas'); ws.freeze_panes='D2'; ws.append(headers)
    end=len(risk)+1; last=get_column_letter(len(names)+1)
    for i,row in risk.iterrows():
        r=i+2
        vals=[row.Date,row.Leg,row.Net_PnL,
            f"=SUMPRODUCT(Risk_Inputs!B{r}:{last}{r},Risk_Coefs!$B$2:${last}$2)",
            f'=1/(1+EXP(-MAX(-25,MIN(25,D{r}))))',
            f"=SUMPRODUCT(Risk_Inputs!B{r}:{last}{r},Risk_Coefs!$B$3:${last}$3)",f'=MAX(0,F{r})',
            '='+excel_rank(f'E{r}',f'$E$2:$E${end}'),
            '='+excel_rank(f'G{r}',f'$G$2:$G${end}'),
            scalar(t.u_range_bps.iloc[i]),scalar(t.str_range_pct.iloc[i]),scalar(t.spread_pct.iloc[i])]
        for c in ('B','C','D'):
            value=f'Risk_Rank_Keys!{c}{r}'; reference=f'Risk_Rank_Keys!${c}$2:${c}${end}'
            vals.append(f'=IF(OR(B{r}="NW",COUNT({value})=0),0.5,{excel_rank(value,reference)})')
        vals.extend([f'=AVERAGE(M{r}:O{r})',f'=0.4*H{r}+0.4*I{r}+0.2*P{r}',
            f'=PERCENTILE($Q$2:$Q${end},0.75)',f'=OR(Q{r}>=R{r},AND(B{r}="CW",COUNT(J{r}:L{r})<3))',
            row.Loss_probability,row.Expected_downside,row.Risk_score,f'=Q{r}-V{r}'])
        ws.append(vals)
    # Fold parameters and sorted reference distributions support exact annual replay.
    coef_rows=[]; ref_rows=[]; folds=[]
    for p in sorted((ROOT/'output/higher_tax_sizing').glob('risk_model_*.json')):
        INPUTS.add(p); model=json.loads(p.read_text()); year=int(p.stem[-4:])
        folds.append({'Year':year,'Train_until':model['Train_until'],'Last_training_entry':model['Last_training_entry'],
            'First_scored_entry':model['First_scored_entry'],'Risk_threshold':model['Risk_threshold']})
        for j,n in enumerate(['Intercept']+model['Features']):
            coef_rows.append({'Year':year,'Feature':n,'Logistic_beta':model['Logistic_beta'][j],
                'Downside_beta':model['Downside_beta'][j],'Median':model['Median'][j-1] if j else np.nan,
                'Mean':model['Mean'][j-1] if j else np.nan,'Std':model['Std'][j-1] if j else np.nan})
        refs={'Loss':model['Loss_reference'],'Downside':model['Downside_reference'],**model['Instability_references']}
        for name,values in refs.items():
            ref_rows.extend({'Year':year,'Reference':name,'Order':j+1,'Value':v} for j,v in enumerate(values))
    table(book,'Annual_Risk_Coefs',pd.DataFrame(coef_rows)); table(book,'Annual_Risk_References',pd.DataFrame(ref_rows))
    table(book,'Annual_Risk_Folds',pd.DataFrame(folds))
    annual=read(ROOT/'output/higher_tax_sizing/trade_comparison.csv')
    table(book,'Annual_Risk_Trades',annual)
    return {'Rows':len(risk),'Dicey':int(flag.sum()),'Threshold':float(np.quantile(score,.75)),
        'Maximum_risk_replay_error':float(np.max(np.abs(score-risk.Risk_score))),
        'Maximum_loss_replay_error':float(np.max(np.abs(loss-risk.Loss_probability))),
        'Maximum_downside_replay_error':float(np.max(np.abs(down-risk.Expected_downside)))}


def source_tables(book,prefix,sheets):
    ws=book.create_sheet(prefix+'_Source_tables')
    for name,frame in sheets.items():
        if name.startswith(('Daily','daily','Equity','Drawdown','Model_inputs')) or name in ('Trades','Trade_comparison','trade_comparison'): continue
        ws.append(['SOURCE SHEET',name]); ws.append(list(map(str,frame.columns)))
        for row in frame.itertuples(index=False,name=None): ws.append([scalar(v) for v in row])
        ws.append([])


def annual_risk_replay(book,raw):
    from sizing_full_history import load
    from higher_tax_sizing import raw_design
    from sizing_vrp import DAILY
    INPUTS.add(Path(DAILY))
    d,_=load(); traded=d.Leg.isin(['CW','NW']).to_numpy()
    td=d[traded].reset_index(drop=True); predictors=raw_design(d)[traded].reset_index(drop=True)
    archived=read(ROOT/'output/higher_tax_sizing/trade_comparison.csv').set_index('Date')
    rows=[]; transformed=[]
    def rank(ref,values):
        ref=np.asarray(ref,float); left=np.searchsorted(ref,values,'left'); right=np.searchsorted(ref,values,'right')
        return np.where(right>left,(left+right+1)/2,left)/len(ref)
    for p in sorted((ROOT/'output/higher_tax_sizing').glob('risk_model_*.json')):
        INPUTS.add(p); model=json.loads(p.read_text()); year=int(p.stem[-4:])
        mask=pd.to_datetime(td.Date).dt.year.eq(year)
        obs=td[mask].reset_index(drop=True); a=predictors.loc[mask,model['Features']].to_numpy(float)
        x=np.clip((np.where(np.isfinite(a),a,np.asarray(model['Median']))-np.asarray(model['Mean']))/np.asarray(model['Std']),-3,3)
        beta=np.asarray(model['Logistic_beta']); gamma=np.asarray(model['Downside_beta'])
        loss=1/(1+np.exp(-np.clip(beta[0]+x@beta[1:],-25,25))); down=np.maximum(gamma[0]+x@gamma[1:],0)
        lr=rank(model['Loss_reference'],loss); dr=rank(model['Downside_reference'],down)
        components=[]; missing=np.zeros(len(obs),bool); cw=obs.Leg.eq('CW').to_numpy()
        for feature in ('u_range_bps','str_range_pct','spread_pct'):
            values=obs[feature].to_numpy(float); known=cw&np.isfinite(values)
            components.append(np.where(known,rank(model['Instability_references'][feature],np.nan_to_num(values)),.5))
            missing|=cw&~np.isfinite(values)
        instability=np.mean(components,axis=0); score=.4*lr+.4*dr+.2*instability
        dicey=(score>=model['Risk_threshold'])|missing
        saved=archived.loc[obs.Date]
        np.testing.assert_allclose(loss,saved.Loss_probability,atol=1e-10)
        np.testing.assert_allclose(down,saved.Expected_downside,atol=1e-10)
        np.testing.assert_allclose(score,saved.Risk_score,atol=1e-10)
        np.testing.assert_array_equal(dicey,saved.Dicey.to_numpy(bool))
        frame=pd.DataFrame({'Date':obs.Date,'Year':year,'Leg':obs.Leg,'Training_cutoff':model['Train_until'],
            'Loss_probability':loss,'Expected_downside':down,'Loss_rank':lr,'Downside_rank':dr,
            'U_rank':components[0],'Straddle_rank':components[1],'Spread_rank':components[2],
            'Instability':instability,'Risk_score':score,'Risk_threshold':model['Risk_threshold'],
            'Missing_CW':missing,'Dicey':dicey,'Saved_risk':saved.Risk_score.to_numpy(),
            'Replay_error':score-saved.Risk_score.to_numpy()})
        rows.append(frame)
        transformed.append(pd.DataFrame({'Date':obs.Date,'Year':year,**{n:x[:,i] for i,n in enumerate(model['Features'])}}))
    result=pd.concat(rows,ignore_index=True)
    table(book,'Annual_Risk_Replay',result); table(book,'Annual_Raw_Inputs',pd.concat([td[['Date']],predictors],axis=1))
    table(book,'Annual_Transformed_Inputs',pd.concat(transformed,ignore_index=True))
    return {'Scored_rows':len(result),'Max_risk_replay_error':float(result.Replay_error.abs().max()),'Dicey':int(result.Dicey.sum())}


def main():
    OUT.mkdir(parents=True,exist_ok=True); INPUTS.update([REGISTRY,Path(__file__),ROOT/'RISK_FORMULAS.md'])
    configs=json.loads(REGISTRY.read_text())
    if len({c['id'] for c in configs})!=len(configs): raise ValueError('Duplicate iteration IDs')
    files=sorted((ROOT/'output/backtest_relax/run_027bf0249b/days').glob('*.pkl')); INPUTS.update(files)
    raw=pd.DataFrame([pickle.load(p.open('rb')) for p in files]).sort_values('Date').reset_index(drop=True)
    raw['Net_PnL']=raw.Net_PnL.fillna(0.); raw['Leg']=raw.Leg.fillna('')
    book=Workbook(write_only=True); book.calculation=CalcProperties(calcId=0,fullCalcOnLoad=True)
    index=book.create_sheet('START_HERE'); overview=book.create_sheet('All_Iterations')
    for column,width in [('A',14),('B',42),('C',12),('D',16),('E',16),('F',15),('G',15),('H',22),('I',22),('J',22),('K',70),('L',80),('M',65)]:
        index.column_dimensions[column].width=width
    formulas=pd.DataFrame({'Line':(ROOT/'RISK_FORMULAS.md').read_text().splitlines()})
    table(book,'Formula_Guide',formulas)
    riskcheck=risk_replay(book,raw)
    annualcheck=annual_risk_replay(book,raw)
    all_summary=[]; registry=[]; audit=[]; algorithm_map=[]
    charts=book.create_sheet('Equity_DD_Charts')
    for c in configs:
        print('Building',c['id'],c['name'],flush=True)
        d,policies,source=load_iteration(c,raw); prefix=c['id']
        # Keep archived split labels where available without moving their boundaries.
        daily_source=source.get(c.get('daily',''),pd.DataFrame())
        split='Part' if 'Part' in daily_source else 'Period' if 'Period' in daily_source else None
        if split:
            mapping=daily_source.set_index('Date')[split]; d['Part']=d.Date.map(mapping)
        input_sheet=next((n for n in ('Daily_risk','Trade_comparison','Trades','Daily','Daily_opportunity','daily_comparison') if n in source),None)
        original_inputs=source[input_sheet] if input_sheet else source['Daily_PnL']
        table(book,prefix+'_Inputs',original_inputs)
        risk_columns=[col for col in ('Loss_probability','Expected_downside','Loss_rank','Downside_rank','Instability',
            'Risk_score','Risk_threshold','Dicey','Flag_reason','Strong_estimated_edge','Boost_eligible',
            'Strong_plus2','Strong_plus4','Warmup','Training_cutoff','Premium_percentile60','Residual_z','Spread_pct',
            'U_range_bps60','Str_change_pct60','Valid','Feature_error') if col in original_inputs]
        if risk_columns:
            extra=original_inputs.set_index('Date')[risk_columns].reindex(d.Date)
            for col in risk_columns: d[col]=extra[col].to_numpy()
        for j,p in enumerate(policies): p['id']=f'{prefix}_P{j:02d}'
        algorithm_map.extend({'Iteration':prefix,'Policy':p['id'],'Policy_name':p['name'],
            'Protocol':c['protocol'],'Coverage':c['coverage']} for p in policies)
        catalog=pd.DataFrame([{'Policy':p['id'],'Name':p['name'],'Protocol':c['protocol'],'Precision_coverage':c['coverage']} for p in policies])
        table(book,prefix+'_Policies',catalog)
        metadata=[v for v in ('Date','Leg','Stage','Entry_Time','Exit_Time','Exit_Reason','Strike','CW_Expiry','NW_Expiry','Event','Gross_PnL','Entry_Cost','Hedge_Cost','Net_PnL','Part') if v in d]
        trade_columns={(col if col in ('Date','Part') else 'Base_'+col):d[col].to_numpy() for col in metadata}
        for col in risk_columns: trade_columns[col]=d[col].to_numpy()
        daily_columns={col:d[col].to_numpy() for col in ('Date','Leg','Net_PnL')}
        union=d.Leg.isin(['CW','NW']).to_numpy().copy()
        group_rows=[]
        for p in policies:
            k=p['id']; union|=p['active']; base=d.Net_PnL.to_numpy(float)
            action=np.where(p['active'],np.where(p['size']<1-1e-9,'cut',np.where(p['size']>1+1e-9,'boost','keep')),'skip')
            action=np.where(p['active']&(p['entry']!=d.Entry_Time.fillna('').to_numpy()),'changed_entry',action)
            trade_columns[k+'_Size']=p['size']; trade_columns[k+'_PnL']=p['pnl']; trade_columns[k+'_Delta']=p['pnl']-base
            trade_columns[k+'_Action']=action; trade_columns[k+'_Entry']=p['entry']; trade_columns[k+'_Leg']=p['leg']
            trade_columns[k+'_Exit']=p['exit'] if p['exit'] is not None else np.where(p['active'],d.Exit_Reason.fillna('')+' '+d.Exit_Time.fillna(''),'')
            eq=np.cumsum(p['pnl']); dd=np.maximum.accumulate(np.r_[0.,eq])[1:]-eq
            daily_columns[k+'_PnL']=p['pnl']; daily_columns[k+'_Equity']=eq; daily_columns[k+'_DD']=dd
            groups=[('Leg',p['leg']),('Original_stage',d.Stage.fillna('').to_numpy())]
            if 'Dicey' in d: groups.append(('Dicey',d.Dicey.fillna(False).astype(str).to_numpy()))
            for typ,values in groups:
                for value in sorted(set(values)):
                    mask=np.asarray(values==value)&union
                    if mask.any(): group_rows.append({'Policy':k,'Grouping':typ,'Group':value,
                        **stats(p['pnl'][mask],p['active'][mask],p['size'][mask],base[mask])})
        table(book,prefix+'_Trades',pd.DataFrame(trade_columns)[union]); table(book,prefix+'_Daily',pd.DataFrame(daily_columns))
        representative={'11':'Dicey<=0.85: Minimum DD','13':'Tax+2 boost1.1','14':'Delaycheap',
            '15':'WF10 maximum profit','16':'WF11 dicey0.85 maximum profit'}.get(prefix)
        selected=next((j for j,p in enumerate(policies) if p['name']==representative),None)
        if selected is not None:
            from openpyxl.chart import LineChart, Reference
            daily_ws=book[prefix+'_Daily']; rowbase={'11':2,'13':30,'14':58,'15':86,'16':114}[prefix]
            for metric,basecol,anchor in [('Equity',5,'A'),('DD',6,'Q')]:
                chart=LineChart(); chart.title=c['name']+' — '+metric; chart.width=25; chart.height=13
                chart.y_axis.title='Normalized PnL'; chart.x_axis.title='Trading day'; chart.style=13
                for col in (basecol,basecol+3*selected):
                    chart.add_data(Reference(daily_ws,min_col=col,max_col=col,min_row=1,max_row=len(d)+1),titles_from_data=True)
                charts.add_chart(chart,anchor+str(rowbase))
        for label,sheet in [('Overview','Summary'),('Quarter','Quarterly'),('Year','Yearly'),('Month','Monthly'),('FY','Financial_year')]:
            frame=summaries(c,d,policies,label); table(book,prefix+'_'+sheet,frame)
            if label=='Overview':
                frame['Protocol']=c['protocol']; frame['Precision_coverage']=c['coverage']; all_summary.append(frame)
        table(book,prefix+'_Groups',pd.DataFrame(group_rows)); source_tables(book,prefix,source)
        registry.append({'Iteration':prefix,'Name':c['name'],'Policies':len(policies),'Calendar_days':len(d),
            'Base_entries':int(d.Leg.isin(['CW','NW']).sum()),'From':d.Date.min(),'To':d.Date.max(),
            'Trades_sheet':prefix+'_Trades','Quarterly_sheet':prefix+'_Quarterly','Yearly_sheet':prefix+'_Yearly',
            'Protocol':c['protocol'],'Coverage':c['coverage'],'Source':c['source']})
        # Cross-check periodic net sums against the entire calendar series.
        for grouping in ('Quarter','Year','Month','FY'):
            check=summaries(c,d,policies,grouping)
            for p in policies:
                total=check.loc[check.Policy.eq(p['id']),'Net'].sum()
                np.testing.assert_allclose(total,p['pnl'].sum(),atol=1e-8)
        audit.append({'Iteration':prefix,'Policies':len(policies),'Days':len(d),'Trade_rows':int(union.sum()),'Periodic_reconciliation':'PASS'})
    algorithm_inputs,algorithm_checks=add_sheets(book,table,algorithm_map); INPUTS.update(algorithm_inputs)
    summary=pd.concat(all_summary,ignore_index=True)
    overview.append(summary.columns.tolist())
    for row in summary.itertuples(index=False,name=None): overview.append([scalar(v) for v in row])
    overview.freeze_panes='E2'; overview.auto_filter.ref=f'A1:{get_column_letter(len(summary.columns))}{len(summary)+1}'
    index.append(['Research ledger — saved iterations, not live recommendations'])
    index.append(['Quarter/year/month/FY DD resets at each period start; Daily DD uses the global running equity peak.'])
    index.append(['Risk: Risk_Excel_Formulas / Risk_Trade_Replay. Boost/cut rules: Algo_Catalog / Algo_Priority / Algo_Annual / Algo_Opt_Replay.'])
    index.append(['Older candidate daily paths were not all saved. Coverage below identifies selected-only and rounded artifacts.'])
    index.append(['Modern full-history optimization is IN SAMPLE. Later overlays remain conditional on legacy LUT/fill assumptions.'])
    index.append(['Calendar quarters; Indian financial year starts April. PnL is normalized backtest units, not account returns.'])
    index.append([]); index.append(list(registry[0]))
    for r in registry:
        values=[]
        for key,value in r.items():
            if key in ('Trades_sheet','Quarterly_sheet','Yearly_sheet'):
                cell=WriteOnlyCell(index,value=value); cell.hyperlink=f"#'{value}'!A1"; cell.font=Font(color='0563C1',underline='single'); values.append(cell)
            else: values.append(value)
        index.append(values)
    index.freeze_panes='C9'
    table(book,'Reporting_Audit',pd.DataFrame(audit))
    path=OUT/'GammaShort_Research_Ledger.xlsx'; print('Saving master workbook',flush=True); book.save(path)
    saved=load_workbook(path,read_only=True,data_only=False); small=Workbook(write_only=True)
    small.calculation=CalcProperties(calcId=0,fullCalcOnLoad=True)
    for name in ('Risk_Inputs','Risk_Coefs','Risk_Rank_Keys','Risk_Excel_Formulas','Risk_Trade_Replay','Formula_Guide'):
        ws=small.create_sheet(name); ws.freeze_panes='D2'
        for row in saved[name].iter_rows(values_only=True): ws.append(list(row))
    small.save(OUT/'Risk_Formulas.xlsx')
    algorithms=Workbook(write_only=True); algorithms.calculation=CalcProperties(calcId=0,fullCalcOnLoad=True)
    for name in saved.sheetnames:
        if name.startswith('Algo_'):
            ws=algorithms.create_sheet(name); ws.freeze_panes='D2'
            for row in saved[name].iter_rows(values_only=True): ws.append(list(row))
    algorithms.save(OUT/'Sizing_Algorithms_Explained.xlsx'); saved.close()
    summary.to_csv(OUT/'all_iterations_summary.csv',index=False)
    pd.DataFrame(registry).to_csv(OUT/'iteration_index.csv',index=False)
    checks={'Status':'PASS','Iterations':len(configs),'Daily_policy_paths':sum(r['Policies'] for r in registry),
        'Periodic_reconciliation':audit,'Risk_replay':riskcheck,'Annual_risk_replay':annualcheck,
        'Algorithms':algorithm_checks,'Excel_formulas':'Recalculate on open; static replay values also provided'}
    (OUT/'audit.json').write_text(json.dumps(checks,indent=2)+'\n')
    manifest={'Stage':'built','Input_sha256':{str(p.relative_to(ROOT)) if p.is_relative_to(ROOT) else str(p):hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(INPUTS)},
        'Output_sha256':{p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in OUT.iterdir() if p.is_file() and p.name not in ('manifest.json','Risk_Formulas_Calculated.xlsx','Sizing_Algorithms_Calculated.xlsx','verification.json')}}
    (OUT/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
    print(json.dumps({'Workbook':str(path),'Iterations':len(configs),'Policy_paths':checks['Daily_policy_paths'],'Risk_replay':riskcheck}),flush=True)


if __name__=='__main__': main()
