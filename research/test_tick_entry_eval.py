import unittest
import numpy as np
import pandas as pd
from tick_entry_eval import decisions, evaluate, prior_thresholds, metrics, placebo_checks


def data():
    dates=list(pd.date_range('2021-01-01',periods=65,freq='D').strftime('%Y-%m-%d'))+['2022-01-01','2022-01-02','2022-01-03']
    d=pd.DataFrame({'Date':dates,'Year':[2021]*65+[2022]*3,'Leg':'CW','Stage':'09:16','DTE_idx':1,
        'Valid':True,'Entry_Time':'09:16','Net_PnL':10.,'Premium_percentile60':.5,'Residual_z':0.,
        'Spread_pct':1.,'U_range_bps60':5.,'Str_change_pct60':0.,
        'Signal_0917':'LUT_ON','Signal_0918':'LUT_OFF','PnL_0917':20.,'PnL_0918':-50.})
    return d


class EvaluationTests(unittest.TestCase):
    def test_thresholds_ignore_current_year_values(self):
        d=data(); before,audit=prior_thresholds(d)
        d.loc[d.Year.eq(2022),['Spread_pct','U_range_bps60','Str_change_pct60']]=1e8
        after,_=prior_thresholds(d)
        pd.testing.assert_frame_equal(before,after)
        self.assertTrue((pd.to_datetime(audit.Fit_max_date)<pd.to_datetime(audit.Year.astype(str)+'-01-01')).all())

    def test_warmup_nw_missing_and_delay_gates(self):
        d=data(); d['Premium_percentile60']=.01
        d.loc[66,'Leg']='NW'; d.loc[67,'Signal_0917']='LUT_OFF'
        daily,s,_,_=evaluate(d)
        np.testing.assert_array_equal(daily.loc[:64,'P20_PnL'],10.)
        self.assertEqual(daily.loc[65,'Delaycheap_PnL'],20.)
        self.assertEqual(daily.loc[65,'Delaycheap_Action'],'delay')
        self.assertEqual(daily.loc[66,'P20_PnL'],10.)
        self.assertEqual(daily.loc[67,'Delaycheap_PnL'],0.)
        d.loc[65,'Premium_percentile60']=np.nan
        daily,_,_,_=evaluate(d)
        self.assertEqual(daily.loc[65,'P20_PnL'],10.)

    def test_combined_missing_required_keeps_original(self):
        d=data(); d.loc[65,'Premium_percentile60']=.01; d.loc[65,'Residual_z']=np.nan
        t,_=prior_thresholds(d); f=decisions(d,t)
        self.assertFalse(f['Combined'].iloc[65]); self.assertTrue(f['Delaycheap'].iloc[65])

    def test_pnl_does_not_determine_entry_flags(self):
        d=data(); t,_=prior_thresholds(d); before=decisions(d,t)
        d['Net_PnL']=np.arange(len(d))*1e7; d['PnL_0917']=-1e7
        after=decisions(d,t)
        for key in before: pd.testing.assert_series_equal(before[key],after[key])

    def test_drawdown_includes_initial_loss(self):
        s=metrics([-5,2,-4,20,-3]); self.assertEqual(s['Net'],10); self.assertEqual(s['MDD'],7)

    def test_placebo_preserves_fully_triggered_group(self):
        d=data(); d['Premium_percentile60']=.01
        daily,_,_,_=evaluate(d)
        got=placebo_checks(d,daily,repetitions=10)
        row=got[got.Policy.eq('P20')].iloc[0]
        self.assertEqual(row.Observed_net,row.Null_median_net)
        self.assertEqual(row.P_net_one_sided,1.)


if __name__=='__main__': unittest.main()
