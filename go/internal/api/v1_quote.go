package api

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"errors"
	"math"
	"slices"
	"sort"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

// v1Quote is Versions::V1::Quote::Base: ECB rates against the euro, scaled by an amount and rebased onto another
// currency. v1EndOfDay and v1Interval supply the data and the response shape.
type v1Quote struct {
	Amount  float64 // defaults to 1
	Base    string  // defaults to EUR
	Symbols []string

	fetch     func(ctx context.Context) ([]rates.Row, error)
	result    dayRates
	performed bool
}

func newV1Quote(q v1Query) v1Quote {
	out := v1Quote{Amount: 1, Base: "EUR", Symbols: q.Symbols}
	if q.HasAmount {
		out.Amount = q.Amount
	}
	if q.HasBase {
		out.Base = q.Base
	}
	return out
}

var errNoData = errors.New("quote has no data source")

// Perform computes the result once; later calls return false.
func (q *v1Quote) Perform(ctx context.Context) (bool, error) {
	if q.performed {
		return false, nil
	}
	if q.fetch == nil {
		return false, errNoData
	}
	data, err := q.fetch(ctx)
	if err != nil {
		return false, err
	}
	if err := q.prepare(data); err != nil {
		return false, err
	}
	if q.mustRebase() {
		if err := q.rebase(); err != nil {
			return false, err
		}
	}
	q.performed = true
	return true, nil
}

func (q *v1Quote) mustRebase() bool { return q.Base != "EUR" }

func (q *v1Quote) shouldRound() bool { return q.Amount != 1 || q.mustRebase() }

// NotFound reports an empty result.
func (q *v1Quote) NotFound() bool { return len(q.result.days) == 0 }

// prepare scales each row by the amount, keyed by date and quote in the order rows arrive. A row whose stored
// components resolve no rate is skipped (Ruby fails the request on the nil).
func (q *v1Quote) prepare(data []rates.Row) error {
	for _, row := range data {
		if math.IsNaN(row.Rate) {
			continue
		}
		rate := q.Amount * row.Rate
		if q.shouldRound() {
			var err error
			if rate, err = v1Round(rate); err != nil {
				return err
			}
		}
		q.result.at(db.FormatDate(row.Date)).set(row.Quote, rate)
	}
	return nil
}

// rebase divides each date's rates by the new base's, adding the euro at the amount unless symbols leave it out. A
// date without the base, or with nothing else, is dropped.
func (q *v1Quote) rebase() error {
	var kept []*day
	for _, day := range q.result.days {
		if q.Symbols == nil || slices.Contains(q.Symbols, "EUR") {
			day.rates.set("EUR", q.Amount)
		}
		divisor, ok := day.rates.remove(q.Base)
		if !ok || len(day.rates) == 0 {
			continue
		}
		sort.Slice(day.rates, func(i, j int) bool { return day.rates[i].quote < day.rates[j].quote })
		for i := range day.rates {
			rate, err := v1Round(q.Amount * day.rates[i].rate / divisor)
			if err != nil {
				return err
			}
			day.rates[i].rate = rate
		}
		kept = append(kept, day)
	}
	q.result.days = kept
	return nil
}

// errNotFinite is Ruby's Roundable#round failing on an infinite or NaN value (an amount like 1e400, or a rebase onto
// a currency whose rate rounded to zero): Float#round and Float() raise, and V1 answers 422.
var errNotFinite = errors.New("rate is not a finite number")

func v1Round(x float64) (float64, error) {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return 0, errNotFinite
	}
	return rates.Round(x), nil
}

// dayRates is the result: rates per date, dates in first-seen order, as Ruby's insertion-ordered Hash.
type dayRates struct {
	days  []*day
	index map[string]*day
}

type day struct {
	date  string
	rates quoteRates
}

// at returns date's rates, adding the date when new.
func (d *dayRates) at(date string) *quoteRates {
	if x, ok := d.index[date]; ok {
		return &x.rates
	}
	if d.index == nil {
		d.index = map[string]*day{}
	}
	x := &day{date: date}
	d.days = append(d.days, x)
	d.index[date] = x
	return &x.rates
}

type quoteRate struct {
	quote string
	rate  float64
}

type quoteRates []quoteRate

func (r *quoteRates) set(quote string, rate float64) {
	for i := range *r {
		if (*r)[i].quote == quote {
			(*r)[i].rate = rate
			return
		}
	}
	*r = append(*r, quoteRate{quote, rate})
}

