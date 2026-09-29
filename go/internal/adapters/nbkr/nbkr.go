// Package nbkr fetches rates from the National Bank of the Kyrgyz Republic,
// which publishes daily rates for 5 major currencies (USD, EUR, RUB, KZT, CNY)
// and weekly rates for about 35 others against the som (KGS).
//
// Two tracks:
//
//  1. Live XML feed (daily.xml + weekly.xml) for the current snapshot. Each request returns only the latest published
//     rates, with no date parameter. The XML is Windows-1251 with comma decimals, and values are per Nominal units.
//  2. Historical HTML scrape (index1.jsp?item=1562) back to 1999-01-01. The page gives one currency's series, keyed by
//     NBKR's internal valuta_id, for the requested window. Nominals are not in the response, so they live in
//     defaultCurrencies alongside the ISO mapping taken from the landing page's <select> options.
//
// Fetch uses the XML feed when the window is open or reaches today, and the
// HTML scrape for bounded windows strictly in the past. Rows keep NBKR's
// direction: foreign currency as base, KGS as quote. Unlike most adapters,
// after is inclusive, as in the Ruby adapter.
package nbkr

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/encoding/charmap"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const (
	dailyURL      = "https://www.nbkr.kg/XML/daily.xml"
	weeklyURL     = "https://www.nbkr.kg/XML/weekly.xml"
	historicalURL = "https://www.nbkr.kg/index1.jsp"
)

var (
	isoCode       = regexp.MustCompile(`^[A-Z]{3}$`)
	historicalRow = regexp.MustCompile(`(?s)<!--date-->(\d{2}\.\d{2}\.\d{4})<!--date-->.*?<!--value-->(\d+(?:,\d+)?)<!--value-->`)
)

type currency struct {
	id      int
	iso     string
	nominal int
}

// NBKR's historical page identifies each currency by an internal valuta_id and
// publishes rates per nominal units. Some currencies appear twice across a
// redenomination (BYR/BYN, RUR/RUB, AZM/AZN, TRL/TRY); each id covers its own
// slice of history.
var defaultCurrencies = []currency{
	{15, "USD", 1}, {56, "AUD", 1}, {16, "ATS", 1}, {82, "AZN", 1}, {37, "AZM", 1000}, {17, "GBP", 1},
	{38, "AMD", 10}, {99, "AFN", 1}, {39, "BYR", 100}, {18, "BEF", 10}, {100, "BGN", 1}, {101, "BRL", 1},
	{51, "HUF", 10}, {25, "KRW", 1}, {102, "GEL", 1}, {19, "DKK", 1}, {103, "AED", 1}, {20, "EUR", 1},
	{21, "INR", 1}, {104, "IRR", 10}, {22, "ITL", 100}, {40, "KZT", 1}, {23, "CAD", 1}, {24, "CNY", 1},
	{50, "KWD", 1}, {41, "LVL", 1}, {42, "LTL", 1}, {105, "MYR", 1}, {43, "MDL", 1}, {106, "MNT", 1},
	{26, "DEM", 1}, {27, "NLG", 1}, {57, "TRY", 1}, {53, "NZD", 1}, {107, "TWD", 1}, {108, "TMT", 1},
	{28, "NOK", 1}, {55, "PKR", 1}, {109, "PLN", 1}, {29, "PTE", 1}, {44, "RUB", 1}, {98, "RUR", 1000},
	{30, "XDR", 1}, {86, "SGD", 1}, {45, "TJS", 1}, {49, "TJR", 100}, {31, "TRL", 1000}, {46, "UZS", 1},
	{47, "UAH", 1}, {32, "FIM", 1}, {33, "FRF", 1}, {52, "CZK", 1}, {34, "SEK", 1}, {35, "CHF", 1},
	{48, "EEK", 1}, {36, "JPY", 10}, {139, "SAR", 1}, {160, "BYN", 1}, {180, "OMR", 1}, {182, "HKD", 1},
	{184, "IDR", 10},
}

