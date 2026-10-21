import unittest
from unittest.mock import MagicMock,patch
from types import SimpleNamespace
import numpy as np
import pandas as pd
from complement_strategy import GRID,availability,direction_signal,lower_iv,execute,greek_values
import complement_strategy as engine


class CausalityAndExecutionTests(unittest.TestCase):
    def books(self):
        n=len(GRID)
        return {s:{'raw_ask':np.full(n,100.),'raw_bid':np.full(n,99.),'bid':np.full(n,99.),
            'ask':np.full(n,100.),'iv':np.full(n,.2),'days':np.full(n,7.)} for s in ('CE','PE')}

    def test_source_bucket_is_available_one_second_later(self):
        a=availability(pd.Series(pd.to_datetime(['1900-01-01 09:30:00','2026-10-21 09:30:01'])))
        np.testing.assert_array_equal(a,[34201,34202])

    def test_future_prices_cannot_change_current_direction(self):
        decision=9*3600+31*60;p=decision-GRID[0]
        u=np.linspace(100.,120.,len(GRID));expected=direction_signal(u,decision,102.,100.)
        self.assertEqual(expected[0],1)
        u[p:]=-10000.
        self.assertEqual(direction_signal(u,decision,102.,100.),expected)

    def test_iv_uses_past_midrank_and_ignores_future(self):
        book=self.books();decision=9*3600+31*60;p=decision-GRID[0]
        self.assertEqual(lower_iv(book,decision),(True,.5))
        for side in book:book[side]['iv'][p:]=2.
        self.assertEqual(lower_iv(book,decision),(True,.5))

    def test_buy_ask_sell_bid_fees_and_cash_budget(self):
        book=self.books();u=np.full(len(GRID),25000.);decision=9*3600+31*60
        r=execute(book,{'CE':1},decision,u,25000.)
        self.assertEqual(r['Entry_Time'],'09:31:02');self.assertEqual(r['Exit_Time'],'09:46:03')
        self.assertAlmostEqual(r['Entry_debit_points']*r['Normalized_quantity'],100.)
        self.assertLess(r['Net_PnL'],0.);self.assertAlmostEqual(r['Double_fee_net'],r['Net_PnL']-r['Fee'])

    def test_multileg_fill_requires_synchronized_quotes(self):
        book=self.books();u=np.full(len(GRID),25000.);decision=9*3600+31*60;p=decision-GRID[0]
        book['PE']['raw_ask'][p+2:p+4]=np.nan
        r=execute(book,{'CE':2,'PE':1},decision,u,25000.)
        self.assertEqual(r['Entry_Time'],'09:31:04')

    def test_stop_fill_can_overshoot_but_gross_loss_is_premium_bounded(self):
        book=self.books();u=np.full(len(GRID),25000.);decision=9*3600+31*60;p=decision-GRID[0]
        book['CE']['bid'][p+3:]=60.;book['CE']['raw_bid'][p+4:]=.01
        r=execute(book,{'CE':1},decision,u,25000.)
        self.assertEqual(r['Exit_Reason'],'Premium_stop30');self.assertLess(r['Gross_PnL'],-30.)
        self.assertGreaterEqual(r['Gross_PnL'],-100.)

    def test_unresolved_entered_position_fails_instead_of_becoming_no_trade(self):
        book=self.books();u=np.full(len(GRID),25000.);decision=9*3600+31*60;p=decision-GRID[0]
        book['CE']['raw_bid'][p+3:]=np.nan
        with self.assertRaisesRegex(RuntimeError,'no synchronized executable exit'):execute(book,{'CE':1},decision,u,25000.)

    def test_long_structures_have_positive_gamma_vega_and_directional_delta(self):
        for direction,q in [(1,{'CE':2,'PE':1}),(-1,{'CE':1,'PE':2})]:
            delta,gamma,vega=greek_values(25000.,25000.,7.,{'CE':.2,'PE':.2},q,1.)
            self.assertGreater(direction*delta,0.);self.assertGreater(gamma,0.);self.assertGreater(vega,0.)

    def test_no_signal_shortcut_matches_full_and_avoids_option_quote_read(self):
        times=pd.Timestamp('1900-01-01')+pd.to_timedelta(GRID,unit='s')
        under=pd.DataFrame({'Time_dt':times,'Expiry':pd.Timestamp('2024-01-04'),'UnderlyingLTP':25000.})
        quotes=pd.DataFrame(columns=['Time_dt','Strike','Type','Bid','Ask','BidQty','AskQty','AskIV','DTE'])
        quotes['Time_dt']=pd.to_datetime(quotes.Time_dt)
        file=MagicMock();file.schema_arrow.names=['Time_dt']
        file.metadata.row_group.return_value.column.return_value.statistics.min=pd.Timestamp('1900-01-01 09:15:00')
        with patch.object(engine.Path,'stat',return_value=SimpleNamespace(st_size=1,st_mtime_ns=1)),patch.object(engine.pq,'ParquetFile',return_value=file):
            with patch.object(engine.pd,'read_parquet',side_effect=[under.copy()]) as read:
                fast=engine.simulate_day('2024-01-02');self.assertEqual(read.call_count,1)
            with patch.object(engine.pd,'read_parquet',side_effect=[under.copy(),quotes.copy()]):
                full=engine.simulate_day('2024-01-02',False)
        self.assertFalse(fast['Error']);self.assertFalse(full['Error'])
        self.assertTrue(fast['No_signal_fast_path'])
        pd.testing.assert_frame_equal(pd.DataFrame(fast['Rows']),pd.DataFrame(full['Rows']))


if __name__=='__main__':unittest.main()
