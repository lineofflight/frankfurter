package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lineofflight/frankfurter/go/internal/currency"
	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/provider"
	"github.com/lineofflight/frankfurter/go/internal/ratequery"
)

// v2Currency is Currency#to_h; Providers and Peg are to_h_with_providers's
// additions.
type v2Currency struct {
	ISOCode    string    `json:"iso_code"`
	ISONumeric *string   `json:"iso_numeric"`
	Name       string    `json:"name"`
	Symbol     *string   `json:"symbol"`
	StartDate  string    `json:"start_date"`
	EndDate    string    `json:"end_date"`
	Providers  *[]string `json:"providers,omitempty"`
	Peg        *v2Peg    `json:"peg,omitempty"`
}

type v2Peg struct {
	Base      string  `json:"base"`
	Rate      float64 `json:"rate"`
	Authority string  `json:"authority"`
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func currencyHash(c currency.Currency) v2Currency {
	out := v2Currency{ISOCode: c.ISOCode, Name: c.ISOCode, StartDate: dateText(c.StartDate), EndDate: dateText(c.EndDate)}
	if info, ok := c.Metadata(); ok {
		out.Name = info.Name
		if currency.HasISONumeric(c.ISOCode) {
			out.ISONumeric = &info.ISONumeric
		}
		if currency.HasSymbol(c.ISOCode) {
			out.Symbol = &info.Symbol
		}
	}
	return out
}

func dateText(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return db.FormatDate(t)
}

func (c *v2Request) currency(code string) {
	found, err := currency.FindCurrency(c.ctx(), c.s.DB, code)
	if err != nil {
		c.fail(err)
		return
	}
	if found == nil {
		c.notFound()
		return
	}
	out := currencyHash(*found)
	providers, err := currency.Providers(c.ctx(), c.s.DB, found.ISOCode)
	if err != nil {
		c.fail(err)
		return
	}
	out.Providers = &providers
	if p := found.Peg; p != nil {
		out.Peg = &v2Peg{Base: p.Base, Rate: p.Rate, Authority: p.Authority}
	}
	writeJSON(c.w, http.StatusOK, contentTypeV2, out)
}

func (c *v2Request) currencies() {
	params := c.query()
	if c.paramsErr != nil {
		c.fail(c.paramsErr)
		return
	}
	scope, hasScope := params["scope"]
	if hasScope && scope != "all" {
		c.fail(&ratequery.ValidationError{Message: "invalid scope"})
		return
	}
	var (
		records []currency.Currency
		err     error
	)
	switch keys, ok := params["providers"]; {
	case ok && keys != nil:
		s, isString := keys.(string)
		if !isString || !validUTF8(s) {
			c.fail(errParamValue("providers"))
			return
		}
		list := strings.Split(strings.ToUpper(s), ",")
		for len(list) > 0 && list[len(list)-1] == "" {
			list = list[:len(list)-1]
		}
		records, err = currency.WithProviders(c.ctx(), c.s.DB, list)
	case hasScope:
		records, err = currency.All(c.ctx(), c.s.DB)
	default:
		records, err = currency.Active(c.ctx(), c.s.DB, c.s.today())
	}
	if err != nil {
		c.fail(err)
		return
	}
	out := make([]v2Currency, len(records))
	for i, r := range records {
		out[i] = currencyHash(r)
	}
	writeJSON(c.w, http.StatusOK, contentTypeV2, out)
}

// v2Provider is one entry of /providers.
type v2Provider struct {
	Key               string   `json:"key"`
	Name              string   `json:"name"`
	CountryCode       *string  `json:"country_code"`
	RateType          *string  `json:"rate_type"`
	PivotCurrency     *string  `json:"pivot_currency"`
	DataURL           *string  `json:"data_url"`
	TermsURL          *string  `json:"terms_url"`
	StartDate         *string  `json:"start_date"`
	EndDate           *string  `json:"end_date"`
	PublishCadence    *string  `json:"publish_cadence"`
	Frequency         string   `json:"frequency"`
	PublishesMissed   *int     `json:"publishes_missed"`
	Currencies        []string `json:"currencies"`
	UnknownCurrencies []string `json:"unknown_currencies"`
}

func (c *v2Request) providers() {
	all, err := provider.All(c.ctx(), c.s.DB)
	if err != nil {
		c.fail(err)
		return
	}
	out := []v2Provider{}
	for _, p := range all {
		entry, err := providerEntry(c.ctx(), c.s.DB, p, c.s.today())
		if err != nil {
			c.fail(err)
			return
		}
		if entry != nil {
			out = append(out, *entry)
		}
	}
	writeJSON(c.w, http.StatusOK, contentTypeV2, out)
}

// providerEntry describes a provider that has stored any currency, known or
// not; nil otherwise.
func providerEntry(ctx context.Context, q db.Querier, p provider.Provider, today time.Time) (*v2Provider, error) {
	rows, err := q.QueryContext(ctx,
		"SELECT iso_code FROM currency_coverages WHERE provider_key = ? ORDER BY iso_code", p.Key)
	if err != nil {
		return nil, err
	}
	currencies := []string{}
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			rows.Close()
			return nil, err
		}
		currencies = append(currencies, code)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var exclusions int
	if err := q.QueryRowContext(ctx, "SELECT count(*) FROM currency_exclusions WHERE provider_key = ?", p.Key).
		Scan(&exclusions); err != nil {
		return nil, err
	}
	if len(currencies) == 0 && exclusions == 0 {
		return nil, nil
	}

	entry := &v2Provider{
		Key: p.Key, Name: p.Name, CountryCode: nullable(p.CountryCode), RateType: nullable(p.RateType),
		PivotCurrency: nullable(p.PivotCurrency), DataURL: nullable(p.DataURL), TermsURL: nullable(p.TermsURL),
		PublishCadence: nullable(p.PublishCadence), Frequency: p.ObservationFrequency(), Currencies: currencies,
	}
	if start, ok, err := p.StartDate(ctx, q); err != nil {
		return nil, err
	} else if ok {
		entry.StartDate = &start
	}
	if end, ok, err := p.EndDate(ctx, q); err != nil {
		return nil, err
	} else if ok {
		entry.EndDate = &end
	}
	if n, ok, err := p.PublishesMissed(ctx, q, today); err != nil {
		return nil, err
	} else if ok {
		entry.PublishesMissed = &n
	}
	if entry.UnknownCurrencies, err = p.UnknownCurrencies(ctx, q); err != nil {
		return nil, err
	}
	return entry, nil
}

func validUTF8(s string) bool { return utf8.ValidString(s) }

// errParamValue is what Ruby raises on a parameter it cannot treat as a string
// (a nested value, invalid UTF-8): an internal error.
func errParamValue(key string) error { return fmt.Errorf("parameter %s is not a valid string", key) }
