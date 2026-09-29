// Package cbk fetches rates from the Central Bank of Kenya, which publishes daily exchange rates in KES.
//
// Rates come from two WPDataTables endpoints: table 32 (2003-2024) and table 193 (2024+). Most rows are quoted as KES
// per foreign unit (foreign base); the East African cross rates ("KES / USHS" and friends) are quoted as foreign per
// KES (KES base). Both are recorded as published.
//
// Unlike most adapters, Fetch treats after as inclusive, as the Ruby adapter does.
package cbk

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

const baseURL = "https://www.centralbank.go.ke/wp-admin/admin-ajax.php"

// currencies is ordered: names are matched by substring in this order when no exact match exists.
var currencies = []struct{ name, code string }{
	{"US DOLLAR", "USD"},
	{"STG POUND", "GBP"},
	{"EURO", "EUR"},
	{"JAPANESE YEN", "JPY"},
	{"JPY", "JPY"},
	{"SWISS FRANC", "CHF"},
	{"S FRANC", "CHF"},
	{"CAN DOLLAR", "CAD"},
	{"CAN $", "CAD"},
	{"AUSTRALIAN DOLLAR", "AUD"},
	{"AUSTRALIAN $", "AUD"},
	{"INDIAN RUPEE", "INR"},
	{"IND RUPEE", "INR"},
	{"SWEDISH KRONA", "SEK"},
	{"SW KRONER", "SEK"},
	{"NORWEGIAN KRONE", "NOK"},
	{"NOR KRONER", "NOK"},
	{"DAN KRONER", "DKK"},
	{"CHINESE YUAN", "CNY"},
	{"S. AFRICAN RAND", "ZAR"},
	{"SA RAND", "ZAR"},
	{"AE DIRHAM", "AED"},
	{"UAE DIRHAM", "AED"},
	{"HONGKONG DOLLAR", "HKD"},
	{"SINGAPORE DOLLAR", "SGD"},
	{"SAUDI RIYAL", "SAR"},
	{"USHS", "UGX"},
	{"TSHS", "TZS"},
	{"RWF", "RWF"},
	{"BIF", "BIF"},
}

var (
	crossRate  = regexp.MustCompile(`KE[SN][\s/]|KEN SHILLING\s*/`)
	unitMarker = regexp.MustCompile(`\s*\((\d+)\)\s*$`)
)

func init() {
	adapter.Register("CBK", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches CBK rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	var rates []adapter.Rate
	tables := []int{193}
	if after.IsZero() || after.Before(adapter.Date(2024, 1, 5)) {
		tables = []int{32, 193}
	}
	for _, id := range tables {
		parsed, err := a.fetchTable(ctx, id)
		if err != nil {
			return nil, err
		}
		rates = append(rates, parsed...)
	}

	var out []adapter.Rate
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

func (a *Adapter) fetchTable(ctx context.Context, id int) ([]adapter.Rate, error) {
	u := fmt.Sprintf("%s?action=get_wdtable&table_id=%d", baseURL, id)
	body, err := a.PostForm(ctx, u, url.Values{"draw": {"1"}, "start": {"0"}, "length": {"-1"}})
	if err != nil {
		return nil, err
	}
	return parse(body)
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
	if len(row) < 2 || row[0] == nil || row[1] == nil {
		return adapter.Rate{}, false, nil
	}
	dateStr, ok1 := row[0].(string)
	name, ok2 := row[1].(string)
	if !ok1 || !ok2 {
		return adapter.Rate{}, false, fmt.Errorf("unexpected row %v", row)
	}

	date, err := time.Parse("02/01/2006", strings.TrimSpace(dateStr))
	if err != nil {
		return adapter.Rate{}, false, err
	}
	code, cross := resolveCurrency(strings.TrimSpace(name))
	if code == "" {
		return adapter.Rate{}, false, nil
	}

	units := 1
	if m := unitMarker.FindStringSubmatch(name); m != nil {
		units, _ = strconv.Atoi(m[1])
	}
	value, err := rateValue(row[2:])
	if err != nil {
		return adapter.Rate{}, false, err
	}
	if value == 0 {
		return adapter.Rate{}, false, nil
	}
	if units > 1 {
		value /= float64(units)
	}

	if cross {
		return adapter.Rate{Date: date, Base: "KES", Quote: code, Rate: value}, true, nil
	}
	return adapter.Rate{Date: date, Base: code, Quote: "KES", Rate: value}, true, nil
}

// rateValue is Ruby's Float(rest[0]), which raises on anything unparseable.
func rateValue(rest []any) (float64, error) {
	if len(rest) == 0 {
		return 0, errors.New("rate missing")
	}
	switch v := rest[0].(type) {
	case float64:
		return v, nil
	case string:
		if f, ok := adapter.ParseFloat(v); ok {
			return f, nil
		}
	}
	return 0, fmt.Errorf("invalid rate %v", rest[0])
}

func resolveCurrency(name string) (string, bool) {
	upper := strings.ToUpper(name)

	// East African cross-rate patterns like "KES / USHS" or "KEN SHILLING / USHS".
	if crossRate.MatchString(upper) {
		parts := strings.Split(upper, "/")
		if code := lookup(strings.TrimSpace(parts[len(parts)-1])); code != "" {
			return code, true
		}
	}

	clean := unitMarker.ReplaceAllString(upper, "")
	if code := lookup(clean); code != "" {
		return code, false
	}
	for _, c := range currencies {
		if strings.Contains(clean, c.name) {
			return c.code, false
		}
	}
	return "", false
}

func lookup(name string) string {
	for _, c := range currencies {
		if c.name == name {
			return c.code
		}
	}
	return ""
}
