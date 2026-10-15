"""Past-only higher-tax sizing overlay, including 09:16 conviction boosts.

Conditional on legacy base entries, not an end-to-end causal strategy test.
See HIGH_TAX_CONVICTION_SPEC.md. Shared cache and LUTs are read-only.
"""
import ast
from collections import defaultdict
import hashlib
import json
import pickle
from pathlib import Path

import numpy as np
import pandas as pd

from lut_tax_closure import solve
from sizing_full_history import B, load
from sizing_drawdown_risk import risk_fit, stress_history
from sizing_v5_capped import dd_stats
from sizing_vrp import DAILY

HERE=Path(__file__).resolve().parent
OUT=HERE/'output/higher_tax_sizing'
CACHE=Path('/mnt/shared/GammaShortData/GammaShort/round48_noroll/sim_cache.pkl')
BASE_TAX=(13,5,2,2,6)
DELTAS=(2,4)
BOOSTS=(1.05,1.10)


def reference_rank(reference, values):
    ref=np.sort(np.asarray(reference,float)); values=np.asarray(values,float)
    left=np.searchsorted(ref,values,side='left'); right=np.searchsorted(ref,values,side='right')
    return np.where(right>left,(left+right+1)/2,right)/len(ref)


def build_rows(blob):
    rows=[]
    for date,d in sorted(blob['days'].items()):
        idv=.2*d['prev_pure']+.5*d['prev_with_prev']+.3*d['yesterday_iv']
        if not np.isfinite(idv) or idv<=0 or d['yesterday_iv']<=0:
            continue
        gap=d['og_pct']*.19/d['yesterday_iv']
        seen=set()
        for m in d['minutes']:
            idx=m['entry_idx']; gross=d['pnl'].get(idx,{}).get(1)
            hn=d.get('hedge_notional',{}).get(idx,{}).get(1)
            if gross is None:
                continue
            if hn is None or not np.isfinite(hn):
                raise ValueError('No hedge turnover for cache cost accounting')
            th=m['time_hhmm']; di=m['dte_idx']; sr=m['str_rat']; iv=m['adjusted_build_iv']
            if th<916 or th>1330 or (di==0 and th>959) or (d['is_event'] and th<918):
                continue
            if sr>B.VETO_STR.get(di,np.inf):
                continue
            stage={916:0,917:1,918:2,919:3}.get(th,4)
            coord=(di,int(np.searchsorted(B.IV_THRESHOLDS,iv/idv,side='right')),
                   int(np.searchsorted(B.STR_THRESHOLDS,sr,side='right')),
                   int(np.searchsorted(B.BLD_THRESHOLDS,iv,side='right')),
                   int(np.searchsorted(B.OG_THRESHOLDS,gap,side='right')),
                   int(np.searchsorted([B.ADJ_LOWER,B.ADJ_HIGHER],d['adj_iv_chg'],side='right')))
            if (stage,coord) in seen:
                continue
            seen.add((stage,coord))
            cost=B.ENTRY_SLIP.get(di,B.ENTRY_SLIP_DEF)+B.HEDGE_SLIP_BPS.get(di,B.HEDGE_SLIP_DEF)/1e4*hn
            rows.append((str(date),stage,coord,float(gross-cost),.333333 if d['is_event'] else 1.))
    return rows


def fit_masks(rows, cutoff, delta):
    profiles=[defaultdict(lambda:[0.,0.]) for _ in range(5)]
    dates=[]
    for date,stage,coord,pnl,count in rows:
        if date<=cutoff:
            profiles[stage][coord][0]+=pnl; profiles[stage][coord][1]+=count
            dates.append(date)
    raw=[solve(dict(profiles[i]),BASE_TAX[i]+delta,tuple(B.DIMS)) for i in range(5)]
    raw[1]&=raw[2]; raw[0]&=raw[1]; raw[3]&=raw[2]; raw[4]&=raw[3]
    return raw,max(dates),len(dates)


