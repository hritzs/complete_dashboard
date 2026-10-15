"""Conviction gating, sizing priority and exposure-budget feasibility."""
import unittest

import numpy as np
import pandas as pd

from sizing_conviction_comparison import action, allocation_bounds, conviction_flags
from sizing_normalized_optimization import project


class ConvictionTests(unittest.TestCase):
    def test_risk_and_0916_override_strong_scores(self):
        td=pd.DataFrame({'Leg':['CW']*7+['NW']*2,
                         'Stage':['09:17']*5+['09:16','09:17','NW','NW'],
                         'Dicey':[False]*6+[True,False,False],
                         'Loss_probability':[.1,.1,.1,.9,.1,.1,.1,.1,.1]})
        X=pd.DataFrame({'S1_fullfit':np.arange(9.),'S2_fullfit':np.arange(9.)})
        strong,eligible,early,_,_=conviction_flags(td,X)
        np.testing.assert_array_equal(np.flatnonzero(strong),[4,5])
        np.testing.assert_array_equal(np.flatnonzero(eligible),[4])
        self.assertTrue(early[5])
        self.assertFalse(eligible[6])
        self.assertFalse(strong[7:].any())

    def test_gentle_budget_can_be_infeasible(self):
        td=pd.DataFrame({'Leg':['CW']*20,'Stage':['09:16']*4+['09:17']*16,
                         'Dicey':[False]*4+[True]*6+[False]*10})
        eligible=np.zeros(20,bool); eligible[10]=True
        early=td.Stage.eq('09:16').to_numpy()
        _,upper,feasible=allocation_bounds(td,eligible,early,'Gentle')
        self.assertFalse(feasible)
        self.assertLess(upper.sum(),20)
        lower,upper,feasible=allocation_bounds(td,eligible,early,'Modest priority')
        self.assertTrue(feasible)
        size,_=project(np.linspace(.5,1.3,20),lower,upper)
        self.assertAlmostEqual(size.mean(),1,places=12)
        self.assertGreaterEqual(size[10],1.10)
        self.assertLessEqual(size[10],1.15)
        self.assertTrue((size[td.Dicey.to_numpy()]<=.85).all())
        self.assertTrue((size[early]<=1).all())
        self.assertGreaterEqual(size[10],size[~eligible].max()-1e-12)

    def test_tiny_roundoff_is_not_a_boost(self):
        np.testing.assert_array_equal(action(np.array([.75,1.,1.1,1.+1e-12])),
                                      ['Cut','Unchanged','Boost','Unchanged'])


if __name__=='__main__':
    unittest.main()
