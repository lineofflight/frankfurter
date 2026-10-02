// Package sbi fetches rates from Seðlabanki Íslands (Central Bank of Iceland),
// which publishes daily reference rates for 30+ currencies against the
// Icelandic króna (ISK).
//
// Two GroupIDs are needed: 9 (official reference, 10 currencies from 1981) and
// 7 (registered mid-rate, 22 currencies from 2006). Rows are not clipped
// locally; the requested date range bounds them, as in Ruby.
package sbi

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const baseURL = "https://sedlabanki.is/xmltimeseries/Default.aspx"

// group9 maps GroupID=9 TimeSeries IDs (official reference rates) to ISO codes.
var group9 = map[string]string{
	"4055": "USD",
	"4061": "DKK",
	"4064": "EUR",
	"4085": "JPY",
	"4088": "CAD",
	"4091": "NOK",
	"4097": "XDR",
	"4103": "GBP",
	"4106": "CHF",
	"4109": "SEK",
}

// group7 maps GroupID=7 TimeSeries IDs (registered mid-rates) to ISO codes.
var group7 = map[string]string{
	"29":    "CNY",
	"31":    "PLN",
	"35":    "NGN",
	"36":    "TWD",
	"37":    "KRW",
	"38":    "SRD",
	"39":    "AUD",
	"40":    "NZD",
	"41":    "HKD",
	"42":    "HUF",
	"43":    "ILS",
	"44":    "ZAR",
	"45":    "SGD",
	"46":    "MXN",
	"48":    "TRY",
	"50":    "INR",
	"52":    "CZK",
	"53":    "BRL",
	"74":    "THB",
	"288":   "JMD",
	"3503":  "SAR",
	"19254": "KWD",
}

func init() {
	adapter.Register("SBI", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches SBI rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter.
func (a *Adapter) BackfillRange() int { return 365 }

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	end := upto
	if end.IsZero() {
		end = a.Today()
	}

	var rates []adapter.Rate
	for _, g := range []struct {
		id         int
		currencies map[string]string
	}{{9, group9}, {7, group7}} {
		body, err := a.fetchGroup(ctx, after, end, g.id)
		if err != nil {
			return nil, err
		}
		parsed, err := parse(body, g.currencies)
		if err != nil {
			return nil, err
		}
		rates = append(rates, parsed...)
	}
	return rates, nil
}

func (a *Adapter) fetchGroup(ctx context.Context, start, end time.Time, group int) ([]byte, error) {
	from := "" // Ruby sends nil.to_s for an open start
	if !start.IsZero() {
		from = start.Format(time.DateOnly)
	}
	return a.Get(ctx, baseURL, url.Values{
		"DagsFra": {from},
		"DagsTil": {end.Format(time.DateOnly)},
		"GroupID": {strconv.Itoa(group)},
		"Type":    {"xml"},
	})
}

type document struct {
	TimeSeries []struct {
		ID      string `xml:"ID,attr"`
		Entries []struct {
			Date  string `xml:"Date"`
			Value string `xml:"Value"`
		} `xml:"TimeSeriesData>Entry"`
	} `xml:"TimeSeries"`
}

func parse(data []byte, currencies map[string]string) ([]adapter.Rate, error) {
	var doc document
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}

	var rates []adapter.Rate
	for _, ts := range doc.TimeSeries {
		currency, ok := currencies[ts.ID]
		if !ok {
			continue
		}
		for _, e := range ts.Entries {
			// Ox gives a missing, empty or whitespace-only element no text, and
			// Ruby skips the entry.
			if strings.TrimSpace(e.Date) == "" || strings.TrimSpace(e.Value) == "" {
				continue
			}
			rate, ok := adapter.ParseFloat(e.Value)
			if !ok {
				return nil, fmt.Errorf("bad rate %q", e.Value)
			}
			if rate == 0 {
				continue
			}
			date, err := parseDate(e.Date)
			if err != nil {
				return nil, err
			}
			rates = append(rates, adapter.Rate{Date: date, Base: currency, Quote: "ISK", Rate: rate})
		}
	}
	return rates, nil
}

// parseDate reads "M/D/YYYY 12:00:00 AM".
func parseDate(s string) (time.Time, error) {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return time.Time{}, fmt.Errorf("bad date %q", s)
	}
	return time.Parse("1/2/2006", fields[0])
}