func init() {
	adapter.Register("NBKR", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches NBKR rates.
type Adapter struct {
	adapter.Base
	currencies []currency
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{Base: adapter.NewBase(client), currencies: defaultCurrencies}
}

// BackfillRange implements adapter.Adapter. Per-currency historical pages
// return up to about 366 rows, so one year per chunk keeps each request
// bounded.
func (a *Adapter) BackfillRange() int { return 365 }

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	var (
		rates []adapter.Rate
		err   error
	)
	if !after.IsZero() && !upto.IsZero() && upto.Before(a.Today()) {
		rates, err = a.fetchHistorical(ctx, after, upto)
	} else {
		rates, err = a.fetchLive(ctx)
	}
	if err != nil {
		return nil, err
	}

	var out []adapter.Rate
	for _, r := range rates {
		if (!after.IsZero() && r.Date.Before(after)) || (!upto.IsZero() && r.Date.After(upto)) {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

func (a *Adapter) fetchLive(ctx context.Context) ([]adapter.Rate, error) {
	var rates []adapter.Rate
	for _, u := range []string{dailyURL, weeklyURL} {
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
	return rates, nil
}

// fetchHistorical requests each currency's series in turn. NBKR drops
// connections after about 20 fresh TLS sessions in quick succession; the client
// keeps one connection alive and the sleep paces the requests further.
func (a *Adapter) fetchHistorical(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	var rates []adapter.Rate
	for i, c := range a.currencies {
		if i > 0 {
			if err := a.Sleep(ctx, 500*time.Millisecond); err != nil {
				return nil, err
			}
		}
		body, err := a.Get(ctx, historicalURL, url.Values{
			"item":      {"1562"},
			"lang":      {"ENG"},
			"valuta_id": {strconv.Itoa(c.id)},
			"beg_day":   {after.Format("02")},
			"beg_month": {after.Format("01")},
			"beg_year":  {after.Format("2006")},
			"end_day":   {upto.Format("02")},
			"end_month": {upto.Format("01")},
			"end_year":  {upto.Format("2006")},
		})
		if err != nil {
			return nil, err
		}
		parsed, err := parseHistorical(body, c.iso, c.nominal)
		if err != nil {
			return nil, err
		}
		rates = append(rates, parsed...)
	}
	return rates, nil
}

type feed struct {
	XMLName    xml.Name `xml:"CurrencyRates"`
	Date       *string  `xml:"Date,attr"`
	Currencies []struct {
		ISOCode string  `xml:"ISOCode,attr"`
		Nominal string  `xml:"Nominal"`
		Value   *string `xml:"Value"`
	} `xml:"Currency"`
}

func parse(data []byte) ([]adapter.Rate, error) {
	text, err := charmap.Windows1251.NewDecoder().Bytes(data)
	if err != nil {
		return nil, err
	}
	dec := xml.NewDecoder(bytes.NewReader(text))
	// Already transcoded; the declaration still says windows-1251.
	dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }

	var doc feed
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	if doc.Date == nil {
		return nil, errors.New("date attribute missing from CurrencyRates feed")
	}
	date, err := time.Parse("02.01.2006", *doc.Date)
	if err != nil {
		return nil, err
	}

	var rates []adapter.Rate
	for _, c := range doc.Currencies {
		if !isoCode.MatchString(c.ISOCode) {
			continue
		}
		nominal, _ := strconv.Atoi(strings.TrimSpace(c.Nominal))
		if nominal == 0 {
			continue
		}
		if c.Value == nil || *c.Value == "" {
			continue
		}
		rate, ok := adapter.ParseFloat(strings.ReplaceAll(*c.Value, ",", "."))
		if !ok || rate <= 0 {
			continue
		}
		rates = append(rates, adapter.Rate{Date: date, Base: c.ISOCode, Quote: "KGS", Rate: rate / float64(nominal)})
	}
	return rates, nil
}

// parseHistorical reads the per-currency HTML series, whose cells are wrapped
// in <!--date--> and <!--value--> comment markers.
func parseHistorical(html []byte, iso string, nominal int) ([]adapter.Rate, error) {
	var rates []adapter.Rate
	for _, m := range historicalRow.FindAllSubmatch(html, -1) {
		rate, ok := adapter.ParseFloat(strings.ReplaceAll(string(m[2]), ",", "."))
		if !ok || rate <= 0 {
			continue
		}
		date, err := time.Parse("02.01.2006", string(m[1]))
		if err != nil {
			return nil, err
		}
		rates = append(rates, adapter.Rate{Date: date, Base: iso, Quote: "KGS", Rate: rate / float64(nominal)})
	}
	return rates, nil
}
