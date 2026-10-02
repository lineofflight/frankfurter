// Package imf fetches the International Monetary Fund's representative exchange
// rates: daily rates for 50+ currencies, mostly foreign currency per USD, with
// currencies marked (1) quoted as USD per foreign unit. It also takes USD/XDR
// from the SDR cross-rate report.
//
// Fetch treats after as inclusive, as the Ruby adapter does, and walks calendar
// months from after's month, or from the first report's when after is earlier.
package imf

import (
	"context"
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

const baseURL = "https://www.imf.org/external/np/fin/data/rms_mth.aspx"

// coverageStart is the first month with a report. Earlier months redirect to
// an error page.
var coverageStart = adapter.Date(2003, 4, 1)

var currencies = map[string]string{
	"afghan afghani":      "AFN",
	"algerian dinar":      "DZD",
	"angolan kwanza":      "AOA",
	"australian dollar":   "AUD",
	"bahamian dollar":     "BSD",
	"bahrain dinar":       "BHD",
	"bangladeshi taka":    "BDT",
	"barbados dollar":     "BBD",
	"botswana pula":       "BWP",
	"brazilian real":      "BRL",
	"brunei dollar":       "BND",
	"canadian dollar":     "CAD",
	"chilean peso":        "CLP",
	"chinese yuan":        "CNY",
	"colombian peso":      "COP",
	"costa rican colon":   "CRC",
	"czech koruna":        "CZK",
	"danish krone":        "DKK",
	"djibouti franc":      "DJF",
	"egyptian pound":      "EGP",
	"euro":                "EUR",
	"fiji dollar":         "FJD",
	"hungarian forint":    "HUF",
	"icelandic krona":     "ISK",
	"indian rupee":        "INR",
	"iranian rial":        "IRR",
	"israeli new shekel":  "ILS",
	"japanese yen":        "JPY",
	"jordanian dinar":     "JOD",
	"kazakhstani tenge":   "KZT",
	"kuwaiti dinar":       "KWD",
	"libyan dinar":        "LYD",
	"malaysian ringgit":   "MYR",
	"mauritian rupee":     "MUR",
	"mexican peso":        "MXN",
	"moldovan leu":        "MDL",
	"mongolian tugrik":    "MNT",
	"moroccan dirham":     "MAD",
	"mozambican metical":  "MZN",
	"myanmar kyat":        "MMK",
	"namibian dollar":     "NAD",
	"nepalese rupee":      "NPR",
	"new zealand dollar":  "NZD",
	"nigerian naira":      "NGN",
	"norwegian krone":     "NOK",
	"omani rial":          "OMR",
	"pakistani rupee":     "PKR",
	"peruvian sol":        "PEN",
	"philippine peso":     "PHP",
	"polish zloty":        "PLN",
	"qatari riyal":        "QAR",
	"romanian leu":        "RON",
	"russian ruble":       "RUB",
	"rwandan franc":       "RWF",
	"samoan tala":         "WST",
	"saudi arabian riyal": "SAR",
	"singapore dollar":    "SGD",
	"south african rand":  "ZAR",
	"korean won":          "KRW",
	"sri lankan rupee":    "LKR",
	"swedish krona":       "SEK",
	"swiss franc":         "CHF",
	"thai baht":           "THB",
	"trinidadian dollar":  "TTD",
	"tunisian dinar":      "TND",
	"ugandan shilling":    "UGX",
	"u.a.e. dirham":       "AED",
	"u.k. pound":          "GBP",
	"u.s. dollar":         "USD",
	"uruguayan peso":      "UYU",
	"venezuelan bolivar":  "VES",
	"yemeni rial":         "YER",
	"zambian kwacha":      "ZMW",
}

func init() {
	adapter.Register("IMF", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches IMF rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	if after.IsZero() {
		return nil, errors.New("fetch needs a start date")
	}
	end := upto
	if end.IsZero() {
		end = a.Today()
	}

	start := after
	if start.Before(coverageStart) {
		start = coverageStart
	}
	var rates []adapter.Rate
	for cursor := adapter.Date(start.Year(), start.Month(), 1); !cursor.After(end); cursor = cursor.AddDate(0, 1, 0) {
		lastDay := cursor.AddDate(0, 1, -1)

		body, err := a.fetchMonth(ctx, lastDay, "REP")
		if err != nil {
			return nil, err
		}
		rep, err := parse(string(body))
		if err != nil {
			return nil, err
		}
		rates = append(rates, rep...)

		body, err = a.fetchMonth(ctx, lastDay, "SDRCV")
		if err != nil {
			return nil, err
		}
		sdr, err := parseSDRCV(string(body))
		if err != nil {
			return nil, err
		}
		rates = append(rates, sdr...)
	}

	kept := rates[:0]
	for _, r := range rates {
		if !r.Date.Before(after) {
			kept = append(kept, r)
		}
	}
	return kept, nil
}

func (a *Adapter) fetchMonth(ctx context.Context, lastDay time.Time, reportType string) ([]byte, error) {
	return a.Get(ctx, baseURL, url.Values{
		"SelectDate": {lastDay.Format(time.DateOnly)},
		"reportType": {reportType},
		"tsvflag":    {"Y"},
	})
}

func parse(tsv string) ([]adapter.Rate, error) {
	return parseRows(tsv, func(name string, rate float64, date time.Time) (adapter.Rate, bool) {
		indirect := strings.HasSuffix(name, "(1)")
		iso, ok := currencies[strings.ToLower(strings.TrimSpace(strings.TrimSuffix(name, "(1)")))]
		if !ok || iso == "USD" {
			return adapter.Rate{}, false
		}
		if indirect {
			return adapter.Rate{Date: date, Base: iso, Quote: "USD", Rate: rate}, true
		}
		return adapter.Rate{Date: date, Base: "USD", Quote: iso, Rate: rate}, true
	})
}

// parseSDRCV reads the "SDRs per Currency unit" report and keeps only USD/XDR.
// The other pairs are derived by the IMF from the representative series and
// would create ambiguous bridges in base conversion.
func parseSDRCV(tsv string) ([]adapter.Rate, error) {
	return parseRows(tsv, func(name string, rate float64, date time.Time) (adapter.Rate, bool) {
		if currencies[strings.ToLower(name)] != "USD" {
			return adapter.Rate{}, false
		}
		return adapter.Rate{Date: date, Base: "USD", Quote: "XDR", Rate: rate}, true
	})
}

var nonNumeric = regexp.MustCompile(`[^0-9.-]`)

// parseRows walks every "Currency" block in the report; each block has its own
// date header.
func parseRows(tsv string, row func(name string, rate float64, date time.Time) (adapter.Rate, bool)) ([]adapter.Rate, error) {
	if strings.TrimSpace(tsv) == "" {
		return nil, errors.New("empty TSV response")
	}

	var rates []adapter.Rate
	var dates []time.Time
	for line := range strings.Lines(tsv) {
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}

		if strings.HasPrefix(line, "Currency\t") {
			headers := splitTabs(line)[1:]
			dates = make([]time.Time, len(headers))
			for i, h := range headers {
				d, err := adapter.ParseDate(strings.TrimSpace(h), "January 02, 2006", "January 2, 2006")
				if err != nil {
					return nil, fmt.Errorf("unparseable date header %q", h)
				}
				dates[i] = d
			}
			continue
		}
		if dates == nil {
			continue
		}

		cols := splitTabs(line)
		name := strings.TrimSpace(cols[0])
		for i, value := range cols[1:] {
			if i >= len(dates) {
				break
			}
			cleaned := nonNumeric.ReplaceAllString(strings.ReplaceAll(value, ",", ""), "")
			if cleaned == "" {
				continue
			}
			rate, err := strconv.ParseFloat(cleaned, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid rate %q for %s", value, name)
			}
			if rate == 0 {
				continue
			}
			if r, ok := row(name, rate, dates[i]); ok {
				rates = append(rates, r)
			}
		}
	}
	return rates, nil
}

// splitTabs splits like Ruby's String#split, dropping trailing empty fields, so
// a header ending in a tab doesn't yield an empty date.
func splitTabs(line string) []string {
	fields := strings.Split(line, "\t")
	for len(fields) > 1 && fields[len(fields)-1] == "" {
		fields = fields[:len(fields)-1]
	}
	return fields
}
