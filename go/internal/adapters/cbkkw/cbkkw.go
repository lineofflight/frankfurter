// Package cbkkw fetches rates from the Central Bank of Kuwait, which publishes
// daily reference rates for ~135 currencies against KWD, quoted in fils (one
// thousandth of a dinar) per unit of foreign currency: "KWD / US Dollar
// 306.650" means 1 USD = 0.30665 KWD. Foreign is the base and KWD the quote;
// the fils figure is divided by 1000 here.
//
// The exchange-rates page carries a lookup form whose currency select pairs
// each ISO code with a CMS id ("USD:128735"). The endpoint keys on the id, and
// a bare code returns nothing, so the list is scraped from the page rather than
// hard-coded. One POST per currency then returns an HTML fragment with a
// date-filtered table (dates DD.MM.YYYY, working days Sun-Thu). Majors run from
// 2008-01-02, most other currencies from 2017-06-18. Some retired codes (ECS,
// VEF, SLL) are still served; blend and catalogue rules exclude them
// downstream.
//
// As in Ruby, rows are not clipped locally. The lookup window starts the day
// before `after` and is exclusive of it, so a row dated `after` itself is
// returned.
package cbkkw

import (
	"context"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const (
	baseURL        = "https://www.cbk.gov.kw/en/monetary-policy/market-operations/exchange-rates"
	formURL        = baseURL + "/usd"
	lookupURL      = baseURL + "/get-exchange-rates"
	fragmentMarker = `id="currencyJSON"`
)

var (
	formIDPattern = regexp.MustCompile(`name="formId"[^>]*value="(\d+)"`)
	optionPattern = regexp.MustCompile(`<option value="([A-Z]{3}):(\d+)"`)
	rowPattern    = regexp.MustCompile(`<tr>\s*<td>(\d{2}\.\d{2}\.\d{4})</td>\s*<td>([\d.]+)</td>\s*</tr>`)
	filsPerDinar  = big.NewRat(1000, 1)
)

func init() {
	adapter.Register("CBKKW", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches CBKKW rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// currency is one option of the lookup form's select.
type currency struct {
	code, id string
}

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	start := after
	if start.IsZero() {
		start = adapter.Date(2008, 1, 2)
	}
	end := upto
	if end.IsZero() {
		end = a.Today()
	}
	if start.After(end) {
		return nil, nil
	}

	page, err := a.Get(ctx, formURL, nil)
	if err != nil {
		return nil, err
	}
	formID, currencies, err := parseForm(string(page))
	if err != nil {
		return nil, err
	}

	var rates []adapter.Rate
	for i, c := range currencies {
		if i > 0 {
			if err := a.Sleep(ctx, 500*time.Millisecond); err != nil {
				return nil, err
			}
		}
		// txtDateFrom is exclusive and txtDateTo inclusive, so the window
		// starts a day early.
		body, err := a.PostForm(ctx, lookupURL, url.Values{
			"formId":      {formID},
			"selCurrency": {c.code + ":" + c.id},
			"txtDateFrom": {start.AddDate(0, 0, -1).Format("02/01/2006")},
			"txtDateTo":   {end.Format("02/01/2006")},
		})
		if err != nil {
			return nil, err
		}
		parsed, err := parse(string(body), c.code)
		if err != nil {
			return nil, err
		}
		rates = append(rates, parsed...)
	}
	return rates, nil
}

// parseForm returns the form id and the currencies in page order. A code listed
// twice keeps its first position and its last id, as Ruby's Array#to_h does.
func parseForm(html string) (string, []currency, error) {
	m := formIDPattern.FindStringSubmatch(html)
	if m == nil {
		return "", nil, fmt.Errorf("formId not found on %s", formURL)
	}

	var currencies []currency
	index := map[string]int{}
	for _, o := range optionPattern.FindAllStringSubmatch(html, -1) {
		if i, ok := index[o[1]]; ok {
			currencies[i].id = o[2]
			continue
		}
		index[o[1]] = len(currencies)
		currencies = append(currencies, currency{o[1], o[2]})
	}
	if len(currencies) == 0 {
		return "", nil, fmt.Errorf("no currency options on %s", formURL)
	}
	return m[1], currencies, nil
}

// parse reads the lookup fragment for one currency. An empty table is a genuine
// no-data window (weekend, retired code); a body without the fragment marker is
// a WAF or error page and fails.
func parse(html, code string) ([]adapter.Rate, error) {
	if !strings.Contains(html, fragmentMarker) {
		return nil, fmt.Errorf("unexpected response for %s", code)
	}

	var rates []adapter.Rate
	for _, m := range rowPattern.FindAllStringSubmatch(html, -1) {
		fils, ok := new(big.Rat).SetString(m[2])
		if !ok {
			return nil, fmt.Errorf("invalid fils value %q for %s", m[2], code)
		}
		rate, _ := fils.Quo(fils, filsPerDinar).Float64()
		if rate == 0 {
			continue
		}
		date, err := time.Parse("02.01.2006", m[1])
		if err != nil {
			return nil, err
		}
		rates = append(rates, adapter.Rate{Date: date, Base: code, Quote: "KWD", Rate: rate})
	}
	return rates, nil
}
