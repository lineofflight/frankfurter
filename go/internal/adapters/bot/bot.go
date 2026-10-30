// Package bot fetches rates from the Bank of Thailand, which publishes daily
// average commercial bank exchange rates for 19 currencies against the Thai
// baht.
//
// It uses mid_rate, the midpoint of buying transfer and selling. Some
// currencies are quoted per 100 or 1,000 units; the adapter normalises them to
// per-unit rates. Requires BOT_API_KEY.
package bot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const baseURL = "https://gateway.api.bot.or.th/Stat-ExchangeRate/v2/DAILY_AVG_EXG_RATE/"

func init() {
	adapter.Register("BOT", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches BOT rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter. The API allows at most 31 days per
// request.
func (a *Adapter) BackfillRange() int { return 30 }

// Fetch implements adapter.Adapter. The API filters by period itself, so rows
// are not windowed again.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	key := os.Getenv("BOT_API_KEY")
	if key == "" {
		return nil, errors.New("no API key")
	}
	if after.IsZero() {
		return nil, errors.New("start date required")
	}
	if upto.IsZero() {
		upto = a.Today()
	}
	query := url.Values{
		"start_period": {after.Format(time.DateOnly)},
		"end_period":   {upto.Format(time.DateOnly)},
	}
	req, err := a.NewRequest(ctx, http.MethodGet, baseURL+"?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", key)
	req.Header.Set("Accept", "application/json")
	resp, err := a.Do(req)
	if err != nil {
		return nil, err
	}
	return parse(resp.Body)
}

type record struct {
	Period   string          `json:"period"`
	Currency string          `json:"currency_id"`
	Name     string          `json:"currency_name_eng"`
	Mid      json.RawMessage `json:"mid_rate"`
}

func parse(data []byte) ([]adapter.Rate, error) {
	var doc struct {
		Result struct {
			Data struct {
				Detail *[]record `json:"data_detail"`
			} `json:"data"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if doc.Result.Data.Detail == nil {
		return nil, errors.New("data_detail missing from response")
	}

	var rates []adapter.Rate
	for _, r := range *doc.Result.Data.Detail {
		mid := midText(r.Mid)
		if mid == "" {
			continue
		}
		rate, ok := adapter.ParseFloat(mid)
		if !ok {
			return nil, fmt.Errorf("invalid mid_rate %q for %s on %s", mid, r.Currency, r.Period)
		}
		if rate == 0 {
			continue
		}
		if unit := unit(r.Name); unit > 1 {
			rate /= unit
		}
		date, err := time.Parse(time.DateOnly, r.Period)
		if err != nil {
			return nil, err
		}
		rates = append(rates, adapter.Rate{Date: date, Base: r.Currency, Quote: "THB", Rate: rate})
	}
	return rates, nil
}

// midText renders mid_rate as Ruby's to_s would: empty for null or a missing
// field, the text of a string or number.
func midText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	if s, err := strconv.Unquote(string(raw)); err == nil {
		return s
	}
	return string(raw)
}

func unit(name string) float64 {
	switch {
	case strings.Contains(name, "(100 "):
		return 100
	case strings.Contains(name, "(1,000 "):
		return 1000
	default:
		return 1
	}
}
