// Package cba fetches rates from the Central Bank of Armenia, which publishes
// daily rates for about 30 currencies against the Armenian dram (AMD) through a
// SOAP service, and has published about 70 since 2000.
//
// The adapter asks for the latest rates to learn the current currency codes,
// adds the ones CBA has since dropped, then requests the range in chunks of up
// to a year. Unlike most adapters, after is inclusive: the first chunk starts
// on it, as in the Ruby adapter.
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

var (
	// The range endpoint returns only the codes it is asked for, and the
	// latest bulletin lists only what CBA still quotes, so the series it has
	// since dropped are requested by name: the legacy euro currencies, the
	// litas, lats, kroon and koruna, and the currencies it stopped quoting
	// after 2022-02-28. Left out are TAD and TMM, which copy the TJS and TMT
	// series through 2000, and the TMT, TRL, ROL and RON series, whose values
	// stray from other sources by 2x to 500x for months or years at a time.
	droppedCodes = []string{
		"ARP", "ARS", "ATS", "BEF", "BGL", "BGN", "BRC", "BYR", "DEM", "DKK", "EEK", "EGP", "ESP", "FIM", "FRF", "GRD",
		"HUF", "IEP", "ILS", "ISK", "ITL", "KRW", "KWD", "LBP", "LTL", "LVL", "MDL", "MXN", "NLG", "PLZ", "PTE", "SAR",
		"SDR", "SKK", "SYP", "TRY", "USM",
	}

	// Labels CBA uses for a current currency: retired codes for the Argentine
	// peso, lev, real and zloty (its history starts in 2000, after each of
	// them was redenominated), SDR for the XDR, and USM for the Uzbek som. Each
	// hands over to the right code without a break: 1 "PLZ" = 124.89 AMD on
	// 2006-12-30, 1 PLN = 125.18 on 2007-01-05.
	aliases = map[string]string{
		"ARP": "ARS",
		"BGL": "BGN",
		"BRC": "BRL",
		"PLZ": "PLN",
		"SDR": "XDR",
		"USM": "UZS",
	}

	// CBA's TJS series holds the Tajik ruble until the somoni takes over: 10
	// "TJS" = 26.71 AMD on 2000-10-30, and the somoni at 1 TJS = 250.74 AMD on
	// 2000-11-01.
	predecessors = map[string]adapter.Predecessor{"TJS": {Code: "TJR", Cutover: adapter.Date(2000, 11, 1)}}

	// Series whose amount field understates the quote tenfold, keyed by label
	// with the first date it is right: 1 KZT = 37.37 AMD on 2004-12-30, 10 KZT
	// = 37.39 AMD on 2005-01-04, and 1 ISK = 35.40 AMD on 2015-03-06, 10 ISK =
	// 35.12 AMD on 2015-03-09. USM is per 10 som throughout, 1 "USM" = 2.93 AMD
	// on 2006-12-30 against 10 UZS = 2.94 AMD on 2007-01-05, and so is the
	// Tajik ruble under TJS, which otherwise comes out at ten times NBU's and
	// CBR's rates.
	understatedAmounts = map[string]time.Time{
		"ISK": adapter.Date(2015, 3, 9),
		"KZT": adapter.Date(2005, 1, 4),
		"TJS": adapter.Date(2000, 11, 1),
		"USM": adapter.Date(2007, 1, 5),
	}
)

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

// BackfillRange implements adapter.Adapter. A full backfill stores about
// 300,000 rows. Yearly windows keep each insert, and the blend refresh that
// follows it, to one year instead of holding the write lock for the whole
// history.
func (a *Adapter) BackfillRange() int { return chunkDays }

// Fetch implements adapter.Adapter. It needs a start date; after is inclusive.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	if after.IsZero() {
		return nil, errors.New("start date required")
	}
	end := upto
	if end.IsZero() {
		end = a.Today()
	}
	current, err := a.currentCodes(ctx)
	if err != nil {
		return nil, err
	}
	codes := current
	for _, c := range droppedCodes {
		if !slices.Contains(codes, c) {
			codes = append(codes, c)
		}
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
</ExchangeRatesByDateRangeByISO>`, strings.Join(codes, ","), start.Format(time.DateOnly), chunkEnd.Format(time.DateOnly)))
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
	// SDR and XDR overlap in early 2017 with equal values.
	type key struct {
		date        time.Time
		base, quote string
	}
	seen := map[key]bool{}
	return slices.DeleteFunc(rates, func(r adapter.Rate) bool {
		k := key{r.Date, r.Base, r.Quote}
		if seen[k] {
			return true
		}
		seen[k] = true
		return false
	}), nil
}

func (a *Adapter) currentCodes(ctx context.Context) ([]string, error) {
	body, err := a.request(ctx, "ExchangeRatesLatest", `<ExchangeRatesLatest xmlns="http://www.cba.am/" />`)
	if err != nil {
		return nil, err
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

// parseCodes lists the ISO codes of the latest rates, none when the response
// has no result.
func parseCodes(data []byte) ([]string, error) {
	var doc struct {
		Result *struct {
			Codes []string `xml:"Rates>ExchangeRate>ISO"`
		} `xml:"Body>ExchangeRatesLatestResponse>ExchangeRatesLatestResult"`
	}
	if err := xml.NewDecoder(bytes.NewReader(data)).Decode(&doc); err != nil {
		return nil, err
	}
	if doc.Result == nil {
		return nil, nil
	}
	return slices.DeleteFunc(doc.Result.Codes, func(c string) bool { return c == "" }), nil
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
		date = adapter.Date(date.Year(), date.Month(), date.Day())
		rate, err := extractRate(r, date)
		if err != nil {
			return nil, err
		}
		base := r.ISO
		if alias, ok := aliases[base]; ok {
			base = alias
		}
		rates = append(rates, adapter.Rate{
			Date:  date,
			Base:  adapter.HistoricalCode(predecessors, base, date),
			Quote: "AMD",
			Rate:  rate,
		})
	}
	return rates, nil
}

func extractRate(r row, date time.Time) (float64, error) {
	amount, err := strconv.Atoi(strings.TrimSpace(r.Amount))
	if err != nil {
		return 0, fmt.Errorf("invalid amount %q for %s", r.Amount, r.ISO)
	}
	if corrected, ok := understatedAmounts[r.ISO]; ok && date.Before(corrected) {
		amount *= 10
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
