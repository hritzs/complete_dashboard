import unittest

import numpy as np
import pandas as pd

from higher_tax_sizing import assign_sizes, fit_masks, fit_risk, reference_rank


class PastOnlyTests(unittest.TestCase):
    def test_risk_overrides_0916_conviction(self):
        strong=np.array([True,True,False,False])
        dicey=np.array([False,True,True,False])
        np.testing.assert_allclose(assign_sizes(strong,dicey,1.1),[1.1,.85,.85,1.])

    def test_reference_ranks_ignore_evaluation_batch(self):
        reference=np.array([1.,2.,2.,4.])
        values=np.array([0.,1.,2.,3.,4.,10.])
        np.testing.assert_allclose(reference_rank(reference,values),[0,.25,.625,.75,1,1])
        self.assertEqual(reference_rank(reference,[2.])[0],reference_rank(reference,[100.,2.,-100.])[1])

    def test_future_labels_cannot_change_risk_fit(self):
        rng=np.random.default_rng(12); n=24
        raw=pd.DataFrame({'IV':rng.normal(size=n),'Range':rng.normal(size=n)})
        td=pd.DataFrame({'Date':[f'd{i:02}' for i in range(n)],'Leg':['CW']*n,
                         'u_range_bps':rng.uniform(1,10,n),'str_range_pct':rng.uniform(1,10,n),
                         'spread_pct':rng.uniform(.1,1,n),'Net_PnL':rng.normal(3,10,n)})
        train=np.arange(n)<16
        before,model=fit_risk(raw,td,train)
        td.loc[~train,'Net_PnL']=1e9
        after,model2=fit_risk(raw,td,train)
        self.assertEqual(model,model2)
        np.testing.assert_allclose(before.Risk_score,after.Risk_score)

    def test_future_counterfactual_rows_do_not_change_luts(self):
        c=(0,2,1,3,1,1)
        rows=[('2021-01-01',2,c,100.,2.)]
        base,date,_=fit_masks(rows,'2021-12-31',2)
        changed,_,_=fit_masks(rows+[('2022-01-01',0,c,1e8,1.)],'2021-12-31',2)
        for left,right in zip(base,changed):
            np.testing.assert_array_equal(left,right)
        self.assertEqual(date,'2021-01-01')


if __name__=='__main__':
    unittest.main()
