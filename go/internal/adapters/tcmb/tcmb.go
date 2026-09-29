// Package tcmb fetches rates from the Central Bank of the Republic of Turkey,
// which publishes indicative buying and selling rates in Turkish lira through
// its EVDS3 bulk API. Each currency's rate is the midpoint of buy and sell.
// Requires the TCMB_API_KEY environment variable.
//
// Rows come back exactly as the API returns them for [after, upto]: like the
// Ruby adapter, Fetch does not clip the start date, so after is inclusive here.
package tcmb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const evdsURL = "https://evds3.tcmb.gov.tr/igmevdsms-dis"

// Hardcoded because the EVDS3 catalog API doesn't expose a clean list and the
// series rarely change. Browse: https://evds3.tcmb.gov.tr > Exchange Rates >
// Indicative Exchange Rates
var currencies = []string{
	"AED", "AUD", "AZN", "CAD", "CHF", "CNY", "DKK", "EUR", "GBP", "JPY", "KRW",
	"KWD", "KZT", "NOK", "PKR", "QAR", "RON", "RUB", "SAR", "SEK", "USD",
}

func init() {
	adapter.Register("TCMB", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches TCMB rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter.
func (a *Adapter) BackfillRange() int { return 730 }

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	key := os.Getenv("TCMB_API_KEY")
	if key == "" {
		return nil, errors.New("no API key")
	}
	if after.IsZero() {
		after = adapter.Date(2012, 1, 2)
	}
	if upto.IsZero() {
		upto = a.Today()
	}

	series := make([]string, 0, 2*len(currencies))
	for _, c := range currencies {
		series = append(series, seriesID(c, "A"), seriesID(c, "S"))
	}
	u := fmt.Sprintf("%s/series=%s&startDate=%s&endDate=%s&type=json&frequency=1",
		evdsURL, strings.Join(series, "-"), after.Format("02-01-2006"), upto.Format("02-01-2006"))

	req, err := a.NewRequest(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("key", key)
	resp, err := a.Do(req)
	if err != nil {
		return nil, err
	}
	var data struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(resp.Body, &data); err != nil {
		return nil, err
	}
	return parse(data.Items)
}

// seriesID is the EVDS series for a currency's buying (A) or selling (S) rate
// in TRY.
func seriesID(currency, side string) string {
	return "TP.DK." + currency + "." + side + ".YTL"
}

// column is the JSON key EVDS uses for a series: dots become underscores.
func column(currency, side string) string {
	return strings.ReplaceAll(seriesID(currency, side), ".", "_")
}

func parse(items []map[string]any) ([]adapter.Rate, error) {
	var rates []adapter.Rate
	for _, item := range items {
		tarih, _ := item["Tarih"].(string)
		date, err := time.Parse("2-1-2006", tarih) // strptime %d-%m-%Y also takes unpadded fields
		if err != nil {
			return nil, err
		}
		for _, c := range currencies {
			buy, okBuy, err := value(item[column(c, "A")])
			if err != nil {
				return nil, err
			}
			sell, okSell, err := value(item[column(c, "S")])
			if err != nil {
				return nil, err
			}
			if !okBuy || !okSell {
				continue
			}

			// JPY is quoted per 100 units in TCMB data (confirmed via series
			// metadata).
			unit := 1.0
			if c == "JPY" {
				unit = 100
			}
			rates = append(rates, adapter.Rate{
				Date:  date,
				Base:  c,
				Quote: "TRY",
				Rate:  adapter.Midpoint(buy, sell) / unit,
				Bid:   adapter.Float(adapter.PerUnit(buy, unit)),
				Ask:   adapter.Float(adapter.PerUnit(sell, unit)),
			})
		}
	}
	return rates, nil
}

// value is Ruby's Float(v) on a JSON value: nil is absent, anything unparseable
// is an error.
func value(v any) (float64, bool, error) {
	switch v := v.(type) {
	case nil:
		return 0, false, nil
	case float64:
		return v, true, nil
	case string:
		f, ok := adapter.ParseFloat(v)
		if !ok {
			return 0, false, fmt.Errorf("invalid rate %q", v)
		}
		return f, true, nil
	default:
		return 0, false, fmt.Errorf("invalid rate %v", v)
	}
}
