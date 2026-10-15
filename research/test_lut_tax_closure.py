import itertools
import unittest

import numpy as np

from lut_tax_closure import implies, solve


class ClosureTests(unittest.TestCase):
    def test_matches_bruteforce_observed_closures(self):
        rng=np.random.default_rng(18)
        dims=(2,2,2,2,3,3)
        grid=np.array(list(itertools.product(*[range(n) for n in dims])))
        for _ in range(6):
            coords=grid[rng.choice(len(grid),8,replace=False)]
            profile={tuple(c):(float(rng.integers(-5,12)),float(rng.integers(1,4))) for c in coords}
            for tax in (0,2,4):
                weights=np.array([round((profile[tuple(c)][0]-tax*profile[tuple(c)][1])*100) for c in coords])
                weights=np.where(weights==0,-1,weights)
                best=0
                for bits in itertools.product([False,True],repeat=len(coords)):
                    chosen=np.array(bits)
                    if any((implies(c,coords)&~chosen).any() for c in coords[chosen]):
                        continue
                    best=max(best,int(weights[chosen].sum()))
                mask=solve(profile,tax,dims)
                actual=np.array([mask[tuple(c)] for c in coords])
                self.assertEqual(int(weights[actual].sum()),best)
                for c in grid[mask.reshape(-1)]:
                    self.assertTrue(mask.reshape(-1)[implies(c,grid)].all())

    def test_higher_tax_and_cold_0916_gate(self):
        dims=(1,2,2,2,3,3)
        profile={(0,0,1,0,0,0):(90.,2.),(0,1,0,1,1,1):(3.,1.)}
        low=solve(profile,13,dims); high=solve(profile,17,dims)
        self.assertFalse((high & ~low).any())
        self.assertTrue(high[0,0,1,0,0,0])
        np.testing.assert_array_equal(solve({},17,dims),False)

    def test_fractional_event_evidence_cannot_anchor(self):
        dims=(1,1,1,1,3,3)
        cell=(0,0,0,0,1,1)
        self.assertFalse(solve({cell:(100.,.333333)},17,dims).any())
        self.assertTrue(solve({cell:(100.,1.333333)},17,dims)[cell])


if __name__=='__main__':
    unittest.main()
