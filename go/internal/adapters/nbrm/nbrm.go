// Package nbrm fetches rates from the National Bank of the Republic of North
// Macedonia, which publishes daily mid-market rates for about 31 currencies
// against MKD.
//
// Fetch walks the range in 90-day chunks starting at after itself, so, as in
// Ruby, after is inclusive and rows are not clipped to the window.
package nbrm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const (
	baseURL   = "https://www.nbrm.mk/KLServiceNOV/GetExchangeRate"
	chunkDays = 90
)

var (
	isoCode = regexp.MustCompile(`\A[A-Z]{3}\z`)

	// The source codes the ECU, which it names the European unit of account,
	// as XBA/955, the bond-market European Composite Unit, rather than
	// XEU/954. Its values track the official ECU basket. It keeps the label
	// until May 1999, quoting the same value it publishes under EUR, since the
	// ECU converted to the euro one for one.
	aliases    = map[string]string{"XBA": "XEU"}
	successors = map[string]adapter.Successor{"XEU": {Code: "EUR", Cutover: adapter.Date(1999, 1, 1)}}
)

func init() {
	adapter.Register("NBRM", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches NBRM rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	if after.IsZero() {
		return nil, errors.New("fetch needs a start date")
	}
	if upto.IsZero() {
		upto = a.Today()
	}

	var rates []adapter.Rate
	for current := after; !current.After(upto); {
		if !current.Equal(after) {
			if err := a.Sleep(ctx, 500*time.Millisecond); err != nil {
				return nil, err
			}
		}
		end := current.AddDate(0, 0, chunkDays-1)
		if end.After(upto) {
			end = upto
		}
		body, err := a.Get(ctx, baseURL, url.Values{
			"StartDate": {current.Format("02.01.2006")},
			"EndDate":   {end.Format("02.01.2006")},
			"format":    {"json"},
		})
		if err != nil {
			return nil, err
		}
		chunk, err := parse(body)
		if err != nil {
			return nil, err
		}
		rates = append(rates, chunk...)
		current = end.AddDate(0, 0, 1)
	}
	return rates, nil
}

type record struct {
	Code    *string         `json:"oznaka"`
	Mid     json.RawMessage `json:"sreden"`
	Nominal json.RawMessage `json:"nomin"`
	Date    string          `json:"datum"`
}

func parse(data []byte) ([]adapter.Rate, error) {
	var records []record
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, fmt.Errorf("expected JSON array from GetExchangeRate: %w", err)
	}

	var rates []adapter.Rate
	for _, r := range records {
		if r.Code == nil {
			continue
		}
		iso := strings.TrimSpace(*r.Code)
		if !isoCode.MatchString(iso) || iso == "MKD" {
			continue
		}

		mid, err := number(r.Mid)
		if err != nil {
			return nil, fmt.Errorf("sreden for %s: %w", iso, err)
		}
		nominal, err := integer(r.Nominal)
		if err != nil {
			return nil, fmt.Errorf("nomin for %s: %w", iso, err)
		}
		rate := mid / float64(nominal)
		if rate == 0 {
			continue
		}

		day, _, _ := strings.Cut(r.Date, "T")
		date, err := time.Parse("2006-01-02", day)
		if err != nil {
			return nil, err
		}
		if alias, ok := aliases[iso]; ok {
			iso = alias
		}
		rates = append(rates, adapter.Rate{Date: date, Base: adapter.SuccessorCode(successors, iso, date), Quote: "MKD",
			Rate: rate})
	}
	return rates, nil
}

// number is Ruby's Float(x) on a JSON number or numeric string.
func number(raw json.RawMessage) (float64, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return 0, err
	}
	switch v := v.(type) {
	case float64:
		return v, nil
	case string:
		if f, ok := adapter.ParseFloat(v); ok {
			return f, nil
		}
	}
	return 0, fmt.Errorf("not a number: %s", raw)
}

// integer is Ruby's Integer(x): a JSON number is truncated, a string must hold
// an integer.
func integer(raw json.RawMessage) (int64, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return 0, err
	}
	switch v := v.(type) {
	case float64:
		return int64(v), nil
	case string:
		return strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	}
	return 0, fmt.Errorf("not an integer: %s", raw)
}
