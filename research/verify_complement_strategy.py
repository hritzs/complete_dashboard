"""Reconcile source trades, risk budget, execution chronology and portfolio Excel."""
import hashlib,json,pickle
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path
import numpy as np
import pandas as pd

ROOT=Path(__file__).resolve().parent
OUT=ROOT/'output/complement_strategy'


def metric(p):
    equity=peak=draw=0.
    for value in p:equity+=value;peak=max(peak,equity);draw=max(draw,peak-equity)
    return [equity,draw]


def seconds(value):
    h,m,s=map(int,str(value).split(':'));return h*3600+m*60+s


def fingerprint(date):
    path=Path('/mnt/shared/TickDataIV')/pd.Timestamp(date).strftime('%d%m%Y')/'NIFTY.parquet'
    stat=path.stat();return stat.st_size,str(stat.st_mtime_ns)


def main():
    from annotate_complement_calendar import verify as verify_calendar
    verify_calendar()
    manifest=json.loads((OUT/'manifest.json').read_text())
    for name,digest in manifest['Inputs'].items():assert hashlib.sha256((ROOT/name).read_bytes()).hexdigest()==digest,name
    for name,digest in manifest['Outputs'].items():assert hashlib.sha256((OUT/name).read_bytes()).hexdigest()==digest,name
    run=json.loads((OUT/'run_manifest.json').read_text());assert run['Status']=='complete' and not run['Failed_dates']
    book=pd.read_excel(OUT/'complement_strategy.xlsx',sheet_name=None)
    trades=book['All_policy_days'];daily=book['Daily_PnL'];summary=book['Summary'];dates=run['Dates'];dt=pd.to_datetime(dates)
    assert len(dates)==1430 and len(trades)==8580 and daily.Date.tolist()==dates
    assert not trades.duplicated(['Date','Policy']).any()
    source=[]
    with ThreadPoolExecutor(max_workers=16) as pool:fingerprints=dict(zip(dates,pool.map(fingerprint,dates)))
    for date in dates:
        rec=pickle.load((OUT/'days'/(date+'.pkl')).open('rb'));assert not rec['Error'];source+=rec['Rows']
        assert fingerprints[date]==(rec['Tick_size'],rec['Tick_mtime_ns'])
        matched=pickle.load((ROOT/'output/logic_comparison_20261021/days'/(date+'.pkl')).open('rb'))
        assert fingerprints[date]==(matched['Tick_size'],matched['Tick_mtime_ns']),'GS/addon tick vintages differ: '+date
    source=pd.DataFrame(source).set_index(['Date','Policy']).sort_index()
    exported=trades.set_index(['Date','Policy']).sort_index()
    np.testing.assert_allclose(source.Net_PnL,exported.Net_PnL,atol=1e-9,rtol=0)
    np.testing.assert_array_equal(source.Traded,exported.Traded)
    gs=book['Latest_GS_daily'];assert gs.Date.tolist()==dates
    np.testing.assert_allclose(daily['Latest GS'],gs.Net_PnL,atol=1e-9,rtol=0)
    count=0
    for row in trades[trades.Traded].itertuples(index=False):
        norm=25000./row.Entry_U;scale=norm*row.Normalized_quantity
        np.testing.assert_allclose(row.Entry_debit_points*scale,100.,atol=1e-8,rtol=0)
        np.testing.assert_allclose((row.Exit_credit_points-row.Entry_debit_points)*scale,row.Gross_PnL,atol=1e-8,rtol=0)
        np.testing.assert_allclose((row.CE_contract_units+row.PE_contract_units)/norm,row.Fee,atol=1e-8,rtol=0)
        np.testing.assert_allclose([row.Gross_PnL-row.Fee,row.Gross_PnL-2*row.Fee],[row.Net_PnL,row.Double_fee_net],atol=1e-8,rtol=0)
        assert row.Gross_PnL>=-100.-1e-8 and row.CE_contract_units>=0 and row.PE_contract_units>=0
        assert seconds(row.Entry_Time)>=seconds(row.Decision_Time)+2
        assert seconds(row.Entry_quote_label)==seconds(row.Entry_Time)-1
        assert seconds(row.Exit_Time)>=seconds(row.Exit_trigger_time)+1
        assert seconds(row.Exit_Time)-seconds(row.Exit_trigger_time)==row.Exit_delay_seconds
        if row.Exit_Reason=='Time15m':assert seconds(row.Exit_trigger_time)==seconds(row.Entry_Time)+900
        else:assert row.Exit_Reason=='Premium_stop30' and seconds(row.Exit_trigger_time)<seconds(row.Entry_Time)+900
        if np.isfinite(row.Gamma_at_entry):assert row.Gamma_at_entry>0 and row.Vega_1pct_at_entry>0
        count+=1
    for policy in trades.Policy.unique():
        frame=trades[trades.Policy.eq(policy)].set_index('Date').loc[dates]
        np.testing.assert_allclose(frame.Net_PnL,daily[policy],atol=1e-9,rtol=0)
        np.testing.assert_allclose(daily['GS + '+policy],daily['Latest GS']+daily[policy],atol=1e-8,rtol=0)
    scopes={'All_dates':np.ones(1430,bool),'2022plus':dt.year>=2022,'2023plus':dt.year>=2023,'2025plus':dt.year>=2025}
    paths=book['Equity_DD'];assert paths.Date.tolist()==dates
    period_rows=0
    for name in daily.columns.drop('Date'):
        p=daily[name].to_numpy(float);eq=p.cumsum();dd=np.maximum.accumulate(np.r_[0.,eq])[1:]-eq
        np.testing.assert_allclose(paths[name+' Equity'],eq,atol=1e-8,rtol=0)
        np.testing.assert_allclose(paths[name+' DD'],dd,atol=1e-8,rtol=0)
        for scope,mask in scopes.items():
            row=summary[summary.Policy.eq(name)&summary.Scope.eq(scope)].iloc[0]
            np.testing.assert_allclose([row.Net,row.MDD],metric(p[mask]),atol=1e-8,rtol=0)
        labels={'By_year':dt.year.astype(str),'By_quarter':dt.to_period('Q').astype(str),'By_month':dt.to_period('M').astype(str),
            'By_FY':pd.Series(np.where(dt.month>=4,dt.year,dt.year-1)).map(lambda y:f'FY{y}-{y+1}').to_numpy()}
        for sheet,periods in labels.items():
            frame=book[sheet];frame=frame[frame.Policy.eq(name)]
            np.testing.assert_allclose(frame.Net.sum(),p.sum(),atol=1e-8,rtol=0)
            for row in frame.itertuples(index=False):
                mask=np.asarray(periods==str(row.Period))
                np.testing.assert_allclose([row.Net,row.MDD],metric(p[mask]),atol=1e-8,rtol=0);period_rows+=1
    result={'Status':'PASS','Dates':1430,'Long_policy_days':8580,'Long_entries_repriced':count,'Daily_paths':13,
        'Calendar_classification':'5 Diwali/Muhurat sessions; 5 missing underlying; 1 insufficient opening; all retained',
        'Period_rows_checked':period_rows,'Tick_fingerprints_unchanged':True,
        'Verifier_SHA256':hashlib.sha256(Path(__file__).read_bytes()).hexdigest()}
    (OUT/'verification.json').write_text(json.dumps(result,indent=2)+'\n');print(json.dumps(result,indent=2))


if __name__=='__main__':main()
