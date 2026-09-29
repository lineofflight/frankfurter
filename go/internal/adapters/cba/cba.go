// Package cba fetches rates from the Central Bank of Armenia, which publishes daily rates for about 30 currencies
// against the Armenian dram (AMD) through a SOAP service.
//
// The adapter asks for the latest rates to learn the current currency codes, then requests the range in chunks of up
// to a year. Unlike most adapters, after is inclusive: the first chunk starts on it, as in the Ruby adapter.
package cba

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const (
	baseURL        = "https://api.cba.am/exchangerates.asmx"
	chunkDays      = 365
	troyOunceGrams = 31.1035
)

var preciousMetals = []string{"XAU", "XAG"}

func init() {
	adapter.Register("CBA", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches CBA rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// Fetch implements adapter.Adapter. It needs a start date; after is inclusive.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	if after.IsZero() {
		return nil, errors.New("start date required")
	}
	end := upto
	if end.IsZero() {
		end = a.Today()
	}
	codes, err := a.currentCodes(ctx)
	if err != nil {
		return nil, err
	}
	var rates []adapter.Rate
	for start := after; !start.After(end); {
		chunkEnd := start.AddDate(0, 0, chunkDays-1)
		if chunkEnd.After(end) {
			chunkEnd = end
		}
		body, err := a.request(ctx, "ExchangeRatesByDateRangeByISO", fmt.Sprintf(`<ExchangeRatesByDateRangeByISO xmlns="http://www.cba.am/">
      <ISOCodes>%s</ISOCodes>
      <DateFrom>%s</DateFrom>
      <DateTo>%s</DateTo>
    </ExchangeRatesByDateRangeByISO>`, codes, start.Format(time.DateOnly), chunkEnd.Format(time.DateOnly)))
		if err != nil {
			return nil, err
		}
		chunk, err := parseRange(body)
		if err != nil {
			return nil, err
		}
		rates = append(rates, chunk...)
		start = chunkEnd.AddDate(0, 0, 1)
	}
	return rates, nil
}

func (a *Adapter) currentCodes(ctx context.Context) (string, error) {
	body, err := a.request(ctx, "ExchangeRatesLatest", `<ExchangeRatesLatest xmlns="http://www.cba.am/" />`)
	if err != nil {
		return "", err
	}
	return parseCodes(body)
}

func (a *Adapter) request(ctx context.Context, action, payload string) ([]byte, error) {
	envelope := `<?xml version="1.0" encoding="utf-8"?>
<soap:Envelope xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
               xmlns:xsd="http://www.w3.org/2001/XMLSchema"
               xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">
  <soap:Body>
    ` + payload + `
  </soap:Body>
</soap:Envelope>
`
	req, err := a.NewRequest(ctx, http.MethodPost, baseURL, strings.NewReader(envelope))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "text/xml; charset=utf-8")
	req.Header.Set("SOAPAction", `"http://www.cba.am/`+action+`"`)
	resp, err := a.Do(req)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// parseCodes joins the ISO codes of the latest rates with commas, or returns "" when the response has no result.
func parseCodes(data []byte) (string, error) {
	var doc struct {
		Result *struct {
			Codes []string `xml:"Rates>ExchangeRate>ISO"`
		} `xml:"Body>ExchangeRatesLatestResponse>ExchangeRatesLatestResult"`
	}
	if err := xml.NewDecoder(bytes.NewReader(data)).Decode(&doc); err != nil {
		return "", err
	}
	if doc.Result == nil {
		return "", nil
	}
	codes := slices.DeleteFunc(doc.Result.Codes, func(c string) bool { return c == "" })
	return strings.Join(codes, ","), nil
}

type row struct {
	ISO      string `xml:"ISO"`
	Amount   string `xml:"Amount"`
	Rate     string `xml:"Rate"`
	RateDate string `xml:"RateDate"`
}

func parseRange(data []byte) ([]adapter.Rate, error) {
	var doc struct {
		Rows []row `xml:"Body>ExchangeRatesByDateRangeByISOResponse>ExchangeRatesByDateRangeByISOResult>diffgram>DocumentElement>ExchangeRatesByRange"`
	}
	if err := xml.NewDecoder(bytes.NewReader(data)).Decode(&doc); err != nil {
		return nil, err
	}
	rates := make([]adapter.Rate, 0, len(doc.Rows))
	for _, r := range doc.Rows {
		if r.ISO == "" {
			continue
		}
		date, err := adapter.ParseDate(strings.TrimSpace(r.RateDate), time.RFC3339, "2006-01-02T15:04:05", time.DateOnly)
		if err != nil {
			return nil, err
		}
		rate, err := extractRate(r)
		if err != nil {
			return nil, err
		}
		rates = append(rates, adapter.Rate{
			Date:  adapter.Date(date.Year(), date.Month(), date.Day()),
			Base:  r.ISO,
			Quote: "AMD",
			Rate:  rate,
		})
	}
	return rates, nil
}

func extractRate(r row) (float64, error) {
	amount, err := strconv.Atoi(strings.TrimSpace(r.Amount))
	if err != nil {
		return 0, fmt.Errorf("invalid amount %q for %s", r.Amount, r.ISO)
	}
	rate, ok := adapter.ParseFloat(r.Rate)
	if !ok {
		return 0, fmt.Errorf("invalid rate %q for %s", r.Rate, r.ISO)
	}
	if slices.Contains(preciousMetals, r.ISO) {
		rate *= troyOunceGrams
	}
	return rate / float64(amount), nil
}
