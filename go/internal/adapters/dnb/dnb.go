// Package dnb fetches rates from Danmarks Nationalbank, which publishes daily
// exchange rates for 30 currencies against the Danish krone via Statistics
// Denmark's Statbank API. Rates are quoted as DKK per 100 units of foreign
// currency.
//
// The date range goes into the request, so Fetch returns what the API sends
// without clipping it again, as Ruby does.
package dnb

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const baseURL = "https://api.statbank.dk/v1/data"

var currencies = []string{
	"EUR", "USD", "GBP", "SEK", "NOK", "CHF", "CAD", "JPY", "AUD", "NZD",
	"PLN", "CZK", "HUF", "HKD", "SGD", "ZAR", "BGN", "RON", "TRY", "KRW",
	"THB", "MYR", "PHP", "IDR", "CNY", "BRL", "MXN", "INR", "ILS", "ISK",
	"ATS", "BEF", "DEM", "ESP", "FIM", "FRF", "GRD", "IEP", "ITL", "NLG",
	"PTE", "SIT", "ROL", "TRL",
}

var codePattern = regexp.MustCompile(`\A[A-Z]{3}\z`)

func init() {
	adapter.Register("DNB", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches DNB rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

type variable struct {
	Code   string   `json:"code"`
	Values []string `json:"values"`
}

type query struct {
	Table             string     `json:"table"`
	Format            string     `json:"format"`
	Lang              string     `json:"lang"`
	ValuePresentation string     `json:"valuePresentation"`
	Variables         []variable `json:"variables"`
}

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	if after.IsZero() {
		return nil, errors.New("fetch needs a start date")
	}
	tid := ">=" + formatDate(after)
	if !upto.IsZero() {
		tid += "<=" + formatDate(upto)
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // keep >= and <= literal, as Oj writes them
	err := enc.Encode(query{
		Table:             "DNVALD",
		Format:            "BULK",
		Lang:              "en",
		ValuePresentation: "Code",
		Variables: []variable{
			{Code: "VALUTA", Values: currencies},
			{Code: "KURTYP", Values: []string{"KBH"}},
			{Code: "Tid", Values: []string{tid}},
		},
	})
	if err != nil {
		return nil, err
	}

	req, err := a.NewRequest(ctx, http.MethodPost, baseURL, bytes.NewReader(bytes.TrimSuffix(buf.Bytes(), []byte("\n"))))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.Do(req)
	if err != nil {
		return nil, err
	}
	return parse(resp.Body)
}

func parse(data []byte) ([]adapter.Rate, error) {
	text := strings.ToValidUTF8(string(data), "\uFFFD")
	text = strings.Replace(text, "\uFEFF", "", 1)

	r := csv.NewReader(strings.NewReader(text))
	r.Comma = ';'
	r.FieldsPerRecord = -1
	records, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, nil
	}

	index := map[string]int{}
	for i, name := range records[0] {
		if _, ok := index[name]; !ok {
			index[name] = i
		}
	}
	// Ruby's CSV reads an empty field as nil, so an empty field counts as
	// missing.
	field := func(row []string, name string) (string, bool) {
		i, ok := index[name]
		if !ok || i >= len(row) || row[i] == "" {
			return "", false
		}
		return row[i], true
	}

	var rates []adapter.Rate
	for _, row := range records[1:] {
		code, ok := field(row, "VALUTA")
		if !ok || !codePattern.MatchString(code) {
			continue
		}
		value, ok := field(row, "INDHOLD")
		if !ok || strings.TrimSpace(value) == ".." {
			continue
		}
		rate, ok := adapter.ParseFloat(value)
		if !ok {
			return nil, fmt.Errorf("invalid rate %q", value)
		}
		if rate == 0 {
			continue
		}
		tid, ok := field(row, "TID")
		if !ok {
			continue
		}
		date, err := time.Parse("2006M01D02", tid)
		if err != nil {
			return nil, fmt.Errorf("invalid date %q", tid)
		}
		rates = append(rates, adapter.Rate{Date: date, Base: code, Quote: "DKK", Rate: rate / 100.0})
	}
	return rates, nil
}

func formatDate(t time.Time) string {
	return t.Format("2006M01D02")
}
