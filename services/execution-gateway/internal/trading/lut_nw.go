package trading

// Next-week (NW) check for the LUT tab -- DISPLAY ONLY, never an order.
//
// GammaShort_Backtest_Final.py's next-week leg (nw_decision): on current-week
// DTE1 / DTE2 days, when the current week has not traded by 09:20, one check
// at 09:21 on the NEXT expiry against NW_LUT.csv (new tables folder):
//
//   features at the 09:16 snapshot (today's recorded next-expiry minute 09:16)
//     ATM       strike nearest the next-week future with both legs priced
//     IV        the OTM leg's implied vol at the next expiry's time to expiry
//     build IV  IV x sqrt(nw_dte / trading days)  (weekend-adjusted)
//     ratio     build IV / today's weighted IDV
//     gap       opening gap % x 0.19 / next-week yesterday Adj IV (NW_Adj_IV)
//   bands     ratio [1.00, 1.20, 1.35] · build IV [0.12, 0.14, 0.17] ·
//             gap [-0.75, 0.7501]
//   verdict   the NW_LUT.csv row for (current-week DTE, ratio, IV, gap).

import (
	"encoding/csv"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	nwRatEdges = []float64{1.00, 1.20, 1.35}
	nwLvlEdges = []float64{0.12, 0.14, 0.17}
	nwOGEdges  = []float64{-0.75, 0.7501}
	nwRatLbl   = []string{"<1.00", "1.00-1.20", "1.20-1.35", ">1.35"}
	nwLvlLbl   = []string{"<0.12", "0.12-0.14", "0.14-0.17", ">0.17"}
	nwOGLbl    = []string{"<-0.75", "-0.75 to 0.75", ">0.75"}
)

// nwBand: np.searchsorted(edges, x) -- the count of edges strictly below x.
func nwBand(edges []float64, x float64) int {
	return sort.Search(len(edges), func(i int) bool { return edges[i] >= x })
}

// LUTNWCheck is the next-week check as the tab shows it.
type LUTNWCheck struct {
	Applies  bool   `json:"applies"` // a DTE1 / DTE2 day with data
	Note     string `json:"note"`
	CWTraded string `json:"cw_traded,omitempty"` // first current-week YES by 09:20
	CurDTE   int    `json:"cur_dte"`
	Expiry   string `json:"expiry,omitempty"`
	Snapshot string `json:"snapshot,omitempty"`

	Future   float64 `json:"future"`
	ATM      float64 `json:"atm"`
	Straddle float64 `json:"straddle"`
	OTMLeg   string  `json:"otm_leg"`
	OTMPrice float64 `json:"otm_price"`
	IV       float64 `json:"iv"`
	NWDTE    float64 `json:"nw_dte"`
	Trading  float64 `json:"trading_days"`
	AdjF     float64 `json:"adj_factor"`
	BuildIV  float64 `json:"build_iv"`
	IDV      float64 `json:"idv"`
	Ratio    float64 `json:"ratio"`
	OGPct    float64 `json:"og_pct"`
	NWAdjIV  float64 `json:"nw_adj_iv"`
	NormOG   float64 `json:"norm_og"`

	RatioBand string `json:"ratio_band"`
	IVBand    string `json:"iv_band"`
	OGBand    string `json:"og_band"`
	Verdict   string `json:"verdict"` // YES / NO / "" (not computable)

	// Grid: NW_LUT for today's current-week DTE and gap band -- rows ratio
	// bands, columns build-IV bands; Row / Col mark today's cell.
	Rows  []string   `json:"rows"`
	Cols  []string   `json:"cols"`
	Cells [][]string `json:"cells"`
	Row   int        `json:"row"`
	Col   int        `json:"col"`
}

var nwLUT struct {
	sync.Mutex
	path string
	at   time.Time
	m    map[string]string // "dte|ratio|iv|og" -> YES/NO
	err  error
}

