package overnight

// One engine for entry, exit, partial exit and roll. Each of them is a
// PLAN (legs with a side and a target quantity) executed in CHUNKS:
//
//   - a chunk sends one LIMIT order per leg, all at once (roll: both sells
//     and both buys together), each at the live bid (sell) / ask (buy);
//   - the next chunk starts only when EVERY order of this one is filled;
//     an order not filled after 2 s is modified to the current bid / ask,
//     again and again;
//   - a rejection, or Stop, cancels what is still pending and ends the job
//     with the position exactly as filled so far.
//
// Entry spreads its quantity over the minutes asked for (1000 in 10 min =
// 100 a minute) and over the seconds of each minute; exit / partial / roll
// send the largest order the broker takes per chunk.
//
// Strikes and prices always come from the last finished minute's
// GreekSoft closes, whether the job starts now or at a set time.

import (
	"context"
	"fmt"
	"log"
	"math"
	"strings"
	"sync"
	"time"
)

// sessionOpen gates LIVE orders (a variable so tests can run at any hour).
var sessionOpen = inSession

type JobReq struct {
	Kind    string  `json:"kind"`
	Delta   float64 `json:"delta"`
	Qty     int64   `json:"qty"`     // per leg (entry / partial)
	Minutes int     `json:"minutes"` // entry: build over this many minutes
	At      string  `json:"at"`      // HH:MM today; empty = now
}

func (s *Service) activeJob() *Job {
	for _, j := range s.st.Jobs {
		if j.Status == "RUNNING" || j.Status == "SCHEDULED" {
			return j
		}
	}
	return nil
}

func (j *Job) logf(format string, a ...interface{}) {
	line := time.Now().In(ist()).Format("15:04:05") + " " + fmt.Sprintf(format, a...)
	j.Log = append(j.Log, line)
	if len(j.Log) > 300 {
		j.Log = j.Log[len(j.Log)-300:]
	}
	log.Printf("[OVERNIGHT] job=%s %s %s", j.ID, j.Kind, fmt.Sprintf(format, a...))
}

// Submit validates a request and starts (or schedules) its job.
func (s *Service) Submit(r JobReq) (*Job, error) {
	r.Kind = strings.ToUpper(strings.TrimSpace(r.Kind))
	now := time.Now().In(ist())
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.chain == nil {
		return nil, fmt.Errorf("monthly chain not loaded: %s", s.chainErr)
	}
	if a := s.activeJob(); a != nil {
		return nil, fmt.Errorf("%s job %s is %s -- stop it or let it finish first", a.Kind, a.ID, strings.ToLower(a.Status))
	}
	p := s.st.Position
	mode := s.st.Mode
	if p != nil {
		mode = p.Mode
	}
	switch r.Kind {
	case "ENTRY":
		if p != nil {
			r.Delta = p.Delta // a top-up keeps the position's delta and strikes
		}
		if r.Delta <= 0 || r.Delta >= 1 {
			return nil, fmt.Errorf("delta must be between 0 and 1 (e.g. 0.25)")
		}
		if r.Qty <= 0 {
			return nil, fmt.Errorf("quantity to buy per leg is required")
		}
		if r.Minutes < 0 || r.Minutes > 375 {
			return nil, fmt.Errorf("build time must be 0-375 minutes")
		}
	case "EXIT", "ROLL":
		if p == nil || p.qty() == 0 {
			return nil, fmt.Errorf("no open overnight position")
		}
	case "PARTIAL":
		if p == nil || p.qty() == 0 {
			return nil, fmt.Errorf("no open overnight position")
		}
		if r.Qty <= 0 {
			return nil, fmt.Errorf("quantity to exit per leg is required")
		}
	default:
		return nil, fmt.Errorf("unknown action %q", r.Kind)
	}
	var at time.Time
	if a := strings.TrimSpace(r.At); a != "" {
		hm, err := time.ParseInLocation("15:04", a, ist())
		if err != nil {
			return nil, fmt.Errorf("time must be HH:MM")
		}
		at = time.Date(now.Year(), now.Month(), now.Day(), hm.Hour(), hm.Minute(), 0, 0, ist())
		if !at.After(now) {
			return nil, fmt.Errorf("%s is already past -- leave the time empty to start now", a)
		}
	}
	if mode == ModeLive {
		chk := now
		if !at.IsZero() {
			chk = at
		}
		if !sessionOpen(chk) {
			return nil, fmt.Errorf("LIVE orders go only in the broker session (09:15-15:40)")
		}
	}
	s.st.Seq++
	j := &Job{ID: fmt.Sprintf("J%s-%d", now.Format("0102"), s.st.Seq), Kind: r.Kind, Status: "RUNNING", Mode: mode,
		Delta: r.Delta, Qty: r.Qty, Minutes: r.Minutes, CreatedAt: now.Format("2006-01-02 15:04:05")}
	if !at.IsZero() {
		j.Status, j.At = "SCHEDULED", at.Format("15:04")
	}
	s.st.Jobs = append(s.st.Jobs, j)
	ctx, cancel := context.WithCancel(context.Background())
	s.jobCancel = cancel
	_ = s.st.save(s.dir)
	go s.run(ctx, j, at)
	return j, nil
}

