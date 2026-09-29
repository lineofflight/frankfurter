// Package provider is lib/provider.rb: the providers table as values, their publishing calendar, and backfill, which
// drives a provider's registered adapter and stores what it returns.
package provider

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/currency"
	"github.com/lineofflight/frankfurter/go/internal/db"
)

// LookbackDays is the carry-forward window per observation frequency and publish cadence (#646). A daily value goes
// stale in two weeks; a monthly or quarterly value stands for its whole period plus the lag before the next one lands.
var LookbackDays = map[string]int{"daily": 14, "weekly": 14, "monthly": 45, "quarterly": 120}

// NonCurrencyCodes are reviewed non-currency labels. They stay available in provider history without triggering
// unknown-currency alerts.
var NonCurrencyCodes = map[string][]string{"NB": {"I44", "TWI"}, "RBA": {"FXRTWI"}}

// Provider is one row of the providers table. Empty strings and a zero CoverageStart are NULL.
type Provider struct {
	Key             string
	Name            string
	DataURL         string
	TermsURL        string
	CoverageStart   time.Time
	PivotCurrency   string
	RateType        string
	CountryCode     string
	PublishSchedule string
	PublishCadence  string
	Frequency       string // observation frequency; empty reads as daily
}

// ObservationFrequency is Frequency, defaulting to daily.
func (p Provider) ObservationFrequency() string {
	if p.Frequency == "" {
		return "daily"
	}
	return p.Frequency
}

// Blends reports whether the provider's rows enter the blend and the currency catalogue: only daily observations do.
func (p Provider) Blends() bool { return p.ObservationFrequency() == "daily" }

// Lookback is the wider of the frequency and cadence windows. A provider that releases a month of daily fixings in
// arrears has nothing newer than the last batch for weeks at a time, and the daily window alone would leave its latest
// query empty for most of every month.
func (p Provider) Lookback() int {
	days := LookbackDays[p.ObservationFrequency()]
	if p.PublishCadence != "" {
		days = max(days, LookbackDays[p.PublishCadence])
	}
	return days
}

const columns = `key, name, data_url, terms_url, coverage_start, pivot_currency, rate_type, country_code,
	publish_schedule, publish_cadence, frequency`

// All returns every provider, ordered by key.
func All(ctx context.Context, q db.Querier) ([]Provider, error) {
	rows, err := q.QueryContext(ctx, "SELECT "+columns+" FROM providers ORDER BY key")
	if err != nil {
		return nil, fmt.Errorf("load providers: %w", err)
	}
	defer rows.Close()
	var out []Provider
	for rows.Next() {
		p, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Find returns the provider with key, or nil.
func Find(ctx context.Context, q db.Querier, key string) (*Provider, error) {
	p, err := scan(q.QueryRowContext(ctx, "SELECT "+columns+" FROM providers WHERE key = ?", key))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find provider %s: %w", key, err)
	}
	return &p, nil
}

func scan(row interface{ Scan(...any) error }) (Provider, error) {
	var p Provider
	var start db.NullDate
	var dataURL, termsURL, pivot, rateType, country, schedule, cadence, frequency sql.NullString
	if err := row.Scan(&p.Key, &p.Name, &dataURL, &termsURL, &start, &pivot, &rateType, &country, &schedule, &cadence,
		&frequency); err != nil {
		return p, err
	}
	p.DataURL, p.TermsURL, p.PivotCurrency, p.RateType = dataURL.String, termsURL.String, pivot.String, rateType.String
	p.CountryCode, p.PublishSchedule, p.PublishCadence = country.String, schedule.String, cadence.String
	p.Frequency = frequency.String
	if start.Valid {
		p.CoverageStart = start.Time
	}
	return p, nil
}

// UnknownCurrencies lists the codes the provider publishes that the Money gem cannot name, less its reviewed
// non-currency labels, sorted.
func (p Provider) UnknownCurrencies(ctx context.Context, q db.Querier) ([]string, error) {
	rows, err := q.QueryContext(ctx, "SELECT iso_code FROM currency_exclusions WHERE provider_key = ?", p.Key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	codes := []string{}
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return nil, err
		}
		if !slices.Contains(NonCurrencyCodes[p.Key], code) && !currency.Named(code) {
			codes = append(codes, code)
		}
	}
	slices.Sort(codes)
	return codes, rows.Err()
}

// StartDate is the earliest start of the provider's coverages and exclusions, as stored text. ok is false when it
// has neither. As in Ruby, a coverage with no start date reads as "" and so wins.
func (p Provider) StartDate(ctx context.Context, q db.Querier) (date string, ok bool, err error) {
	return p.coverageBound(ctx, q, "min", "start_date")
}

// EndDate is the latest end of the provider's coverages and exclusions, as stored text. ok is false when it has
// neither.
func (p Provider) EndDate(ctx context.Context, q db.Querier) (date string, ok bool, err error) {
	return p.coverageBound(ctx, q, "max", "end_date")
}

func (p Provider) coverageBound(ctx context.Context, q db.Querier, agg, col string) (string, bool, error) {
	var s sql.NullString
	err := q.QueryRowContext(ctx, "SELECT "+agg+"(d) FROM (SELECT coalesce("+col+", '') AS d FROM currency_coverages "+
		"WHERE provider_key = ?1 UNION ALL SELECT coalesce("+col+", '') FROM currency_exclusions WHERE provider_key = ?1)",
		p.Key).Scan(&s)
	return s.String, s.Valid, err
}

// LastSynced is the date of the provider's newest stored rate, zero when it has none.
func (p Provider) LastSynced(ctx context.Context, q db.Querier) (time.Time, error) {
	var s sql.NullString
	if err := q.QueryRowContext(ctx, "SELECT max(date) FROM rates WHERE provider = ?", p.Key).Scan(&s); err != nil {
		return time.Time{}, err
	}
	if !s.Valid {
		return time.Time{}, nil
	}
	return db.ParseDate(s.String)
}

// PublishesMissed counts the publications the provider's schedule says should have landed after its end date and
// before reference (see MissedSince). ok is false when it has no schedule or no stored coverage.
func (p Provider) PublishesMissed(ctx context.Context, q db.Querier, reference time.Time) (n int, ok bool, err error) {
	if p.PublishSchedule == "" {
		return 0, false, nil
	}
	end, found, err := p.EndDate(ctx, q)
	if err != nil || !found {
		return 0, false, err
	}
	return p.MissedSince(end, reference)
}
