package trading

import (
	"fmt"
	"time"
)

// Muhurat selling (owner, 2026-11-06): the Muhurat tab has its own straddle-sell
// rule(s) on the Straddle Build engine -- same one-lot IOC clips while the bid
// straddle is at or above the target, same PMS / hedging / SL / TP, and a trade
// on the Portfolio tab (SB-MUH...). What differs, and only for a rule whose
// Session is "muhurat":
//   - LIVE starts only inside a listed special session (special_sessions.json),
//     from its open until muhuratExitBefore ahead of its close;
//   - the exit time is always that cap (15:37 for a 15:40 close), whatever the
//     rule says; SL / TP in bps as set on the rule;
//   - greeks and hedging use the session clock the feed already applies on that
//     date (time to expiry = calendar days + the session's remaining minutes);
//   - the rule never appears on the Straddle Build tab, and nothing starts it but
//     the Start button. The LUT still never sells in a session (lut_build.go).
// A PAPER (shadow) run of a Muhurat rule may start any time, for a dry run.

const sbSessionMuhurat = "muhurat"

// muhuratSellCheck: may a Muhurat rule start now? For LIVE it must be inside a
// listed session before its exit cap. Returns the session's exit time (zero when
// there is no session today: a paper dry run on another day).
func muhuratSellCheck(now time.Time, live bool) (time.Time, error) {
	ss, open, close, ok := specialSessionOn(now)
	if !ok {
		if live {
			return time.Time{}, fmt.Errorf("a Muhurat rule starts LIVE only on a listed special-session date (Muhurat tab); today is not one")
		}
		return time.Time{}, nil
	}
	exit := close.Add(-muhuratExitBefore)
	if live {
		switch {
		case now.Before(open):
			return exit, fmt.Errorf("the %s session opens at %s: LIVE can start from then", ss.Name, ss.Open)
		case !now.Before(exit):
			return exit, fmt.Errorf("the %s session's exit time %s has passed (close %s): no new LIVE build", ss.Name, exit.Format("15:04:05"), ss.Close)
		}
	}
	return exit, nil
}
