// Package nbk fetches rates from the National Bank of Kazakhstan, which publishes daily official rates for about 40
// currencies against the Kazakhstani tenge (KZT).
//
// The RSS endpoint serves one date per request, so Fetch walks the weekdays one by one. As in Ruby, `after` is
// inclusive: the walk starts on `after` itself. A zero `after` starts at `upto`.
package nbk

import (
	"context"
	"encoding/xml"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const baseURL = "https://nationalbank.kz/rss/get_rates.cfm"

var (
	isoCode     = regexp.MustCompile(`^[A-Z]{3}$`)
	floatPrefix = regexp.MustCompile(`^[-+]?(\d+(_\d+)*)?(\.\d+(_\d+)*)?([eE][-+]?\d+)?`)
)

func init() {
	adapter.Register("NBK", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches NBK rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter: one request per day, so keep windows short.
func (a *Adapter) BackfillRange() int { return 30 }

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	end := upto
	if end.IsZero() {
		end = a.Today()
	}
	start := after
	if start.IsZero() {
		start = end
	}

	var rates []adapter.Rate
	first := true
	for date := start; !date.After(end); date = date.AddDate(0, 0, 1) {
		if wd := date.Weekday(); wd == time.Saturday || wd == time.Sunday {
			continue
		}
		if !first {
			if err := a.Sleep(ctx, 200*time.Millisecond); err != nil {
				return nil, err
			}
		}
		first = false

		body, err := a.Get(ctx, baseURL, url.Values{"fdate": {date.Format("02.01.2006")}})
		if err != nil {
			return nil, err
		}
		parsed, err := parse(body)
		if err != nil {
			return nil, err
		}
		rates = append(rates, parsed...)
	}
	return rates, nil
}

type document struct {
	Date  *string `xml:"date"`
	Items []struct {
		Title       *string `xml:"title"`
		Description string  `xml:"description"`
		Quant       string  `xml:"quant"`
	} `xml:"item"`
}

func parse(data []byte) ([]adapter.Rate, error) {
	var doc document
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if doc.Date == nil {
		return nil, errors.New("<date> missing from rates XML at " + baseURL)
	}
	date, err := time.Parse("2.1.2006", *doc.Date)
	if err != nil {
		return nil, err
	}

	var rates []adapter.Rate
	for _, item := range doc.Items {
		if item.Title == nil || !isoCode.MatchString(*item.Title) {
			continue
		}
		rate, quant := toF(item.Description), toF(item.Quant)
		if rate == 0 || quant == 0 {
			continue
		}
		rates = append(rates, adapter.Rate{Date: date, Base: *item.Title, Quote: "KZT", Rate: rate / quant})
	}
	return rates, nil
}

// toF mirrors Ruby's String#to_f: it reads the leading number and yields 0 when there is none.
func toF(s string) float64 {
	f, _ := strconv.ParseFloat(strings.ReplaceAll(floatPrefix.FindString(strings.TrimLeft(s, " \t\n\r\f\v")), "_", ""), 64)
	return f
}