def raw_design(D):
    cw=D.Leg.eq('CW')
    columns={c:D[c].to_numpy(float) for c in ('IV','RV10','RV20','VRP10','VRP20','SPREAD20','RV_TREND')}
    for c in ('IV_Ratio','Str_Ratio','Norm_OG','Adj_IV_Chg','Fixed_IDV','u_range_bps','str_range_pct','str_chg_abs','spread_pct'):
        columns[c]=D[c].where(cw).to_numpy(float)
    for stage in B.STAGE_NAMES:
        columns['Stage_'+stage]=(cw & D.Stage.eq(stage)).to_numpy(float)
    for di in range(7):
        columns[f'CW_DTE_idx_{di}']=(cw & D.DTE_idx.eq(di)).to_numpy(float)
    for wd in range(5):
        columns[f'Weekday_{wd}']=pd.to_datetime(D.Date).dt.weekday.eq(wd).to_numpy(float)
    columns['Event']=D.Event.fillna(False).to_numpy(float)
    columns['NW']=D.Leg.eq('NW').to_numpy(float)
    dd,streak=stress_history(D.Net_PnL.to_numpy(float))
    columns['Prior_flat_DD']=dd; columns['Prior_flat_loss_streak']=streak
    return pd.DataFrame(columns)


def fit_risk(raw, td, train):
    A=raw.to_numpy(float)
    med=np.array([np.median(A[train,i][np.isfinite(A[train,i])]) if np.isfinite(A[train,i]).any() else 0. for i in range(A.shape[1])])
    filled=np.where(np.isfinite(A),A,med)
    mean=filled[train].mean(0); std=filled[train].std(0); std[std<1e-12]=1.
    X=np.clip((filled-mean)/std,-3,3)
    T=td[train].reset_index(drop=True)
    risk,coef=risk_fit(X[train],T.Net_PnL.to_numpy(float),list(raw),T)
    beta=coef.Logistic_beta.to_numpy(); downside_beta=coef.Downside_beta.to_numpy()
    loss=1/(1+np.exp(-np.clip(beta[0]+X@beta[1:],-25,25)))
    downside=np.maximum(0,downside_beta[0]+X@downside_beta[1:])
    instability=[]; refs={}; missing=np.zeros(len(td),bool); cw=td.Leg.eq('CW').to_numpy()
    for c in ('u_range_bps','str_range_pct','spread_pct'):
        values=td[c].to_numpy(float); known=cw & np.isfinite(values)
        ref=values[train & known]
        refs[c]=np.sort(ref).tolist()
        if not len(ref):
            raise ValueError('No prior-year instability observations')
        instability.append(np.where(known,reference_rank(ref,np.nan_to_num(values)),.5))
        missing |= cw & ~np.isfinite(values)
    instability=np.mean(instability,axis=0)
    score=.4*reference_rank(loss[train],loss)+.4*reference_rank(downside[train],downside)+.2*instability
    threshold=float(risk.Risk_threshold.iloc[0])
    np.testing.assert_allclose(score[train],risk.Risk_score,atol=1e-10,rtol=0)
    model=dict(Features=list(raw),Median=med.tolist(),Mean=mean.tolist(),Std=std.tolist(),
        Logistic_beta=beta.tolist(),Downside_beta=downside_beta.tolist(),
        Loss_reference=np.sort(loss[train]).tolist(),Downside_reference=np.sort(downside[train]).tolist(),
        Instability_references=refs,Risk_threshold=threshold)
    return pd.DataFrame(dict(Loss_probability=loss,Expected_downside=downside,Risk_score=score,
                              Risk_threshold=threshold,Dicey=(score>=threshold)|missing)),model


def assign_sizes(strong,dicey,boost):
    # The risk cut wins even if a 09:16 cell survives a higher tax.
    return np.where(dicey,.85,np.where(strong,boost,1.))


