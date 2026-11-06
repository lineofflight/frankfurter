package ratequery

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"math/rand/v2"
	"slices"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/currency"
	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/heavyslots"
)

// BlendParity (lib/blend_parity.rb, the blend:parity task) replays
// table-eligible query shapes through the materialized path and the live path
// and compares the serialized records. The live pipeline is the oracle.
//
// One declared divergence is asserted rather than ignored (#570): snap-back
// rows serve the canonical anchor-date value, so when a quote's contributor set
// changed between its observation date and the range start, the table row may
// differ from what the live path computes at the range-start anchor.
// Canonicality is asserted in the pivot frame: derive divides a whole batch by
// the base's rate at the range-start anchor (a batch property, identical on
// both paths and pinned by the byte-equal fresh rows around it), so only the
// row's own pivot-frame value distinguishes canonical from aged. Any other
// difference is a failure. The second declared change, pivot-frame
// canonicalization of range batches, lives in emitBlended itself and so is
// exercised by the comparison on both paths.

// Shape is a query shape: from, to, base, quotes, group (absent keys are not
// given).
type Shape map[string]string

func (s Shape) String() string {
	keys := slices.Sorted(maps.Keys(s))
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + ": " + s[k]
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// ParityIssue is a failed or incomplete shape.
type ParityIssue struct {
	Shape  Shape
	Reason string
}

// ParityReport is BlendParity::Report.
type ParityReport struct {
	Shapes          int
	SnapbackRows    int
	Failures        []ParityIssue
	GroupedCoverage map[string]*RollupCoverage
	Incomplete      []ParityIssue
}

// Passed reports whether every shape matched and every grouped chunk was
// verified against the table.
func (r ParityReport) Passed() bool { return len(r.Failures) == 0 && len(r.Incomplete) == 0 }

func (r ParityReport) String() string {
	lines := []string{fmt.Sprintf("blend:parity: %d shapes compared, %d snap-back rows verified canonical, %d failures",
		r.Shapes, r.SnapbackRows, len(r.Failures))}
	for _, group := range []string{"week", "month"} {
		c := r.GroupedCoverage[group]
		lines = append(lines, fmt.Sprintf("  %s: %d materialized chunks compared, %d live fallback chunks unverified, "+
			"%d empty chunks", group, c.Materialized, c.Fallback, c.Empty))
	}
	if len(r.Incomplete) > 0 {
		lines = append(lines, fmt.Sprintf("  INCOMPLETE: %d coverage gaps; fallback includes legitimately empty USD blends",
			len(r.Incomplete)))
		for _, e := range r.Incomplete[:min(10, len(r.Incomplete))] {
			lines = append(lines, "    "+e.Shape.String()+": "+e.Reason)
		}
	}
	for _, f := range r.Failures[:min(10, len(r.Failures))] {
		lines = append(lines, "  FAIL "+f.Shape.String()+": "+f.Reason)
	}
	return strings.Join(lines, "\n")
}

// PopularBases are the bases random shapes draw from.
var PopularBases = []string{"EUR", "USD", "GBP", "JPY", "CHF", "AED", "CAD", "TRY"}

// Parity runs the comparison over the adversarial shapes (each also grouped by
// week and month) and samples random ones drawn with seed. It needs stored
// rates; run it after the materialized blends are rebuilt.
func Parity(ctx context.Context, conn *sql.DB, samples int, seed uint64, today time.Time) (ParityReport, error) {
	p := &parity{conn: conn, today: today, rng: rand.New(rand.NewPCG(seed, seed)),
		slots: heavyslots.New(math.MaxInt)}
	return p.run(ctx, samples)
}

type parity struct {
	conn  *sql.DB
	q     db.Querier // the current shape's transaction
	today time.Time
	rng   *rand.Rand
	slots *heavyslots.Slots

	first, last time.Time
	pool        []string
	frameCache  map[string]map[[2]string]Number
}

func (p *parity) run(ctx context.Context, samples int) (ParityReport, error) {
	report := ParityReport{GroupedCoverage: map[string]*RollupCoverage{"week": {}, "month": {}}}
	if err := p.loadCoverage(ctx); err != nil {
		return report, err
	}
	var shapes []Shape
	for _, s := range p.adversarialShapes() {
		shapes = append(shapes, s, with(s, "group", "week"), with(s, "group", "month"))
	}
	for range samples {
		s, err := p.randomShape(ctx)
		if err != nil {
			return report, err
		}
		shapes = append(shapes, s)
	}

	for _, shape := range shapes {
		// Both replays and the canonical probes see the same source and
		// materialized rows during concurrent ingestion.
		if err := p.inTx(ctx, func() error { return p.compare(ctx, shape, &report) }); err != nil {
			return report, fmt.Errorf("blend parity %s: %w", shape, err)
		}
	}
	for _, group := range []string{"week", "month"} {
		if report.GroupedCoverage[group].Materialized == 0 {
			report.Incomplete = append(report.Incomplete,
				ParityIssue{Shape{"group": group}, "no nonempty materialized chunks compared"})
		}
	}
	report.Shapes = len(shapes)
	return report, nil
}

func (p *parity) inTx(ctx context.Context, fn func() error) error {
	tx, err := p.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	p.q = tx
	p.frameCache = map[string]map[[2]string]Number{}
	return fn()
}

func (p *parity) compare(ctx context.Context, shape Shape, report *ParityReport) error {
	var counts RollupCoverage
	table, err := p.records(ctx, shape, false, &counts)
	if err != nil {
		return err
	}
	live, err := p.records(ctx, shape, true, nil)
	if err != nil {
		return err
	}
	group := shape["group"]
	if group != "" {
		c := report.GroupedCoverage[group]
		c.Materialized += counts.Materialized
		c.Fallback += counts.Fallback
		c.Empty += counts.Empty
		if counts.Fallback > 0 {
			report.Incomplete = append(report.Incomplete,
				ParityIssue{shape, fmt.Sprintf("%d chunks compared live versus live", counts.Fallback)})
		}
	}
	if same, err := sameJSON(table, live); err != nil || same {
		return err
	}
	if group != "" {
		report.Failures = append(report.Failures, ParityIssue{shape, "grouped response bytes differ"})
		return nil
	}
	verified, reason, err := p.explainDivergence(ctx, shape, table, live)
	if err != nil {
		return err
	}
	if reason != "" {
		report.Failures = append(report.Failures, ParityIssue{shape, reason})
	} else {
		report.SnapbackRows += verified
	}
	return nil
}

func sameJSON(a, b []Record) (bool, error) {
	x, err := json.Marshal(a)
	if err != nil {
		return false, err
	}
	y, err := json.Marshal(b)
	return string(x) == string(y), err
}

// records runs a shape without a deadline: a forced-live replay of a
// full-history shape legitimately outlives the request timeout, and bounding it
// would abort the harness, not a client request.
func (p *parity) records(ctx context.Context, shape Shape, forceLive bool, coverage *RollupCoverage) ([]Record, error) {
	params := Params{}
	for k, v := range shape {
		params[k] = v
	}
	q, err := New(ctx, p.q, params, Options{Today: p.today, Slots: p.slots})
	if err != nil {
		return nil, err
	}
	q.ForceLive = forceLive
	records, err := q.All(ctx)
	if coverage != nil {
		*coverage = q.Coverage
	}
	return records, err
}

type recordKey [2]string // date, quote

func keyOf(r Record) recordKey { return recordKey{r.Date, r.Quote} }

// explainDivergence passes a divergent shape only when every difference traces
// to the canonical anchor-date rule:
//
//   - A record only the live path emits must be a consensus-masked emergence (the live pipeline anchored at the
//     record's own date yields no row of that date for the quote, so it has no canonical value) or superseded (live
//     re-surfaced an older pre-range row mid-range after a consensus flip, where the table emits only the newest one).
//   - A record only the table emits must sit in the snap-back region (before the range start) and be canonical.
//   - Records both emit must align in order and keys; a rate difference is allowed only when the table value verifies
//     canonical in the pivot frame. Most such rows sit in the snap-back region, but they can also appear inside the
//     range at a base currency's coverage boundary (the euro's birth), where live first emits a row only once the
//     derive base exists, at an anchor past the row's own date.
//
// It returns how many rows it verified, or the reason the shape fails.
func (p *parity) explainDivergence(ctx context.Context, shape Shape, table, live []Record) (int, string, error) {
	tableKeys, liveKeys := map[recordKey]bool{}, map[recordKey]bool{}
	for _, r := range table {
		tableKeys[keyOf(r)] = true
	}
	for _, r := range live {
		liveKeys[keyOf(r)] = true
	}
	from := shape["from"]
	verified := 0

	for _, r := range live {
		if tableKeys[keyOf(r)] {
			continue
		}
		superseded := r.Date < from && slices.ContainsFunc(table, func(t Record) bool {
			return t.Quote == r.Quote && t.Date > r.Date && t.Date < from
		})
		if !superseded {
			_, found, err := p.canonicalRate(ctx, shape, r)
			if err != nil {
				return 0, "", err
			}
			if found {
				return 0, fmt.Sprintf("live-only record %s on %s is neither a masked emergence nor superseded",
					r.Quote, r.Date), nil
			}
		}
		verified++
	}

	for _, r := range table {
		if liveKeys[keyOf(r)] {
			continue
		}
		ok := false
		if r.Date < from {
			var err error
			if ok, err = p.pivotPairCanonical(ctx, shape, r); err != nil {
				return 0, "", err
			}
		}
		if !ok {
			return 0, fmt.Sprintf("table-only record %s on %s is not a canonical snap-back row", r.Quote, r.Date), nil
		}
		verified++
	}

	var commonTable, commonLive []Record
	for _, r := range table {
		if liveKeys[keyOf(r)] {
			commonTable = append(commonTable, r)
		}
	}
	for _, r := range live {
		if tableKeys[keyOf(r)] {
			commonLive = append(commonLive, r)
		}
	}
	for i, t := range commonTable {
		l := commonLive[i]
		if t.Date != l.Date || t.Base != l.Base || t.Quote != l.Quote {
			return 0, fmt.Sprintf("record keys diverge at %s %s %s vs %s %s %s", t.Date, t.Base, t.Quote, l.Date, l.Base,
				l.Quote), nil
		}
		if t.Rate.Value == l.Rate.Value {
			continue
		}
		ok, err := p.pivotPairCanonical(ctx, shape, t)
		if err != nil {
			return 0, "", err
		}
		if !ok {
			return 0, fmt.Sprintf("rate mismatch for %s on %s where the table value is not canonical: table %s, live %s",
				t.Quote, t.Date, t.Rate, l.Rate), nil
		}
		verified++
	}
	return verified, "", nil
}

func (p *parity) pivotPairCanonical(ctx context.Context, shape Shape, r Record) (bool, error) {
	// A derived base->PIVOT row carries the reciprocal of the base's rate, so
	// its canonicality is the base currency's; the pivot frame has no
	// PIVOT-quoted row to probe directly.
	probe := r
	if r.Quote == Pivot {
		base := "EUR"
		if b, ok := shape["base"]; ok {
			base = strings.ToUpper(b)
		}
		probe = Record{Date: r.Date, Quote: base}
	}
	tablePivot, tableFound, err := p.pivotFrameRate(ctx, shape, probe, shape["from"], shape["to"], false)
	if err != nil {
		return false, err
	}
	canonical, found, err := p.canonicalRate(ctx, shape, probe)
	if err != nil {
		return false, err
	}
	return found && tableFound && tablePivot.Value == canonical.Value, nil
}

// canonicalRate is what a live range anchored at the record's own date
// computes, in the pivot frame so no derive denominator muddies the comparison.
func (p *parity) canonicalRate(ctx context.Context, shape Shape, r Record) (Number, bool, error) {
	return p.pivotFrameRate(ctx, shape, r, r.Date, r.Date, true)
}

// pivotFrameRate is the record's pivot-frame value over [from, to]: the
// original request based on the pivot so the blend is emitted undivided.
// Memoized per resulting query, since a full-history shape with several
// divergent records would otherwise replay per record.
func (p *parity) pivotFrameRate(ctx context.Context, shape Shape, r Record, from, to string, forceLive bool) (Number, bool, error) {
	params := Shape{"base": Pivot}
	for k, v := range shape {
		if k != "from" && k != "to" && k != "base" {
			params[k] = v
		}
	}
	if q, ok := params["quotes"]; ok {
		params["quotes"] = q + "," + r.Quote
	}
	params["from"] = from
	if to != "" {
		params["to"] = to
	}
	cacheKey := fmt.Sprint(params, forceLive)
	lookup, ok := p.frameCache[cacheKey]
	if !ok {
		records, err := p.records(ctx, params, forceLive, nil)
		if err != nil {
			return Number{}, false, err
		}
		lookup = map[[2]string]Number{}
		for _, rec := range records {
			lookup[[2]string{rec.Date, rec.Quote}] = rec.Rate
		}
		p.frameCache[cacheKey] = lookup
	}
	rate, found := lookup[[2]string{r.Date, r.Quote}]
	return rate, found, nil
}

func (p *parity) loadCoverage(ctx context.Context) error {
	var first, last sql.NullString
	if err := p.conn.QueryRowContext(ctx, "SELECT min(date), max(date) FROM rates").Scan(&first, &last); err != nil {
		return err
	}
	if !first.Valid {
		return fmt.Errorf("no rates data")
	}
	var err error
	if p.first, err = db.ParseDate(first.String); err != nil {
		return err
	}
	p.last, err = db.ParseDate(last.String)
	return err
}

func (p *parity) coverageDays() int { return int(p.last.Sub(p.first).Hours() / 24) }

func with(s Shape, k, v string) Shape {
	out := maps.Clone(s)
	out[k] = v
	return out
}

func (p *parity) adversarialShapes() []Shape {
	saturday := p.last.AddDate(0, 0, -((int(p.last.Weekday()) + 1) % 7))
	mid := p.first.AddDate(0, 0, p.coverageDays()/2)
	f := db.FormatDate
	return []Shape{
		{"from": f(p.first)},
		{"from": f(p.first), "quotes": "USD,GBP,JPY"},
		{"from": f(p.last.AddDate(0, 0, -60)), "base": "USD"},
		{"from": f(p.last.AddDate(0, 0, -60)), "base": "AED"},
		{"from": f(p.last.AddDate(0, 0, -90)), "quotes": "AED,XAU,USD"},
		{"from": f(saturday), "to": f(saturday.AddDate(0, 0, 10))},
		{"from": f(mid), "to": f(mid)},
		{"from": f(p.last.AddDate(0, 0, -30))},
	}
}

// between is Ruby's rng.rand(lo..hi), inclusive.
func (p *parity) between(lo, hi int) int {
	if hi < lo {
		return lo
	}
	return lo + p.rng.IntN(hi-lo+1)
}

func (p *parity) randomShape(ctx context.Context) (Shape, error) {
	days := p.coverageDays()
	var span int
	switch roll := p.rng.Float64(); {
	case roll < 0.75:
		span = p.between(1, min(60, days))
	case roll < 0.98:
		span = p.between(min(61, days), min(730, days))
	default:
		span = p.between(min(731, days), min(2200, days))
	}
	from := p.first.AddDate(0, 0, p.between(0, max(days-span, 0)))
	shape := Shape{"from": db.FormatDate(from), "to": db.FormatDate(from.AddDate(0, 0, span))}
	if p.rng.Float64() < 0.5 {
		shape["base"] = PopularBases[p.rng.IntN(len(PopularBases))]
	}
	if p.rng.Float64() < 0.5 {
		pool, err := p.quotePool(ctx)
		if err != nil {
			return nil, err
		}
		n := min(p.between(1, 6), len(pool))
		picked := slices.Clone(pool)
		p.rng.Shuffle(len(picked), func(i, j int) { picked[i], picked[j] = picked[j], picked[i] })
		shape["quotes"] = strings.Join(picked[:n], ",")
	}
	if p.rng.Float64() < 0.5 {
		shape["group"] = []string{"week", "month"}[p.rng.IntN(2)]
	}
	return shape, nil
}

// quotePool is the catalogue's codes the Money gem knows.
func (p *parity) quotePool(ctx context.Context) ([]string, error) {
	if p.pool != nil {
		return p.pool, nil
	}
	rows, err := p.conn.QueryContext(ctx, "SELECT iso_code FROM currencies")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	p.pool = []string{}
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return nil, err
		}
		if currency.Named(code) {
			p.pool = append(p.pool, code)
		}
	}
	return p.pool, rows.Err()
}