// Stop ends the active job; its pending orders are cancelled.
func (s *Service) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeJob() == nil || s.jobCancel == nil {
		return fmt.Errorf("no job is running")
	}
	s.jobCancel()
	return nil
}

func (s *Service) run(ctx context.Context, j *Job, at time.Time) {
	err := s.runJob(ctx, j, at)
	s.mu.Lock()
	defer s.mu.Unlock()
	j.EndedAt = time.Now().In(ist()).Format("2006-01-02 15:04:05")
	switch {
	case err == nil:
		j.Status = "DONE"
		j.logf("done")
	case ctx.Err() != nil:
		j.Status, j.Note = "STOPPED", err.Error()
		j.logf("stopped: %v", err)
	default:
		j.Status, j.Note = "FAILED", err.Error()
		j.logf("FAILED: %v", err)
	}
	if p := s.st.Position; p != nil {
		switch {
		case p.qty() == 0 && len(p.Legs) == 0: // an entry that bought nothing
			s.st.Position = nil
		case p.qty() == 0:
			p.ClosedAt = j.EndedAt
			s.st.History = append(s.st.History, p)
			s.st.Position = nil
		case j.Kind == "ROLL" && err == nil:
			p.Rolls++
		}
	}
	s.jobCancel = nil
	_ = s.st.save(s.dir)
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// lastMinute is the scenario of the last finished minute, with the picks
// for delta d. In market hours it is computed now if not there yet.
func (s *Service) lastMinute(ctx context.Context, d float64, mode string) (*Scenario, error) {
	now := time.Now().In(ist())
	if inMarket(now) && now.Hour()*100+now.Minute() >= 916 {
		if now.Second() < 3 { // let the intraday minute-end capture go first
			if !sleepCtx(ctx, time.Duration(3-now.Second())*time.Second) {
				return nil, ctx.Err()
			}
		}
		end := now.Truncate(time.Minute)
		sc, err := s.minuteEnd(end)
		if err != nil {
			return nil, err
		}
		if d > 0 && sc.picks(d) == nil { // this delta was not followed yet
			s.computing.Lock()
			extra, err := s.compute(ctx, end)
			s.computing.Unlock()
			if err != nil {
				return nil, err
			}
			s.mu.Lock()
			sc.Picks[deltaKey(d)] = extra.picks(d)
			s.mu.Unlock()
		}
		return sc, nil
	}
	if mode == ModeLive {
		return nil, fmt.Errorf("market is closed")
	}
	sc := s.lastRow()
	if sc == nil {
		return nil, fmt.Errorf("no minute-end data yet")
	}
	return sc, nil
}

type part struct {
	pl  *PlanLeg
	in  Instrument
	qty int64
}

func (s *Service) runJob(ctx context.Context, j *Job, at time.Time) error {
	if !at.IsZero() {
		s.mu.Lock()
		j.logf("scheduled for %s", j.At)
		s.mu.Unlock()
		if !sleepCtx(ctx, time.Until(at)) {
			return fmt.Errorf("stopped before its start time")
		}
		s.mu.Lock()
		j.Status = "RUNNING"
		s.mu.Unlock()
	}
	s.mu.Lock()
	j.StartedAt = time.Now().In(ist()).Format("2006-01-02 15:04:05")
	c := s.chain
	s.mu.Unlock()

	lot, maxQ, err := s.limits(ctx, j.Mode)
	if err != nil {
		return err
	}
	maxQ -= maxQ % lot
	lots := func(q int64) int64 { return q - q%lot }

	d := j.Delta
	s.mu.Lock()
	if p := s.st.Position; p != nil {
		d = p.Delta
	}
	s.mu.Unlock()
	sc, err := s.lastMinute(ctx, d, j.Mode)
	if err != nil {
		return err
	}

	// ---- the plan
	s.mu.Lock()
	j.Minute, j.Delta = sc.Minute, d
	p := s.st.Position
	var plan []*PlanLeg
	add := func(in Instrument, side string, q int64) {
		if q > 0 {
			plan = append(plan, &PlanLeg{Token: in.Token, Strike: in.Strike, Opt: in.Opt, Side: side, Target: q})
		}
	}
	legIn := func(l *Leg) Instrument {
		return Instrument{GToken: l.GToken, Token: l.Token, Strike: l.Strike, Opt: l.Opt}
	}
	var perr error
	switch j.Kind {
	case "ENTRY":
		q := lots(j.Qty)
		if q <= 0 {
			perr = fmt.Errorf("quantity %d is below one lot (%d)", j.Qty, lot)
			break
		}
		if p != nil && p.qty() > 0 { // top-up: the strikes of the entry
			for _, opt := range []string{"CE", "PE"} {
				if l := p.held(opt); l != nil {
					add(legIn(l), "BUY", q)
				}
			}
			break
		}
		pk := sc.picks(d)
		if pk == nil || pk.CE == nil || pk.PE == nil {
			perr = fmt.Errorf("no %.4f-delta strikes at the %s minute end", d, sc.Minute)
			break
		}
		ce, _ := c.byToken(pk.CE.Token)
		pe, _ := c.byToken(pk.PE.Token)
		add(ce, "BUY", q)
		add(pe, "BUY", q)
		s.st.Seq++
		p = &Position{ID: fmt.Sprintf("N%s%d", time.Now().In(ist()).Format("020106"), s.st.Seq), Mode: j.Mode, Expiry: c.Expiry, Delta: d, LotSize: lot,
			OpenedAt: j.StartedAt}
		s.st.Position = p
		j.logf("strikes from the %s minute end: %.0f CE (delta %.4f @ %.2f), %.0f PE (delta %.4f @ %.2f)", sc.Minute,
			pk.CE.Strike, pk.CE.Delta, pk.CE.Price, pk.PE.Strike, pk.PE.Delta, pk.PE.Price)
	case "EXIT":
		for _, l := range p.Legs {
			add(legIn(l), "SELL", l.Qty)
		}
	case "PARTIAL":
		for _, l := range p.Legs {
			add(legIn(l), "SELL", min64(lots(j.Qty), l.Qty))
		}
	case "ROLL":
		pk := sc.picks(d)
		if pk == nil || pk.CE == nil || pk.PE == nil {
			perr = fmt.Errorf("no %.4f-delta strikes at the %s minute end", d, sc.Minute)
			break
		}
		for _, x := range []struct {
			opt string
			pk  *Pick
		}{{"CE", pk.CE}, {"PE", pk.PE}} {
			old := p.held(x.opt)
			if old == nil {
				continue
			}
			if old.Strike == x.pk.Strike {
				j.logf("%s stays at %.0f (still the %.4f-delta strike)", x.opt, old.Strike, d)
				continue
			}
			in, _ := c.byToken(x.pk.Token)
			j.logf("%s %.0f -> %.0f (delta %.4f @ %.2f), qty %d", x.opt, old.Strike, in.Strike, x.pk.Delta, x.pk.Price, old.Qty)
			add(legIn(old), "SELL", old.Qty)
			add(in, "BUY", old.Qty)
		}
	}
	j.Plan = plan
	_ = s.st.save(s.dir)
	s.mu.Unlock()
	if perr != nil {
		return perr
	}
	if len(plan) == 0 {
		return nil
	}

	ins := map[int64]Instrument{}
	var gt []int64
	for _, pl := range plan {
		in, ok := c.byToken(pl.Token)
		if !ok {
			return fmt.Errorf("%.0f %s is not in the %s chain", pl.Strike, pl.Opt, c.Expiry)
		}
		ins[pl.Token] = in
		gt = append(gt, in.GToken)
	}
	if err := s.feed.subscribe(ctx, gt, true); err != nil {
		return err
	}

	// ---- entry: paced over the minutes asked for
	if j.Kind == "ENTRY" && j.Minutes > 0 {
		total := plan[0].Target / lot
		start := time.Now()
		done := int64(0)
		for m := 0; m < j.Minutes; m++ {
			slice := int64(math.Round(float64(total)*float64(m+1)/float64(j.Minutes))) - done
			if slice <= 0 {
				continue
			}
			clips := int64(6) // spread inside the minute
			if slice < clips {
				clips = slice
			}
			inSlice := int64(0)
			for i := int64(0); i < clips; i++ {
				due := start.Add(time.Duration(m)*time.Minute + time.Duration(i)*time.Minute/time.Duration(clips))
				if !sleepCtx(ctx, time.Until(due)) {
					return fmt.Errorf("stopped by user")
				}
				n := int64(math.Round(float64(slice)*float64(i+1)/float64(clips))) - inSlice
				inSlice += n
				for n > 0 { // a clip above the broker's order size goes in several chunks
					q := min64(n*lot, maxQ)
					var parts []part
					for _, pl := range plan {
						parts = append(parts, part{pl, ins[pl.Token], q})
					}
					if err := s.runChunk(ctx, j, parts, lot); err != nil {
						return err
					}
					n -= q / lot
				}
			}
			done += slice
		}
		return nil
	}

	// ---- everything else: the largest order per leg, chunk after chunk
	for {
		var parts []part
		for _, pl := range plan {
			if left := pl.Target - pl.Done; left > 0 {
				parts = append(parts, part{pl, ins[pl.Token], min64(left, maxQ)})
			}
		}
		if len(parts) == 0 {
			return nil
		}
		if err := s.runChunk(ctx, j, parts, lot); err != nil {
			return err
		}
	}
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// price is the limit an order goes at: the live ask for a buy, the live
// bid for a sell (last traded price when that side of the book is empty).
func (s *Service) price(ctx context.Context, in Instrument, side string) (float64, error) {
	deadline := time.Now().Add(4 * time.Second)
	for {
		q, _, err := s.feed.quotes(ctx, []int64{in.GToken})
		if err != nil {
			return 0, err
		}
		t := q[in.GToken]
		px := t.Bid
		if side == "BUY" {
			px = t.Ask
		}
		if px <= 0 {
			px = t.LTP
		}
		if px > 0 {
			return tick(px), nil
		}
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("no live GreekSoft price for %.0f %s", in.Strike, in.Opt)
		}
		if !sleepCtx(ctx, 300*time.Millisecond) {
			return 0, ctx.Err()
		}
	}
}

func terminal(st string) bool { return st == "FILLED" || st == "REJECTED" || st == "CANCELLED" }

// runChunk sends one order per part and returns when all are filled.
func (s *Service) runChunk(ctx context.Context, j *Job, parts []part, lot int64) error {
	br := Broker(s.paper)
	if j.Mode == ModeLive {
		br = s.live
		if !sessionOpen(time.Now()) {
			return fmt.Errorf("broker session is closed")
		}
	}
	bg := context.Background() // booking and cancelling must outlive a Stop

	s.mu.Lock()
	j.Chunk++
	p := s.st.Position
	userTag := p.userTag()
	recs := make([]*OrderRec, len(parts))
	for i, pt := range parts {
		s.st.Seq++
		recs[i] = &OrderRec{Time: time.Now().In(ist()).Format("2006-01-02 15:04:05"), JobID: j.ID,
			Tag: fmt.Sprintf("%s-%s-%.0f%s-%d", p.ID, j.Kind[:3], pt.in.Strike, pt.in.Opt, s.st.Seq), UserTag: userTag,
			Token: pt.in.Token, Strike: pt.in.Strike, Opt: pt.in.Opt, Side: pt.pl.Side, Qty: pt.qty, Status: "NEW", Mode: j.Mode}
	}
	s.mu.Unlock()

	// All orders of the chunk go out together.
	var wg sync.WaitGroup
	for i := range parts {
		wg.Add(1)
		go func(pt part, rec *OrderRec) {
			defer wg.Done()
			px, err := s.price(ctx, pt.in, rec.Side)
			if err == nil {
				rec.Price = px
				rec.OrderID, err = br.Place(ctx, PlaceReq{Tag: rec.Tag, UserTag: rec.UserTag, Token: pt.in.Token, Side: rec.Side, Qty: pt.qty, LotSize: lot, Price: px})
			}
			if err != nil || rec.OrderID == "" {
				rec.Status = "REJECTED"
				rec.Reason = fmt.Sprintf("not placed: %v", err)
				return
			}
			rec.Status = "OPEN"
		}(parts[i], recs[i])
	}
	wg.Wait()

	s.mu.Lock()
	var abort error
	for _, rec := range recs {
		s.st.Orders = append(s.st.Orders, rec)
		j.logf("chunk %d: %s %d x %.0f %s @ %.2f -> %s %s tag=%s", j.Chunk, rec.Side, rec.Qty, rec.Strike, rec.Opt, rec.Price, rec.Status, rec.OrderID, rec.Tag)
		if rec.Status == "REJECTED" && abort == nil {
			abort = fmt.Errorf("%s %.0f %s %s", rec.Side, rec.Strike, rec.Opt, rec.Reason)
		}
	}
	_ = s.st.save(s.dir)
	s.mu.Unlock()

	cancelled := false
	var cancelAt time.Time
	lastReprice := time.Now()
	for {
		book, err := br.Orders(bg)
		byID := map[string]Order{}
		for _, o := range book {
			byID[o.ID] = o
		}
		all := true
		s.mu.Lock()
		for i, rec := range recs {
			if rec.OrderID == "" {
				continue
			}
			o, ok := byID[rec.OrderID]
			if err != nil || !ok {
				all = false
				continue
			}
			if o.Filled > rec.Filled {
				inc := o.Filled - rec.Filled
				avg := o.AvgFill
				if avg <= 0 {
					if v, ok := br.FillPrice(bg, rec.OrderID); ok {
						avg = v
					}
				}
				px := rec.Price
				if avg > 0 {
					if v := (avg*float64(o.Filled) - rec.value) / float64(inc); v > 0 {
						px = v
					}
				}
				rec.value += px * float64(inc)
				rec.Filled = o.Filled
				rec.AvgFill = round2(rec.value / float64(rec.Filled))
				p.fill(parts[i].in, rec.Side, inc, px)
				parts[i].pl.Done += inc
			}
			rec.Status = o.Status
			if o.Status == "REJECTED" {
				rec.Reason = o.Reason
				if abort == nil {
					abort = fmt.Errorf("%s %d x %.0f %s rejected by the broker: %s", rec.Side, rec.Qty, rec.Strike, rec.Opt, o.Reason)
				}
			}
			if o.Status == "CANCELLED" && rec.Filled < rec.Qty && abort == nil && ctx.Err() == nil {
				abort = fmt.Errorf("%s %.0f %s was cancelled outside the module with %d of %d filled", rec.Side, rec.Strike, rec.Opt, rec.Filled, rec.Qty)
			}
			if !terminal(o.Status) {
				all = false
			}
		}
		_ = s.st.save(s.dir)
		s.mu.Unlock()
		if all {
			break
		}

		if (abort != nil || ctx.Err() != nil) && !cancelled {
			cancelled, cancelAt = true, time.Now()
			for _, rec := range recs {
				if rec.OrderID != "" && !terminal(rec.Status) {
					if cerr := br.Cancel(bg, rec.OrderID); cerr != nil {
						s.mu.Lock()
						j.logf("⚠ cancel %s (%.0f %s) failed: %v -- cancel it at the broker", rec.OrderID, rec.Strike, rec.Opt, cerr)
						s.mu.Unlock()
					}
				}
			}
		}
		if cancelled && time.Since(cancelAt) > 12*time.Second {
			break // fills so far are booked; anything still open is reported below
		}
		if !cancelled && time.Since(lastReprice) >= 2*time.Second {
			lastReprice = time.Now()
			for i, rec := range recs {
				if rec.OrderID == "" || terminal(rec.Status) {
					continue
				}
				px, perr := s.price(ctx, parts[i].in, rec.Side)
				if perr != nil || px == rec.Price {
					continue
				}
				if merr := br.Modify(bg, rec.OrderID, px, rec.Qty, lot); merr != nil {
					s.mu.Lock()
					j.logf("modify %s %.0f %s to %.2f failed: %v", rec.OrderID, rec.Strike, rec.Opt, px, merr)
					s.mu.Unlock()
					continue
				}
				s.mu.Lock()
				rec.Modifies++
				j.logf("chunk %d: %.0f %s %d/%d filled, modified %.2f -> %.2f", j.Chunk, rec.Strike, rec.Opt, rec.Filled, rec.Qty, rec.Price, px)
				rec.Price = px
				s.mu.Unlock()
			}
		}
		time.Sleep(600 * time.Millisecond)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	short := ""
	for _, rec := range recs {
		if rec.Filled < rec.Qty {
			short += fmt.Sprintf(" %s %.0f %s %d/%d (%s)", rec.Side, rec.Strike, rec.Opt, rec.Filled, rec.Qty, rec.Status)
		}
	}
	if abort != nil {
		return fmt.Errorf("%v; chunk %d ended with:%s", abort, j.Chunk, short)
	}
	if ctx.Err() != nil {
		return fmt.Errorf("stopped by user; chunk %d ended with:%s", j.Chunk, short)
	}
	if short != "" {
		return fmt.Errorf("chunk %d not complete:%s", j.Chunk, short)
	}
	j.logf("chunk %d filled", j.Chunk)
	return nil
}
