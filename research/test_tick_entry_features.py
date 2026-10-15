"""Causality, quote quality and rank tests for tick-entry research."""
import unittest
import numpy as np
import pandas as pd
from tick_entry_features import compute_features, asof_book, mid_percentile


def fixture():
    decision=pd.Timestamp('1900-01-01 09:16:00')
    times=pd.date_range(decision-pd.Timedelta(seconds=60),periods=60,freq='s').as_unit('ns')
    u=pd.DataFrame({'Time_dt':times,'UnderlyingLTP':20000+np.sin(np.arange(60)/9)})
    q=pd.DataFrame([{'Time_dt':t,'Strike':k,'Type':typ,'Bid':100+(k-20000)*(.5 if typ=='PE' else -.5)+np.sin(i/5),
        'Ask':100.2+(k-20000)*(.5 if typ=='PE' else -.5)+np.sin(i/5),'BidQty':10,'AskQty':10}
        for k in (19950,20000,20050) for typ in ('CE','PE') for i,t in enumerate(times)])
    return u,q,decision


class FeatureTests(unittest.TestCase):
    def test_future_ticks_and_future_atm_do_not_change_features(self):
        u,q,t=fixture(); base=compute_features(u,q,t)
        future_u=pd.DataFrame({'Time_dt':pd.DatetimeIndex([t,t+pd.Timedelta(seconds=1)]).as_unit('ns'),'UnderlyingLTP':[23000,24000]})
        future_q=q.tail(2).copy(); future_q['Time_dt']=t; future_q['Bid']=9999; future_q['Ask']=10000
        changed=compute_features(pd.concat([u,future_u]),pd.concat([q,future_q]),t)
        self.assertEqual(base.keys(),changed.keys())
        for key in base:
            if isinstance(base[key],float) and np.isnan(base[key]): self.assertTrue(np.isnan(changed[key]),key)
            else: self.assertEqual(base[key],changed[key],key)
        self.assertTrue(base['Valid']); self.assertEqual(base['Feature_strike'],20000)
        self.assertLess(pd.Timestamp(base['Max_feature_tick']),t)

    def test_stale_current_quote_fails_closed(self):
        u,q,t=fixture(); q=q[q.Time_dt<t-pd.Timedelta(seconds=4)]
        self.assertFalse(compute_features(u,q,t)['Valid'])

    def test_crossed_current_quote_fails_closed(self):
        u,q,t=fixture(); q.loc[q.Type.eq('CE'),'Ask']=q.loc[q.Type.eq('CE'),'Bid']-.1
        self.assertFalse(compute_features(u,q,t)['Valid'])

    def test_midrank_ties(self):
        self.assertEqual(mid_percentile([2,2,2],2),.5)
        self.assertEqual(mid_percentile([1,2,3],2),.5)
        self.assertEqual(mid_percentile([1,2,3],0),0)

    def test_asof_never_backfills(self):
        t=pd.Timestamp('1900-01-01 09:16:00')
        got=asof_book(pd.DataFrame({'Time_dt':[t],'Bid':[10.]}),pd.DatetimeIndex([t-pd.Timedelta(seconds=1),t]))
        self.assertTrue(pd.isna(got.Bid.iloc[0])); self.assertEqual(got.Bid.iloc[1],10)


if __name__=='__main__': unittest.main()
