// Package bcv fetches rates from the Banco Central de Venezuela, which
// publishes the "Tipo de Cambio de Referencia" of the bolívar (VES) daily for
// around 20 currencies.
//
// The rates come as one legacy .xls workbook per calendar quarter, linked from
// a paginated statistics page. Each workbook holds one sheet per operation day,
// newest first, named DDMMYYYY after the operation date. A sheet carries a
// "Fecha Operacion" and a "Fecha Valor" banner, a two-line header, then one row
// per currency: ISO code, country, foreign-per-USD bid/ask, and Bs./M.E.
// bid/ask. The Bs./M.E. ask is the figure BCV's homepage and press releases
// show as the reference rate; the bid is a fixed 0.25% below it. We relay the
// ask.
//
// Workbook filenames follow 2_1_2{a-d}{YY}_smc.xls, a=Q1 through d=Q4, but the
// pattern can't be trusted on its own: a re-uploaded quarter gets a Drupal
// suffix (2_1_2c23_smc_60.xls) while the unsuffixed name keeps serving a stale
// two-sheet stub. We scrape the statistics page for the current link per
// quarter, walking its pages newest-first until every quarter in the requested
// window has one.
//
// Rows are dated by Fecha Valor, the date the rate applies to, which is the
// next business day after the operation date: Friday's sheet prices Monday.
// Workbooks group sheets by operation date, so a value date's sheet can sit in
// the previous quarter's file; the fetch reaches back a couple of weeks to
// catch that at quarter boundaries.
//
// USD is the market-derived rate; every other Bs./M.E. figure is that rate
// crossed through the foreign-per-USD column. The list includes CUC (defunct,
// kept at USD parity) and Mexico's peso under the pre-1993 code MXP, relabelled
// MXN.
//
// BCV redenominated the bolívar 1,000,000:1 on 2021-10-01 and every sheet,
// before and after, prices "VES". The series starts at the first
// post-redenomination value date, 2021-10-04, so the two scales never share a
// code.
//
// Unlike most adapters, Fetch keeps rows dated on after itself: the window is
// after through upto, both inclusive, as in the Ruby adapter.
//
// www.bcv.org.ve sends a stale Sectigo intermediate that did not issue its
// leaf; adapter.NewClient bundles the right one.
package bcv

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/xls"
)

const dataURL = "https://www.bcv.org.ve/estadisticas/tipo-cambio-de-referencia-smc"

// lookback is how far before the requested window to start fetching, in days,
// so the sheet that prices the window's first value date is found even when it
// sits in the previous quarter's workbook.
const lookback = 14

// floor is the first value date priced in bolívar digital, after the 2021-10-01
// redenomination.
var floor = adapter.Date(2021, 10, 4)

var (
	workbookLink = regexp.MustCompile(`(?i)href=["']([^"']*2_1_2([a-d])(\d{2})_smc[^"']*\.xls)["']`)
	fechaValor   = regexp.MustCompile(`Fecha Valor:\s*(\d{2})/(\d{2})/(\d{4})`)
	isoCode      = regexp.MustCompile(`\A[A-Z]{3}\z`)
)

var aliases = map[string]string{"MXP": "MXN"}

