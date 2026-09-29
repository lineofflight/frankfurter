// Package boja fetches Bank of Jamaica daily counter rates in JMD.
//
// The rates come from a WPDataTables endpoint (table_id=134), which requires a nonce extracted from the page HTML.
// Unlike most adapters, Fetch treats after as inclusive, as the Ruby adapter does.
package boja

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
	pageURL = "https://boj.org.jm/market/foreign-exchange/counter-rates/"
	baseURL = "https://boj.org.jm/wp-admin/admin-ajax.php"
	tableID = 134
)

var noncePattern = regexp.MustCompile(`wdtNonceFrontendServerSide_` + strconv.Itoa(tableID) + `"\s+value="([^"]+)"`)

var currencies = map[string]string{
	"U.S. DOLLAR":            "USD",
	"GREAT BRITAIN POUND":    "GBP",
	"CANADA DOLLAR":          "CAD",
	"EURO":                   "EUR",
	"JAPANESE YEN":           "JPY",
	"SWISS FRANC":            "CHF",
	"AUSTRALIAN DOLLAR":      "AUD",
	"DANISH KRONE":           "DKK",
	"NORWEGIAN KRONE":        "NOK",
	"SWEDISH KRONA":          "SEK",
	"HONG KONG DOLLAR":       "HKD",
	"BARBADOS DOLLAR":        "BBD",
	"BELIZE DOLLAR":          "BZD",
	"T&T DOLLAR":             "TTD",
	"BAHAMAS DOLLAR":         "BSD",
	"CAYMAN DOLLAR":          "KYD",
	"GUYANA DOLLAR":          "GYD",
	"E. C. DOLLAR":           "XCD",
	"DOMINICAN REP. PESO":    "DOP",
	"GIBRALTAR POUND":        "GIP",
	"NORTHERN IRELAND POUND": "GBP",
}

func init() {
	adapter.Register("BOJA", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches BOJA rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// Fetch implements adapter.Adapter. Rows dated on after are included.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	page, err := a.Get(ctx, pageURL, nil)
	if err != nil {
		return nil, err
	}
	m := noncePattern.FindSubmatch(page)
	if m == nil {
		return nil, errors.New("wdtNonce not found on counter-rates page")
	}

	uri := fmt.Sprintf("%s?action=get_wdtable&table_id=%d", baseURL, tableID)
	form := url.Values{"draw": {"1"}, "start": {"0"}, "length": {"-1"}, "wdtNonce": {string(m[1])}}
	body, err := a.PostForm(ctx, uri, form)
	if err != nil {
		return nil, err
	}
	rates, err := parse(body)
	if err != nil {
		return nil, err
	}

	out := rates[:0]
	for _, r := range rates {
		if !after.IsZero() && r.Date.Before(after) {
			continue
		}
		if !upto.IsZero() && r.Date.After(upto) {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

func parse(data []byte) ([]adapter.Rate, error) {
	var doc struct {
		Data *[][]any `json:"data"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if doc.Data == nil {
		return nil, errors.New("data array missing from WPDataTables response")
	}

	var rates []adapter.Rate
	for _, row := range *doc.Data {
		r, ok, err := parseRow(row)
		if err != nil {
			return nil, err
		}
		if ok {
			rates = append(rates, r)
		}
	}
	return rates, nil
}

func parseRow(row []any) (adapter.Rate, bool, error) {
	cell := func(i int) any {
		if i < len(row) {
			return row[i]
		}
		return nil
	}
	if cell(0) == nil || cell(1) == nil {
		return adapter.Rate{}, false, nil
	}
	dateStr, ok1 := cell(0).(string)
	name, ok2 := cell(1).(string)
	if !ok1 || !ok2 {
		return adapter.Rate{}, false, fmt.Errorf("unexpected row %v", row)
	}

	date, err := time.Parse("2 Jan 2006", strings.TrimSpace(dateStr))
	if err != nil {
		return adapter.Rate{}, false, err
	}
	iso, ok := currencies[strings.ToUpper(strings.TrimSpace(name))]
	if !ok {
		return adapter.Rate{}, false, nil
	}

	sell, err := toFloat(cell(2))
	if err != nil {
		return adapter.Rate{}, false, err
	}
	buy, err := toFloat(cell(3))
	if err != nil {
		return adapter.Rate{}, false, err
	}
	rate := sell
	if buy != 0 {
		rate = adapter.Midpoint(sell, buy)
	}
	if rate == 0 {
		return adapter.Rate{}, false, nil
	}

	return adapter.Rate{
		Date:  date,
		Base:  iso,
		Quote: "JMD",
		Rate:  rate,
		Bid:   adapter.Float(buy),
		Ask:   adapter.Float(sell),
	}, true, nil
}

// toFloat mirrors Ruby's Float(), which raises on nil and on malformed text.
func toFloat(v any) (float64, error) {
	switch x := v.(type) {
	case float64:
		return x, nil
	case string:
		if f, ok := adapter.ParseFloat(x); ok {
			return f, nil
		}
	}
	return 0, fmt.Errorf("invalid price %v", v)
}