def main():
    OUT.mkdir(parents=True,exist_ok=True)
    D,_=load(); traded=D.Leg.isin(['CW','NW']).to_numpy()
    td=D[traded].reset_index(drop=True); raw=raw_design(D)[traded].reset_index(drop=True)
    years=pd.to_datetime(td.Date).dt.year.to_numpy()
    base=B.load_cw_luts(B.LUT_DIR)
    stage_map={s:i for i,s in enumerate(B.STAGE_NAMES)}
    for row in td[td.Leg.eq('CW')].itertuples():
        if not base[stage_map[row.Stage]][ast.literal_eval(row.Cell)]:
            raise ValueError('An archived entry does not pass the original base tax LUT')
    blob=pickle.load(CACHE.open('rb')); rows=build_rows(blob)
    cache_meta=blob['meta']; cache_end=max(blob['days']); del blob
    details=[]; fold_rows=[]
    for year in sorted(set(years)):
        if year==years.min():
            continue
        train=years<year; apply=years==year; cutoff=f'{year-1}-12-31'
        if train.sum()<50:
            raise ValueError('Insufficient past-only risk training entries')
        risk,model=fit_risk(raw,td,train)
        model.update(Train_until=cutoff,Last_training_entry=str(td.loc[train,'Date'].max()),
                     First_scored_entry=str(td.loc[apply,'Date'].min()))
        assert model['Last_training_entry']<model['First_scored_entry']
        (OUT/f'risk_model_{year}.json').write_text(json.dumps(model,indent=2)+'\n')
        masks={}
        last_cache=None
        for delta in DELTAS:
            print(f'Fitting higher-tax LUTs: {year}, tax +{delta}, cutoff {cutoff}',flush=True)
            lut,last_cache,nrows=fit_masks(rows,cutoff,delta)
            if last_cache>=str(td.loc[apply,'Date'].min()):
                raise ValueError('Future cache observations entered training')
            masks[delta]=lut
            np.savez_compressed(OUT/f'higher_tax_luts_{year}_plus{delta}.npz',
                               **{f'stage{i}':a for i,a in enumerate(lut)})
            fold_rows.append(dict(Year=year,Delta=delta,Base_taxes=str(BASE_TAX),Higher_taxes=str(tuple(t+delta for t in BASE_TAX)),
                                  Train_until=cutoff,Last_cache_day=last_cache,Last_risk_entry=model['Last_training_entry'],
                                  First_scored_entry=model['First_scored_entry'],Risk_training_entries=int(train.sum()),
                                  Counterfactual_rows=nrows,Cells_0916=int(lut[0].sum())))
        for i in range(5):
            if (masks[4][i] & ~masks[2][i]).any():
                raise ValueError('Higher-tax LUT is not nested within lower-tax LUT')
        for idx in np.flatnonzero(apply):
            r=td.iloc[idx]; k=stage_map.get(r.Stage)
            coord=ast.literal_eval(r.Cell) if r.Leg=='CW' else None
            details.append(dict(Date=r.Date,Year=year,Leg=r.Leg,Stage=r.Stage,Training_cutoff=cutoff,
                Base_tax=BASE_TAX[k] if k is not None else np.nan,
                Strong_plus2=bool(k is not None and masks[2][k][coord]),
                Strong_plus4=bool(k is not None and masks[4][k][coord]),
                Loss_probability=risk.loc[idx,'Loss_probability'],Expected_downside=risk.loc[idx,'Expected_downside'],
                Risk_score=risk.loc[idx,'Risk_score'],Risk_threshold=risk.loc[idx,'Risk_threshold'],Dicey=bool(risk.loc[idx,'Dicey'])))
    flags=pd.DataFrame(details)
    trades=td[['Date','Leg','Stage','Entry_Time','Strike','Net_PnL']].merge(flags.drop(columns=['Leg','Stage']),on='Date',how='left',validate='one_to_one')
    # Base entry taxes apply in the warm-up year too; only the additional
    # learned conviction layer is absent until sufficient prior data exists.
    trades['Base_tax']=trades.Stage.map({s:BASE_TAX[i] for i,s in enumerate(B.STAGE_NAMES)})
    trades['Higher_tax_plus2']=trades.Base_tax+2
    trades['Higher_tax_plus4']=trades.Base_tax+4
    trades['Warmup']=trades.Year.isna()
    trades['Dicey']=trades.Dicey.fillna(False).astype(bool)
    S={'Flat size 1':np.ones(len(D))}; p=D.Net_PnL.to_numpy(float)
    results=[]; by_stage=[]; yearly=[]
    for delta in DELTAS:
        strong=trades[f'Strong_plus{delta}'].fillna(False).to_numpy(bool)
        for boost in BOOSTS:
            name=f'Tax+{delta} boost{boost:g}'
            v=assign_sizes(strong,trades.Dicey.to_numpy(bool),boost)
            if (v[trades.Dicey.to_numpy(bool)]>.85+1e-12).any() or v.max()>1.15:
                raise AssertionError('Risk/boost limits broken')
            full=np.ones(len(D)); full[traded]=v; S[name]=full
            trades[name+'_size']=v; trades[name+'_PnL']=v*trades.Net_PnL
            for label,mask in [('All including warmup',np.ones(len(D),bool)),('Past-only overlay 2022+',pd.to_datetime(D.Date).dt.year.to_numpy()>years.min())]:
                entry_mask=mask & traded; mean=full[entry_mask].mean(); st=dd_stats((p*full)[mask])
                st.update(Candidate=name,Period=label,Mean_size=mean,Same_mean_flat_net=float(p[mask].sum()*mean),
                          Extra_vs_same_mean_flat=float(st['Net']-p[mask].sum()*mean),Boosts=int((full[entry_mask]>1).sum()),
                          Cuts=int((full[entry_mask]<1).sum()),Boosts_0916=int(((v>1)&td.Stage.eq('09:16').to_numpy()&mask[traded]).sum()))
                results.append(st)
            for stage in sorted(td.Stage.unique()):
                mask=td.Stage.eq(stage).to_numpy()
                by_stage.append(dict(Candidate=name,Stage=stage,Entries=int(mask.sum()),Strong=int((strong&mask).sum()),
                     Boosts=int(((v>1)&mask).sum()),Cuts=int(((v<1)&mask).sum()),Mean_size=v[mask].mean(),
                     Flat_PnL=td.Net_PnL.to_numpy()[mask].sum(),Sized_PnL=(td.Net_PnL.to_numpy()*v)[mask].sum()))
            for year in sorted(set(years)):
                mask=years==year; yearly.append(dict(Candidate=name,Year=year,Mean_size=v[mask].mean(),**dd_stats((td.Net_PnL.to_numpy()*v)[mask])))
    for label,mask in [('All including warmup',np.ones(len(D),bool)),('Past-only overlay 2022+',pd.to_datetime(D.Date).dt.year.to_numpy()>years.min())]:
        st=dd_stats(p[mask]); st.update(Candidate='Flat size 1',Period=label,Mean_size=1.,Same_mean_flat_net=st['Net'],Extra_vs_same_mean_flat=0.,Boosts=0,Cuts=0,Boosts_0916=0); results.append(st)
    R=pd.DataFrame(results); R.to_csv(OUT/'candidates.csv',index=False)
    trades.to_csv(OUT/'trade_comparison.csv',index=False)
    folds=pd.DataFrame(fold_rows); folds.to_csv(OUT/'training_cutoffs.csv',index=False)
    notes=[
        'Updated owner rule: 09:16 may receive a modest boost if its base-tax13 cell ALSO survives a separately fitted tax15 or17 LUT and is not dicey. Original base LUTs/taxes are unchanged.',
        'Annual sizing overlay fits use prior years ONLY. 2021 is warmup at size1. Risk scalers, classifier/downside models, empirical ranks/thresholds and extra-hurdle LUTs are saved per fold. No future whole-period normalization or coefficient optimization.',
        'Four fixed candidates: tax increments2/4 × boosts5%/10%; dicey size0.85, ordinary size1. CW risk cuts override high-tax conviction. NW receives no CW-tax boost.',
        'Actual mean is reported, not forced to1 using future dates. Same_mean_flat_net is an EX-POST exposure-matched diagnostic, not a live benchmark decision or normalization rule.',
        'Conditional overlay only: the LEGACY fixed entry LUTs used wider history, including later observations. Original feature/cache timing and execution are not certified causal. Do not call these end-to-end leakage-free strategy returns.',
        f'Counterfactual source: round48 no-roll ps1, cache ends {cache_end}. 2026 LUT/risk fitting stops2025-12-31. Counterfactual PnL includes matching modeled entry/hedge costs. The legacy published LUTs predate this no-roll refit; they are retained as entry gates, not re-solved.',
        'Monotone higher-tax LUTs use the original maximum-closure objective totalPnL-weightedCount*tax, cents rounding, minimum anchor evidence1 and stage cascade. A compressed observed-cell solver is checked against brute-force closures; unobserved cells expand by implication.',
        'Saved production PnL/costs scale linearly; fractional research allocations, no lot rounding, new fill replay or fixed-fee recalculation. Units normalized Rs. These are research artifacts; no live service edits.',
    ]
    with pd.ExcelWriter(OUT/'higher_tax_comparison.xlsx') as w:
        for sheet,data in {'Read_me':pd.DataFrame({'Method':notes}),'Candidates':R,'Trades':trades,'By_stage':pd.DataFrame(by_stage),
                           'By_year':pd.DataFrame(yearly),'Training_cutoffs':folds,'Cache_parameters':pd.DataFrame(cache_meta['param_sets'])}.items():
            data.to_excel(w,sheet_name=sheet,index=False)
    inputs=[Path(__file__).resolve(),HERE/'lut_tax_closure.py',HERE/'HIGH_TAX_CONVICTION_SPEC.md',CACHE,
            HERE/'output/entry_window_features.csv',HERE/'sizing_full_history.py',HERE/'sizing_drawdown_risk.py',
            HERE/'GammaShort_Backtest_Final.py',HERE/'sizing_vrp.py',HERE/'sizing_v5_capped.py',Path(DAILY),
            HERE/'output/backtest_final/run_027bf0249b/daily_log.csv']
    inputs += [Path(B.LUT_DIR)/name for name in B.STAGE_FILES]
    hashes={}
    for path in inputs:
        h=hashlib.sha256()
        with path.open('rb') as handle:
            for block in iter(lambda:handle.read(1024*1024),b''): h.update(block)
        hashes[str(path)]=h.hexdigest()
    h=hashlib.sha256()
    for path in sorted((HERE/'output/backtest_relax/run_027bf0249b/days').glob('*.pkl')):
        h.update(path.name.encode()); h.update(path.read_bytes())
    (OUT/'manifest.json').write_text(json.dumps(dict(Input_sha256=hashes,Daily_pickles_sha256=h.hexdigest(),Base_taxes=BASE_TAX,Deltas=DELTAS,Boosts=BOOSTS,
        Cache_last_day=cache_end,Entry_rows=len(td),Scope='Past-only overlay conditional on legacy base entry list'),indent=2)+'\n')
    table=R[R.Period.eq('All including warmup')]
    lines=['# Higher-tax conviction sizing including09:16','',*['- '+n for n in notes],'',
           '| Candidate | Net | MDD | Net/MDD | Mean size | Boosts09:16 |', '|---|---:|---:|---:|---:|---:|']
    for r in table.itertuples():
        lines.append(f'| {r.Candidate} | {r.Net:.2f} | {r.MDD:.2f} | {r.Net_per_MDD:.2f} | {r.Mean_size:.6f} | {r.Boosts_0916} |')
    (OUT/'SUMMARY.md').write_text('\n'.join(lines)+'\n')
    print(table[['Candidate','Net','MDD','Net_per_MDD','Mean_size','Boosts','Cuts','Boosts_0916']].to_string(index=False))
    print('Saved',OUT)


if __name__=='__main__':
    main()
