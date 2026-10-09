package trading

// Wings follow an exit step by step. A wing exists only to cover a short
// (net CE / PE across all strikes stays zero), so while an exit shrinks the
// net short, the wings of that type are sold by the same quantity right
// after each lot / chunk -- not all at the end. Only reductions are
// followed (an exit never buys a wing); the exit's final close (all wings
// once flat, or the partial's own adjustment) stays as the safety net and
// takes whatever is left.

import (
	"context"
	"log"
)

// wingFollower remembers what one exit already sold of each wing type.
type wingFollower struct {
	soldCE, soldPE int64
}

// follow sells wings until they match the exit's net short reduction so
// far (shortChangeCE / PE: cumulative, negative = the short shrank).
func (w *wingFollower) follow(s *Service, executor Executor, tr StoredTrade, shortChangeCE, shortChangePE int64, reason string) {
	for _, side := range []struct {
		opt    string
		change int64
		sold   *int64
	}{{"CE", shortChangeCE, &w.soldCE}, {"PE", shortChangePE, &w.soldPE}} {
		want := -side.change - *side.sold
		if want <= 0 {
			continue
		}
		sold, err := s.sellWings(context.Background(), executor, tr, side.opt, want, reason)
		*side.sold += sold
		if err != nil {
			log.Printf("[WINGS] ⚠ %s in-step wing sell trade=%s %s %d: %v -- the rest goes at the end", reason, tr.TradeUID, side.opt, want, err)
		}
	}
}

// rest is the short change still to follow at the end of the exit
// (what the follower already sold counts as done).
func (w *wingFollower) rest(shortChangeCE, shortChangePE int64) (int64, int64) {
	return shortChangeCE + w.soldCE, shortChangePE + w.soldPE
}