func (r *quoteRates) remove(quote string) (float64, bool) {
	for i, x := range *r {
		if x.quote == quote {
			*r = slices.Delete(*r, i, i+1)
			return x.rate, true
		}
	}
	return 0, false
}

func (r quoteRates) toMap() map[string]float64 {
	m := make(map[string]float64, len(r))
	for _, x := range r {
		m[x.quote] = x.rate
	}
	return m
}

func md5Hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// ecbScope is ECB's stored rows, narrowed to the pairs of symbols plus the base when symbols are given.
func (q *v1Quote) ecbScope(base rates.Query) rates.Query {
	if q.Symbols != nil {
		base = base.Only(append(slices.Clone(q.Symbols), q.Base)...)
	}
	return base.Columns("rates.date, rates.base, rates.quote, rates.provider, rates.rate")
}

// v1EndOfDay is Quote::EndOfDay: the snapshot as of one date, carrying each rate forward up to 14 days.
type v1EndOfDay struct {
	v1Quote
	date time.Time
}

func newV1EndOfDay(conn *sql.DB, q v1Query) *v1EndOfDay {
	e := &v1EndOfDay{v1Quote: newV1Quote(q), date: q.Date}
	e.fetch = func(ctx context.Context) ([]rates.Row, error) {
		from := e.date.AddDate(0, 0, -rates.LookbackDays)
		scope := rates.Daily.Dataset().Filter("provider = 'ECB'").
			Filter("date >= " + db.LitDate(from) + " AND date <= " + db.LitDate(e.date))
		query := e.ecbScope(scope).OrderBy("rates.date, rates.base, rates.quote")
		rows, err := rates.Select(ctx, conn, query.SQL())
		if err != nil {
			return nil, err
		}
		return rates.CarryForward(rows, e.date, rates.LookbackDays), nil
	}
	return e
}

type v1EndOfDayBody struct {
	Amount float64            `json:"amount"`
	Base   string             `json:"base"`
	Date   string             `json:"date"`
	Rates  map[string]float64 `json:"rates"`
}

// Formatted is the response: the first date in the result and its rates.
func (e *v1EndOfDay) Formatted() v1EndOfDayBody {
	out := v1EndOfDayBody{Amount: e.Amount, Base: e.Base}
	if len(e.result.days) > 0 {
		first := e.result.days[0]
		out.Date, out.Rates = first.date, first.rates.toMap()
	}
	return out
}

// CacheKey hashes the quoted date; empty when not found.
func (e *v1EndOfDay) CacheKey() string {
	if e.NotFound() {
		return ""
	}
	return md5Hex(e.result.days[0].date)
}

// v1Interval is Quote::Interval: every stored date in a range, the range widened back to the last publication on or
// before its start.
type v1Interval struct {
	v1Quote
	start, end time.Time
}

func newV1Interval(conn *sql.DB, q v1Query, today time.Time) *v1Interval {
	iv := &v1Interval{v1Quote: newV1Quote(q), start: q.Start, end: q.End}
	iv.fetch = func(ctx context.Context) ([]rates.Row, error) {
		scope := rates.Daily.Dataset().Filter("provider = 'ECB'").Between(iv.start, iv.end, today)
		return rates.Select(ctx, conn, iv.ecbScope(scope).SQL())
	}
	return iv
}

type v1IntervalBody struct {
	Amount    float64                       `json:"amount"`
	Base      string                        `json:"base"`
	StartDate string                        `json:"start_date"`
	EndDate   string                        `json:"end_date"`
	Rates     map[string]map[string]float64 `json:"rates"`
}

// Formatted is the response: every date's rates, bounded by the first and last dates found.
func (iv *v1Interval) Formatted() v1IntervalBody {
	out := v1IntervalBody{Amount: iv.Amount, Base: iv.Base, Rates: map[string]map[string]float64{}}
	days := iv.result.days
	if n := len(days); n > 0 {
		out.StartDate, out.EndDate = days[0].date, days[n-1].date
	}
	for _, d := range days {
		out.Rates[d.date] = d.rates.toMap()
	}
	return out
}

// CacheKey hashes the last date found; empty when not found.
func (iv *v1Interval) CacheKey() string {
	if iv.NotFound() {
		return ""
	}
	return md5Hex(iv.result.days[len(iv.result.days)-1].date)
}
