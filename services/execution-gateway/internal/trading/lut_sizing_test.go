package trading

import (
	"math"
	"testing"
)

// The LUT build sizes its CE:PE ratio at its minute's prices (as the Paper
// Sim enters), not the live chain a moment later. 2026-10-26 09:16: future
// 22519.25, 22500 CE 143.75 / PE 124.50, raw DTE 1.997.
func TestLUTSizingDeltas(t *testing.T) {
	rec := LUTEvaluation{Time: "09:16", Strike: 22500, Underlying: 22519.25, RawDTE: 1.997, CELTP: 143.75, PELTP: 124.50}
	ce, pe := lutSizingDeltas(rec, 22500)
	if ce < 0.52 || ce > 0.53 || math.Abs(ce-pe-1) > 1e-9 {
		t.Fatalf("deltas at the minute's future: ce %.4f pe %.4f", ce, pe)
	}
	// the simulation's own entry delta at the same minute
	T := rec.RawDTE / 365
	iv := simStrikeIV(rec.Underlying, 22500, T, rec.CELTP, rec.PELTP)
	_, sce, _ := simBlack76(true, rec.Underlying, 22500, T, iv)
	if math.Abs(sce-ce) > 1e-12 {
		t.Fatalf("not the simulation's delta: %.6f vs %.6f", ce, sce)
	}
	// 192 lots -> the cross-weight split DeployStraddle makes
	pool := 2.0 * 192 * 65
	ceLots := math.Round(pool * (-pe / (ce - pe)) / 65)
	peLots := math.Round(pool * (ce / (ce - pe)) / 65)
	if ceLots != 182 || peLots != 202 {
		t.Fatalf("lots %v / %v (live used 178 / 206 off the chain at ~22527.5)", ceLots, peLots)
	}
	if ce, pe := lutSizingDeltas(rec, 22550); ce != 0 || pe != 0 {
		t.Fatal("another strike than the minute's: live chain")
	}
	if ce, pe := lutSizingDeltas(LUTEvaluation{Strike: 22500, Underlying: 22519.25, RawDTE: 1.997}, 22500); ce != 0 || pe != 0 {
		t.Fatal("no prices: live chain")
	}
}
