package trading

import (
	"testing"
	"time"
)

func released(g *exitStepGate, qty int64) bool {
	done := make(chan struct{})
	go func() { g.waitStraddleTurn(qty); close(done) }()
	select {
	case <-done:
		return true
	case <-time.After(50 * time.Millisecond):
		return false
	}
}

func TestExitStepGate_NoHedgesNeverWaits(t *testing.T) {
	g := newExitStepGate()
	g.planStraddle(100)
	g.planStraddle(100)
	if !released(g, 100) || !released(g, 100) {
		t.Fatal("a round without hedge legs must not hold the straddle")
	}
}

func TestExitStepGate_StraddleFollowsHedgeFills(t *testing.T) {
	g := newExitStepGate()
	g.planHedge(500)
	g.planHedge(500)
	for i := 0; i < 10; i++ {
		g.planStraddle(100)
	}
	if !released(g, 100) {
		t.Fatal("the first straddle order may lead by one order")
	}
	// second order would put the straddle two orders ahead of zero hedge fills
	done := make(chan struct{})
	go func() { g.waitStraddleTurn(100); close(done) }()
	select {
	case <-done:
		t.Fatal("straddle ran ahead of unfilled hedges")
	case <-time.After(50 * time.Millisecond):
	}
	g.hedgeFilled(100) // hedge 10% filled -> straddle may reach 20%
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("straddle not released after the hedge filled")
	}
}

func TestExitStepGate_ReleasedWhenHedgesFinish(t *testing.T) {
	g := newExitStepGate()
	g.planHedge(1000)
	for i := 0; i < 5; i++ {
		g.planStraddle(200)
	}
	if !released(g, 200) {
		t.Fatal("first order")
	}
	if released(g, 200) {
		t.Fatal("second order must wait while the hedge has filled nothing")
	}
	g.hedgeDone() // the hedge order finished (e.g. outcome unknown) -- nothing left to wait for
	if !released(g, 200) {
		t.Fatal("straddle must be released once no hedge order is live")
	}
}
