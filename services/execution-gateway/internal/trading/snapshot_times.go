package trading

import "time"

// snapshotTradeTime is an LTP's exchange trade time (unix seconds) as IST
// HH:MM:SS for the per-tick snapshot line; "-" when the feed sent none.
func snapshotTradeTime(ltt int64) string {
	if ltt <= 0 {
		return "-"
	}
	return time.Unix(ltt, 0).In(istLoc()).Format("15:04:05")
}

// snapshotSynLTP is the plain synthetic future strike + CE LTP - PE LTP of
// the trade's own rows (what the tick data's UnderlyingLTP is); 0 when the
// legs are on different strikes or a price is missing.
func snapshotSynLTP(ce, pe *OptionChainRow) float64 {
	if ce == nil || pe == nil || ce.Strike != pe.Strike || ce.CELtp <= 0 || pe.PELtp <= 0 {
		return 0
	}
	return ce.Strike + ce.CELtp - pe.PELtp
}
