import json
import unittest
import numpy as np
import pandas as pd
from walkforward_sizing import project,scalefit,base_design,risk_design,fold
from sizing_normalized_optimization import project as old_project
from wf_sizing_features import calculate
from audit_walkforward_sizing import replay


def synthetic():
    rng=np.random.default_rng(44);n=90
    dates=list(pd.date_range('2021-01-01',periods=65).strftime('%Y-%m-%d'))+list(pd.date_range('2022-01-01',periods=25).strftime('%Y-%m-%d'))
    d=pd.DataFrame({'Date':dates,'Year':[2021]*65+[2022]*25,'Leg':'CW','Stage':'09:17','Event':False,'Table':1,
        'DTE':1+np.arange(n)%5,'Net_PnL':np.where(np.arange(n)%4==0,-15.,20.),
        'Prior_flat_DD':0.,'Prior_flat_loss_streak':0.})
    for col in ['IV','RV10','RV20','VRP10','VRP20','SPREAD20','RV_TREND','IV_Ratio','Str_Ratio','Build_IV','Norm_OG',
                'Adj_IV_Chg','Fixed_IDV','u_range_bps','str_range_pct','str_chg_abs','spread_pct']:
        d[col]=rng.uniform(.1,1,n)
    return d


class WFTests(unittest.TestCase):
    def test_breakpoint_projection_matches_bisection(self):
        rng=np.random.default_rng(1)
        for _ in range(30):
            raw=rng.normal(1,.5,80);lo=np.full(80,.75);hi=np.full(80,1.15);hi[:10]=.85;hi[10:20]=1.
            actual,_=project(raw,lo,hi);expected,_=old_project(raw,lo,hi)
            np.testing.assert_allclose(actual,expected,atol=1e-10,rtol=0)
        with self.assertRaises(ValueError):project(np.ones(5),np.full(5,.75),np.full(5,.85))

    def test_scalers_do_not_use_future_values(self):
        a=np.array([[1.,np.nan],[2.,3.],[100.,200.]])
        _,first=scalefit(a,np.array([True,True,False]));a[-1]=1e12
        _,second=scalefit(a,np.array([True,True,False]));self.assertEqual(first,second)

    def test_frozen_models_ignore_scoring_year_outcomes_and_features(self):
        d=synthetic();_,_,_,_,before=fold(d,2022)
        d.loc[d.Year.eq(2022),'Net_PnL']=-1e8
        for col in ['IV','IV_Ratio','Str_Ratio','u_range_bps','spread_pct','str_range_pct']:
            d.loc[d.Year.eq(2022),col]=1e9
        _,_,_,_,after=fold(d,2022)
        self.assertEqual(json.dumps(before,sort_keys=True),json.dumps(after,sort_keys=True))

    def test_tick_features_ignore_entry_and_future_ticks(self):
        t=pd.Timestamp('1900-01-01 09:16:00');times=pd.date_range(t-pd.Timedelta(seconds=60),periods=60,freq='s').as_unit('ns')
        q=pd.DataFrame([{'Time_dt':stamp,'Type':typ,'Bid':100.,'Ask':100.2,'LTP':100.1,'IV':.2,'DTE':2.,'UnderlyingLTP':20000.}
            for typ in ['CE','PE'] for stamp in times])
        a=calculate(q,t,20000.,20000);future=q.tail(2).copy();future.Time_dt=t;future.LTP=1e5
        b=calculate(pd.concat([q,future]),t,20000.,20000);self.assertEqual(a,b)

    def test_saved_model_replay_without_refitting(self):
        d=synthetic();_,sizes,risk,features,model=fold(d,2022)
        pred,inputs,loss,down,score,dicey=replay(d,json.loads(json.dumps(model)))
        for key in sizes:
            np.testing.assert_allclose(pred[key],sizes[key],atol=1e-12,rtol=0)
            np.testing.assert_allclose(inputs[key],features[key].drop(columns='Date'),atol=1e-12,rtol=0)
        np.testing.assert_allclose(loss,risk.Loss_probability,atol=1e-12,rtol=0)
        np.testing.assert_allclose(down,risk.Expected_downside,atol=1e-12,rtol=0)
        np.testing.assert_allclose(score,risk.Risk_score,atol=1e-12,rtol=0)
        np.testing.assert_array_equal(dicey,risk.Dicey)


if __name__=='__main__':unittest.main()
