import unittest
import numpy as np
from nested_sizing import metrics,placement,unique_top,choose


class NestedTests(unittest.TestCase):
    def test_metrics_include_initial_zero_peak(self):
        p=np.array([-10.,-5.,30.,-12.]);s=np.column_stack([np.ones(4),np.full(4,1.15)])
        m=metrics(p,s);self.assertEqual(m.MDD.iloc[0],15.)
        np.testing.assert_allclose(m.Net.iloc[1],m.Net.iloc[0]*1.15)
        np.testing.assert_allclose(m.Allocation_excess,0,atol=1e-10)

    def test_identical_sizes_have_no_placement_excess(self):
        p=np.array([3.,-2.,6.,-1.]);idx=np.array([[0,1,2,3],[3,2,1,0]])
        a,ad,b,bd=placement(p,np.ones(4),idx,np.random.default_rng(3))
        np.testing.assert_array_equal(a,b);np.testing.assert_array_equal(ad,bd)

    def test_deduplication_keeps_distinct_training_paths(self):
        sizes=np.array([[1.,1.,.8],[1.,1.,1.1]])
        self.assertEqual(unique_top([0,1,2],sizes,3),[0,2])

    def test_selection_has_no_outer_outcome_argument_and_falls_back(self):
        p=np.array([10.,-5.,10.,-5.]);s=np.column_stack([np.ones(4),np.full(4,.85)])
        a=metrics(p,s);a['Candidate']=a.index
        candidate,_,status=choose(a,a.copy(),s,'Strict profit')
        self.assertEqual(candidate,0);self.assertIn('fallback',status)


if __name__=='__main__':unittest.main()
