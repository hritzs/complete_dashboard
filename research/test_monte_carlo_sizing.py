import unittest
import numpy as np
import pandas as pd
from monte_carlo_sizing import draw_indices,path_stats,assign,select


class MonteCarloTests(unittest.TestCase):
    def test_blocks_are_contiguous_and_reproducible(self):
        p=np.arange(31.)
        a=draw_indices(p,np.random.default_rng(42),20,17,5)
        b=draw_indices(p,np.random.default_rng(42),20,17,5)
        np.testing.assert_array_equal(a,b)
        self.assertTrue((a>=0).all() and (a<31).all())
        for start in (0,5,10):np.testing.assert_array_equal(np.diff(a[:,start:start+5],axis=1),np.ones((20,4)))

    def test_sizing_does_not_take_own_outcome_and_risk_overrides_boost(self):
        dicey=np.array([True,False,False]);strong=np.array([True,True,False])
        np.testing.assert_allclose(assign(dicey,strong,.8,1.15),[.8,1.15,1.])

    def test_constant_size_scales_profit_and_dd_even_with_initial_losses(self):
        p=np.array([[-10.,-5.,30.,-12.],[2.,-4.,1.,6.]])
        n,d,_=path_stats(p);n2,d2,_=path_stats(p*1.15)
        np.testing.assert_allclose(n2,n*1.15);np.testing.assert_allclose(d2,d*1.15)
        self.assertEqual(d[0],15.)

    def test_tail_budget_selection_uses_training_values(self):
        t=pd.DataFrame({'Candidate':['Flat size 1','large','adaptive'],'Mean_net':[100,120,108],'MDD_p95':[20,24,19]})
        params=pd.DataFrame({'Candidate':['Flat size 1','large','adaptive'],'Family':['Exposure benchmark','Exposure benchmark','Adaptive']})
        selected=select(t,params)
        self.assertEqual(selected['Maximum simulated mean profit'],'large')
        self.assertEqual(selected['Maximum mean profit at flat tail-DD budget'],'adaptive')


if __name__=='__main__':unittest.main()
