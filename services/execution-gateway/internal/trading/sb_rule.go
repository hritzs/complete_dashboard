package trading

// Saved rules for the straddle-target build: a list of rules persisted to
// disk (the tab reloads them after a restart), each editable while its
// build runs. The old single-rule file is migrated as the first rule.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

func sbRulesPath() string {
	if p := strings.TrimSpace(os.Getenv("SBUILD_RULES_PATH")); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".trading-platform", "sbuild_rules.json")
}

// sbLegacyRulePath is the single-rule file used before multiple rules.
func sbLegacyRulePath() string {
	if p := strings.TrimSpace(os.Getenv("SBUILD_RULE_PATH")); p != "" {
		return p
	}
	return filepath.Join(filepath.Dir(sbRulesPath()), "sbuild_rule.json")
}

func defaultSBRule() SBConfig {
	return sbDefaults(SBConfig{SLBps: 14, TPBps: 14, ExitTime: "15:37:00"})
}

var sbRuleSeq uint32

func newSBRuleID() string {
	n := atomic.AddUint32(&sbRuleSeq, 1)
	return "R" + strings.ToUpper(strconv.FormatInt(time.Now().UnixMilli(), 36)) + strconv.FormatUint(uint64(n%36), 36)
}

// LoadSBRules returns the saved rules (migrating the old single rule).
func LoadSBRules() []SBConfig {
	var rules []SBConfig
	if b, err := os.ReadFile(sbRulesPath()); err == nil {
		if err := json.Unmarshal(b, &rules); err != nil {
			log.Printf("[SBUILD-SHADOW] rules file unreadable: %v", err)
			rules = nil
		}
	} else if b, err := os.ReadFile(sbLegacyRulePath()); err == nil {
		var c SBConfig
		if json.Unmarshal(b, &c) == nil && c.TargetStraddle > 0 {
			c.ID = newSBRuleID()
			rules = []SBConfig{c}
			_ = saveSBRules(rules)
			log.Printf("[SBUILD-SHADOW] migrated single rule to %s as %s", sbRulesPath(), c.ID)
		}
	}
	out := rules[:0]
	for _, c := range rules {
		if c.ID == "" {
			c.ID = newSBRuleID()
		}
		out = append(out, sbDefaults(c))
	}
	return out
}

func saveSBRules(rules []SBConfig) error {
	if rules == nil {
		rules = []SBConfig{}
	}
	b, _ := json.MarshalIndent(rules, "", "  ")
	return sbWriteAtomic(sbRulesPath(), b)
}

// sbRuleChanges lists user-facing differences (for the audit trail).
func sbRuleChanges(a, b SBConfig) []string {
	var out []string
	add := func(name, x, y string) {
		if x != y {
			out = append(out, fmt.Sprintf("%s %s -> %s", name, x, y))
		}
	}
	g := func(v float64) string { return fmt.Sprintf("%g", v) }
	add("name", a.Name, b.Name)
	add("symbol", a.Symbol, b.Symbol)
	add("expiry", a.Expiry, b.Expiry)
	add("target straddle", g(a.TargetStraddle), g(b.TargetStraddle))
	add("straddle qty", fmt.Sprint(a.Straddles), fmt.Sprint(b.Straddles))
	add("SL bps", g(a.SLBps), g(b.SLBps))
	add("TP bps", g(a.TPBps), g(b.TPBps))
	add("exit time", a.ExitTime, b.ExitTime)
	add("straddle div", g(a.StraddleDiv), g(b.StraddleDiv))
	add("hedge div", g(a.HedgeDiv), g(b.HedgeDiv))
	add("hedge min bps", g(a.HedgeMinBps), g(b.HedgeMinBps))
	return out
}

// SaveSBRule creates a rule (blank id) or saves an existing one and applies
// it to that rule's running build at once.
func (s *Service) SaveSBRule(c SBConfig) (SBConfig, []string, error) {
	c = sbDefaults(c)
	if c.TargetStraddle <= 0 || c.Straddles <= 0 {
		return c, nil, fmt.Errorf("target straddle and straddle quantity are required")
	}
	if lot, err := s.sbLotSize(c.Symbol, c.Expiry); err == nil {
		if c.Straddles = sbRoundToLot(c.Straddles, lot); c.Straddles < lot {
			return c, nil, fmt.Errorf("straddle quantity must be at least one lot (%d)", lot)
		}
	}
	if c.ExitTime != "" {
		if _, err := ParseClockTodayIST(c.ExitTime, time.Now()); err != nil {
			return c, nil, fmt.Errorf("exit time: %w", err)
		}
	}
	if strings.TrimSpace(c.ID) == "" {
		c.ID = newSBRuleID()
		sbEng.add(newSBRunner(c))
		if err := saveSBRules(sbEng.configs()); err != nil {
			return c, nil, fmt.Errorf("save rules: %w", err)
		}
		return c, []string{"new rule " + c.ID}, nil
	}
	r := sbEng.get(c.ID)
	if r == nil {
		return c, nil, fmt.Errorf("unknown rule %q", c.ID)
	}
	r.mu.Lock()
	live := r.active()
	if live && c.Symbol != r.cfg.Symbol {
		r.mu.Unlock()
		return c, nil, fmt.Errorf("symbol cannot change while this rule's build is running")
	}
	if live && c.Expiry != r.cfg.Expiry {
		r.mu.Unlock()
		return c, nil, fmt.Errorf("expiry cannot change while this rule's build is running (position is in %s)", r.expiry)
	}
	changes := sbRuleChanges(r.cfg, c)
	r.cfg = c
	if live {
		if len(changes) > 0 {
			r.event("RULE", "changed live: %s", strings.Join(changes, "; "))
		}
		if v := r.pms.View(nil); r.phase == "COMPLETE" && !r.noEntries && 2*c.Straddles-(v.BuildCE+v.BuildPE) >= int64(r.lotSize) {
			r.phase = "BUILDING"
			r.event("RULE", "target raised -- building resumed")
		}
		r.persistLocked()
	}
	r.mu.Unlock()
	if err := saveSBRules(sbEng.configs()); err != nil {
		return c, nil, fmt.Errorf("save rules: %w", err)
	}
	return c, changes, nil
}

