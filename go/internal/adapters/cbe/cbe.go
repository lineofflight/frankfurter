// Package cbe fetches rates from the Central Bank of Egypt, which publishes daily buy/sell rates against EGP for 18
// currencies via an XLSX download from its historical-data page.
//
// The page requires a session cookie and anti-forgery token scraped from the HTML form; both are reused across
// batches within a single backfill (FetchEach keeps one adapter instance). The site sits behind an F5 BIG-IP ASM WAF,
// so the backfill range is small (30 days) and requests pause one second between batches.
//
// The XLSX has four columns: Date (Excel serial), Currency (full English name), Buy, Sell. The rate is the midpoint.
// JPY is quoted per 100 units. CBE publishes "1 foreign = X EGP", so foreign is the base and EGP the quote.
//
// Fetch treats after as inclusive: it is sent as the export's start date and the result is not windowed.
//
// Attribution required: the Central Bank of Egypt must be cited as the source.
package cbe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const (
	historicalURL = "https://www.cbe.org.eg/en/economic-research/statistics/cbe-exchange-rates/historical-data"
	apiURL        = "https://www.cbe.org.eg/api/statistics/GetHistoricalData"
	dataSourceID  = "19CFFDDBFF494350A5E9C6A4397FC7DF"
	fallbackURL   = "/en/economic-research/statistics/cbe-exchange-rates/historical-data"
	userAgent     = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
		"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36"
)

var tokenPattern = regexp.MustCompile(`name="__RequestVerificationToken"[^>]*value="([^"]+)"`)

type currency struct {
	name  string
	iso   string
	units float64
}

// currencies maps CBE's currency names to ISO codes, in the order the form posts them.
var currencies = []currency{
	{"US Dollar", "USD", 1},
	{"Euro", "EUR", 1},
	{"Pound Sterling", "GBP", 1},
	{"Canadian Dollar", "CAD", 1},
	{"Danish Krone", "DKK", 1},
	{"Norwegian Krone", "NOK", 1},
	{"Swedish Krona", "SEK", 1},
	{"Swiss Franc", "CHF", 1},
	{"Japanese Yen 100", "JPY", 100},
	{"Saudi Riyal", "SAR", 1},
	{"Kuwaiti Dinar", "KWD", 1},
	{"UAE Dirham", "AED", 1},
	{"Australian Dollar", "AUD", 1},
	{"Bahraini Dinar", "BHD", 1},
	{"Omani Riyal", "OMR", 1},
	{"Qatari Riyal", "QAR", 1},
	{"Jordanian Dinar", "JOD", 1},
	{"Chinese Yuan", "CNY", 1},
}

