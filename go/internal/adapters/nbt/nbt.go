// Package nbt fetches rates from the National Bank of Tajikistan, which publishes daily rates for about 36 currencies
// against the somoni (TJS), back to 2001-01-01.
//
// The snapshot endpoint returns one date per request and carries forward the most recent trading-day rate on weekends
// and holidays. Unlike most adapters, Fetch includes the after date itself, as the Ruby range (after..upto) does.
//
// The Valute ID attribute is unreliable for historical records (ID 810 appears with CharCode RUB after originally
// tagging the Soviet rouble SUR), so CharCode is trusted for the ISO code. Nominal may be 10, 100 or 1000 for low-value
// currencies; Value is divided by Nominal to get a per-unit rate.
//
// Out-of-range requests (before 2001 or beyond today) silently return today's snapshot, so the response's Date
// attribute must match the requested date.
//
// Rows keep NBT's direction: foreign currency as base, TJS as quote. Attribution required: reference to www.nbt.tj.
package nbt

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

const baseURL = "https://www.nbt.tj/en/kurs/export_xml.php"

var isoCode = regexp.MustCompile(`^[A-Z]{3}$`)

func init() {
	adapter.Register("NBT", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches NBT rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter: one request per day.
func (a *Adapter) BackfillRange() int { return 1 }

// Fetch implements adapter.Adapter. It requests every date from after through upto, both inclusive. Ruby cannot
// iterate an open range, so a zero after is an error.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	if after.IsZero() {
		return nil, errors.New("after date required")
	}
	end := upto
	if end.IsZero() {
		end = a.Today()
	}

	var rates []adapter.Rate
	for date := after; !date.After(end); date = date.AddDate(0, 0, 1) {
		if err := a.Sleep(ctx, 200*time.Millisecond); err != nil {
			return nil, err
		}
		body, err := a.Get(ctx, baseURL, url.Values{"date": {date.Format(time.DateOnly)}, "export": {"xmlout"}})
		if err != nil {
			return nil, err
		}
		parsed, err := parse(body, date)
		if err != nil {
			return nil, err
		}
		rates = append(rates, parsed...)
	}
	return rates, nil
}

type valCurs struct {
	XMLName xml.Name
	Date    *string `xml:"Date,attr"`
	Valutes []struct {
		CharCode *string `xml:"CharCode"`
		Nominal  *string `xml:"Nominal"`
		Value    *string `xml:"Value"`
	} `xml:"Valute"`
}

// parse reads one snapshot. A non-zero expected date drops a snapshot dated differently.
func parse(data []byte, expected time.Time) ([]adapter.Rate, error) {
	var doc valCurs
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if doc.XMLName.Local != "ValCurs" {
		return nil, errors.New("ValCurs root missing from XML snapshot")
	}
	if doc.Date == nil {
		return nil, errors.New("date attribute missing from ValCurs")
	}
	date, err := time.Parse(time.DateOnly, strings.TrimSpace(*doc.Date))
	if err != nil {
		return nil, err
	}
	if !expected.IsZero() && !date.Equal(expected) {
		return nil, nil
	}

	var rates []adapter.Rate
	for _, v := range doc.Valutes {
		if v.CharCode == nil || !isoCode.MatchString(*v.CharCode) || v.Nominal == nil || v.Value == nil {
			continue
		}
		// Base 0 accepts Ruby Integer()'s prefixes (0x, 0b, 0o, leading-zero octal).
		nominal, err := strconv.ParseInt(strings.TrimSpace(*v.Nominal), 0, 64)
		if err != nil {
			continue
		}
		value, ok := adapter.ParseFloat(*v.Value)
		if !ok || nominal == 0 || value == 0 {
			continue
		}
		rates = append(rates, adapter.Rate{Date: date, Base: *v.CharCode, Quote: "TJS", Rate: value / float64(nominal)})
	}
	return rates, nil
}
