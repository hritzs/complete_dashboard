"""Independently reconcile the completed matched replay and exported workbook."""
import hashlib
import json
import pickle
from pathlib import Path

import numpy as np
import pandas as pd

ROOT=Path(__file__).resolve().parent
OUT=ROOT/'output/logic_comparison_20261021'


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def metrics(frame):
    # Include the starting equity peak of zero, including all no-trade dates.
    equity=0.;peak=0.;maximum=0.
    for value in frame.Net_PnL:
        equity+=value;peak=max(peak,equity);maximum=max(maximum,peak-equity)
    return equity,maximum


def main():
    run=json.loads((OUT/'run_manifest.json').read_text())
    assert run['Status']=='complete' and run['Failed_dates']==[]
    for name,expected in run['Inputs'].items():assert digest(ROOT/name)==expected,name
    manifest=json.loads((OUT/'manifest.json').read_text())
    for name,expected in manifest['Inputs'].items():assert digest(ROOT/name)==expected,name
    for name,expected in manifest['Outputs'].items():assert digest(OUT/name)==expected,name
    pilot=json.loads((OUT/'pilot_manifest.json').read_text())
    assert pilot['Status']=='complete' and len(pilot['Dates'])==6 and not pilot['Failed_dates']
    for date in pilot['Dates']:
        row=pickle.load((OUT/'pilot_days'/(date+'.pkl')).open('rb'))
        assert row['Pilot_full_parity'] and not row['Error']
    records=[pickle.load((OUT/'days'/(date+'.pkl')).open('rb')) for date in run['Dates']]
    assert len(records)==1430 and [r['Date'] for r in records]==run['Dates']
    assert all(not r['Error'] for r in records)
    for row in records:
        if not row.get('Native_empty_expiry_fallback'):continue
        assert digest(ROOT/'replay_empty_expiry_native.py')==row['Fallback_code_SHA256']
        assert digest(OUT/'empty_expiry_raw_errors'/(row['Date']+'.pkl'))==row['Original_error_SHA256']
        assert row['Old'].get('Leg','')==row['New'].get('Leg','')==''
        assert row['Old'].get('Net_PnL',0)==row['New']['Net_PnL']==0
    changed=[]
    for row in records:
        date=pd.Timestamp(row['Date']);path=Path('/mnt/shared/TickDataIV')/date.strftime('%d%m%Y')/'NIFTY.parquet'
        if not path.exists():path=path.with_suffix('.feather')
        stat=path.stat()
        if (stat.st_size,str(stat.st_mtime_ns))!=(row['Tick_size'],row['Tick_mtime_ns']):changed.append(row['Date'])
    assert not changed,('Tick files changed since replay',changed)
    book=pd.read_excel(OUT/'latest_logic_comparison.xlsx',sheet_name=None)
    summary=book['Summary'];daily=book['Daily_comparison'];paths=book['Equity_DD']
    assert daily.Date.tolist()==run['Dates']==paths.Date.tolist()
    archive=ROOT/'output/backtest_relax/run_027bf0249b/days'
    actual_books={'Archived':pd.DataFrame([pickle.load((archive/(date+'.pkl')).open('rb')) for date in run['Dates']]),
        'Old':pd.DataFrame([r['Old'] for r in records]),'New':pd.DataFrame([r['New'] for r in records])}
    period_rows=0
    for key,actual in actual_books.items():
        actual['Net_PnL']=actual.Net_PnL.fillna(0.)
        actual['Leg']=actual.Leg.fillna('')
        exported=book[{'Archived':'Archived_daily','Old':'Old_daily','New':'New_daily'}[key]]
        assert actual.Date.tolist()==run['Dates']==exported.Date.tolist()
        np.testing.assert_allclose(actual.Net_PnL,exported.Net_PnL,atol=1e-9,rtol=0)
        np.testing.assert_allclose(actual.Net_PnL,daily[key+'_Net_PnL'],atol=1e-9,rtol=0)
        assert (actual.Leg.to_numpy()==exported.Leg.fillna('').to_numpy()).all()
        cw=actual.Leg.eq('CW');inactive=~actual.Leg.isin(['CW','NW'])
        assert actual.loc[inactive,'Net_PnL'].eq(0).all()
        np.testing.assert_allclose(actual.loc[cw,'Gross_PnL']-actual.loc[cw,'Entry_Cost']-actual.loc[cw,'Hedge_Cost'],actual.loc[cw,'Net_PnL'],atol=1e-9,rtol=0)
        dates=pd.to_datetime(actual.Date)
        selections={'All_common_dates':np.ones(len(actual),bool),'Research_scored_2022plus':dates.dt.year.ge(2022),
            'Nested_scored_2023plus':dates.dt.year.ge(2023),'MC_evaluation_2025plus':dates.dt.year.ge(2025)}
        for label,mask in selections.items():
            net,dd=metrics(actual[mask]);saved=summary[summary.Code.eq(key)&summary.Period.eq(label)].iloc[0]
            np.testing.assert_allclose([saved.Net,saved.MDD],[net,dd],atol=1e-8,rtol=0)
            assert saved.Entries==actual.loc[mask,'Leg'].isin(['CW','NW']).sum()
        eq=actual.Net_PnL.cumsum().to_numpy();draw=np.maximum.accumulate(np.r_[0.,eq])[1:]-eq
        np.testing.assert_allclose(paths[key+'_Equity'],eq,atol=1e-8,rtol=0)
        np.testing.assert_allclose(paths[key+'_DD'],draw,atol=1e-8,rtol=0)
        episodes=book['DD_episodes'];episodes=episodes[episodes.Code.eq(key)]
        np.testing.assert_allclose(episodes.Maximum_DD.max(),draw.max(),atol=1e-8,rtol=0)
        periods={'By_year':dates.dt.year.astype(str),'By_quarter':dates.dt.to_period('Q').astype(str),
            'By_month':dates.dt.to_period('M').astype(str),'By_financial_year':pd.Series(np.where(dates.dt.month>=4,dates.dt.year,dates.dt.year-1)).map(lambda y:f'FY{y}-{y+1}')}
        for sheet,labels in periods.items():
            table=book[sheet];part=table[table.Code.eq(key)]
            np.testing.assert_allclose(part.Net.sum(),actual.Net_PnL.sum(),atol=1e-8,rtol=0)
            for _,saved in part.iterrows():
                frame=actual[labels.eq(str(saved.Period))];assert len(frame)>0
                np.testing.assert_allclose([saved.Net,saved.MDD],metrics(frame),atol=1e-8,rtol=0);period_rows+=1
    delta=actual_books['New'].Net_PnL-actual_books['Old'].Net_PnL
    np.testing.assert_allclose(daily.New_minus_old,delta,atol=1e-9,rtol=0)
    cohort=book['Entry_cohorts']
    for key,label in [('Old','Old rules / matched current inputs'),('New','Latest rules / matched current inputs')]:
        part=cohort[cohort.Book.eq(label)]
        assert part.Dates.sum()==1430
        np.testing.assert_allclose(part.Net.sum(),actual_books[key].Net_PnL.sum(),atol=1e-8,rtol=0)
    assert book['Leg_transitions'].iloc[:,1:].to_numpy().sum()==1430
    result={'Status':'PASS','Dates':1430,'Engine_paths':3,'Pilot_full_and_loader_parity_days':6,
        'Period_rows_reconciled':period_rows,'Errors':0,'Tick_fingerprints_unchanged':True,
        'Verifier_SHA256':digest(Path(__file__)),'Report_manifest_SHA256':digest(OUT/'manifest.json')}
    (OUT/'verification.json').write_text(json.dumps(result,indent=2)+'\n');print(json.dumps(result,indent=2))


if __name__=='__main__':main()
