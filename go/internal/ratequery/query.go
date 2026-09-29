// Package ratequery is the v2 rate query (lib/versions/v2/rate_query.rb and lib/rate_coverage.rb): it validates a
// request's parameters and yields the records of a latest, single-date, daily-range or grouped-range response, from the
// materialized blends where they can answer and from a live blend of the stored rates where they cannot.
//
// Every shape blends through the pivot currency (USD) and derives the requested base from it, so a range and a single
// date agree about the same data. quotes= filters rows after blending, never the rows blended. A single provider
// skips the pivot: its rows are rebased through its own base, and its native pairs echo its published digits.
package ratequery

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lineofflight/frankfurter/go/internal/blend"
	"github.com/lineofflight/frankfurter/go/internal/currency"
	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/heavyslots"
	"github.com/lineofflight/frankfurter/go/internal/provider"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

// Query limits and constants, as RateQuery defines them.
const (
	Pivot = blend.Pivot

	DefaultChunkMonths = 3
	LatestFutureDays   = 1

	// Residual cap for daily-range shapes the materialized blend cannot serve (providers= reads raw rows,
	// expand=providers needs contributor metadata, and plain ranges fall back to live compute until the table is
	// ready): those recompute the blend per date, and past 5 years unfiltered the compute outlives the request timeout
	// and Cloudflare's origin ceiling.
	MaxDailyRangeYears     = 5
	MaxDailyRangeQuotes    = 5
	MaxDailyRangeProviders = 5
)

// AllowedParams are the parameters a rate query takes; CoverageParams those /coverage takes.
var (
	AllowedParams     = []string{"base", "quotes", "providers", "date", "from", "to", "group", "expand"}
	AllowedExpansions = []string{"providers"}
	CoverageParams    = []string{"base", "quotes", "providers"}

	chunkMonths = map[string]int{"week": 21, "month": 84}
)

// Params are a request's parameters as Rack parses the query string: a string, nil for a key given without a value
// (which reads as absent), or a nested array or hash (which a query rejects as Ruby fails on it: an internal error).
type Params map[string]any

// ValidationError is RateQuery::ValidationError: the request is invalid (HTTP 422).
type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }

func invalid(format string, args ...any) error { return &ValidationError{fmt.Sprintf(format, args...)} }

// DeadlineError is RequestTimeout::Error raised where the compute happens: the request outlived its deadline (503).
type DeadlineError struct{ Timeout time.Duration }

func (e *DeadlineError) Error() string {
	return fmt.Sprintf("request exceeded %ds timeout", int(e.Timeout/time.Second))
}

// BusyError is HeavySlots::Busy: every heavy compute slot is held (503 with Retry-After).
type BusyError struct{}

func (BusyError) Error() string {
	return fmt.Sprintf("too many range computes in progress; retry after %ds", heavyslots.RetryAfterSeconds)
}

func (BusyError) Unwrap() error { return heavyslots.ErrBusy }

// errParam is what Ruby raises on a parameter it cannot treat as a string: NoMethodError on a nested value,
// ArgumentError on invalid UTF-8. Either is an internal error.
type errParam struct{ key, reason string }

func (e *errParam) Error() string { return "parameter " + e.key + ": " + e.reason }

// DefaultSlots is the process-wide cap every query draws on unless Options names another.
var DefaultSlots = heavyslots.New(heavyslots.DefaultMax)

// Options are what a query takes from its surroundings.
type Options struct {
	Today    time.Time         // Ruby's Date.today; zero means rates.Today()
	Deadline time.Time         // when compute must stop; zero means never
	Timeout  time.Duration     // the budget Deadline came from, for the error message
	Slots    *heavyslots.Slots // heavy compute slots; nil means DefaultSlots
}

// RollupCoverage counts how the chunks of a grouped range were served.
type RollupCoverage struct {
	Materialized, Fallback, Empty int
}