// lutLoadNWLUT reads NW_LUT.csv (re-read every 10 minutes).
func lutLoadNWLUT() (map[string]string, error) {
	path := filepath.Join(lutTablesDir(lutSchemaNew), "NW_LUT.csv")
	nwLUT.Lock()
	defer nwLUT.Unlock()
	if nwLUT.path == path && time.Since(nwLUT.at) < 10*time.Minute && (nwLUT.m != nil || nwLUT.err != nil) {
		return nwLUT.m, nwLUT.err
	}
	nwLUT.path, nwLUT.at, nwLUT.m, nwLUT.err = path, time.Now(), nil, nil
	f, err := os.Open(path)
	if err != nil {
		nwLUT.err = err
		return nil, err
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil || len(rows) < 2 {
		nwLUT.err = fmt.Errorf("NW_LUT.csv unreadable: %v", err)
		return nil, nwLUT.err
	}
	m := map[string]string{}
	for _, r := range rows[1:] {
		if len(r) < 5 {
			continue
		}
		m[strings.Join([]string{strings.TrimSpace(r[0]), strings.TrimSpace(r[1]), strings.TrimSpace(r[2]), strings.TrimSpace(r[3])}, "|")] = strings.ToUpper(strings.TrimSpace(r[4]))
	}
	nwLUT.m = m
	return m, nil
}

// lutNWCheck builds the check from today's recorded 09:16 next-expiry
// minute (the latest one before 09:16 is recorded). Display only.
func (e *lutEngine) lutNWCheck(now time.Time, day string, in LUTDailyInputs, inOK bool, curBusDays int, u0916 float64) *LUTNWCheck {
	c := &LUTNWCheck{CurDTE: curBusDays, Row: -1, Col: -1, Rows: nwRatLbl, Cols: nwLvlLbl}
	if curBusDays != 1 && curBusDays != 2 {
		c.Note = fmt.Sprintf("current week is %d business day(s) to expiry -- the next-week check is for DTE 1 / 2 days only", curBusDays)
		return c
	}
	for _, m := range e.minutes {
		if m.Allowed && m.Time <= "09:20" {
			c.CWTraded = m.Time
			break
		}
	}
	lut, err := lutLoadNWLUT()
	if err != nil {
		c.Note = "NW_LUT.csv: " + err.Error()
		return c
	}
	mins := lutLoadChainSet(day, "next")
	m, ok := mins["09:16"]
	if !ok {
		c.Note = "no next-expiry minute recorded at 09:16 yet"
		return c
	}
	exp, err := lutParseExpiry(m.Expiry)
	if err != nil || m.Future <= 0 {
		c.Note = "next-expiry 09:16 minute unusable"
		return c
	}
	c.Applies, c.Expiry, c.Snapshot = true, m.Expiry, "09:16 (recorded next-expiry minute)"
	S := m.Future
	c.Future = S
	best := math.Inf(1)
	var row lutChainRow
	for _, r := range m.Rows {
		if r.CE > 0 && r.PE > 0 && math.Abs(r.K-S) < best {
			best, row = math.Abs(r.K-S), r
		}
	}
	if math.IsInf(best, 1) {
		c.Note = "no strike with both legs priced at 09:16"
		return c
	}
	c.ATM, c.Straddle = row.K, row.CE+row.PE
	at := time.Date(now.Year(), now.Month(), now.Day(), 9, 16, 0, 0, now.Location())
	c.NWDTE = lutRawDTE(at, exp)
	isCall := row.K > S // the OTM leg
	c.OTMLeg, c.OTMPrice = "PE", row.PE
	if isCall {
		c.OTMLeg, c.OTMPrice = "CE", row.CE
	}
	iv, ok := lutImpliedVol(isCall, c.OTMPrice, S, row.K, math.Max(c.NWDTE/365.0, 1e-5))
	if !ok || iv <= 0 {
		c.Note = "next-week OTM leg IV not solvable"
		return c
	}
	c.IV = iv
	cal := int(math.Ceil(c.NWDTE))
	wknd := cal - lutBusDays(now, cal)
	c.Trading = math.Round(c.NWDTE - float64(wknd))
	if c.Trading <= 0 {
		c.Note = "next-week trading days <= 0"
		return c
	}
	c.AdjF = math.Sqrt(c.NWDTE / c.Trading)
	c.BuildIV = iv * c.AdjF
	if !inOK || in.WeightedIDV <= 0 {
		c.Note = "daily inputs not loaded"
		return c
	}
	c.IDV = in.WeightedIDV
	c.Ratio = c.BuildIV / c.IDV
	c.NWAdjIV = in.NWAdjIV
	if u0916 > 0 && in.PrevFuture > 0 {
		c.OGPct = (u0916 - in.PrevFuture) / in.PrevFuture * 100
	}
	if c.NWAdjIV <= 0 || u0916 <= 0 {
		c.Note = "no gap feature (NW_Adj_IV or the 09:16 future missing)"
		return c
	}
	c.NormOG = c.OGPct * (0.19 / c.NWAdjIV)
	ri, li, oi := nwBand(nwRatEdges, c.Ratio), nwBand(nwLvlEdges, c.BuildIV), nwBand(nwOGEdges, c.NormOG)
	c.RatioBand, c.IVBand, c.OGBand = nwRatLbl[ri], nwLvlLbl[li], nwOGLbl[oi]
	c.Row, c.Col = ri, li
	dte := strconv.Itoa(curBusDays)
	c.Cells = make([][]string, len(nwRatLbl))
	for i := range nwRatLbl {
		c.Cells[i] = make([]string, len(nwLvlLbl))
		for j := range nwLvlLbl {
			c.Cells[i][j] = lut[strings.Join([]string{dte, nwRatLbl[i], nwLvlLbl[j], c.OGBand}, "|")]
		}
	}
	c.Verdict = c.Cells[ri][li]
	switch {
	case c.CWTraded != "":
		c.Note = fmt.Sprintf("current week said YES at %s, so the backtest would NOT run this check today (it runs at 09:21 only when the current week has not traded by 09:20)", c.CWTraded)
	default:
		c.Note = "current week had no YES by 09:20: the backtest checks next week at 09:21 (display only here -- nothing is sold)"
	}
	return c
}

var nwCache struct {
	sync.Mutex
	key string
	c   *LUTNWCheck
}

// lutNWCheckCached: lutNWCheck at most once a minute (the tab updates every
// 100 ms; the check reads files).
func (e *lutEngine) lutNWCheckCached(now time.Time, day string, in LUTDailyInputs, inOK bool, curBusDays int, u0916 float64) *LUTNWCheck {
	key := fmt.Sprintf("%s|%s|%d|%.2f|%t|%d", day, now.Format("15:04"), curBusDays, u0916, inOK, len(e.minutes))
	nwCache.Lock()
	defer nwCache.Unlock()
	if nwCache.key != key {
		nwCache.key, nwCache.c = key, e.lutNWCheck(now, day, in, inOK, curBusDays, u0916)
	}
	return nwCache.c
}