func init() {
	adapter.Register("BCV", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches BCV rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

type quarter struct{ year, q int }

func (q quarter) String() string { return fmt.Sprintf("%dQ%d", q.year, q.q) }

func quarterOf(date time.Time) quarter {
	return quarter{date.Year(), (int(date.Month())-1)/3 + 1}
}

// quarters lists every quarter from the one holding start through the one
// holding end.
func quarters(start, end time.Time) []quarter {
	first := start.Year()*4 + quarterOf(start).q - 1
	last := end.Year()*4 + quarterOf(end).q - 1
	var qs []quarter
	for i := first; i <= last; i++ {
		qs = append(qs, quarter{i / 4, i%4 + 1})
	}
	return qs
}

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	start := floor
	if after.After(start) {
		start = after
	}
	end := upto
	if end.IsZero() {
		end = a.Today()
	}
	if start.After(end) {
		return nil, nil
	}

	wanted := quarters(start.AddDate(0, 0, -lookback), end)
	urls, err := a.workbookURLs(ctx, wanted)
	if err != nil {
		return nil, err
	}

	var rates []adapter.Rate
	for _, q := range wanted {
		u, ok := urls[q]
		if !ok {
			// The current quarter's workbook appears on its first business day,
			// so a missing link there is a not-yet.
			if q == quarterOf(a.Today()) {
				continue
			}
			return nil, fmt.Errorf("no workbook for %s on %s", q, dataURL)
		}
		if err := a.Sleep(ctx, 500*time.Millisecond); err != nil {
			return nil, err
		}
		body, err := a.Get(ctx, u, nil)
		if err != nil {
			return nil, err
		}
		parsed, err := parse(body)
		if err != nil {
			return nil, err
		}
		rates = append(rates, parsed...)
	}

	var kept []adapter.Rate
	for _, r := range rates {
		if !r.Date.Before(start) && !r.Date.After(end) {
			kept = append(kept, r)
		}
	}
	return kept, nil
}

// workbookURLs walks the statistics page, newest quarters first, until every
// wanted quarter has a link or a page adds none. An out-of-range page comes
// back empty today; a pager that repeated its last page instead would add
// nothing new, so either shape ends the walk.
func (a *Adapter) workbookURLs(ctx context.Context, wanted []quarter) (map[quarter]string, error) {
	urls := map[quarter]string{}
	for page := 0; ; page++ {
		html, err := a.Get(ctx, dataURL, url.Values{"page": {strconv.Itoa(page)}})
		if err != nil {
			return nil, err
		}
		added := false
		for q, u := range workbookLinks(string(html)) {
			if _, ok := urls[q]; !ok {
				urls[q] = u
				added = true
			}
		}
		if !added || hasAll(urls, wanted) {
			return urls, nil
		}
	}
}

func hasAll(urls map[quarter]string, wanted []quarter) bool {
	for _, q := range wanted {
		if _, ok := urls[q]; !ok {
			return false
		}
	}
	return true
}

// workbookLinks returns the workbook links on one statistics page, keyed by
// quarter. The first link seen for a quarter wins.
func workbookLinks(html string) map[quarter]string {
	base, _ := url.Parse(dataURL)
	links := map[quarter]string{}
	for _, m := range workbookLink.FindAllStringSubmatch(html, -1) {
		yy, _ := strconv.Atoi(m[3])
		q := quarter{2000 + yy, int(strings.ToLower(m[2])[0]-'a') + 1}
		if _, ok := links[q]; ok {
			continue
		}
		ref, err := url.Parse(m[1])
		if err != nil {
			continue
		}
		links[q] = base.ResolveReference(ref).String()
	}
	return links
}

func parse(data []byte) ([]adapter.Rate, error) {
	sheets, err := xls.Open(data)
	if err != nil {
		return nil, err
	}
	var rates []adapter.Rate
	for _, sheet := range sheets {
		r, err := parseSheet(sheet)
		if err != nil {
			return nil, err
		}
		rates = append(rates, r...)
	}
	return rates, nil
}

func parseSheet(sheet xls.Sheet) ([]adapter.Rate, error) {
	date, ok := valueDate(sheet.Rows)
	if !ok {
		return nil, fmt.Errorf("no Fecha Valor in sheet %s", sheet.Name)
	}
	ask, ok := askColumn(sheet.Rows)
	if !ok {
		return nil, fmt.Errorf("no Venta (ASK) column in sheet %s", sheet.Name)
	}

	var rates []adapter.Rate
	for _, row := range sheet.Rows {
		code := row.At(1)
		if code.Kind != xls.String || !isoCode.MatchString(code.String) {
			continue
		}
		rate := row.At(ask)
		if rate.Kind != xls.Number || rate.Number <= 0 {
			continue
		}
		base := code.String
		if alias, ok := aliases[base]; ok {
			base = alias
		}
		rates = append(rates, adapter.Rate{Date: date, Base: base, Quote: "VES", Rate: rate.Number})
	}
	return rates, nil
}

func valueDate(rows []xls.Row) (time.Time, bool) {
	for _, row := range rows {
		for _, cell := range row {
			m := fechaValor.FindStringSubmatch(cell.Text())
			if m == nil {
				continue
			}
			d, _ := strconv.Atoi(m[1])
			mo, _ := strconv.Atoi(m[2])
			y, _ := strconv.Atoi(m[3])
			return adapter.Date(y, time.Month(mo), d), true
		}
	}
	return time.Time{}, false
}

// askColumn finds the header that repeats "Venta (ASK)" for both column pairs;
// the Bs./M.E. one is the rightmost.
func askColumn(rows []xls.Row) (int, bool) {
	for _, row := range rows {
		col := -1
		for i, cell := range row {
			if strings.TrimSpace(cell.Text()) == "Venta (ASK)" {
				col = i
			}
		}
		if col >= 0 {
			return col, true
		}
	}
	return 0, false
}