// Query is a validated rate query.
type Query struct {
	db    db.Querier
	today time.Time
	opts  Options

	base      string
	baseGiven bool
	quotes    []string // nil when absent
	providers []string // nil when absent
	group     string
	expand    []string // nil when absent

	date, from, to          time.Time
	dateStr, fromStr, toStr string
	hasDate, hasFrom, hasTo bool

	// ForceLive makes every shape compute live (the parity harness compares it with the tables).
	ForceLive bool
	// Coverage counts grouped chunks by how they were served.
	Coverage RollupCoverage

	held        *heavyslots.Slots
	lookback    int
	nonBlending []string
	loaded      bool

	checkHook func()       // test seam: runs on every deadline check
	emitHook  func() error // test seam: runs before every live blend
}

// Test seams, production values.
var (
	blendRows  = blend.Blend
	dailyReady = blend.DailyReady
	readRollup = func(ctx context.Context, r blend.Rollup, q db.Querier, start, end, today time.Time) ([]currency.Blended, bool, error) {
		return r.Read(ctx, q, start, end, today)
	}
)

// New validates params and returns the query. Validation runs in Ruby's order, so the first failure reported is the
// same: unknown parameters, dates, conflicting dates, group, expand, currencies, then the daily range cost.
func New(ctx context.Context, q db.Querier, params Params, opts Options) (*Query, error) {
	if opts.Today.IsZero() {
		opts.Today = rates.Today()
	}
	query := &Query{db: q, today: opts.Today, opts: opts, base: "EUR"}
	if err := query.parse(ctx, params); err != nil {
		return nil, err
	}
	return query, nil
}

func (q *Query) parse(ctx context.Context, params Params) error {
	var unknown []string
	for k := range params {
		if !slices.Contains(AllowedParams, k) {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		slices.Sort(unknown)
		return invalid("unknown parameter: %s", strings.Join(unknown, ", "))
	}

	// validate_dates!: the first given date that does not parse fails.
	for _, d := range []struct {
		key  string
		t    *time.Time
		s    *string
		seen *bool
	}{{"date", &q.date, &q.dateStr, &q.hasDate}, {"from", &q.from, &q.fromStr, &q.hasFrom}, {"to", &q.to, &q.toStr, &q.hasTo}} {
		s, ok, err := str(params, d.key)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		t, valid := ParseDate(s)
		if !valid {
			return invalid("invalid date")
		}
		*d.t, *d.s, *d.seen = t, s, true
	}
	if q.hasDate && (q.hasFrom || q.hasTo) {
		return invalid("conflicting params")
	}

	group, ok, err := str(params, "group")
	if err != nil {
		return err
	}
	if ok {
		q.group = strings.ToLower(group)
		if q.group != "week" && q.group != "month" {
			return invalid("invalid group")
		}
	}

	expand, ok, err := str(params, "expand")
	if err != nil {
		return err
	}
	if ok {
		q.expand = rubySplit(strings.ToLower(expand))
		var bad []string
		for _, e := range q.expand {
			if !slices.Contains(AllowedExpansions, e) {
				bad = append(bad, e)
			}
		}
		if len(bad) > 0 {
			return invalid("invalid expand: %s", strings.Join(bad, ","))
		}
	}

	// validate_currencies! reads base, then quotes; providers only when a code is not a known currency.
	base, ok, err := str(params, "base")
	if err != nil {
		return err
	}
	if ok {
		q.base, q.baseGiven = strings.ToUpper(base), true
	}
	quotes, ok, err := str(params, "quotes")
	if err != nil {
		return err
	}
	if ok {
		q.quotes = rubySplit(strings.ToUpper(quotes))
	}
	providers, providersOK, providersErr := str(params, "providers")
	if providersOK {
		q.providers = rubySplit(strings.ToUpper(providers))
	}

	var candidates []string
	if q.baseGiven {
		candidates = append(candidates, q.base)
	}
	candidates = append(candidates, q.quotes...)
	var bad []string
	for _, code := range candidates {
		if _, known := currency.Find(code); known {
			continue
		}
		if providersErr != nil {
			return providersErr
		}
		stored, err := q.storedByProvider(ctx, code)
		if err != nil {
			return err
		}
		if !stored {
			bad = append(bad, code)
		}
	}
	if len(bad) > 0 {
		return invalid("invalid currency: %s", strings.Join(bad, ","))
	}
	if providersErr != nil {
		return providersErr // Ruby fails on it as soon as anything reads providers, which every query does
	}
	return q.validateRangeCost(ctx)
}

// str reads a parameter as Ruby's string methods would: absent or nil is not given; anything but a valid UTF-8 string
// fails.
func str(params Params, key string) (string, bool, error) {
	v, ok := params[key]
	if !ok || v == nil {
		return "", false, nil
	}
	s, isString := v.(string)
	if !isString {
		return "", false, &errParam{key, fmt.Sprintf("undefined method for %T", v)}
	}
	if !utf8.ValidString(s) {
		return "", false, &errParam{key, "invalid byte sequence in UTF-8"}
	}
	return s, true, nil
}

// rubySplit is Ruby's String#split(","): trailing empty fields are dropped, so "" gives an empty (non-nil) list.
func rubySplit(s string) []string {
	parts := strings.Split(s, ",")
	for len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

var dateRe = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)

// ParseDate is RateQuery#parse_date: YYYY-MM-DD naming a real day. Like Ruby's Date, days before the Gregorian
// reform (1582-10-15) follow the Julian calendar, and the ten days the reform skipped do not exist.
func ParseDate(s string) (time.Time, bool) {
	if !dateRe.MatchString(s) {
		return time.Time{}, false
	}
	var y, m, d int
	fmt.Sscanf(s, "%d-%d-%d", &y, &m, &d)
	if m < 1 || m > 12 || d < 1 {
		return time.Time{}, false
	}
	days := [...]int{31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}[m-1]
	julian := s < "1582-10-15"
	if m == 2 && (y%4 == 0 && (julian || y%100 != 0 || y%400 == 0)) {
		days = 29
	}
	if d > days || s >= "1582-10-05" && julian {
		return time.Time{}, false
	}
	return time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC), true
}

