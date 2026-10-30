// Package banxico fetches rates from Banco de México, which publishes daily FIX
// and reference exchange rates for 5 currencies against the Mexican peso (MXN)
// through the SIE REST API, one batched query for all series.
package banxico

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

const baseURL = "https://www.banxico.org.mx/SieAPIRest/service/v1/series"

// seriesIDs lists the series in request order; series maps each to its base
// currency, quoted against MXN.
var (
	seriesIDs = []string{"SF43718", "SF46410", "SF46407", "SF46406", "SF60632"}
	series    = map[string]string{
		"SF43718": "USD",
		"SF46410": "EUR",
		"SF46407": "GBP",
		"SF46406": "JPY",
		"SF60632": "CAD",
	}
)

func init() {
	adapter.Register("BANXICO", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches BANXICO rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// Fetch implements adapter.Adapter. With an open after it asks for the full
// series; the API does the date filtering.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	key := os.Getenv("BANXICO_API_KEY")
	if key == "" {
		return nil, errors.New("no API key")
	}

	u := baseURL + "/" + strings.Join(seriesIDs, ",") + "/datos"
	if !after.IsZero() {
		if upto.IsZero() {
			upto = a.Today()
		}
		u += "/" + after.Format(time.DateOnly) + "/" + upto.Format(time.DateOnly)
	}

	req, err := a.NewRequest(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Bmx-Token", key)
	resp, err := a.Do(req)
	if err != nil {
		return nil, err
	}
	return parse(resp.Body)
}

type response struct {
	Bmx struct {
		Series []struct {
			ID    string `json:"idSerie"`
			Datos []struct {
				Fecha string  `json:"fecha"`
				Dato  *string `json:"dato"`
			} `json:"datos"`
		} `json:"series"`
	} `json:"bmx"`
}

func parse(data []byte) ([]adapter.Rate, error) {
	var r response
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}

	var rates []adapter.Rate
	for _, s := range r.Bmx.Series {
		base, ok := series[s.ID]
		if !ok {
			continue
		}
		for _, obs := range s.Datos {
			if obs.Dato == nil {
				continue
			}
			// "N/E" (not available) fails to parse and is skipped.
			rate, ok := adapter.ParseFloat(strings.ReplaceAll(*obs.Dato, ",", ""))
			if !ok || rate <= 0 {
				continue
			}
			// Unpadded layout, as Ruby's strptime("%d/%m/%Y") also takes
			// one-digit days and months.
			date, err := time.Parse("2/1/2006", obs.Fecha)
			if err != nil {
				return nil, fmt.Errorf("invalid fecha %q: %w", obs.Fecha, err)
			}
			rates = append(rates, adapter.Rate{Date: date, Base: base, Quote: "MXN", Rate: rate})
		}
	}
	return rates, nil
}
