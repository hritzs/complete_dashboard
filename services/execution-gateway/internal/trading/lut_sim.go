package trading

// LUT entry check -- PAPER ONLY, always on. Started at gateway boot, it
// re-evaluates the LUT on every chain update (the decoder publishes every
// 100ms) and pushes the result to the UI as a "lut_update" websocket event.
// At the first tick of each minute 09:16-13:30 it records that minute's
// official YES/NO; the first YES is the paper entry. Nothing in this file
// submits, modifies or cancels any order.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	defaultLUTDir      = "/mnt/shared/Hardik/Profilers"
	defaultLUTDailyCSV = "/mnt/shared/Hardik/Project_Codes/GammaShortDailyProcess/GammaShortDailydata.csv"
	lutSymbol          = "NIFTY"
	lutRecordUntil     = 1540                   // minutes are recorded (data) to 15:40 (session close); entries only to 13:30
	lutTickInterval    = 100 * time.Millisecond // = decoder chain publish interval
	lutGridEvery       = 10                     // push the (larger) grids every 10th tick = 1s
	lutInputsRetry     = 30 * time.Second
)

func lutEnv(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func lutIST() *time.Location {
	if loc, err := time.LoadLocation("Asia/Kolkata"); err == nil {
		return loc
	}
	return time.FixedZone("IST", 5*3600+1800)
}

func lutParseExpiry(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{"02-Jan-06", "02-Jan-2006", "2006-01-02", "02Jan2006", "02Jan06"} {
		if t, err := time.ParseInLocation(layout, s, lutIST()); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognised expiry %q", s)
}

func lutUnderlying(chain *OptionChainSnapshot) float64 {
	// Always the synthetic future (from the ATM CE/PE), the platform's
	// canonical underlying. future_ltp is NOT used: on 2026-10-05 it read
	// 22955 / 22712 / 22726 while every strike implied ~22625, and even a
	// 0.4% error put the LUT on the wrong ATM and gave a false YES.
	if chain.SyntheticFuture > 0 {
		return chain.SyntheticFuture
	}
	return chain.SyntheticSpot
}

// LUTConfig is what the user sets on the tab.
type LUTConfig struct {
	Lots           int       `json:"lots"`
	Expiry         string    `json:"expiry,omitempty"`          // blank = nearest
	Underlying0916 float64   `json:"underlying_0916,omitempty"` // override the captured 09:16 future
	Manual         LUTManual `json:"manual"`                    // what-if overrides (nil fields = live)
}

// LUTEntry is the first YES of the day: the paper entry (never executed).
type LUTEntry struct {
	Time       string        `json:"time"`
	Stage      string        `json:"stage"`
	Strike     float64       `json:"strike"`
	Expiry     string        `json:"expiry"`
	Lots       int           `json:"lots"`
	LotSize    int           `json:"lot_size"`
	Qty        int           `json:"qty"`
	CEToken    int64         `json:"ce_token"`
	PEToken    int64         `json:"pe_token"`
	CEBid      float64       `json:"ce_bid"`
	CEAsk      float64       `json:"ce_ask"`
	PEBid      float64       `json:"pe_bid"`
	PEAsk      float64       `json:"pe_ask"`
	Underlying float64       `json:"underlying"`
	SLBps      float64       `json:"sl_bps"`
	TPBps      float64       `json:"tp_bps"`
	SLPoints   float64       `json:"sl_points"`
	TPPoints   float64       `json:"tp_points"`
	SLRupees   float64       `json:"sl_rupees"`
	TPRupees   float64       `json:"tp_rupees"`
	Evaluation LUTEvaluation `json:"evaluation"`
	Source     string        `json:"source"` // "LUT YES" or "started manually"
}

// LUTLive is the tick-rate payload pushed to the UI.
type LUTLive struct {
	Preopen        string         `json:"preopen,omitempty"`    // pre-open estimate in use (lut_preopen.go)
	LastClose      *MinuteClose   `json:"last_close,omitempty"` // last minute's ATM candle closes
	Now            string         `json:"now"`
	Day            string         `json:"day"`
	Config         LUTConfig      `json:"config"`
	Expiry         string         `json:"expiry"`
	LotSize        int            `json:"lot_size"`
	Inputs         LUTDailyInputs `json:"inputs"`
	InputsError    string         `json:"inputs_error,omitempty"`
	TablesError    string         `json:"tables_error,omitempty"`
	TableYes       [5]int         `json:"table_yes"`
	BusDays        int            `json:"bus_days"`
	Underlying0916 float64        `json:"underlying_0916"`
	OGSource       string         `json:"og_source"`
	NormOG         float64        `json:"norm_og"`
	Phase          string         `json:"phase"` // before 09:16 / scanning / after 13:30
	Eval           *LUTEvaluation `json:"eval,omitempty"`
	Grid           *LUTGrid       `json:"grid,omitempty"`
	Grid0920       *LUTGrid       `json:"grid_0920,omitempty"`
	Entry          *LUTEntry      `json:"entry,omitempty"`
	MinutesCount   int            `json:"minutes_count"`
	ChainError     string         `json:"chain_error,omitempty"`
	EvalError      string         `json:"eval_error,omitempty"`
	NextCheck      string         `json:"next_check,omitempty"` // minute whose table is shown
	Manual         bool           `json:"manual"`               // a what-if is shown instead of live values
	ManualFields   []string       `json:"manual_fields"`        // which values are manual
	SellRange      *LUTSellRange  `json:"sell_range,omitempty"`
	SellRange0920  *LUTSellRange  `json:"sell_range_0920,omitempty"`
}

// LUTState is the full state (live payload + every recorded minute).
type LUTState struct {
	LUTLive
	Params  LUTParams       `json:"params"`
	Minutes []LUTEvaluation `json:"minutes"`
}

type lutEngine struct {
	mu sync.Mutex

	preopen   lutPreopen   // pre-open estimate (lut_preopen.go)
	lastClose *MinuteClose // the last minute's ATM candle closes
	gsKick    int64        // boundary the GreekSoft close fetch was started for
	// ATM straddle high / low: the minute being built, the finished one
	// (keyed HHMM of the minute it covers) and the day so far.
	strMin, strPrevMin int
	strCur, strPrev    lutStrHL
	strDay             lutStrHL
	preopenAt          time.Time

	cfg LUTConfig

	day          string
	set          LUTSet
	tablesLoaded bool
	tablesErr    string
	tablesTried  time.Time
	inputs       LUTDailyInputs
	inputsOK     bool
	inputsErr    string
	inputsTried  time.Time

	expiry        string
	lotSize       int
	lotSizeExpiry string
	lotSizeTried  time.Time
	u0916         float64
	ogSource      string

	minutes     []LUTEvaluation
	lastMinute  int
	lastChain   int // minute whose whole chain was last saved
	lastNext    int // same, next-expiry chain
	lastIV      float64
	lastLTP     map[int64]lutLTPSeen // token -> last non-zero LTP, refreshed every tick
	build       *LUTBuildRun         // today's REAL build fired by the LUT (one per day)
	tests       []*LUTBuildRun       // today's user test fires (never the day's entry)
	carryLogged int                  // minute whose carried-LTP fill was last logged
	entry       *LUTEntry
	startNow    bool

	live LUTLive
}

var lutEng = &lutEngine{cfg: LUTConfig{Lots: 1}}

// lutLTPCarry: a leg reading 0 at the minute's first tick takes its last
// non-zero LTP from at most this long ago (the previous second's data).
const lutLTPCarry = time.Second

type lutLTPSeen struct {
	px float64
	at time.Time
}

// noteLTPs remembers every non-zero CE/PE LTP in the chain. Caller holds e.mu.
func (e *lutEngine) noteLTPs(now time.Time, chain *OptionChainSnapshot) {
	if e.lastLTP == nil {
		e.lastLTP = make(map[int64]lutLTPSeen)
	}
	for _, r := range chain.Chain {
		if r.CEToken > 0 && r.CELtp > 0 {
			e.lastLTP[r.CEToken] = lutLTPSeen{r.CELtp, now}
		}
		if r.PEToken > 0 && r.PELtp > 0 {
			e.lastLTP[r.PEToken] = lutLTPSeen{r.PELtp, now}
		}
	}
}

// carryLTP returns px, or -- when px is 0 -- the token's last non-zero LTP
// if it was seen within lutLTPCarry. Caller holds e.mu.
func (e *lutEngine) carryLTP(now time.Time, token int64, px float64) (float64, bool) {
	if px > 0 || token <= 0 {
		return px, false
	}
	if s, ok := e.lastLTP[token]; ok && now.Sub(s.at) <= lutLTPCarry {
		return s.px, true
	}
	return px, false
}

func carriedTag(carried bool) string {
	if carried {
		return " (prev second)"
	}
	return ""
}

// StartLUTEngine starts the always-on paper LUT check (call once at boot).
func (s *Service) StartLUTEngine() {
	gsc.warmOnce.Do(func() { go s.gsWarmLoop() }) // GreekSoft candles warm from the start
	go func() {
		log.Printf("[LUT] entry check engine started (PAPER, YES/NO only, never executes); tick %s", lutTickInterval)
		t := time.NewTicker(lutTickInterval)
		defer t.Stop()
		n := 0
		for range t.C {
			n++
			s.lutTick(n%lutGridEvery == 0)
		}
	}()
}

// lutNewDay resets per-day state and (re)loads the tables.
func (e *lutEngine) newDay(day string) {
	e.day = day
	e.inputsOK, e.inputsErr, e.inputsTried = false, "", time.Time{}
	e.u0916, e.ogSource = 0, ""
	e.minutes, e.lastMinute, e.lastIV, e.entry = nil, 0, 0, nil
	e.build, e.tests = nil, nil
	e.lastLTP = nil
	e.strMin, e.strPrevMin, e.strCur, e.strPrev, e.strDay = 0, 0, lutStrHL{}, lutStrHL{}, lutStrHL{}
	e.lastChain, e.lastNext = 0, 0
	for t := range lutLoadChainRecorded(day) {
		if hm := lutHHMM(t); hm > e.lastChain {
			e.lastChain = hm
		}
	}
	for t := range lutLoadChainFile(lutChainFile(day, "next")) {
		if hm := lutHHMM(t); hm > e.lastNext {
			e.lastNext = hm
		}
	}
	// Restart mid-day: reload what was already recorded today.
	if mins, st := lutLoadDay(day); len(mins) > 0 || st.U0916 > 0 || st.Entry != nil {
		for i := range mins {
			lutRefreshTP(&mins[i])
		}
		if d := st.Entry; d != nil && lutRefreshTP(&d.Evaluation) {
			d.TPBps = d.Evaluation.TPBps
			d.TPPoints = d.Underlying * d.TPBps / 10000
			d.TPRupees = d.TPPoints * float64(d.Qty)
		}
		e.minutes, e.entry, e.build, e.tests = mins, st.Entry, st.Build, st.Tests
		for _, t := range e.tests {
			if t.Status == "FIRING" {
				t.Status, t.Error = "UNKNOWN", "gateway restarted while the test build was being sent -- check Portfolio; NOT re-sent"
			}
		}
		if b := e.build; b != nil && b.Status == "FIRING" {
			// Restarted while the real build was in flight: never re-send it.
			b.Status = "UNKNOWN"
			b.Error = "gateway restarted while the build was being sent -- check Portfolio / the broker; it is NOT re-sent"
			e.saveDayLocked(day)
			log.Printf("[LUT-BUILD] ⚠ today's REAL build was in flight at restart -- marked UNKNOWN, not re-sent (check Portfolio)")
		}
		e.u0916, e.ogSource = st.U0916, st.OGSource
		if n := len(mins); n > 0 {
			e.lastMinute = lutHHMM(mins[n-1].Time)
			for i := n - 1; i >= 0; i-- {
				if mins[i].BuildIV > 0 {
					e.lastIV = mins[i].BuildIV
					break
				}
			}
		}
		log.Printf("[LUT] %s: reloaded %d recorded minute(s) (last %02d:%02d), 09:16 future %.2f, paper entry %v from %s",
			day, len(mins), e.lastMinute/100, e.lastMinute%100, e.u0916, e.entry != nil, lutDataDir())
	}
	e.tablesTried = time.Now()
	set, err := LoadLUTSet(lutEnv("LUT_DIR", defaultLUTDir))
	if err != nil {
		e.tablesLoaded, e.tablesErr = false, err.Error()
		log.Printf("[LUT] tables not loaded: %v", err)
		return
	}
	e.set, e.tablesLoaded, e.tablesErr = set, true, ""
	log.Printf("[LUT] %s: tables loaded, YES cells %d/%d/%d/%d/%d", day, set[0].Yes, set[1].Yes, set[2].Yes, set[3].Yes, set[4].Yes)
}

func (s *Service) lutTick(withGrid bool) {
	if s.Snapshot == nil {
		return
	}
	now := time.Now().In(lutIST())
	day := now.Format("2006-01-02")
	hhmm := now.Hour()*100 + now.Minute()

	e := lutEng
	e.mu.Lock()
	if e.day != day {
		e.newDay(day)
	}
	if !e.tablesLoaded && time.Since(e.tablesTried) > lutInputsRetry {
		e.newDay(day) // retry tables
	}
	if !e.inputsOK && time.Since(e.inputsTried) > lutInputsRetry {
		e.inputsTried = time.Now()
		in, err := LoadLUTDailyInputs(lutEnv("LUT_DAILY_CSV", defaultLUTDailyCSV), day)
		if err != nil {
			e.inputsErr = err.Error()
		} else {
			e.inputs, e.inputsOK, e.inputsErr = in, true, ""
			log.Printf("[LUT] %s inputs: prev straddle %.2f prev future %.2f yday IV %.4f IDV %.4f/%.4f weighted %.4f adj chg %.5f",
				day, in.PrevStraddle, in.PrevFuture, in.YesterdayIV, in.IDVPure, in.IDVWithPrev, in.WeightedIDV, in.NormalizedAdjChg)
		}
	}
	cfg := e.cfg
	e.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	chain, err := s.Snapshot.GetOptionChain(ctx, lutSymbol, cfg.Expiry)
	cancel()
	if err == nil && chain == nil {
		err = fmt.Errorf("empty chain")
	}
	if err != nil {
		chain = nil
	}

	e.mu.Lock()
	live := s.lutComputeLocked(now, day, hhmm, chain, err, cfg, withGrid)
	e.live = live
	e.mu.Unlock()

	// Always pushed -- also before the open / with errors -- so the tab
	// shows what is missing and evaluates manual what-ifs at any time.
	if p, ok := s.Snapshot.(interface {
		PushEvent(ctx context.Context, eventType string, data interface{}) error
	}); ok {
		pctx, pcancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		_ = p.PushEvent(pctx, "lut_update", live)
		pcancel()
	}
}

// lutComputeLocked builds one tick's payload. The LIVE evaluation (chain
// only) records minutes and paper entries; a manual what-if is evaluated
// separately and only shown. Caller holds e.mu.
func (s *Service) lutComputeLocked(now time.Time, day string, hhmm int, chain *OptionChainSnapshot, chainErr error, cfg LUTConfig, withGrid bool) LUTLive {
	e := lutEng
	man := cfg.Manual
	whatIf := man.active()
	live := LUTLive{
		Now: now.Format("15:04:05.0"), Day: day, Config: cfg,
		Inputs: e.inputs, InputsError: e.inputsErr, TablesError: e.tablesErr,
		MinutesCount: len(e.minutes), Entry: e.entry, Manual: whatIf, ManualFields: man.fields(),
	}
	if e.tablesLoaded {
		for i, t := range e.set {
			live.TableYes[i] = t.Yes
		}
	}
	switch {
	case hhmm < 916:
		live.Phase = "before 09:16 -- preview with the 09:16 table, nothing recorded yet"
	case hhmm > lutLastScan:
		live.Phase = "after 13:30 -- no entries (scan window over); every minute still recorded to 15:40"
	default:
		live.Phase = "scanning -- each minute's first tick is recorded; showing the NEXT check's table"
	}
	if whatIf {
		live.Phase += " · MANUAL WHAT-IF shown (live minutes still recorded from the feed)"
	}
	if chainErr != nil {
		live.ChainError = chainErr.Error()
		if hhmm < 915 || hhmm > 1540 {
			live.ChainError += " -- market closed: no live option prices; enter manual values to check"
		}
	}

	if chain != nil {
		e.noteLTPs(now, chain)
		if e.expiry != chain.Expiry {
			e.expiry = chain.Expiry
		}
		// Lot size: the chain usually doesn't carry it -- ask the contract
		// master (same source as builds), once per expiry.
		if chain.LotSize > 0 {
			e.lotSize = chain.LotSize
		} else if (e.lotSize <= 0 || e.lotSizeExpiry != chain.Expiry) && s.LotSize != nil && time.Since(e.lotSizeTried) > 30*time.Second {
			e.lotSizeTried = time.Now()
			lctx, lcancel := context.WithTimeout(context.Background(), 2*time.Second)
			if ls, lerr := s.LotSize.GetLotSize(lctx, lutSymbol, chain.Expiry); lerr == nil && ls > 0 {
				e.lotSize, e.lotSizeExpiry = ls, chain.Expiry
			} else {
				log.Printf("[LUT] lot size for %s %s not available yet: %v", lutSymbol, chain.Expiry, lerr)
			}
			lcancel()
		}
	}
	// The minute's record uses candle closes (last trade before hh:mm:00 by
	// exchange trade time), built once the ATM closes are final or the
	// grace period is over -- see lut_close.go.
	// High / low of the ATM straddle inside each minute (every 100 ms tick).
	if chain != nil {
		e.trackStraddle(now, chain)
	}
	// The minute's closes: GreekSoft's candle closes for the ATM CE / PE
	// (gs_close.go) once they are in (~0.3-0.5 s), else after
	// gsCloseDeadline our feed closes. Fetched in the background so this
	// 100 ms tick never blocks.
	var closed *OptionChainSnapshot
	closeSrc, feedPx := "feed", map[int64]float64{}
	if chain != nil && (hhmm != e.lastChain || hhmm != e.lastMinute || e.u0916 <= 0) {
		b := now.Truncate(time.Minute)
		if cc, ready := lutCloseChain(chain, b, now); ready {
			toks := atmTokens(cc)
			if gsClosesOn() && e.gsKick != b.Unix() {
				e.gsKick = b.Unix()
				for t := range toks {
					go s.gsCloseAt(t, b)
				}
			}
			switch {
			case !gsClosesOn():
				closed = cc // GreekSoft closes switched off: feed closes, no wait
			case gsHaveAll(toks, b):
				n, fp := s.gsApplyCloses(cc, b, toks)
				if n > 0 {
					closeSrc, feedPx = "GreekSoft", fp
				}
				closed = cc
			case now.Sub(b) >= gsCloseDeadline:
				closed = cc // GreekSoft not in time: feed closes
			}
		}
	}
	if closed != nil {
		// Every strike each minute, 09:15-15:40 (paper sim data), and the
		// next weekly expiry's chain (Paper Sim "Next" view).
		if hhmm >= 915 && hhmm <= lutRecordUntil && hhmm != e.lastChain {
			e.lastChain = hhmm
			mc := minuteCloseOf(closed, chain, now.Truncate(time.Minute))
			mc.Source = closeSrc
			for _, r := range closed.Chain {
				if r.Strike == mc.ATM {
					mc.FeedCE, mc.FeedPE = feedPx[r.CEToken], feedPx[r.PEToken]
				}
			}
			if hl := e.strPrev; e.strPrevMin == lutPrevMinute(hhmm) && hl.hi > 0 {
				mc.StrLow, mc.StrHigh, mc.StrLowAt, mc.StrHighAt = hl.lo, hl.hi, hl.loAt, hl.hiAt
			}
			mc.DayLow, mc.DayHigh, mc.DayLowAt, mc.DayHighAt = e.strDay.lo, e.strDay.hi, e.strDay.loAt, e.strDay.hiAt
			lutSaveChainMinuteWith(day, "", hhmm, closed, func(m *lutChainMinute) {
				m.CloseSrc, m.StrLow, m.StrHigh = closeSrc, mc.StrLow, mc.StrHigh
			})
			e.lastClose = &mc
			log.Printf("[MINUTE-CLOSE] %s", mc)
			if nx := lutNextExpiry(chain); nx != "" && hhmm != e.lastNext {
				e.lastNext = hhmm
				go func(nx string, hhmm int, b time.Time) {
					nctx, ncancel := context.WithTimeout(context.Background(), 2*time.Second)
					defer ncancel()
					if nc, nerr := s.Snapshot.GetOptionChain(nctx, lutSymbol, nx); nerr == nil && nc != nil {
						ncc, _ := lutCloseChain(nc, b, time.Now())
						lutSaveChainMinuteTo(day, "next", hhmm, ncc)
					}
				}(nx, hhmm, now.Truncate(time.Minute))
			}
		}
		// Capture the 09:16 future from the live feed only.
		if S := lutUnderlying(closed); e.u0916 <= 0 && hhmm >= 916 && S > 0 {
			e.u0916 = S
			e.ogSource = fmt.Sprintf("captured live at %s", now.Format("15:04:05"))
			if hhmm != 916 {
				e.ogSource += " (gateway was not running at 09:16)"
			}
			e.saveDayLocked(day)
		}
	}
	live.LastClose = e.lastClose
	live.Expiry, live.LotSize = e.expiry, e.lotSize
	if live.Expiry == "" {
		live.Expiry = cfg.Expiry
	}

	// Live evaluation (records) and, when set, the what-if (shown).
	var lc lutCtx
	if chain != nil {
		lc = e.evaluate(now, hhmm, chain, cfg, LUTManual{})
	}
	// The display looks AHEAD: a minute's decision is recorded at its first
	// tick, so during 09:15 show the 09:16 table, during 09:17 the 09:18
	// table, and so on -- whether the NEXT check would say YES.
	next := lutNextCheck(hhmm)
	dispMan := LUTManual{}
	if whatIf {
		dispMan = man
	}
	// Before the open (or with no live chain): the estimate from the last
	// trading day's close stands in for the live prices -- display only.
	dispChain := chain
	if hhmm < 915 || chain == nil {
		exp := cfg.Expiry
		if exp == "" {
			exp = e.expiry
		}
		if pc, note := e.lutPreopenLocked(now, exp); pc != nil {
			dispChain, live.Preopen = pc, note
			if live.Expiry == "" {
				live.Expiry = pc.Expiry
			}
		} else {
			live.Preopen = note
		}
	}
	shown := e.evaluate(now, next, dispChain, cfg, dispMan)
	if shown.Ev != nil && shown.EvalHHMM != hhmm {
		shown.Ev.Preview = true
	}
	if shown.EvalHHMM > 0 {
		live.NextCheck = fmt.Sprintf("%02d:%02d", shown.EvalHHMM/100, shown.EvalHHMM%100)
	}
	live.Inputs = shown.In
	if shown.InOK {
		live.InputsError = ""
	} else if shown.InErr != "" {
		live.InputsError = shown.InErr
	}
	live.Underlying0916, live.OGSource, live.NormOG = shown.U0916, shown.OGSource, shown.NormOG
	live.BusDays = shown.BusDays
	live.EvalError = shown.Err
	if shown.Ev != nil {
		live.Eval = shown.Ev
		if withGrid && shown.Ev.CoordText != "" { // SKIP minutes keep their grid (data recorded)
			stage := lutStage(shown.EvalHHMM)
			g := BuildLUTGrid(e.set[stage], lutStageNames[stage], *shown.Ev, shown.In)
			live.Grid = &g
			sr := lutScanSellRange(e.set, shown, shown.EvalHHMM)
			live.SellRange = &sr
			if stage != 4 {
				g2 := BuildLUTGrid(e.set[4], lutStageNames[4], *shown.Ev, shown.In)
				live.Grid0920 = &g2
				sr2 := lutScanSellRange(e.set, shown, 920)
				live.SellRange0920 = &sr2
			}
		}
	}

	ev := lc.Ev
	if ev == nil {
		if e.startNow {
			e.startNow = false
			log.Printf("[LUT] start-now ignored: no live evaluation (%s)", lc.Err)
		}
		return live
	}
	if ev.BuildIV > 0 {
		e.lastIV = ev.BuildIV
	}

	// Official record: each minute inside the window, from the candle
	// closes (live feed only, never the what-if).
	var rc lutCtx
	if closed != nil && !lc.Preview && hhmm != e.lastMinute {
		rc = e.evaluate(now, hhmm, closed, cfg, LUTManual{})
	}
	if rc.Ev != nil && !rc.Preview {
		e.lastMinute = hhmm
		rec := *rc.Ev
		rec.Time = fmt.Sprintf("%02d:%02d", hhmm/100, hhmm%100)
		rec.Preview = false
		rec.NearestYes, rec.NearestYes0920 = nil, nil
		e.minutes = append(e.minutes, rec)
		lutSaveMinute(day, rec)
		lutLogEval(rec)
		// REAL build: the first YES (minute-end record) while armed, on its
		// own -- independent of the paper entry ("Start now" never blocks
		// it); once sent it never fires again that day; a refusal retries
		// at the next YES.
		if rec.Allowed {
			s.lutFireBuildLocked(day, rec, lutMakeEntry(rec, closed, rc.Row, cfg.Lots, e.lotSize))
		}
		if rec.Allowed && e.entry == nil {
			e.entry = lutMakeEntry(rec, closed, rc.Row, cfg.Lots, e.lotSize)
			e.entry.Source = "LUT YES"
			d := e.entry
			log.Printf("[LUT] ✅ FIRST YES -- PAPER ENTRY (NOT executed) %s table %s NIFTY %s %.0f SELL CE %d + SELL PE %d (%d lots x %d) fut %.2f build IV %.4f adj build IV %.4f SL %.0fbps=%.2fpts TP %.2fbps=%.2fpts coord %s",
				d.Time, d.Stage, d.Expiry, d.Strike, d.Qty, d.Qty, d.Lots, d.LotSize, d.Underlying, rec.BuildIV, rec.AdjBuildIV, d.SLBps, d.SLPoints, d.TPBps, d.TPPoints, rec.CoordText)
			e.saveDayLocked(day)
		}
		live.MinutesCount, live.Entry = len(e.minutes), e.entry
	}

	// "Start now": paper entry at this tick from LIVE values, regardless of
	// the LUT answer.
	if e.startNow {
		e.startNow = false
		if ev.Skip != "" || lc.Row == nil {
			log.Printf("[LUT] start-now ignored: %s", ev.Skip)
		} else {
			rec := *ev
			rec.Preview = false
			e.entry = lutMakeEntry(rec, chain, lc.Row, cfg.Lots, e.lotSize)
			e.entry.Source = "started manually"
			e.saveDayLocked(day)
			d := e.entry
			log.Printf("[LUT] ▶ STARTED MANUALLY -- PAPER ENTRY (NOT executed) %s NIFTY %s %.0f SELL CE %d + SELL PE %d fut %.2f build IV %.4f adj build IV %.4f LUT said %v SL %.2fpts TP %.2fpts",
				d.Time, d.Expiry, d.Strike, d.Qty, d.Qty, d.Underlying, rec.BuildIV, rec.AdjBuildIV, rec.Allowed, d.SLPoints, d.TPPoints)
			live.Entry = e.entry
		}
	}
	return live
}

// lutNextCheck is the minute whose decision is still ahead: inside the
// scan window the current minute is already recorded at its first tick,
// so the next one; 09:15 -> 09:16; outside the window, unchanged.
func lutNextCheck(hhmm int) int {
	if hhmm < 916 || hhmm >= lutLastScan {
		return hhmm
	}
	if hhmm%100 == 59 {
		return (hhmm/100 + 1) * 100
	}
	return hhmm + 1
}

func lutMakeEntry(ev LUTEvaluation, chain *OptionChainSnapshot, row *OptionChainRow, lots, lotSize int) *LUTEntry {
	if lots <= 0 {
		lots = 1
	}
	d := &LUTEntry{
		Time: ev.Time, Stage: ev.Stage, Strike: ev.Strike, Expiry: chain.Expiry,
		Lots: lots, LotSize: lotSize, Qty: lots * lotSize, Underlying: ev.Underlying,
		SLBps: lutSLBps, TPBps: ev.TPBps, Evaluation: ev,
	}
	if row != nil {
		d.CEToken, d.PEToken = row.CEToken, row.PEToken
		d.CEBid, d.CEAsk, d.PEBid, d.PEAsk = row.CEBid, row.CEAsk, row.PEBid, row.PEAsk
	}
	d.SLPoints = ev.Underlying * lutSLBps / 10000
	d.TPPoints = ev.Underlying * ev.TPBps / 10000
	d.SLRupees = d.SLPoints * float64(d.Qty)
	d.TPRupees = d.TPPoints * float64(d.Qty)
	return d
}

func lutLogEval(ev LUTEvaluation) {
	if ev.Skip != "" && ev.CoordText == "" {
		if ev.Underlying > 0 {
			log.Printf("[LUT] %s table=%s SKIP %s (fut=%.2f atm=%.0f ce=%.2f pe=%.2f)", ev.Time, ev.Stage, ev.Skip, ev.Underlying, ev.Strike, ev.CELTP, ev.PELTP)
		} else {
			log.Printf("[LUT] %s table=%s SKIP %s", ev.Time, ev.Stage, ev.Skip)
		}
		return
	}
	verdict := "NO"
	if ev.Skip != "" {
		verdict = fmt.Sprintf("SKIP (%s; table says %s)", ev.Skip, ev.TableSays)
	} else if ev.Allowed {
		verdict = "YES"
	}
	log.Printf("[LUT] %s table=%s fut=%.2f atm=%.0f ce=%.2f pe=%.2f otm=%s@%.2f dte=%.3f/%.3f build_iv=%.4f adj_iv=%.4f [%s] iv_ratio=%.4f [%s] straddle=%.2f str_ratio=%.4f [%s] og=%.4f [%s] adj_chg=%.5f [%s] coord=%s -> %s tp=%.2fbps",
		ev.Time, ev.Stage, ev.Underlying, ev.Strike, ev.CELTP, ev.PELTP, ev.OTMLeg, ev.OTMPrice, ev.RawDTE, ev.TradingDTE,
		ev.BuildIV, ev.AdjBuildIV, ev.BldLabel, ev.IVRatio, ev.IVLabel, ev.Straddle, ev.StrRatio, ev.StrLabel,
		ev.NormOG, ev.OGLabel, ev.AdjIVChg, ev.AdjLabel, ev.CoordText, verdict, ev.TPBps)
}

// LUTStateNow returns the full state (live + all recorded minutes).
func (s *Service) LUTStateNow() LUTState {
	e := lutEng
	e.mu.Lock()
	defer e.mu.Unlock()
	return LUTState{LUTLive: e.live, Params: lutParams(), Minutes: append([]LUTEvaluation(nil), e.minutes...)}
}

// SetLUTConfig updates lots / expiry / 09:16 override / manual what-if.
func (s *Service) SetLUTConfig(c LUTConfig) (LUTConfig, error) {
	if c.Lots <= 0 {
		c.Lots = 1
	}
	c.Expiry = strings.TrimSpace(c.Expiry)
	if err := c.Manual.validate(time.Now().In(lutIST())); err != nil {
		return c, err
	}
	e := lutEng
	e.mu.Lock()
	e.cfg = c
	e.mu.Unlock()
	if c.Manual.active() {
		log.Printf("[LUT] config: lots=%d expiry=%q 09:16 override=%.2f MANUAL WHAT-IF %s", c.Lots, c.Expiry, c.Underlying0916, strings.Join(c.Manual.fields(), ", "))
	} else {
		log.Printf("[LUT] config: lots=%d expiry=%q 09:16 override=%.2f (live values)", c.Lots, c.Expiry, c.Underlying0916)
	}
	return c, nil
}

// --- HTTP -----------------------------------------------------------------

func lutJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// LUTStateHandler: GET /api/lut/state
func (h *Handlers) LUTStateHandler(w http.ResponseWriter, r *http.Request) {
	lutJSON(w, http.StatusOK, map[string]interface{}{"success": true, "state": h.Service.LUTStateNow()})
}

// LUTConfigHandler: POST /api/lut/config {lots, expiry?, underlying_0916?}
func (h *Handlers) LUTConfigHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		lutJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"success": false, "error": "method not allowed"})
		return
	}
	var c LUTConfig
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		lutJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	saved, err := h.Service.SetLUTConfig(c)
	if err != nil {
		lutJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	lutJSON(w, http.StatusOK, map[string]interface{}{"success": true, "config": saved})
}

// LUTStartNowHandler: POST /api/lut/start-now -- records a PAPER entry at
// the next tick, whatever the LUT says. Never executes.
func (h *Handlers) LUTStartNowHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		lutJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"success": false, "error": "method not allowed"})
		return
	}
	lutEng.mu.Lock()
	lutEng.startNow = true
	lutEng.mu.Unlock()
	lutJSON(w, http.StatusOK, map[string]interface{}{"success": true, "message": "paper entry will be recorded at the next tick (not executed)"})
}
