// Package fbil fetches rates from Financial Benchmarks India, which publishes daily reference rates for major
// currencies against the Indian rupee through a public JSON API.
//
// Rates are INR per N units of foreign currency, where N comes from the subProdName field ("INR / 100 JPY" means per
// 100). History starts 2018-07-10. Like the Ruby adapter, Fetch passes the window to the API and does not clip the
// response, so rows dated on after itself come back too.
package fbil

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const baseURL = "https://www.fbil.org.in/wasdm/refrates/fetchfiltered"

var subProdPattern = regexp.MustCompile(`INR / (\d+) ([A-Z]{3})`)

func init() {
	adapter.Register("FBIL", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches FBIL rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	if upto.IsZero() {
		upto = a.Today()
	}
	from := ""
	if !after.IsZero() {
		from = after.Format(time.DateOnly)
	}
	body, err := a.Get(ctx, baseURL, url.Values{
		"fromDate":      {from},
		"toDate":        {upto.Format(time.DateOnly)},
		"authenticated": {"false"},
	})
	if err != nil {
		return nil, err
	}
	return parse(body)
}

type record struct {
	SubProdName    *string `json:"subProdName"`
	ProcessRunDate *string `json:"processRunDate"`
	Rate           any     `json:"rate"`
}

func parse(data []byte) ([]adapter.Rate, error) {
	var records []record
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, fmt.Errorf("expected JSON array of reference rates: %w", err)
	}

	var rates []adapter.Rate
	for _, r := range records {
		value, ok := r.Rate.(float64)
		if r.SubProdName == nil || r.ProcessRunDate == nil || !ok {
			continue
		}
		m := subProdPattern.FindStringSubmatch(*r.SubProdName)
		if m == nil {
			continue
		}
		units, err := strconv.Atoi(m[1])
		if err != nil || units == 0 {
			continue
		}
		rate := value / float64(units)
		if rate == 0 {
			continue
		}
		date, err := adapter.ParseDate(*r.ProcessRunDate, time.DateTime, time.DateOnly)
		if err != nil {
			return nil, err
		}
		rates = append(rates, adapter.Rate{Date: date.Truncate(24 * time.Hour), Base: m[2], Quote: "INR", Rate: rate})
	}
	return rates, nil
}