// DeleteSBRule removes a rule that is not running (and its saved run).
func (s *Service) DeleteSBRule(id string) error {
	r := sbEng.get(id)
	if r == nil {
		return fmt.Errorf("unknown rule %q", id)
	}
	r.mu.Lock()
	live := r.holding()
	r.mu.Unlock()
	if live {
		return fmt.Errorf("the rule's run still holds a build / position -- stop or exit it before deleting")
	}
	sbEng.remove(id)
	_ = os.Remove(sbRunPath(id))
	return saveSBRules(sbEng.configs())
}

// --- HTTP -----------------------------------------------------------------

// SBRuleHandler: GET /api/sbuild/rule -> every saved rule (+ a default for
// a new one); POST {SBConfig} -> create (blank id) or save (+apply live).
func (h *Handlers) SBRuleHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		lutJSON(w, http.StatusOK, map[string]interface{}{"success": true, "rules": sbEng.configs(), "default": defaultSBRule()})
		return
	}
	var c SBConfig
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		lutJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	saved, changes, err := h.Service.SaveSBRule(c)
	if err != nil {
		lutJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	lutJSON(w, http.StatusOK, map[string]interface{}{"success": true, "rule": saved, "changes": changes})
}

// SBRuleDelete: POST /api/sbuild/rule/delete {"id": rule}
func (h *Handlers) SBRuleDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		lutJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"success": false, "error": "method not allowed"})
		return
	}
	if err := h.Service.DeleteSBRule(sbDecodeID(r)); err != nil {
		lutJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	lutJSON(w, http.StatusOK, map[string]interface{}{"success": true})
}

// SBExpiries: GET /api/sbuild/expiries?symbol= -> available expiries + lot size.
func (h *Handlers) SBExpiries(w http.ResponseWriter, r *http.Request) {
	if h.Service.Snapshot == nil {
		lutJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"success": false, "error": "snapshot unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	symbol := NormalizeSymbol(r.URL.Query().Get("symbol"))
	if symbol == "" {
		symbol = "NIFTY"
	}
	chain, err := h.Service.Snapshot.GetOptionChain(ctx, symbol, "")
	if err != nil {
		lutJSON(w, http.StatusBadGateway, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	lot, _ := h.Service.sbLotSize(symbol, r.URL.Query().Get("expiry"))
	lutJSON(w, http.StatusOK, map[string]interface{}{"success": true, "symbol": symbol, "nearest": chain.Expiry, "expiries": chain.AvailableExpiries, "lot_size": lot})
}

// SBQuote: GET /api/sbuild/quote?symbol=&expiry= -> the live ATM straddle
// of any symbol / expiry (for the rule editor, before a rule exists).
func (h *Handlers) SBQuote(w http.ResponseWriter, r *http.Request) {
	if h.Service.Snapshot == nil {
		lutJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"success": false, "error": "snapshot unavailable"})
		return
	}
	symbol := NormalizeSymbol(r.URL.Query().Get("symbol"))
	if symbol == "" {
		symbol = "NIFTY"
	}
	expiry := strings.TrimSpace(r.URL.Query().Get("expiry"))
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	chain, err := h.Service.Snapshot.GetOptionChain(ctx, symbol, expiry)
	if err != nil || chain == nil {
		msg := "no chain"
		if err != nil {
			msg = err.Error()
		}
		lutJSON(w, http.StatusBadGateway, map[string]interface{}{"success": false, "error": msg})
		return
	}
	lot, _ := h.Service.sbLotSize(symbol, chain.Expiry)
	lv := sbComputeLive(SBConfig{Symbol: symbol}, chain, lot, 0, 0, 0.5)
	lutJSON(w, http.StatusOK, map[string]interface{}{"success": true, "quote": lv, "expiry": chain.Expiry, "lot_size": lot,
		"time": time.Now().In(lutIST()).Format("15:04:05")})
}
