package trading

// Event days for the LUT entry, read from the same spreadsheet the backtest
// uses (\\172.16.1.85\Shared\EventDates.xlsx, first column of the first
// sheet, dates as Excel serial numbers -- formula cells carry their cached
// value). On an event day the backtest takes no entry before 09:18 and
// trades one third of the size (GammaShort_Backtest_Final.py).
//
// Standard library only: an .xlsx is a zip of XML parts.

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

const lutEventDatesDefault = "/mnt/shared/EventDates.xlsx"

// lutEventSize is the share of the normal size traded on an event day.
const lutEventSize = 1.0 / 3.0

func lutEventDatesPath() string { return lutEnv("LUT_EVENT_DATES", lutEventDatesDefault) }

// lutLoadEventDays returns the set of event days (YYYY-MM-DD).
func lutLoadEventDays(path string) (map[string]bool, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	var sheet *zip.File
	for _, f := range zr.File {
		if f.Name == "xl/worksheets/sheet1.xml" {
			sheet = f
		}
	}
	if sheet == nil {
		return nil, fmt.Errorf("%s: no first worksheet", path)
	}
	rc, err := sheet.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return lutParseEventSheet(rc)
}

// lutParseEventSheet reads column A of a worksheet XML: numeric cells are
// Excel serial dates (1900 system).
func lutParseEventSheet(r io.Reader) (map[string]bool, error) {
	type cell struct {
		Ref  string `xml:"r,attr"`
		Type string `xml:"t,attr"`
		V    string `xml:"v"`
	}
	type row struct {
		Cells []cell `xml:"c"`
	}
	var ws struct {
		Rows []row `xml:"sheetData>row"`
	}
	if err := xml.NewDecoder(r).Decode(&ws); err != nil {
		return nil, err
	}
	base := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
	out := map[string]bool{}
	for _, rw := range ws.Rows {
		for _, c := range rw.Cells {
			if !strings.HasPrefix(c.Ref, "A") || c.Type == "s" || c.Type == "str" {
				continue
			}
			v, err := strconv.ParseFloat(strings.TrimSpace(c.V), 64)
			if err != nil || v < 20000 || v > 80000 { // 1954 .. 2119
				continue
			}
			out[base.AddDate(0, 0, int(v)).Format("2006-01-02")] = true
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no dates found in column A")
	}
	return out, nil
}
