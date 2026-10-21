"""Causal signal windows and exact prefix cash stop behavior."""
import unittest
from unittest.mock import patch
import numpy as np
import pandas as pd
import gs_intraday_replay as replay
from report_gs_intraday_stops import intraday_series,update_episodes


class IntradayTests(unittest.TestCase):
    def setUp(self):
        self.previous=replay.CURRENT

    def tearDown(self):
        replay.CURRENT=self.previous

    def test_future_underlying_cannot_change_current_regime(self):
        t=np.arange(9*3600+15*60,10*3600+1)
        u=22000+np.sin(np.arange(len(t))/80)
        decision=9*3600+45*60
        before=replay.regime_signal(t,u,decision)
        altered=u.copy();altered[t>=decision-1]+=np.arange((t>=decision-1).sum())*100
        self.assertEqual(before,replay.regime_signal(t,altered,decision))

    def test_missing_history_never_becomes_calm(self):
        t=np.arange(1000,1100);u=np.full(len(t),22000.)
        self.assertEqual(replay.regime_signal(t,u,1080)[0],'Unavailable')

    def test_tighter_stop_closes_actual_prefix_not_clipped_final_pnl(self):
        replay.CURRENT={'Date':'2024-01-01','Leg':'CW','Entry_Time':'09:16','Exit_Time':'09:20:00',
            'Exit_Reason':'TP','Net_PnL':10.}
        frame=pd.DataFrame({'Second':[33360,33420,33480],'U':[22000.,22000.,22000.],
            'Mark_net':[0.,-32.,10.],'Close_net':[-1.,-38.,10.],'Hedge_cost':[0.,2.,8.],
            'Fut_trade':[0.,.1,-.2],'Is_check':[True,True,True],'Trigger_gross':[0.,-31.,12.]})
        capture={'Path':frame,'Feature_seconds':np.array([33360,33420,33480]),'Feature_U':np.array([22000.]*3),'Budget':40.}
        rows,_=replay.policies(capture)
        stop=next(r for r in rows if r['Policy']=='Stop075')
        self.assertEqual(stop['Net_PnL'],-38.)
        self.assertEqual(stop['Hedge_cost'],2.)
        self.assertEqual(stop['Post_entry_hedge_count'],1)
        self.assertEqual(rows[0]['Net_PnL'],10.)

    def test_nw_is_preserved_by_cw_only_experiment(self):
        replay.CURRENT={'Date':'2024-01-01','Leg':'NW','Entry_Time':'09:21','Exit_Time':'09:22',
            'Exit_Reason':'SL','Net_PnL':-50.}
        frame=pd.DataFrame({'Second':[33660,33720],'U':[22000.,22000.],
            'Mark_net':[-1.,-50.],'Close_net':[-1.,-50.],'Hedge_cost':[0.,1.],
            'Fut_trade':[0.,.1],'Is_check':[True,True],'Trigger_gross':[0.,-49.]})
        rows,_=replay.policies({'Path':frame,'Feature_seconds':np.array([33660,33720]),'Feature_U':np.array([22000.,22000.]),'Budget':np.nan})
        self.assertTrue(all(r['Net_PnL']==-50. and not r['Changed_exit'] for r in rows))

    def test_global_intraday_peak_is_carried_across_days(self):
        result=intraday_series([2.,-1.],[-3.,-4.],[10.,1.],[13.,5.])
        # Day1 reaches10 then falls to-3 =>13 DD; day2 falls to prior cash2-4=-2.
        self.assertEqual(result[-1,-1],13.)
        self.assertEqual(result[-1,1],10.)
        self.assertEqual(result[-1,2],9.)

    def test_extrema_summary_matches_direct_ordered_paths(self):
        days=[np.array([0.,5.,-1.,2.]),np.array([0.,-2.,4.,1.]),np.array([0.,-8.,-3.])]
        net=[x[-1] for x in days];low=[x.min() for x in days];high=[x.max() for x in days]
        mdd=[(np.maximum.accumulate(x)-x).max() for x in days]
        result=intraday_series(net,low,high,mdd);cash=0.;full=[0.]
        for path in days:full.extend(cash+path);cash+=path[-1]
        full=np.asarray(full);self.assertEqual(result[-1,-1],(np.maximum.accumulate(full)-full).max())

    def test_intraday_episode_keeps_peak_and_recovers_next_session(self):
        state={'Policy':'Baseline'}
        update_episodes(state,'2024-01-01',[33000,33001,33002,86399],[10.,-3.,2.,2.])
        self.assertEqual(state['episode']['Depth'],13.)
        self.assertEqual(state['episode']['Peak_time'],'2024-01-01 09:10:00')
        update_episodes(state,'2024-01-02',[33000,86399],[9.,9.])
        self.assertEqual(state['episodes'][0]['Recovery_time'],'2024-01-02 09:10:00')

    def test_direction_flip_resets_confirmation_and_second_agreement_exits(self):
        replay.CURRENT={'Date':'2024-01-01','Leg':'CW','Entry_Time':'09:16','Exit_Time':'09:20:00',
            'Exit_Reason':'TP','Net_PnL':10.}
        t=np.arange(33360,33601,60)
        frame=pd.DataFrame({'Second':t,'U':22000.,'Mark_net':[0.,1.,2.,3.,10.],
            'Close_net':[-1.,0.,1.,2.,10.],'Hedge_cost':0.,'Fut_trade':0.,'Is_check':True,'Trigger_gross':0.})
        capture={'Path':frame,'Feature_seconds':t,'Feature_U':np.full(5,22000.),'Budget':40.}
        answers=[('Directional_expansion',d,.9,2.) for d in (1,-1,1,1,1)]
        with patch.object(replay,'regime_signal',side_effect=answers):rows,signals=replay.policies(capture)
        regime=next(r for r in rows if r['Policy']=='RegimeExit')
        self.assertEqual(signals.Confirmed.tolist(),[False,False,False,True,True])
        self.assertEqual(regime['Path_cut_index'],3)
        self.assertEqual(regime['Net_PnL'],2.)


if __name__=='__main__':unittest.main()
