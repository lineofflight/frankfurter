// Package bnr fetches rates from the National Bank of Romania, which publishes daily reference rates for about 35
// currencies against the Romanian leu (RON).
//
// Yearly XML archives back to 2005 are kept current within the year. The 10-day feed is a redundant window over the
// same data, so we always use the yearly archive. The feeds live on curs.bnr.ro; the paths under www redirect to the
// homepage since the August 2026 site relaunch.
package bnr

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const baseURL = "https://curs.bnr.ro"

var isoCode = regexp.MustCompile(`^[A-Z]{3}$`)

func init() {
	adapter.Register("BNR", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches BNR rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter: a year per archive.
func (a *Adapter) BackfillRange() int { return 365 }

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	end := upto
	if end.IsZero() {
		end = a.Today()
	}
	first := end.Year()
	if !after.IsZero() {
		first = after.Year()
	}

	var rates []adapter.Rate
	for year := first; year <= end.Year(); year++ {
		body, err := a.Get(ctx, fmt.Sprintf("%s/files/xml/years/nbrfxrates%d.xml", baseURL, year), nil)
		if err != nil {
			return nil, err
		}
		parsed, err := parse(body)
		if err != nil {
			return nil, err
		}
		rates = append(rates, parsed...)
	}
	return adapter.Window(rates, after, end), nil
}

type dataSet struct {
	Cubes []struct {
		Date  string `xml:"date,attr"`
		Rates []struct {
			Currency   string `xml:"currency,attr"`
			Multiplier string `xml:"multiplier,attr"`
			Value      string `xml:",chardata"`
		} `xml:"Rate"`
	} `xml:"Body>Cube"`
}

func parse(data []byte) ([]adapter.Rate, error) {
	var doc dataSet
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}

	var rates []adapter.Rate
	for _, cube := range doc.Cubes {
		date, err := time.Parse(time.DateOnly, cube.Date)
		if err != nil {
			return nil, err
		}
		for _, r := range cube.Rates {
			if !isoCode.MatchString(r.Currency) {
				continue
			}
			value, ok := adapter.ParseFloat(r.Value)
			if !ok {
				continue
			}
			if multiplier, _ := strconv.Atoi(strings.TrimSpace(r.Multiplier)); multiplier > 1 {
				value /= float64(multiplier)
			}
			if r.Currency == "XAU" {
				value *= adapter.GramsPerTroyOunce
			}
			if value == 0 {
				continue
			}
			rates = append(rates, adapter.Rate{Date: date, Base: r.Currency, Quote: "RON", Rate: value})
		}
	}
	return rates, nil
}
