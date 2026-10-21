import unittest
import numpy as np
from gs_stop_risk_baseline import equity_path,episodes


class RiskSeriesTests(unittest.TestCase):
    def test_initial_loss_uses_initial_zero_peak_and_full_recovery(self):
        e,h,dd=equity_path([-10,5,5])
        np.testing.assert_array_equal(dd,[10,5,0])
        row=episodes(['2024-01-01','2024-01-02','2024-01-03'],[-10,5,5]).iloc[0]
        self.assertEqual(row.Peak_date,'Initial_equity_0')
        self.assertEqual(row.Underwater_observations,2)
        self.assertEqual(row.Recovery_date,'2024-01-03')

    def test_unrecovered_drawdown_crosses_year_boundary(self):
        row=episodes(['2023-12-29','2024-01-01','2024-01-02'],[10,-7,3]).iloc[0]
        self.assertEqual(row.Peak_date,'2023-12-29')
        self.assertEqual(row.Depth,7)
        self.assertEqual(row.Status,'Unrecovered')
        self.assertEqual(row.Recovery_date,'')
        self.assertEqual(row.Elapsed_calendar_days,4)

    def test_nan_is_not_a_zero_pnl_or_risk_observation(self):
        with self.assertRaises(ValueError):equity_path([1,np.nan,-1])

    def test_repeated_peak_and_multiple_episodes(self):
        result=episodes(['2024-01-01','2024-01-02','2024-01-03','2024-01-04','2024-01-05'],[5,0,-2,2,-1])
        self.assertEqual(result.Peak_date.tolist(),['2024-01-02','2024-01-04'])
        self.assertEqual(result.Depth.tolist(),[2,1])


if __name__=='__main__':unittest.main()