func uniqCount(values []string) int {
	seen := map[string]bool{}
	for _, v := range values {
		seen[v] = true
	}
	return len(seen)
}

// single reports whether exactly one distinct provider is named.
func (q *Query) single() bool { return q.providers != nil && uniqCount(q.providers) == 1 }

// storedByProvider is the second half of available_currency?: with one provider named, a code it has stored counts
// even when the Money gem does not know it (index labels, withdrawn codes).
func (q *Query) storedByProvider(ctx context.Context, code string) (bool, error) {
	if !q.single() {
		return false, nil
	}
	var one int
	err := q.db.QueryRowContext(ctx, "SELECT 1 FROM rates WHERE provider IN "+db.LitList(q.providers)+
		" AND (base = ? OR quote = ?) LIMIT 1", code, code).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (q *Query) validateRangeCost(ctx context.Context) error {
	if !q.Range() || q.rollup() {
		return nil
	}
	if q.providers == nil && !q.ExpandProviders() {
		ready, err := dailyReady(ctx, q.db)
		if err != nil {
			return err
		}
		if ready {
			return nil
		}
	}
	// One provider is a fetch plus a one-contributor blend per date, not a cross-provider recompute: measured in
	// prod at 7s for five years of the largest provider (BDI, 173 quotes), so full history stays inside the request
	// deadline (#644).
	if q.single() {
		return nil
	}
	// A short provider list bounds the fetch, so a small quotes list on top stays cheap; naming every provider
	// reproduces the unbounded workload, hence the provider-count bound. quotes= filters rows only, after blending,
	// so neither a provider-unbounded expand=providers range nor a plain range on the not-ready live fallback gets a
	// quotes exemption.
	if q.providers != nil && q.quotes != nil && uniqCount(q.providers) <= MaxDailyRangeProviders &&
		uniqCount(q.quotes) <= MaxDailyRangeQuotes {
		return nil
	}
	// Cost follows computable days, so a `to` in the future counts only up to today.
	start, end := q.rangeBounds()
	if end.After(q.today) {
		end = q.today
	}
	if !end.After(addMonths(start, MaxDailyRangeYears*12)) {
		return nil
	}
	return invalid("date range exceeds %d years at daily granularity; filter with quotes= (%d currencies or fewer), "+
		"aggregate with group=week or group=month, or split the range into shorter requests",
		MaxDailyRangeYears, MaxDailyRangeQuotes)
}

// Range reports whether the query spans dates (from= given) rather than a single snapshot.
func (q *Query) Range() bool { return q.hasFrom && !q.hasDate }

// DateRelative reports whether the date scope anchors on the service clock (latest or an open-ended range), so the
// response changes at UTC midnight even if no new data arrives.
func (q *Query) DateRelative() bool { return !q.hasDate && !(q.hasFrom && q.hasTo) }

// ExpandProviders reports whether expand=providers asked for contributor lists.
func (q *Query) ExpandProviders() bool { return slices.Contains(q.expand, "providers") }

// Base is the requested base, upper-cased (EUR by default).
func (q *Query) Base() string { return q.base }

// Quotes are the requested quotes, upper-cased; nil when not given.
func (q *Query) Quotes() []string { return q.quotes }

// Providers are the requested providers, upper-cased; nil when not given.
func (q *Query) Providers() []string { return q.providers }

func (q *Query) rollup() bool { return q.Range() && (q.group == "week" || q.group == "month") }

// rangeBounds is the range date scope: from through to, or today when to is not given.
func (q *Query) rangeBounds() (time.Time, time.Time) {
	end := q.today
	if q.hasTo {
		end = q.to
	}
	return q.from, end
}

// snapshotDate is the single date scope: the given date, or tomorrow for latest (a provider's next-day publication
// belongs in the latest snapshot).
func (q *Query) snapshotDate() time.Time {
	if q.hasDate {
		return q.date
	}
	return q.today.AddDate(0, 0, LatestFutureDays)
}

// CSVFilename names CSV downloads after the query so repeat exports don't pile up as rates (1).csv. Stripped to a
// safe set because providers= is not checked against known keys and lands in a response header.
func (q *Query) CSVFilename() string {
	var parts []string
	if q.providers != nil && uniqCount(q.providers) == 1 {
		parts = append(parts, q.providers[0])
	}
	if q.quotes != nil && uniqCount(q.quotes) == 1 {
		parts = append(parts, q.base+"-"+q.quotes[0])
	} else {
		parts = append(parts, q.base+"-rates")
	}
	switch {
	case q.Range():
		end := db.FormatDate(q.today)
		if q.hasTo {
			end = q.toStr
		}
		parts = append(parts, q.fromStr+"_"+end)
	case q.hasDate:
		parts = append(parts, q.dateStr)
	default:
		parts = append(parts, "latest")
	}
	if q.rollup() {
		parts = append(parts, q.group+"ly")
	}
	name := strings.Join(parts, "_") + ".csv"
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-' {
			return r
		}
		return -1
	}, name)
}

