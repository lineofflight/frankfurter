// Package nrbt fetches rates from the National Reserve Bank of Tonga, which publishes "Authorized Persons' Average
// Daily Exchange Rates" as a single rolling XLSX covering 2017-01 to present in biennial sheets.
//
// Each sheet has the same layout: an Excel serial date in column A, then three blocks of twelve quote currencies: BUY
// (B-M), MID (O-Z) and SELL (AB-AM), with N and AA as spacers. Rates read "1 TOP = X foreign", so TOP is the base. We
// emit the published MID rather than reconstructing it from buy and sell.
//
// Holidays and closures are flagged inline as shared strings in the rate cells (e.g. "Public Holiday: ANZAC Day");
// those rows have no numeric values and are skipped.
//
// Unlike adapter.Window, the date filter treats after as inclusive, as the Ruby adapter does.
package nrbt

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const dataURL = "https://www.reservebank.to/data/docs/fmarkets/exrates/average_daily_exchange_rates.xlsx"

// quoteCurrencies are in column order; the sequence repeats across the BUY, MID and SELL blocks.
var quoteCurrencies = [12]string{"AUD", "EUR", "FJD", "GBP", "JPY", "NZD", "USD", "WST", "CHF", "CAD", "SEK", "SGD"}

// Zero-based column indexes of the MID block (O through Z).
const (
	midFirst = 14
	midLast  = 25
)

var excelEpoch = adapter.Date(1899, 12, 30)

func init() {
	adapter.Register("NRBT", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches NRBT rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter. A single XLSX holds the full history, so one window covers it.
func (a *Adapter) BackfillRange() int { return 36525 }

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	body, err := a.Get(ctx, dataURL, nil)
	if err != nil {
		return nil, err
	}
	return parse(body, after, upto)
}

type workbook struct {
	Sheets []struct {
		// Matches r:id (or a bare id), since the tag names no namespace.
		ID string `xml:"id,attr"`
	} `xml:"sheets>sheet"`
}

type relationships struct {
	Rels []struct {
		ID     string `xml:"Id,attr"`
		Target string `xml:"Target,attr"`
	} `xml:"Relationship"`
}

type worksheet struct {
	Rows []struct {
		Cells []struct {
			Ref  string  `xml:"r,attr"`
			Type string  `xml:"t,attr"`
			V    *string `xml:"v"`
		} `xml:"c"`
	} `xml:"sheetData>row"`
}

func parse(data []byte, after, upto time.Time) ([]adapter.Rate, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	var wb workbook
	if err := readXML(zr, "xl/workbook.xml", &wb); err != nil {
		return nil, err
	}
	var rels relationships
	if err := readXML(zr, "xl/_rels/workbook.xml.rels", &rels); err != nil {
		return nil, err
	}
	targets := make(map[string]string, len(rels.Rels))
	for _, r := range rels.Rels {
		targets[r.ID] = r.Target
	}

	var rates []adapter.Rate
	for _, s := range wb.Sheets {
		target, ok := targets[s.ID]
		if !ok {
			continue
		}
		var ws worksheet
		if err := readXML(zr, "xl/"+target, &ws); err != nil {
			return nil, err
		}
		rates = append(rates, parseSheet(&ws, after, upto)...)
	}
	return rates, nil
}

func parseSheet(ws *worksheet, after, upto time.Time) []adapter.Rate {
	var rates []adapter.Rate
	for _, row := range ws.Rows {
		var mids [midLast - midFirst + 1]float64
		var have [len(mids)]bool
		serial, hasSerial := 0, false

		for _, c := range row.Cells {
			// Shared strings are headers in column A and holiday flags in the rate cells.
			if c.Ref == "" || c.V == nil || *c.V == "" || c.Type == "s" {
				continue
			}
			col, ok := columnIndex(c.Ref)
			if !ok {
				continue
			}
			switch {
			case col == 0:
				if n, err := strconv.Atoi(*c.V); err == nil {
					serial, hasSerial = n, true
				} else if f, ok := adapter.ParseFloat(*c.V); ok {
					serial, hasSerial = int(f), true
				}
			case col >= midFirst && col <= midLast:
				if f, ok := adapter.ParseFloat(*c.V); ok {
					mids[col-midFirst], have[col-midFirst] = f, true
				}
			}
		}

		if !hasSerial {
			continue
		}
		date := excelEpoch.AddDate(0, 0, serial)
		if !after.IsZero() && date.Before(after) {
			continue
		}
		if !upto.IsZero() && date.After(upto) {
			continue
		}
		for i, v := range mids {
			if !have[i] || v == 0 {
				continue
			}
			rates = append(rates, adapter.Rate{Date: date, Base: "TOP", Quote: quoteCurrencies[i], Rate: v})
		}
	}
	return rates
}

// columnIndex converts the letters leading a cell reference (A, B, ..., Z, AA, ...) to a zero-based index.
func columnIndex(ref string) (int, bool) {
	n, i := 0, 0
	for ; i < len(ref) && ref[i] >= 'A' && ref[i] <= 'Z'; i++ {
		n = n*26 + int(ref[i]-'A'+1)
	}
	return n - 1, i > 0
}

func readXML(zr *zip.Reader, name string, v any) error {
	f, err := zr.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	if err := xml.Unmarshal(data, v); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}
