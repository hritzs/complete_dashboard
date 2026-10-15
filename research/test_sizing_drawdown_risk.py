"""Entry-risk flags, stress timing, and mandatory cut constraints."""
import contextlib
import io
import unittest

import numpy as np
import pandas as pd

from sizing_drawdown_risk import measure, qualifies, risk_fit, search, stress_history


class RiskTests(unittest.TestCase):
    def test_stress_uses_only_previous_results(self):
        original = np.array([10., -5., 0., -3., 4.])
        dd, streak = stress_history(original)
        np.testing.assert_array_equal(dd, [0, 0, 5, 5, 8])
        np.testing.assert_array_equal(streak, [0, 0, 1, 1, 2])
        changed = original.copy(); changed[3:] = [100., -200.]
        dd2, streak2 = stress_history(changed)
        np.testing.assert_array_equal(dd[:4], dd2[:4])
        np.testing.assert_array_equal(streak[:4], streak2[:4])

    def test_risk_excludes_s1_s2_and_flags_missing_cw(self):
        rng = np.random.default_rng(9)
        X = rng.normal(size=(12, 3))
        names = ['IV', 'S1_fullfit', 'S2_fullfit']
        td = pd.DataFrame({'Date': [str(i) for i in range(12)], 'Leg':['CW']*10+['NW']*2,
                           'u_range_bps': np.arange(12, dtype=float),
                           'str_range_pct':np.arange(12, dtype=float), 'spread_pct':.5})
        td.loc[0, 'spread_pct'] = np.nan
        pnl = np.array([3., -5., 10., -2., 9., -4., 6., -1., 5., 0., 7., -3.])
        risk, _ = risk_fit(X, pnl, names, td)
        X[:, 1:] *= 1000
        changed, _ = risk_fit(X, pnl, names, td)
        np.testing.assert_allclose(risk.Loss_probability, changed.Loss_probability)
        np.testing.assert_allclose(risk.Expected_downside, changed.Expected_downside)
        self.assertTrue(risk.loc[0, 'Dicey'])
        self.assertIn('missing_CW_entry_features', risk.loc[0, 'Flag_reason'])
        np.testing.assert_allclose(risk.Instability.iloc[-2:], .5)

    def test_qualification_rejects_each_risk_violation(self):
        flat = (100., 10., -7.)
        self.assertTrue(qualifies(flat, flat))
        for candidate in ((99., 9., -6.), (101., 11., -6.), (101., 9., -8.)):
            self.assertFalse(qualifies(candidate, flat))

    def test_search_preserves_cuts_and_mean(self):
        p = np.array([9., -5., 7., -3., 8., -4., 6., -2., 11., -1., 10., -3.])
        X = np.column_stack([np.linspace(-1, 1, 12), np.array([1., -1.]*6)])
        lower = np.full(12, .75)
        upper = np.full(12, 1.15); upper[0] = 1.
        dicey = np.array([1, 3, 5, 7]); upper[dicey] = .85
        flat = measure(p)
        with contextlib.redirect_stdout(io.StringIO()):
            results, _, _ = search(X, p, lower, upper, flat, [])
        self.assertTrue(any(qualifies(r['stats'], flat) for r in results.values()))
        for r in results.values():
            self.assertAlmostEqual(r['size'].mean(), 1., places=12)
            self.assertTrue((r['size'][dicey] <= .85).all())
            self.assertTrue((r['size'] <= upper).all())
            self.assertTrue((r['size'] >= lower).all())
            self.assertTrue(qualifies(r['stats'], flat))


if __name__ == '__main__':
    unittest.main()
