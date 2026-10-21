"""Execution-version books must retain their own trades, not rescale old PnL."""
import unittest
from unittest.mock import patch
import numpy as np
import pandas as pd
import build_research_ledger as ledger


class ExecutionBookTests(unittest.TestCase):
    def setUp(self):
        self.raw=pd.DataFrame({'Date':['2024-01-02','2024-01-03'],'Net_PnL':[10.,0.],
            'Leg':['CW',''],'Entry_Time':['09:16',''],'Exit_Reason':['EOD',''],'Exit_Time':['15:27','']})
        old=self.raw.copy()
        new=pd.DataFrame({'Date':self.raw.Date,'Net_PnL':[-7.,21.],'Leg':['NW','CW'],
            'Entry_Time':['09:21','09:18'],'Exit_Reason':['SL','TP'],'Exit_Time':['10:05','11:12']})
        self.book={'Daily_PnL':pd.DataFrame({'Date':self.raw.Date,'Flat size 1':self.raw.Net_PnL,
            'Old rules matched':old.Net_PnL,'Latest rules matched':new.Net_PnL}),'Old_daily':old,'New_daily':new}
        self.config={'id':'19','kind':'logic','source':'unused.xlsx'}

    def load(self):
        with patch.object(ledger,'read',return_value=self.book):return ledger.load_iteration(self.config,self.raw)

    def test_independent_pnl_and_actual_entry_exit_are_preserved(self):
        _,policies,_=self.load();latest=policies[2]
        np.testing.assert_array_equal(latest['pnl'],[-7.,21.])
        np.testing.assert_array_equal(latest['size'],[1.,1.])
        np.testing.assert_array_equal(latest['active'],[True,True])
        np.testing.assert_array_equal(latest['leg'],['NW','CW'])
        np.testing.assert_array_equal(latest['entry'],['09:21','09:18'])
        np.testing.assert_array_equal(latest['exit'],['SL 10:05','TP 11:12'])

    def test_pnl_on_nonexecuted_entry_is_rejected(self):
        self.book['New_daily'].loc[1,'Leg']=''
        with self.assertRaisesRegex(ValueError,'nonexecuted'):self.load()

    def test_parallel_book_uses_latest_gs_baseline_and_counts_both_positions(self):
        latest=self.book['Old_daily'].copy();long=self.book['New_daily'].copy()
        long['Policy']='Directional Breakout';long['Traded']=True
        self.book={'Latest_GS_daily':latest,'All_policy_days':long,
            'Daily_PnL':pd.DataFrame({'Date':self.raw.Date,'Latest GS':latest.Net_PnL,
                'Directional Breakout':long.Net_PnL,'GS + Directional Breakout':latest.Net_PnL+long.Net_PnL})}
        self.config['kind']='portfolio';self.raw['Net_PnL']=[1000.,1000.]
        d,policies,_=self.load()
        np.testing.assert_array_equal(d.Net_PnL,[10.,0.])
        np.testing.assert_array_equal(policies[2]['pnl'],[3.,21.])
        np.testing.assert_array_equal(policies[2]['positions'],[2,1])
        for i,p in enumerate(policies):p['id']=str(i)
        result=ledger.summaries(self.config,d,policies,'Overview')
        combined=result[result.Policy.eq('2')].iloc[0]
        self.assertEqual(combined.Entries,2);self.assertEqual(combined.Underlying_strategy_entries,3)


if __name__=='__main__':unittest.main()