func init() {
	adapter.Register("CBE", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches CBE rates. It holds the WAF session between Fetch calls.
type Adapter struct {
	adapter.Base
	token, cookie string
	session       bool
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{Base: adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter.
func (a *Adapter) BackfillRange() int { return 30 }

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	start := after
	if start.IsZero() {
		start = adapter.Date(2024, 3, 1)
	}
	end := upto
	if end.IsZero() {
		end = a.Today()
	}
	if start.After(end) {
		return nil, nil
	}

	if a.session {
		if err := a.Sleep(ctx, time.Second); err != nil {
			return nil, err
		}
	}
	if err := a.ensureSession(ctx); err != nil {
		return nil, err
	}
	a.session = true

	body, err := a.postHistorical(ctx, start, end)
	if err != nil {
		return nil, err
	}
	return parse(body)
}

func (a *Adapter) ensureSession(ctx context.Context) error {
	// The cookie may be empty (no Set-Cookie); the token alone marks an established session, as in Ruby.
	if a.token != "" {
		return nil
	}
	req, err := a.NewRequest(ctx, http.MethodGet, historicalURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := a.Do(req)
	if err != nil {
		return err
	}
	m := tokenPattern.FindSubmatch(resp.Body)
	if m == nil {
		return errors.New("token not found on historical-data page")
	}
	a.token = string(m[1])
	a.cookie = adapter.CookieHeader(resp.Header)
	return nil
}

func (a *Adapter) postHistorical(ctx context.Context, start, end time.Time) ([]byte, error) {
	pairs := [][2]string{
		{"__RequestVerificationToken", a.token},
		{"DataSourceId", dataSourceID},
		{"FallbackUrl", fallbackURL},
		{"LanguageName", "en"},
		{"FromDateRaw", start.Format("02/01/2006")},
		{"ToDateRaw", end.Format("02/01/2006")},
	}
	for _, c := range currencies {
		pairs = append(pairs, [2]string{"SelectedSelectOptions", c.name})
	}
	pairs = append(pairs, [2]string{"SubmitAction", "2"})

	// Keep the form's field order, as URI.encode_www_form does; url.Values.Encode would sort it.
	var form strings.Builder
	for i, p := range pairs {
		if i > 0 {
			form.WriteByte('&')
		}
		form.WriteString(url.QueryEscape(p[0]) + "=" + url.QueryEscape(p[1]))
	}

	req, err := a.NewRequest(ctx, http.MethodPost, apiURL, strings.NewReader(form.String()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", a.cookie)
	req.Header.Set("Referer", historicalURL)
	req.Header.Set("Origin", "https://www.cbe.org.eg")
	resp, err := a.Do(req)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// row is one data row of the export. Buy and sell are nil when the cell is not numeric.
type row struct {
	serial    float64
	currency  string
	buy, sell *float64
}

func parse(data []byte) ([]adapter.Rate, error) {
	rows, err := readXLSX(data)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, errors.New("no data rows in historical-data XLSX")
	}

	var rates []adapter.Rate
	for _, r := range rows {
		i := indexCurrency(r.currency)
		if i < 0 || r.buy == nil || r.sell == nil {
			continue
		}
		c := currencies[i]
		mid := adapter.Midpoint(*r.buy, *r.sell)
		if mid == 0 {
			continue
		}
		rates = append(rates, adapter.Rate{
			Date:  excelDate(r.serial),
			Base:  c.iso,
			Quote: "EGP",
			Rate:  mid / c.units,
			Bid:   adapter.Float(adapter.PerUnit(*r.buy, c.units)),
			Ask:   adapter.Float(adapter.PerUnit(*r.sell, c.units)),
		})
	}
	return rates, nil
}

func indexCurrency(name string) int {
	for i, c := range currencies {
		if c.name == name {
			return i
		}
	}
	return -1
}

// excelDate converts an Excel serial. The epoch is 1899-12-30 (off by one for the fictional 1900-02-29).
func excelDate(serial float64) time.Time {
	return adapter.Date(1899, 12, 30).AddDate(0, 0, int(serial))
}

// readXLSX reads the first sheet's data rows: those with a numeric date in column A and at least four cells.
func readXLSX(data []byte) ([]row, error) {
	f, err := excelize.OpenReader(bytes.NewReader(data), excelize.Options{RawCellValue: true})
	if err != nil {
		return nil, fmt.Errorf("historical-data export is not an XLSX workbook: %w", err)
	}
	defer f.Close()
	sheet := f.GetSheetList()[0]
	cells, err := f.GetRows(sheet)
	if err != nil {
		return nil, err
	}
	// number returns the cell's value when it is stored as a number: nil for blanks and text, even numeric text.
	number := func(r, c int) *float64 {
		ref, _ := excelize.CoordinatesToCellName(c+1, r+1)
		if t, err := f.GetCellType(sheet, ref); err != nil || (t != excelize.CellTypeUnset && t != excelize.CellTypeNumber) {
			return nil
		}
		v, err := strconv.ParseFloat(cells[r][c], 64)
		if err != nil {
			return nil
		}
		return &v
	}
	var rows []row
	for i, c := range cells {
		if len(c) < 4 {
			continue
		}
		serial := number(i, 0)
		if serial == nil {
			continue
		}
		rows = append(rows, row{serial: *serial, currency: c[1], buy: number(i, 2), sell: number(i, 3)})
	}
	return rows, nil
}
