import unittest
import numpy as np
import gs_adjustment_overlays as adj
import gs_intraday_replay as base


class Rules(unittest.TestCase):
    def test_fixed_legs_per_regime(self):
        up=adj.legs_for('BuyTested','Directional_expansion',1,100.,102.,98.)
        self.assertEqual(up,[('cw',100.,'CE',1,.5)])
        self.assertEqual(adj.legs_for('SellUntested','Directional_expansion',-1,100.,102.,98.),[('cw',100.,'CE',-1,.5)])
        self.assertIsNone(adj.legs_for('BuyTested','Volatile_whipsaw',0,100.,102.,98.))
        self.assertIsNone(adj.legs_for('SellMore','Directional_expansion',1,100.,102.,98.))
        self.assertEqual(adj.legs_for('SellMore','Volatile_whipsaw',0,100.,102.,98.),[('cw',100.,'CE',-1,.5),('cw',100.,'PE',-1,.5)])
        self.assertEqual(adj.legs_for('Wings','Volatile_whipsaw',0,100.,102.,98.),[('cw',102.,'CE',1,1.),('cw',98.,'PE',1,1.)])
        self.assertEqual(adj.legs_for('NWLongStraddle','Directional_expansion',-1,100.,102.,98.),[('nw',100.,'CE',1,.5),('nw',100.,'PE',1,.5)])

    def test_risk_cap(self):
        # No candidate adds more than 0.5 short units per side to the base straddle (1.5x cap).
        for name in adj.CANDIDATES[1:]:
            for regime,d in (('Directional_expansion',1),('Directional_expansion',-1),('Volatile_whipsaw',0)):
                legs=adj.legs_for(name,regime,d,100.,102.,98.) or []
                for side in ('CE','PE'):
                    self.assertLessEqual(sum(u for _,_,t,s,u in legs if t==side and s<0),.5)


class Pricing(unittest.TestCase):
    def setUp(self):
        base.CURRENT={'Event':False}

    def test_buy_at_ask_sell_at_bid_and_marks(self):
        grid=np.arange(100,121);U=np.full(len(grid),100.)
        # One long CE: quotes every second; bid rises by 1 per second from 5, ask = bid + 1.
        s=np.arange(90,130);bid=5.+np.maximum(0,s-100);ask=bid+1
        books={('cw',100.,'CE'):(s,bid,ask)}
        res=adj.price_overlay([('cw',100.,'CE',1,1.)],books,100,120,grid,U,2.)
        self.assertEqual(res['Entry_second'],102)
        self.assertEqual(res['Entry_px'],[8.])      # ask at 102: bid 7 + 1
        self.assertEqual(res['Exit_px'],[25.])      # bid at 120
        self.assertAlmostEqual(res['Net'],(25-8)*2.-.25)
        self.assertAlmostEqual(res['Mark'][-1],res['Net'])
        self.assertEqual(res['Mark'][0],0.)          # before entry: nothing held

    def test_missing_entry_quote_is_reported(self):
        grid=np.arange(100,121);U=np.full(len(grid),100.)
        books={('cw',100.,'CE'):(np.array([90,150]),np.array([5.,6.]),np.array([6.,7.]))}
        self.assertEqual(adj.price_overlay([('cw',100.,'CE',1,1.)],books,100,120,grid,U,1.)['Status'],'no_entry_quote')


if __name__=='__main__':unittest.main()
