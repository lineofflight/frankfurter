// Package bdp fetches rates from Banco de Portugal, which published pre-euro daily PTE reference rates, now served
// by BPstat as JSON-stat 2.0. Coverage runs from 1987-01-02 through 1998-12-31, when the euro replaced the escudo.
//
// Direction: foreign currency in base, PTE in quote (1 foreign = X PTE), like the other pivot-in-quote adapters.
//
// The dim_cats filter restricts to BdP-authored, PTE-referenced, daily series. Post-1999 EUR-quoted PTE series are
// sourced from LSEG (redistribution-restricted) and are structurally excluded by source=BdP, not by date. Post-1999
// BdP-authored EUR rates would mirror ECB and are also excluded by reference=PTE.
package bdp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const datasetURL = "https://bpstat.bportugal.pt/data/v1/domains/29/datasets/23e0cdd56bddb4ad3016a9c3ad63a539/"

// dimCats are reference=PTE (794), source=Banco de Portugal (35), periodicity=daily (4263). They go on every page
// request: the API's next_page URL drops some of them, which lets non-BdP-sourced rows bleed into the result set.
var dimCats = []string{"13:794", "18:35", "40:4263"}

// labelCode finds the ISO code in a series label, e.g. "US, Dollars (USD) against Escudo - daily".
var labelCode = regexp.MustCompile(`\(([A-Z]{3})\)`)

// codeRemap maps BdP's "ECU" to the ISO 4217 code for the European Currency Unit.
var codeRemap = map[string]string{"ECU": "XEU"}

func init() {
	adapter.Register("BDP", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches BDP rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter: about five years per chunk.
func (a *Adapter) BackfillRange() int { return 1826 }

// Fetch implements adapter.Adapter, following the API's pagination.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	if upto.IsZero() {
		upto = a.Today()
	}
	var rates []adapter.Rate
	for page := 1; ; page++ {
		body, err := a.Get(ctx, datasetURL, query(after, upto, page))
		if err != nil {
			return nil, err
		}
		res, err := decode(body)
		if err != nil {
			return nil, err
		}
		parsed, err := res.rates()
		if err != nil {
			return nil, err
		}
		rates = append(rates, parsed...)
		if res.Extension == nil || res.Extension.NextPage == nil {
			return rates, nil
		}
		if err := a.Sleep(ctx, 200*time.Millisecond); err != nil {
			return nil, err
		}
	}
}

func query(after, upto time.Time, page int) url.Values {
	q := url.Values{"lang": {"EN"}, "page": {strconv.Itoa(page)}, "dim_cats": dimCats}
	if !after.IsZero() {
		q.Set("obs_since", after.Format(time.DateOnly))
	}
	if !upto.IsZero() {
		q.Set("obs_to", upto.Format(time.DateOnly))
	}
	return q
}

type response struct {
	Dimension map[string]struct {
		Category struct {
			Index []string `json:"index"`
		} `json:"category"`
	} `json:"dimension"`
	Value     *[]*float64 `json:"value"`
	Extension *struct {
		Series   *[]series `json:"series"`
		NextPage *string   `json:"next_page"`
	} `json:"extension"`
}

type series struct {
	Label             *string `json:"label"`
	DimensionCategory []struct {
		DimensionID int         `json:"dimension_id"`
		CategoryID  json.Number `json:"category_id"`
	} `json:"dimension_category"`
}

func decode(data []byte) (*response, error) {
	var res response
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, fmt.Errorf("expected JSON-stat object from BPstat: %w", err)
	}
	if res.Dimension == nil {
		return nil, errors.New("dimension envelope missing from JSON-stat response")
	}
	return &res, nil
}

func parse(data []byte) ([]adapter.Rate, error) {
	res, err := decode(data)
	if err != nil {
		return nil, err
	}
	return res.rates()
}

func (res *response) rates() ([]adapter.Rate, error) {
	codes := res.codes()
	counterparties := res.Dimension["12"].Category.Index
	dates := res.Dimension["reference_date"].Category.Index
	var values []*float64
	if res.Value != nil {
		values = *res.Value
	}
	if len(dates) == 0 {
		// The dataset ends 1998-12-31; later windows return a well-formed empty JSON-stat body: an explicit empty
		// value array and series list alongside the empty date index.
		if res.Value != nil && len(values) == 0 && res.Extension != nil && res.Extension.Series != nil &&
			len(*res.Extension.Series) == 0 {
			return nil, nil
		}
		return nil, errors.New("dateless JSON-stat response is not the observed empty shape")
	}
	if len(codes) == 0 {
		return nil, errors.New("no recognizable currency series in JSON-stat response")
	}

	parsed := make([]time.Time, len(dates))
	for j, s := range dates {
		d, err := time.Parse(time.DateOnly, s)
		if err != nil {
			return nil, err
		}
		parsed[j] = d
	}

	var rates []adapter.Rate
	for i, id := range counterparties {
		code, ok := codes[id]
		if !ok {
			continue
		}
		for j, date := range parsed {
			k := i*len(dates) + j
			if k >= len(values) || values[k] == nil {
				continue
			}
			rates = append(rates, adapter.Rate{Date: date, Base: code, Quote: "PTE", Rate: *values[k]})
		}
	}
	return rates, nil
}

// codes maps counterparty category ids to ISO codes taken from the series labels.
func (res *response) codes() map[string]string {
	codes := map[string]string{}
	if res.Extension == nil || res.Extension.Series == nil {
		return codes
	}
	for _, s := range *res.Extension.Series {
		var id string
		for _, c := range s.DimensionCategory {
			if c.DimensionID == 12 {
				id = c.CategoryID.String()
				break
			}
		}
		if id == "" || s.Label == nil {
			continue
		}
		m := labelCode.FindStringSubmatch(*s.Label)
		if m == nil {
			continue
		}
		code := m[1]
		if remapped, ok := codeRemap[code]; ok {
			code = remapped
		}
		codes[id] = code
	}
	return codes
}
