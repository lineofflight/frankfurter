// Package nbm fetches rates from the National Bank of Moldova (Banca Națională
// a Moldovei), which publishes daily rates for 30+ currencies against the
// Moldovan leu (MDL), plus daily reference prices for gold and silver.
//
// Date-parameterized XML endpoints, one request per day. Metals come from
// official_metal_rates in MDL per gram; values are normalized to per troy ounce
// here. As in the Ruby adapter, after is inclusive: every weekday from after
// through upto is requested and no window is applied.
package nbm

import (
	"context"
	"encoding/xml"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const (
	fxURL    = "https://www.bnm.md/en/official_exchange_rates"
	metalURL = "https://www.bnm.md/en/official_metal_rates"
)

var (
	isoCode    = regexp.MustCompile(`^[A-Z]{3}$`)
	leadingNum = regexp.MustCompile(`^[+-]?(\d[\d_]*)?(\.\d+)?([eE][+-]?\d+)?`)
)

func init() {
	adapter.Register("NBM", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches NBM rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter: one request per day, so keep
// windows short.
func (a *Adapter) BackfillRange() int { return 30 }

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	if after.IsZero() {
		// Ruby iterates after..end_date and cannot start from nil.
		return nil, errors.New("after is required")
	}
	end := upto
	if end.IsZero() {
		end = a.Today()
	}

	var rates []adapter.Rate
	first := true
	for date := after; !date.After(end); date = date.AddDate(0, 0, 1) {
		if weekend(date) {
			continue
		}
		if !first {
			if err := a.Sleep(ctx, 500*time.Millisecond); err != nil {
				return nil, err
			}
		}
		first = false

		fx, err := a.get(ctx, fxURL, date, parse)
		if err != nil {
			return nil, err
		}
		rates = append(rates, fx...)

		if err := a.Sleep(ctx, 500*time.Millisecond); err != nil {
			return nil, err
		}
		metals, err := a.metals(ctx, date)
		if err != nil {
			return nil, err
		}
		rates = append(rates, metals...)
	}
	return rates, nil
}

// metals fetches the day's metal prices. The endpoint has no data for older
// dates (it answers 404 as late as 2010), and a fresh backfill starts in 1999,
// so a 404 there means no metals that day rather than a failed fetch.
func (a *Adapter) metals(ctx context.Context, date time.Time) ([]adapter.Rate, error) {
	u := metalURL + "?" + url.Values{"get_xml": {"1"}, "date": {date.Format("02.01.2006")}}.Encode()
	req, err := a.NewRequest(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.Do(req, http.StatusNotFound)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	return parseMetals(resp.Body)
}

func (a *Adapter) get(ctx context.Context, u string, date time.Time, parse func([]byte) ([]adapter.Rate, error)) ([]adapter.Rate, error) {
	body, err := a.Get(ctx, u, url.Values{"get_xml": {"1"}, "date": {date.Format("02.01.2006")}})
	if err != nil {
		return nil, err
	}
	return parse(body)
}

type entry struct {
	CharCode *string `xml:"CharCode"`
	Nominal  *string `xml:"Nominal"`
	Value    *string `xml:"Value"`
}

// rate returns value per nominal, or false when the code is not wanted or
// either number is zero.
func (e entry) rate(valid func(string) bool) (string, float64, bool) {
	if e.CharCode == nil {
		return "", 0, false
	}
	code := strings.TrimSpace(*e.CharCode)
	if !valid(code) {
		return "", 0, false
	}
	nominal, value := toF(e.Nominal), toF(e.Value)
	if nominal == 0 || value == 0 {
		return "", 0, false
	}
	return code, value / nominal, true
}

type document struct {
	XMLName xml.Name
	Date    *string `xml:"Date,attr"`
	Valutes []entry `xml:"Valute"`
	Metals  []entry `xml:"Metal"`
}

func load(data []byte, root string) (document, time.Time, error) {
	var doc document
	if err := xml.Unmarshal(data, &doc); err != nil {
		return doc, time.Time{}, err
	}
	if doc.XMLName.Local != root {
		return doc, time.Time{}, errors.New(root + " root missing from XML")
	}
	if doc.Date == nil {
		return doc, time.Time{}, errors.New("Date attribute missing from " + root)
	}
	date, err := time.Parse("02.01.2006", *doc.Date)
	return doc, date, err
}

func parse(data []byte) ([]adapter.Rate, error) {
	doc, date, err := load(data, "ValCurs")
	if err != nil {
		return nil, err
	}
	var rates []adapter.Rate
	for _, v := range doc.Valutes {
		if code, rate, ok := v.rate(isoCode.MatchString); ok {
			rates = append(rates, adapter.Rate{Date: date, Base: code, Quote: "MDL", Rate: rate})
		}
	}
	return rates, nil
}

func parseMetals(data []byte) ([]adapter.Rate, error) {
	doc, date, err := load(data, "MetalPrice")
	if err != nil {
		return nil, err
	}
	// NBM prices metals on weekends too; skip them to mirror the FX path.
	if weekend(date) {
		return nil, nil
	}
	var rates []adapter.Rate
	for _, m := range doc.Metals {
		if code, rate, ok := m.rate(isMetal); ok {
			rates = append(rates, adapter.Rate{Date: date, Base: code, Quote: "MDL", Rate: rate * adapter.GramsPerTroyOunce})
		}
	}
	return rates, nil
}

func isMetal(code string) bool { return code == "XAU" || code == "XAG" }

func weekend(d time.Time) bool {
	return d.Weekday() == time.Saturday || d.Weekday() == time.Sunday
}

// toF mimics Ruby's String#to_f (nil and unparsable text give 0).
func toF(s *string) float64 {
	if s == nil {
		return 0
	}
	m := leadingNum.FindString(strings.TrimSpace(*s))
	f, err := strconv.ParseFloat(strings.ReplaceAll(m, "_", ""), 64)
	if err != nil {
		return 0
	}
	return f
}
