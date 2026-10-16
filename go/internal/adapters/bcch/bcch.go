// Package bcch fetches rates from Banco Central de Chile, which publishes daily
// rates for 8 currencies against the Chilean peso (CLP) through the BDE REST
// API. The API requires registered credentials (email and password, from
// BCCH_USER and BCCH_PASS) passed as query parameters.
//
// Like the Ruby adapter, Fetch does not clip to the window: it returns whatever
// the API sends for the requested dates.
package bcch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const apiURL = "https://si3.bcentral.cl/SieteRestWS/SieteRestWS.ashx"

// series pairs each series ID with its base currency, all quoted against CLP,
// in the order Ruby fetches them.
var series = []struct{ id, base string }{
	{"F073.TCO.PRE.Z.D", "USD"},
	{"F072.CLP.EUR.N.O.D", "EUR"},
	{"F072.CLP.GBP.N.O.D", "GBP"},
	{"F072.CLP.JPY.N.O.D", "JPY"},
	{"F072.CLP.CAD.N.O.D", "CAD"},
	{"F072.CLP.AUD.N.O.D", "AUD"},
	{"F072.CLP.CNY.N.O.D", "CNY"},
	{"F072.CLP.BRL.N.O.D", "BRL"},
}

// The BRL series starts in January 1994 under the cruzeiro real, six months
// before the real existed, and is not restated: 0.16 CLP on 1994-06-30, 418.34
// on 1994-07-01. Rows before the Real Plan are cruzeiro real (BRR).
var predecessors = map[string]adapter.Predecessor{
	"BRL": {Code: "BRR", Cutover: adapter.Date(1994, 7, 1)},
}

func init() {
	adapter.Register("BCCH", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches BCCH rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	params := url.Values{
		"user":     {os.Getenv("BCCH_USER")},
		"pass":     {os.Getenv("BCCH_PASS")},
		"function": {"GetSeries"},
	}
	if !after.IsZero() {
		if upto.IsZero() {
			upto = a.Today()
		}
		params.Set("firstdate", after.Format(time.DateOnly))
		params.Set("lastdate", upto.Format(time.DateOnly))
	}

	var rates []adapter.Rate
	for _, s := range series {
		if err := a.Sleep(ctx, 200*time.Millisecond); err != nil {
			return nil, err
		}
		params.Set("timeseries", s.id)
		body, err := a.Get(ctx, apiURL, params)
		if err != nil {
			return nil, err
		}
		parsed, err := parse(body, s.base)
		if err != nil {
			return nil, err
		}
		rates = append(rates, parsed...)
	}
	return rates, nil
}

type response struct {
	Series struct {
		Obs []struct {
			Date   string  `json:"indexDateString"`
			Value  *string `json:"value"`
			Status string  `json:"statusCode"`
		}
	}
}

func parse(data []byte, base string) ([]adapter.Rate, error) {
	var resp response
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	var rates []adapter.Rate
	for _, obs := range resp.Series.Obs {
		if obs.Status != "OK" || obs.Value == nil {
			continue
		}
		rate, ok := adapter.ParseFloat(strings.ReplaceAll(*obs.Value, ",", ""))
		if !ok {
			return nil, fmt.Errorf("invalid value %q on %s", *obs.Value, obs.Date)
		}
		if rate == 0 {
			continue
		}
		date, err := time.Parse("02-01-2006", obs.Date)
		if err != nil {
			return nil, err
		}
		rates = append(rates, adapter.Rate{
			Date:  date,
			Base:  adapter.HistoricalCode(predecessors, base, date),
			Quote: "CLP",
			Rate:  rate,
		})
	}
	return rates, nil
}