// CacheKey is the ETag value: the newest stored date in scope and the expansions. The raw rates decide it, so a
// rollup's content changing within a day leaves it stable.
func (q *Query) CacheKey(ctx context.Context) (string, error) {
	scope, err := q.rawScope(ctx, rates.Daily)
	if err != nil {
		return "", err
	}
	var start, end time.Time
	if q.Range() {
		start, end = q.rangeBounds()
	} else {
		lookback, err := q.lookbackDays(ctx)
		if err != nil {
			return "", err
		}
		end = q.snapshotDate()
		start = end.AddDate(0, 0, -lookback)
	}
	var maxDate *string
	err = q.db.QueryRowContext(ctx, scope.Filter(dateBetween("date", start, end)).Columns("max(date)").SQL()).
		Scan(&maxDate)
	if err != nil {
		return "", err
	}
	key := ""
	if maxDate != nil {
		key = *maxDate
	}
	sum := md5.Sum([]byte(key + "|" + strings.Join(q.expand, "|")))
	return hex.EncodeToString(sum[:]), nil
}

func dateBetween(col string, start, end time.Time) string {
	return col + " >= " + db.LitDate(start) + " AND " + col + " <= " + db.LitDate(end)
}

// load reads what the query needs from the providers table: the non-blending keys and the carry-forward window.
func (q *Query) load(ctx context.Context) error {
	if q.loaded {
		return nil
	}
	keys, err := rates.NonBlendingKeys(ctx, q.db)
	if err != nil {
		return err
	}
	q.nonBlending = keys
	// Carry-forward window: the named providers' own, else the blend's (#646).
	q.lookback = 0
	for _, key := range q.providers {
		p, err := provider.Find(ctx, q.db, key)
		if err != nil {
			return err
		}
		if p != nil {
			q.lookback = max(q.lookback, p.Lookback())
		}
	}
	if q.lookback == 0 {
		q.lookback = rates.LookbackDays
	}
	q.loaded = true
	return nil
}

