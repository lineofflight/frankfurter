// Package ust fetches the U.S. Department of the Treasury reporting rates of exchange. Quarterly: one figure per
// currency, foreign units per USD, effective from the quarter end for the following quarter's federal reporting, with
// mid-quarter amendments carrying their own effective date. Frequency quarterly, so these never blend (#646, #647).
//
// Rows are keyed by a "Country-Currency" label, not an ISO code, and the labels drift: a currency can appear under
// several labels over the years, and a label can keep a predecessor's magnitudes past a redenomination. currencies
// maps each label we relay to a code, with date bounds where a label's values change unit; anything unmapped is
// dropped, and a label listed earlier wins when two map to the same pair on the same date.
//
// Unlike most adapters, Fetch treats after as inclusive: a fresh backfill starts on coverage_start, and a re-fetch
// from last_synced picks up amendments effective that day. The insert is conflict-free, so replaying a day is free.
package ust

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const (
	apiURL   = "https://api.fiscaldata.treasury.gov/services/api/fiscal_service/v1/accounting/od/rates_of_exchange"
	pageSize = 10000
	// Amendments to a quarter land up to two months after its record date, under that record date.
	amendmentWindowMonths = 4
)

func init() {
	adapter.Register("UST", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches UST rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

type row struct {
	RecordDate    string `json:"record_date"`
	EffectiveDate string `json:"effective_date"`
	Label         string `json:"country_currency_desc"`
	ExchangeRate  string `json:"exchange_rate"`
}

type page struct {
	Data []row `json:"data"`
	Meta struct {
		TotalPages *int `json:"total-pages"`
	} `json:"meta"`
}

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	if upto.IsZero() {
		upto = a.Today()
	}
	var rows []row
	for n := 1; ; n++ {
		body, err := a.Get(ctx, apiURL, query(after, n))
		if err != nil {
			return nil, err
		}
		var p page
		if err := json.Unmarshal(body, &p); err != nil {
			return nil, err
		}
		rows = append(rows, p.Data...)
		total := 1
		if p.Meta.TotalPages != nil {
			total = *p.Meta.TotalPages
		}
		if n >= total {
			break
		}
	}

	// Parse once over every page, so a record date straddling a page boundary still collapses to one row per pair.
	rates, err := parse(rows)
	if err != nil {
		return nil, err
	}
	var out []adapter.Rate
	for _, r := range rates {
		if (after.IsZero() || !r.Date.Before(after)) && !r.Date.After(upto) {
			out = append(out, r)
		}
	}
	return out, nil
}

func query(after time.Time, n int) url.Values {
	q := url.Values{
		"page[size]":   {strconv.Itoa(pageSize)},
		"page[number]": {strconv.Itoa(n)},
		"sort":         {"record_date"},
		"fields":       {"record_date,effective_date,country_currency_desc,exchange_rate"},
	}
	if !after.IsZero() {
		q.Set("filter", "record_date:gte:"+monthsBefore(after, amendmentWindowMonths).Format("2006-01-02"))
	}
	return q
}

// monthsBefore is Ruby's Date#<<: it clamps to the last day of the target month rather than overflowing into the
// next, so 2026-06-30 << 4 is 2026-02-28.
func monthsBefore(d time.Time, months int) time.Time {
	first := time.Date(d.Year(), d.Month()-time.Month(months), 1, 0, 0, 0, 0, time.UTC)
	last := first.AddDate(0, 1, -1).Day()
	return first.AddDate(0, 0, min(d.Day(), last)-1)
}

// parse returns one record per (date, quote): amendments carry their own effective date, and when two labels reach
// the same pair on the same date the one listed first in currencies wins.
func parse(rows []row) ([]adapter.Rate, error) {
	type key struct {
		date  time.Time
		quote string
	}
	type slot struct{ index, priority int }
	var out []adapter.Rate
	seen := map[key]slot{}
	for _, r := range rows {
		p, ok := priority[r.Label]
		if !ok {
			continue
		}
		date, err := time.Parse("2006-01-02", r.EffectiveDate)
		if err != nil {
			return nil, fmt.Errorf("effective date %q: %w", r.EffectiveDate, err)
		}
		code := codeFor(currencies[p].spans, date)
		if code == "" {
			continue
		}
		rate, err := strconv.ParseFloat(r.ExchangeRate, 64)
		if err != nil {
			return nil, fmt.Errorf("exchange rate %q: %w", r.ExchangeRate, err)
		}
		rec := adapter.Rate{Date: date, Base: "USD", Quote: code, Rate: rate}
		k := key{date, code}
		if s, ok := seen[k]; ok {
			if p < s.priority {
				out[s.index] = rec
				seen[k] = slot{s.index, p}
			}
			continue
		}
		seen[k] = slot{len(out), p}
		out = append(out, rec)
	}
	return out, nil
}

// codeFor returns the code a label maps to on date, or "" when none of its spans covers it.
func codeFor(spans []span, date time.Time) string {
	d := date.Format("2006-01-02")
	for _, s := range spans {
		if s.from != "" && d < s.from {
			continue
		}
		if s.until != "" && d >= s.until {
			continue
		}
		return s.code
	}
	return ""
}

// span maps a label to code from from (inclusive) until until (exclusive); an empty bound is open. Dates are
// YYYY-MM-DD, so they compare as strings.
type span struct {
	code, from, until string
}

var priority = func() map[string]int {
	m := make(map[string]int, len(currencies))
	for i, c := range currencies {
		m[c.label] = i
	}
	return m
}()

// currencies is ordered: a label listed earlier wins a shared pair.
var currencies = []struct {
	label string
	spans []span
}{
	{"Euro Zone-Euro", []span{{"EUR", "", ""}}},
	{"Afghanistan-Afghani", []span{{"AFN", "2003-01-01", ""}}},
	{"Albania-Lek", []span{{"ALL", "", ""}}},
	{"Algeria-Dinar", []span{{"DZD", "", ""}}},
	{"Angola-Kwanza", []span{{"AOA", "", ""}}},
	{"Antigua & Barbuda-East Caribbean Dollar", []span{{"XCD", "", ""}}},
	{"Argentina-Peso", []span{{"ARS", "", ""}}},
	{"Armenia-Dram", []span{{"AMD", "", ""}}},
	{"Australia-Dollar", []span{{"AUD", "", ""}}},
	{"Austria-Schilling", []span{{"ATS", "", "2002-03-01"}}},
	{"Azerbaijan-Manat", []span{{"AZN", "", ""}}},
	{"Azerbaijan-Second Manat", []span{{"AZM", "", "2006-01-01"}}},
	{"Bahamas-Dollar", []span{{"BSD", "", ""}}},
	{"Bahrain-Dinar", []span{{"BHD", "", ""}}},
	{"Bangladesh-Taka", []span{{"BDT", "", ""}}},
	{"Barbados-Dollar", []span{{"BBD", "", ""}}},
	{"Belarus-New Ruble", []span{{"BYN", "", ""}}},
	{"Belarus-Ruble", []span{{"BYR", "", "2016-07-01"}}},
	{"Belgium-Francs", []span{{"BEF", "", "2002-03-01"}}},
	{"Belize-Dollar", []span{{"BZD", "", ""}}},
	{"Benin-Cfa Franc", []span{{"XOF", "", ""}}},
	{"Bermuda-Dollar", []span{{"BMD", "", ""}}},
	{"Bolivia-Boliviano", []span{{"BOB", "", ""}}},
	{"Bosnia-Marka", []span{{"BAM", "", ""}}},
	{"Botswana-Pula", []span{{"BWP", "", ""}}},
	{"Brazil-Real", []span{{"BRL", "", ""}}},
	{"Brunei-Dollar", []span{{"BND", "", ""}}},
	{"Bulgaria-Lev New", []span{{"BGN", "", ""}}},
	{"Burkina Faso-Cfa Franc", []span{{"XOF", "", ""}}},
	{"Burma-Kyat", []span{{"MMK", "", ""}}},
	{"Myanmar-Kyat", []span{{"MMK", "", ""}}},
	{"Burundi-Franc", []span{{"BIF", "", ""}}},
	{"Cambodia-Riel", []span{{"KHR", "", ""}}},
	{"Cameroon-Cfa Franc", []span{{"XAF", "", ""}}},
	{"Canada-Dollar", []span{{"CAD", "", ""}}},
	{"Cape Verde-Escudo", []span{{"CVE", "", ""}}},
	{"Cayman Islands-Dollar", []span{{"KYD", "", ""}}},
	{"Central African Republic-Cfa Franc", []span{{"XAF", "", ""}}},
	{"Chad-Cfa Franc", []span{{"XAF", "", ""}}},
	{"Chile-Peso", []span{{"CLP", "", ""}}},
	{"China-Renminbi", []span{{"CNY", "", ""}}},
	{"Colombia-Peso", []span{{"COP", "", ""}}},
	{"Comoros-Franc", []span{{"KMF", "", ""}}},
	{"Congo-Cfa Franc", []span{{"XAF", "", ""}}},
	{"Costa Rica-Colon", []span{{"CRC", "", ""}}},
	{"Cote D'Ivoire-Cfa Franc", []span{{"XOF", "", ""}}},
	{"Croatia-Kuna", []span{{"HRK", "", "2023-01-01"}}},
	{"Cuba-Chavito", []span{{"CUC", "", ""}}},
	{"Cuba-Peso", []span{{"CUP", "", ""}}},
	{"Curacao-Caribbean Guilder", []span{{"XCG", "", ""}}},
	{"Cyprus-Pound", []span{{"CYP", "", "2008-01-01"}}},
	{"Czech Republic-Koruna", []span{{"CZK", "", ""}}},
	{"Democratic Republic Of Congo-Congolese Franc", []span{{"CDF", "", ""}}},
	{"Democratic Republic Of Congo-Franc", []span{{"CDF", "", ""}}},
	{"Denmark-Krone", []span{{"DKK", "", ""}}},
	{"Djibouti-Franc", []span{{"DJF", "", ""}}},
	{"Dominican Republic-Peso", []span{{"DOP", "", ""}}},
	{"Egypt-Pound", []span{{"EGP", "", ""}}},
	{"El Salvador-Colon", []span{{"SVC", "", "2015-01-01"}}},
	{"Equatorial Guinea-Cfa Franc", []span{{"XAF", "", ""}}},
	{"Eritrea-Nakfa", []span{{"ERN", "", ""}}},
	{"Estonia-Kroon", []span{{"EEK", "", "2011-01-01"}}},
	{"Eswatini-Lilangeni", []span{{"SZL", "", ""}}},
	{"Swaziland-Lilangeni", []span{{"SZL", "", ""}}},
	{"Ethiopia-Birr", []span{{"ETB", "", ""}}},
	{"Fiji-Dollar", []span{{"FJD", "", ""}}},
	{"Finland-Markka", []span{{"FIM", "", "2002-03-01"}}},
	{"France-Franc", []span{{"FRF", "", "2002-03-01"}}},
	{"Gabon-Cfa Franc", []span{{"XAF", "", ""}}},
	{"Gambia-Dalasi", []span{{"GMD", "", ""}}},
	{"Georgia-Lari", []span{{"GEL", "", ""}}},
	{"Germany-Mark", []span{{"DEM", "", "2002-03-01"}}},
	{"Ghana-Cedi", []span{{"GHS", "", ""}}},
	{"Ghana-Second Cedi", []span{{"GHC", "", "2007-07-01"}, {"GHS", "2007-07-01", "2015-01-01"}}},
	{"Greece-Drachma", []span{{"GRD", "", "2002-03-01"}}},
	{"Grenada-East Caribbean Dollar", []span{{"XCD", "", ""}}},
	{"Guatemala-Quetzal", []span{{"GTQ", "", ""}}},
	{"Guinea Bissau-Cfa Franc", []span{{"XOF", "", ""}}},
	{"Guinea-Franc", []span{{"GNF", "", ""}}},
	{"Guyana-Dollar", []span{{"GYD", "", ""}}},
	{"Haiti-Gourde", []span{{"HTG", "", ""}}},
	{"Honduras-Lempira", []span{{"HNL", "", ""}}},
	{"Hong Kong-Dollar", []span{{"HKD", "", ""}}},
	{"Hungary-Forint", []span{{"HUF", "", ""}}},
	{"Iceland-Krona", []span{{"ISK", "", ""}}},
	{"India-Rupee", []span{{"INR", "", ""}}},
	{"Indonesia-Rupiah", []span{{"IDR", "", ""}}},
	{"Iran-Rial", []span{{"IRR", "", ""}}},
	{"Iraq-Dinar", []span{{"IQD", "", ""}}},
	{"Ireland-Pound", []span{{"IEP", "", "2002-03-01"}}},
	{"Israel-Shekel", []span{{"ILS", "", ""}}},
	{"Italy-Lira", []span{{"ITL", "", "2002-03-01"}}},
	{"Jamaica-Dollar", []span{{"JMD", "", ""}}},
	{"Japan-Yen", []span{{"JPY", "", ""}}},
	{"Jordan-Dinar", []span{{"JOD", "", ""}}},
	{"Kazakhstan-Tenge", []span{{"KZT", "", ""}}},
	{"Kenya-Shilling", []span{{"KES", "", ""}}},
	{"Korea-Won", []span{{"KRW", "", ""}}},
	{"Kuwait-Dinar", []span{{"KWD", "", ""}}},
	{"Kyrgyzstan-Som", []span{{"KGS", "", ""}}},
	{"Laos-Kip", []span{{"LAK", "", ""}}},
	{"Latvia-Lats", []span{{"LVL", "", "2014-01-01"}}},
	{"Lebanon-Pound", []span{{"LBP", "", ""}}},
	{"Lesotho-Maloti", []span{{"LSL", "", ""}}},
	{"Liberia-Dollar", []span{{"LRD", "", ""}}},
	{"Libya-Dinar", []span{{"LYD", "", ""}}},
	{"Lithuania-Lita", []span{{"LTL", "", "2015-01-01"}}},
	{"Luxembourg-Franc", []span{{"LUF", "", "2002-03-01"}}},
	{"Macao-Mop", []span{{"MOP", "", ""}}},
	{"Madagascar-Ariary", []span{{"MGA", "", ""}}},
	{"Malawi-Kwacha", []span{{"MWK", "", ""}}},
	{"Malaysia-Ringgit", []span{{"MYR", "", ""}}},
	{"Maldives-Rufiyaa", []span{{"MVR", "", ""}}},
	{"Mali-Cfa Franc", []span{{"XOF", "", ""}}},
	{"Maltese-Lira", []span{{"MTL", "", "2008-01-01"}}},
	{"Mauritania-First Ouguiya", []span{{"MRO", "", ""}}},
	{"Mauritania-Ouguiya", []span{{"MRO", "", "2018-06-30"}, {"MRU", "2018-06-30", ""}}},
	{"Mauritius-Rupee", []span{{"MUR", "", ""}}},
	{"Mexico-Peso", []span{{"MXN", "", ""}}},
	{"Moldova-Leu", []span{{"MDL", "", ""}}},
	{"Mongolia-Tugrik", []span{{"MNT", "", ""}}},
	{"Morocco-Dirham", []span{{"MAD", "", ""}}},
	{"Mozambique-Metical", []span{{"MZN", "", ""}}},
	{"Namibia-Dollar", []span{{"NAD", "", ""}}},
	{"Nepal-Rupee", []span{{"NPR", "", ""}}},
	{"Netherlands Antilles-Guilder", []span{{"ANG", "", ""}}},
	{"Netherlands-Guilder", []span{{"NLG", "", "2002-03-01"}}},
	{"New Zealand-Dollar", []span{{"NZD", "", ""}}},
	{"Nicaragua-Cordoba", []span{{"NIO", "", ""}}},
	{"Niger-Cfa Franc", []span{{"XOF", "", ""}}},
	{"Nigeria-Naira", []span{{"NGN", "", ""}}},
	{"Norway-Krone", []span{{"NOK", "", ""}}},
	{"Oman-Rial", []span{{"OMR", "", ""}}},
	{"Pakistan-Rupee", []span{{"PKR", "", ""}}},
	{"Papua New Guinea-Kina", []span{{"PGK", "", ""}}},
	{"Paraguay-Guarani", []span{{"PYG", "", ""}}},
	{"Peru-Sol", []span{{"PEN", "", ""}}},
	{"Philippines-Peso", []span{{"PHP", "", ""}}},
	{"Poland-Zloty", []span{{"PLN", "", ""}}},
	{"Portugal-Escudo", []span{{"PTE", "", "2002-03-01"}}},
	{"Qatar-Riyal", []span{{"QAR", "", ""}}},
	{"Republic Of North Macedonia-Denar", []span{{"MKD", "", ""}}},
	{"Romania-New Leu", []span{{"RON", "", ""}}},
	{"Romania-Third Leu", []span{{"ROL", "", "2005-07-01"}}},
	{"Russia-Ruble", []span{{"RUB", "", ""}}},
	{"Rwanda-Franc", []span{{"RWF", "", ""}}},
	{"Sao Tome & Principe-New Dobras", []span{{"STN", "", ""}}},
	{"Sao Tome & Principe-Dobras", []span{{"STD", "", "2018-01-01"}}},
	{"Saudi Arabia-Riyal", []span{{"SAR", "", ""}}},
	{"Senegal-Cfa Franc", []span{{"XOF", "", ""}}},
	{"Serbia-Dinar", []span{{"RSD", "", ""}}},
	{"Seychelles-Rupee", []span{{"SCR", "", ""}}},
	{"Sierra Leone-Leone", []span{{"SLL", "", "2022-07-01"}, {"SLE", "2022-07-01", ""}}},
	{"Singapore-Dollar", []span{{"SGD", "", ""}}},
	{"Slovak-Korun", []span{{"SKK", "", "2009-01-01"}}},
	{"Slovenia-Tolars", []span{{"SIT", "", "2007-01-01"}}},
	{"Solomon Islands-Dollar", []span{{"SBD", "", ""}}},
	{"Somali-Shilling", []span{{"SOS", "", ""}}},
	{"South Africa-Rand", []span{{"ZAR", "", ""}}},
	{"South Sudan-Sudanese Pound", []span{{"SSP", "", ""}}},
	{"Spain-Peseta", []span{{"ESP", "", "2002-03-01"}}},
	{"Sri Lanka-Rupee", []span{{"LKR", "", ""}}},
	{"St. Lucia-East Caribbean Dollar", []span{{"XCD", "", ""}}},
	{"Sudan-Pound", []span{{"SDG", "", ""}}},
	{"Sudan-Sudanese Pound", []span{{"SDG", "", ""}}},
	{"Sudan-Dinar", []span{{"SDG", "2007-07-01", ""}}},
	{"Suriname-Dollar", []span{{"SRD", "", ""}}},
	{"Suriname-Guilder", []span{{"SRD", "2004-07-01", ""}}},
	{"Sweden-Krona", []span{{"SEK", "", ""}}},
	{"Switzerland-Franc", []span{{"CHF", "", ""}}},
	{"Syria-Pound", []span{{"SYP", "", ""}}},
	{"Taiwan-Dollar", []span{{"TWD", "", ""}}},
	{"Tajikistan-Somoni", []span{{"TJS", "", ""}}},
	{"Tanzania-Shilling", []span{{"TZS", "", ""}}},
	{"Thailand-Baht", []span{{"THB", "", ""}}},
	{"Togo-Cfa Franc", []span{{"XOF", "", ""}}},
	{"Tonga-Pa'Anga", []span{{"TOP", "", ""}}},
	{"Trinidad & Tobago-Dollar", []span{{"TTD", "", ""}}},
	{"Tunisia-Dinar", []span{{"TND", "", ""}}},
	{"Turkey-New Lira", []span{{"TRY", "", ""}}},
	{"Turkey-Lira", []span{{"TRL", "", "2005-01-01"}}},
	{"Turkmenistan-New Manat", []span{{"TMT", "", ""}}},
	{"Turkmenistan-Manat", []span{{"TMM", "", "2009-01-01"}}},
	{"Uganda-Shilling", []span{{"UGX", "", ""}}},
	{"Ukraine-Hryvnia", []span{{"UAH", "", ""}}},
	{"United Arab Emirates-Dirham", []span{{"AED", "", ""}}},
	{"United Kingdom-Pound", []span{{"GBP", "", ""}}},
	{"Uruguay-Peso", []span{{"UYU", "", ""}}},
	{"Uzbekistan-Som", []span{{"UZS", "", ""}}},
	{"Vanuatu-Vatu", []span{{"VUV", "", ""}}},
	{"Venezuela-Bolivar Soberano", []span{{"VES", "", ""}}},
	{"Venezuela-Bolivar", []span{{"VEF", "", ""}}},
	{"Venezuela-Soberano", []span{{"VEF", "2008-01-01", ""}}},
	{"Vietnam-Dong", []span{{"VND", "", ""}}},
	{"Western Samoa-Tala", []span{{"WST", "", ""}}},
	{"Yemen-Rial", []span{{"YER", "", ""}}},
	{"Zambia-New Kwacha", []span{{"ZMW", "", ""}}},
	{"Zambia-Kwacha", []span{{"ZMK", "", "2013-01-01"}}},
	{"Zimbabwe-Gold", []span{{"ZWG", "", ""}}},
	{"Zimbabwe-Rtgs", []span{{"ZWL", "2019-02-22", ""}}},
}
