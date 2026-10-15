"""Check the exposure projection and hard optimizer constraints."""
import contextlib
import io
import unittest

import numpy as np

from sizing_normalized_optimization import metrics, optimize, project


class ProjectionTests(unittest.TestCase):
    def test_mean_and_caps_after_projection(self):
        rng = np.random.default_rng(5)
        raw = rng.normal(1., .8, 100)
        lower = np.full(100, .75)
        upper = np.r_[np.ones(35), np.full(65, 1.15)]
        size, offset = project(raw, lower, upper)
        self.assertAlmostEqual(size.mean(), 1., places=12)
        self.assertTrue((size >= lower).all())
        self.assertTrue((size <= upper).all())
        self.assertLessEqual(np.sum((size-raw)**2), np.sum((1-raw)**2)+1e-10)
        free = (size > lower+1e-10) & (size < upper-1e-10)
        np.testing.assert_allclose(size[free]-raw[free], offset, atol=1e-12)

    def test_no_eligible_boosts_forces_flat(self):
        size, _ = project(np.array([.2, .7, 1.3, 2.]), np.full(4, .75), np.ones(4))
        np.testing.assert_allclose(size, np.ones(4), atol=1e-12)

    def test_balanced_example_already_mean_one(self):
        # Five increases of 0.15 balance three decreases of 0.25.
        raw = np.array([1.15]*5+[.75]*3)
        size, _ = project(raw, np.full(8, .75), np.full(8, 1.15))
        np.testing.assert_allclose(size, raw, atol=1e-12)

    def test_invalid_and_infeasible_bounds(self):
        cases = [([1.], [1.1], [1.2]), ([1.], [.5], [.9]),
                 ([1., 1.], [1.1, .5], [1., 1.5]), ([np.nan], [.75], [1.15]),
                 ([], [], [])]
        for raw, lower, upper in cases:
            with self.subTest(raw=raw), self.assertRaises(ValueError):
                project(raw, lower, upper)


class OptimizerTests(unittest.TestCase):
    def test_hard_profit_drawdown_and_mean_constraints(self):
        pnl = np.array([3., -1., 2., -3., 4., -1., 3., -2., 5., -2.])
        X = np.column_stack([np.linspace(-1, 1, 10), np.array([1., -1.]*5)])
        lower = np.full(10, .75)
        upper = np.r_[np.ones(2), np.full(8, 1.15)]
        base_net, base_dd = metrics(pnl)
        with contextlib.redirect_stdout(io.StringIO()):
            results, _, _ = optimize(X, pnl, lower, upper, base_net, base_dd)
        for result in results.values():
            self.assertAlmostEqual(result['size'].mean(), 1., places=12)
            self.assertTrue((result['size'] >= lower).all())
            self.assertTrue((result['size'] <= upper).all())
            self.assertGreaterEqual(result['net'], base_net-1e-8)
        capped = results['Profit DD capped']
        self.assertLessEqual(capped['dd'], base_dd+1e-8)
        if results['Max profit']['dd'] <= base_dd+1e-9:
            self.assertGreaterEqual(capped['net'], results['Max profit']['net']-1e-6)


if __name__ == '__main__':
    unittest.main()
