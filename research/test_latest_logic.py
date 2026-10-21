"""Regression examples for the actual snapshotted hedge kernels."""
import ast,math,unittest
from pathlib import Path
import numpy as np

SRC=Path(__file__).resolve().parent/'output/logic_comparison_20261021/sources'


def kernel(file):
    tree=ast.parse((SRC/file).read_text())
    fn=next(n for n in tree.body if isinstance(n,ast.FunctionDef) and n.name=='hedge_walk')
    fn.decorator_list=[];module=ast.Module(body=[fn],type_ignores=[]);ast.fix_missing_locations(module)
    env={'np':np,'math':math};exec(compile(module,str(SRC/file),'exec'),env)
    return env['hedge_walk']


class KernelTests(unittest.TestCase):
    def setUp(self):
        self.old=kernel('Archived_GammaShort_Backtest_Final.py');self.new=kernel('7DResume_0.75_1.2.py')

    def call(self,delta,minute,threshold):
        od=np.array([0.,delta]);og=np.full(2,-.001);pa=np.full(2,10.);flags=np.array([True,minute])
        old=self.old(od,og,pa,np.ones(2,dtype=np.int64),flags,.75,1.2,1.,threshold)
        new=self.new(od,og,pa,flags,.75,1.2,1.,threshold)
        return old,new

    def test_negative_delta_residual_changes_from_opposite_to_same_side(self):
        old,new=self.call(-.3,True,.25)
        self.assertAlmostEqual(-.3+old[0][-1],.0625)
        self.assertAlmostEqual(-.3+new[0][-1],-.0625)

    def test_positive_delta_residual_changes_from_opposite_to_same_side(self):
        old,new=self.call(.3,True,.25)
        self.assertAlmostEqual(.3+old[0][-1],-.0625)
        self.assertAlmostEqual(.3+new[0][-1],.0625)

    def test_hard_intra_minute_delta_hedge_still_goes_to_neutral(self):
        old,new=self.call(-.31,False,.25)
        self.assertAlmostEqual(-.31+old[0][-1],0.)
        np.testing.assert_array_equal(old,new)

    def test_points_branch_unchanged_for_one_held_strike(self):
        for delta in (-.3,.3):
            old,new=self.call(delta,True,0.)
            np.testing.assert_array_equal(old,new)


if __name__=='__main__':unittest.main()
