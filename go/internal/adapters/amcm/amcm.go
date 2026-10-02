// Package amcm fetches rates from the Autoridade Monetária de Macau (AMCM),
// which publishes daily interbank middle exchange rates against the Macanese
// pataca (MOP) for 17 active foreign currencies plus historical pre-euro
// entries.
//
// The endpoint accepts Begin/End in YYYYMMDD format and caps each response at
// roughly four calendar months, so backfill chunks in 90-day windows.
//
// The usdMeanValue field, despite its name, is "1 foreign = X MOP", so the
// foreign currency is the base. The unit field is the multiplier (JPY and KRW
// are quoted per 100 units). ECU is rewritten to XEU. LIQ, a non-currency
// liquidity indicator, has unit 0 and is skipped.
//
// Unlike most adapters, Fetch keeps rows dated on after itself, as the Ruby
// between?(after, end_date) does.
package amcm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const baseURL = "https://www.amcm.gov.mo/api/v1.0/cms/financial_info"

var (
	codeAliases = map[string]string{"ECU": "XEU"}
	codePattern = regexp.MustCompile(`\A[A-Z]{3}\z`)
)

func init() {
	adapter.Register("AMCM", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches AMCM rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter.
func (a *Adapter) BackfillRange() int { return 90 }

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	end := upto
	if end.IsZero() {
		end = a.Today()
	}
	body, err := a.Get(ctx, baseURL, url.Values{
		"QueryType": {"1"},
		"Begin":     {after.Format("20060102")},
		"End":       {end.Format("20060102")},
	})
	if err != nil {
		return nil, err
	}
	rates, err := parse(body)
	if err != nil {
		return nil, err
	}
	var kept []adapter.Rate
	for _, r := range rates {
		if !r.Date.Before(after) && !r.Date.After(end) {
			kept = append(kept, r)
		}
	}
	return kept, nil
}

type response struct {
	Message any             `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type row struct {
	Date     *string `json:"date"`
	Currency any     `json:"currency"`
	Unit     any     `json:"unit"`
	Mean     any     `json:"usdMeanValue"`
}

func parse(data []byte) ([]adapter.Rate, error) {
	var resp response
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("expected JSON object from %s: %w", baseURL, err)
	}
	var rows []row
	if len(resp.Data) == 0 || resp.Data[0] != '[' || json.Unmarshal(resp.Data, &rows) != nil {
		return nil, fmt.Errorf("response has no data array (message: %v)", resp.Message)
	}

	var rates []adapter.Rate
	for _, r := range rows {
		code, ok := r.Currency.(string)
		if !ok || !codePattern.MatchString(code) || r.Date == nil {
			continue
		}
		unit := toF(r.Unit)
		if unit == 0 {
			continue
		}
		rate := toF(r.Mean)
		if rate <= 0 {
			continue
		}
		// Before mid-2012 rows carry a time of day ("2012-05-14 14:00:00").
		// Ruby's Date.parse drops it; keeping it would put the row after
		// midnight of upto and out of the window.
		s := *r.Date
		if len(s) > len(time.DateOnly) {
			s = s[:len(time.DateOnly)]
		}
		date, err := time.Parse(time.DateOnly, s)
		if err != nil {
			return nil, fmt.Errorf("unrecognised date %q", *r.Date)
		}
		if alias, ok := codeAliases[code]; ok {
			code = alias
		}
		rates = append(rates, adapter.Rate{Date: date, Base: code, Quote: "MOP", Rate: rate / unit})
	}
	return rates, nil
}

// toF mirrors Ruby's to_f on a JSON value: numbers pass through, numeric
// strings parse, anything else is 0.
func toF(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case string:
		f, _ := adapter.ParseFloat(x)
		return f
	}
	return 0
}