func (q *Query) lookbackDays(ctx context.Context) (int, error) {
	if err := q.load(ctx); err != nil {
		return 0, err
	}
	return q.lookback, nil
}

// rawScope is apply_filters on table t: the blendable rows, or with providers= just theirs (named currencies only
// when several are named, since one provider's own labels are its view).
func (q *Query) rawScope(ctx context.Context, t rates.Table) (rates.Query, error) {
	if err := q.load(ctx); err != nil {
		return rates.Query{}, err
	}
	if q.providers == nil {
		return t.Blendable(q.nonBlending), nil
	}
	scope := t.Dataset().Filter("provider IN " + db.LitList(q.providers))
	if !q.single() {
		scope = scope.Filter(rates.NamedCondition())
	}
	return scope, nil
}

// blendedTable reports whether the materialized daily blend answers: plain shapes only, once it covers full history.
// providers= needs raw rows and expand=providers needs contributor metadata the table does not store; until the
// table is ready a deploy before blend:rebuild (or an incremental refresh landing first) stays correct on the live
// path.
func (q *Query) blendedTable(ctx context.Context) (bool, error) {
	if q.ForceLive || q.providers != nil || q.ExpandProviders() {
		return false, nil
	}
	return dailyReady(ctx, q.db)
}

// checkDeadline enforces the request deadline where the compute happens, so a doomed or abandoned range stops at the
// deadline server-side (#569).
func (q *Query) checkDeadline() error {
	if q.checkHook != nil {
		q.checkHook()
	}
	if q.opts.Deadline.IsZero() || time.Now().Before(q.opts.Deadline) {
		return nil
	}
	return &DeadlineError{q.opts.Timeout}
}

func (q *Query) acquireSlot() error {
	slots := q.opts.Slots
	if slots == nil {
		slots = DefaultSlots
	}
	if !slots.TryAcquire() {
		return BusyError{}
	}
	q.held = slots
	return nil
}

// ReleaseSlot returns the heavy slot the query holds, if any. It is idempotent: the heavy range returns it however
// its enumeration ends, and a handler may return it again when a client goes away mid-stream.
func (q *Query) ReleaseSlot() {
	slots := q.held
	q.held = nil
	if slots != nil {
		slots.Release()
	}
}

// addMonths is Ruby's Date#>>: the same day n months on, clamped to the end of a shorter month.
func addMonths(d time.Time, n int) time.Time {
	y, m, day := d.Date()
	first := time.Date(y, m+time.Month(n), 1, 0, 0, 0, 0, time.UTC)
	last := first.AddDate(0, 1, -1).Day()
	return time.Date(first.Year(), first.Month(), min(day, last), 0, 0, 0, 0, time.UTC)
}

// IsValidation reports whether err is a validation failure.
func IsValidation(err error) bool {
	var v *ValidationError
	return errors.As(err, &v)
}
