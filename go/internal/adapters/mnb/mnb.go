// Package mnb fetches rates from Magyar Nemzeti Bank, which publishes daily
// exchange rates for 30+ currencies against the Hungarian forint (HUF) via a
// SOAP/XML web service.
//
// Rates use unit multipliers (100 JPY = X HUF) and Hungarian decimal commas.
// Fetch returns every row the service sends for the requested range, start date
// included, without further windowing, as the Ruby adapter does.
package mnb

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const endpoint = "http://www.mnb.hu/arfolyamok.asmx"

// currencies are the active currencies as of 2026. GetExchangeRates requires
// explicit currency names; an empty list returns empty days.
const currencies = "AUD,BRL,CAD,CHF,CNY,CZK,DKK,EUR,GBP,HKD,IDR,ILS,INR,ISK,JPY,KRW," +
	"MXN,MYR,NOK,NZD,PHP,PLN,RON,RSD,RUB,SEK,SGD,THB,TRY,UAH,USD,ZAR"

var isoCode = regexp.MustCompile(`^[A-Z]{3}$`)

func init() {
	adapter.Register("MNB", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches MNB rates.
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
	start := ""
	if !after.IsZero() {
		start = after.Format(time.DateOnly)
	}
	params := fmt.Sprintf("<web:startDate>%s</web:startDate>\n<web:endDate>%s</web:endDate>\n"+
		"<web:currencyNames>%s</web:currencyNames>", start, end.Format(time.DateOnly), currencies)

	req, err := a.NewRequest(ctx, http.MethodPost, endpoint, strings.NewReader(envelope("GetExchangeRates", params)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "text/xml; charset=utf-8")
	req.Header.Set("SOAPAction", "http://www.mnb.hu/webservices/MNBArfolyamServiceSoap/GetExchangeRates")
	resp, err := a.Do(req)
	if err != nil {
		return nil, err
	}
	result, err := extractResult(resp.Body, "GetExchangeRatesResult")
	if err != nil {
		return nil, err
	}
	return parse([]byte(result))
}

func envelope(operation, params string) string {
	return `<?xml version="1.0" encoding="utf-8"?>
<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"
               xmlns:web="http://www.mnb.hu/webservices/">
  <soap:Body>
    <web:` + operation + `>
      ` + params + `
    </web:` + operation + `>
  </soap:Body>
</soap:Envelope>
`
}

// extractResult returns the text of the first element named tag, or "" when
// there is none.
func extractResult(data []byte, tag string) (string, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == tag {
			var text string
			if err := dec.DecodeElement(&text, &se); err != nil {
				return "", err
			}
			return text, nil
		}
	}
}

type exchangeRates struct {
	Days []struct {
		Date  string `xml:"date,attr"`
		Rates []struct {
			Unit     string `xml:"unit,attr"`
			Currency string `xml:"curr,attr"`
			Value    string `xml:",chardata"`
		} `xml:"Rate"`
	} `xml:"Day"`
}

func parse(data []byte) ([]adapter.Rate, error) {
	// Ruby raises on an empty result (Ox.load("") is nil), so a response
	// without one is an error, not an empty day.
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, errors.New("mnb: empty GetExchangeRatesResult")
	}
	var doc exchangeRates
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}

	var rates []adapter.Rate
	for _, day := range doc.Days {
		date, err := time.Parse(time.DateOnly, day.Date)
		if err != nil {
			return nil, err
		}
		for _, r := range day.Rates {
			if !isoCode.MatchString(r.Currency) {
				continue
			}
			unit := toI(r.Unit)
			if unit == 0 {
				continue
			}
			value, err := strconv.ParseFloat(strings.TrimSpace(strings.ReplaceAll(r.Value, ",", ".")), 64)
			if err != nil {
				return nil, fmt.Errorf("parse rate %q for %s: %w", r.Value, r.Currency, err)
			}
			if value == 0 {
				continue
			}
			rates = append(rates, adapter.Rate{Date: date, Base: r.Currency, Quote: "HUF", Rate: value / float64(unit)})
		}
	}
	return rates, nil
}

// toI reads a leading integer the way Ruby's String#to_i does, returning 0 when
// there is none.
func toI(s string) int {
	s = strings.TrimLeft(s, " \t\n\v\f\r")
	end := 0
	if end < len(s) && (s[end] == '+' || s[end] == '-') {
		end++
	}
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	n, _ := strconv.Atoi(s[:end])
	return n
}
