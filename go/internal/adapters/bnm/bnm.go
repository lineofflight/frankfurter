// Package bnm fetches rates from Bank Negara Malaysia, which publishes daily
// ringgit rates for about 25 currencies. Data is available from 2006-01-03.
// Historical rates are fetched per currency per month.
//
// As in Ruby, after only picks the first month: rows earlier in that month are
// kept, and only upto clips.
package bnm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const (
	baseURL = "https://api.bnm.gov.my/public/exchange-rate"
	session = "0900"
	accept  = "application/vnd.BNM.API.v1+json"
)

var aliases = map[string]string{"SDR": "XDR"}

func init() {
	adapter.Register("BNM", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches BNM rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter.
func (a *Adapter) BackfillRange() int { return 30 }

// Fetch implements adapter.Adapter. after is required, as Ruby's
// Date.parse(after.to_s) fails on nil.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	if after.IsZero() {
		return nil, errors.New("after is required")
	}
	if upto.IsZero() {
		upto = a.Today()
	}
	codes, err := a.currencies(ctx)
	if err != nil {
		return nil, err
	}
	first := time.Date(after.Year(), after.Month(), 1, 0, 0, 0, 0, time.UTC)
	var rates []adapter.Rate
	for _, code := range codes {
		for m := first; !m.After(upto); m = m.AddDate(0, 1, 0) {
			body, err := a.get(ctx, fmt.Sprintf("%s/%s/year/%d/month/%d", baseURL, code, m.Year(), int(m.Month())))
			if err != nil {
				return nil, err
			}
			month, ok, err := parseMonth(body, code, upto)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			rates = append(rates, month...)
			if err := a.Sleep(ctx, time.Second); err != nil {
				return nil, err
			}
		}
	}
	return rates, nil
}

func (a *Adapter) get(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := a.NewRequest(ctx, http.MethodGet, rawURL+"?session="+session, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	resp, err := a.Do(req)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func (a *Adapter) currencies(ctx context.Context) ([]string, error) {
	body, err := a.get(ctx, baseURL)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Data *[]struct {
			Code string `json:"currency_code"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, err
	}
	if doc.Data == nil {
		return nil, errors.New("bnm: currency list has no data")
	}
	codes := make([]string, len(*doc.Data))
	for i, item := range *doc.Data {
		codes[i] = item.Code
	}
	return codes, nil
}

type rate struct {
	Date string   `json:"date"`
	Mid  *float64 `json:"middle_rate"`
}

// parseMonth parses one currency-month response. ok is false when data is
// missing or an array, which Ruby skips without sleeping.
func parseMonth(data []byte, code string, upto time.Time) (rates []adapter.Rate, ok bool, err error) {
	var doc struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, false, err
	}
	if len(doc.Data) == 0 || doc.Data[0] != '{' {
		return nil, false, nil
	}
	var cd struct {
		Unit *float64        `json:"unit"`
		Rate json.RawMessage `json:"rate"`
	}
	if err := json.Unmarshal(doc.Data, &cd); err != nil {
		return nil, false, err
	}
	unit := 1.0
	if cd.Unit != nil {
		unit = *cd.Unit
	}
	// A month with one rate carries an object instead of a list. A missing or
	// null rate fails, as in Ruby.
	var list []*rate
	if len(cd.Rate) > 0 && cd.Rate[0] == '[' {
		if err := json.Unmarshal(cd.Rate, &list); err != nil {
			return nil, false, err
		}
	} else {
		var one *rate
		if len(cd.Rate) > 0 {
			if err := json.Unmarshal(cd.Rate, &one); err != nil {
				return nil, false, err
			}
		}
		list = []*rate{one}
	}

	base := code
	if alias, found := aliases[code]; found {
		base = alias
	}
	for _, r := range list {
		if r == nil {
			return nil, false, fmt.Errorf("bnm: %s rate entry is missing", code)
		}
		if r.Mid == nil {
			continue
		}
		date, err := time.Parse(time.DateOnly, r.Date)
		if err != nil {
			return nil, false, err
		}
		if date.After(upto) {
			continue
		}
		rates = append(rates, adapter.Rate{Date: date, Base: base, Quote: "MYR", Rate: *r.Mid / unit})
	}
	return rates, true, nil
}
