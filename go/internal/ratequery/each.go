package ratequery

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/blend"
	"github.com/lineofflight/frankfurter/go/internal/currency"
	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

// errRebuilt fails a response whose materialized blend a rebuild deleted under
// its reads. Failing keeps the truncation out of caches; a retry lands on the
// capped live fallback.
var errRebuilt = errors.New("materialized blend rebuilt mid-request")

// Each yields the query's records in response order: a range in date order
// (quotes alphabetical within a date), a snapshot alphabetically by quote. It
// stops at the first error, from the query or from yield.
func (q *Query) Each(ctx context.Context, yield func(Record) error) error {
	if !q.Range() {
		stored, err := q.blendedTable(ctx)
		if err != nil {
			return err
		}
		return q.eachSnapshot(ctx, q.snapshotDate(), stored, yield)
	}
	start, end := q.rangeBounds()
	if q.rollup() {
		return q.eachRollupRange(ctx, start, end, yield)
	}
	stored, err := q.blendedTable(ctx)
	if err != nil {
		return err
	}
	if stored {
		return q.eachBlendedRange(ctx, start, end, yield)
	}
	return q.eachDailyRange(ctx, start, end, yield)
}

// All collects the records.
func (q *Query) All(ctx context.Context) ([]Record, error) {
	out := []Record{}
	err := q.Each(ctx, func(r Record) error {
		out = append(out, r)
		return nil
	})
	return out, err
}

// eachChunk walks [start, end] in chunks (3 months for daily ranges, 21 for
// weekly, 84 for monthly), checking the deadline before every chunk.
func (q *Query) eachChunk(start, end time.Time, fn func(start, end time.Time) error) error {
	months, ok := chunkMonths[q.group]
	if !ok {
		months = DefaultChunkMonths
	}
	for cursor := start; !cursor.After(end); {
		if err := q.checkDeadline(); err != nil {
			return err
		}
		chunkEnd := addMonths(cursor, months).AddDate(0, 0, -1)
		if chunkEnd.After(end) {
			chunkEnd = end
		}
		if err := fn(cursor, chunkEnd); err != nil {
			return err
		}
		cursor = chunkEnd.AddDate(0, 0, 1)
	}
	return nil
}

func (q *Query) rollupTables() (rates.Table, blend.Rollup) {
	if q.group == "week" {
		return rates.Weekly, blend.Weekly
	}
	return rates.Monthly, blend.Monthly
}

// eachRollupRange serves a grouped range from the materialized grouped blend
// where a chunk is fully materialized, and blends the provider rollups live
// otherwise (and always for providers= and expand=providers).
func (q *Query) eachRollupRange(ctx context.Context, start, end time.Time, yield func(Record) error) error {
	table, model := q.rollupTables()
	return q.eachChunk(start, end, func(cs, ce time.Time) error {
		if !q.ForceLive && q.providers == nil && !q.ExpandProviders() {
			stored, ok, err := readRollup(ctx, model, q.db, cs, ce, q.today)
			if err != nil {
				return err
			}
			if ok {
				if len(stored) == 0 {
					q.Coverage.Empty++
				} else {
					q.Coverage.Materialized++
				}
				for _, group := range groupBlendedByDate(stored) {
					blended := group
					if q.base != Pivot {
						blended = derive(group, q.base)
					}
					if err := q.emitRecords(blended, nil, yield); err != nil {
						return err
					}
				}
				return nil
			}
		}

		q.Coverage.Fallback++
		scope, err := q.rawScope(ctx, table)
		if err != nil {
			return err
		}
		rows, err := rates.Select(ctx, q.db, scope.Between(cs, ce, q.today).
			Columns("bucket_date, base, quote, provider, rate").SQL())
		if err != nil {
			return err
		}
		for _, group := range groupRowsByDate(rows) {
			if err := q.emitBlended(group, yield); err != nil {
				return err
			}
		}
		return nil
	})
}

// eachBlendedRange mirrors eachDailyRange on materialized rows: per-quote
// carry-forward reconstructs each anchor's batch (a snap-back echo keeps a
// silent quote visible), then emit derives the base, filters quotes, adds the
// identity row and rounds. Stored rows are canonical anchor-date values, so a
// snap-back row equals the value a range anchored at the row's own date would
// produce (#570).
func (q *Query) eachBlendedRange(ctx context.Context, start, end time.Time, yield func(Record) error) error {
	seen := map[[2]string]bool{}
	err := q.eachChunk(start, end, func(cs, ce time.Time) error {
		rows, err := q.storedRows(ctx, cs.AddDate(0, 0, -rates.LookbackDays), ce)
		if err != nil {
			return err
		}
		return q.eachAnchor(rows, cs, ce, rates.LookbackDays, func(contributors []rates.Row) error {
			blended := toBlended(contributors)
			if q.base != Pivot {
				blended = derive(blended, q.base)
			}
			return q.emitRecords(blended, nil, dedupe(seen, yield))
		})
	})
	if err != nil {
		return err
	}
	ready, err := dailyReady(ctx, q.db)
	if err != nil {
		return err
	}
	if !ready {
		return errRebuilt
	}
	return nil
}

// eachDailyRange is the heavy path: the shapes validateRangeCost bounds
// (providers=, expand=providers and the not-ready fallback) recompute the blend
// per date, so it draws on the process-wide slot cap and returns the slot
// however the enumeration ends (#650). When the range start is silent,
// carry-forward anchors on it as well so the response surfaces the most recent
// prior data, as ?date=start would (#71); records dedupe on quote and
// observation date so a pair whose contributors have not changed does not
// reappear.
func (q *Query) eachDailyRange(ctx context.Context, start, end time.Time, yield func(Record) error) error {
	if err := q.acquireSlot(); err != nil {
		return err
	}
	defer q.ReleaseSlot()
	lookback, err := q.lookbackDays(ctx)
	if err != nil {
		return err
	}
	seen := map[[2]string]bool{}
	return q.eachChunk(start, end, func(cs, ce time.Time) error {
		rows, err := q.rawRows(ctx, cs.AddDate(0, 0, -lookback), ce)
		if err != nil {
			return err
		}
		return q.eachAnchor(rows, cs, ce, lookback, func(contributors []rates.Row) error {
			return q.emitBlended(contributors, dedupe(seen, yield))
		})
	})
}

// eachAnchor runs fn on the non-empty carry-forward snapshots at each date in
// [cs, ce] that has rows, plus cs itself.
func (q *Query) eachAnchor(rows []rates.Row, cs, ce time.Time, lookback int, fn func([]rates.Row) error) error {
	var anchors []time.Time
	seen := map[int64]bool{}
	for _, r := range rows {
		if !r.Date.Before(cs) && !r.Date.After(ce) && !seen[r.Date.Unix()] {
			seen[r.Date.Unix()] = true
			anchors = append(anchors, r.Date)
		}
	}
	if !seen[cs.Unix()] {
		anchors = append(anchors, cs)
	}
	var err error
	rates.EachSnapshot(rows, anchors, lookback, func(_ time.Time, contributors []rates.Row) {
		if err == nil && len(contributors) > 0 {
			err = fn(contributors)
		}
	})
	return err
}

func dedupe(seen map[[2]string]bool, yield func(Record) error) func(Record) error {
	return func(r Record) error {
		key := [2]string{r.Quote, r.Date}
		if seen[key] {
			return nil
		}
		seen[key] = true
		return yield(r)
	}
}

// eachSnapshot serves latest and single dates. From the table, the newest
// stored row per quote within the lookback is the canonical anchor-date value,
// so a dated row means the same thing here as in a range instead of re-decaying
// against the asking day (#573).
func (q *Query) eachSnapshot(ctx context.Context, date time.Time, stored bool, yield func(Record) error) error {
	lookback, err := q.lookbackDays(ctx)
	if err != nil {
		return err
	}
	start := date.AddDate(0, 0, -lookback)
	if stored {
		rows, err := q.storedRows(ctx, start, date)
		if err != nil {
			return err
		}
		blended := toBlended(rates.CarryForward(rows, date, lookback))
		if q.base != Pivot {
			blended = derive(blended, q.base)
		}
		if err := q.emitRecords(blended, nil, yield); err != nil {
			return err
		}
		ready, err := dailyReady(ctx, q.db)
		if err != nil {
			return err
		}
		if !ready {
			return errRebuilt
		}
		return nil
	}
	rows, err := q.rawRows(ctx, start, date)
	if err != nil {
		return err
	}
	return q.emitBlended(rates.CarryForward(rows, date, lookback), yield)
}

// rawRows reads the query's daily rows dated in [start, end].
func (q *Query) rawRows(ctx context.Context, start, end time.Time) ([]rates.Row, error) {
	scope, err := q.rawScope(ctx, rates.Daily)
	if err != nil {
		return nil, err
	}
	return rates.Select(ctx, q.db, scope.Filter(dateBetween("date", start, end)).
		Columns("date, base, quote, provider, rate").SQL())
}

// storedRows reads the materialized daily blend in [start, end], as pivot-based
// rows.
func (q *Query) storedRows(ctx context.Context, start, end time.Time) ([]rates.Row, error) {
	return rates.Select(ctx, q.db, "SELECT date, '"+Pivot+"', quote, '', rate FROM blended_rates WHERE "+
		dateBetween("date", start, end))
}

func toBlended(rows []rates.Row) []currency.Blended {
	out := make([]currency.Blended, len(rows))
	for i, r := range rows {
		out[i] = currency.Blended{Date: r.Date, Base: r.Base, Quote: r.Quote, Rate: r.Rate}
	}
	return out
}

func groupRowsByDate(rows []rates.Row) [][]rates.Row {
	index := map[int64]int{}
	var groups [][]rates.Row
	for _, r := range rows {
		i, ok := index[r.Date.Unix()]
		if !ok {
			i = len(groups)
			index[r.Date.Unix()] = i
			groups = append(groups, nil)
		}
		groups[i] = append(groups[i], r)
	}
	sort.SliceStable(groups, func(i, j int) bool { return groups[i][0].Date.Before(groups[j][0].Date) })
	return groups
}

func groupBlendedByDate(rows []currency.Blended) [][]currency.Blended {
	index := map[int64]int{}
	var groups [][]currency.Blended
	for _, r := range rows {
		i, ok := index[r.Date.Unix()]
		if !ok {
			i = len(groups)
			index[r.Date.Unix()] = i
			groups = append(groups, nil)
		}
		groups[i] = append(groups[i], r)
	}
	return groups
}

// emitBlended blends a batch through the pivot frame and emits it. A per-batch
// choice of frame used to make ranges and snapshots disagree, since consensus
// and weighting see differently shaped numbers in each (#570, #573).
func (q *Query) emitBlended(rows []rates.Row, yield func(Record) error) error {
	if q.emitHook != nil {
		if err := q.emitHook(); err != nil {
			return err
		}
	}
	return q.emitRecords(q.pivotPathBlend(rows), rows, yield)
}

func (q *Query) providerPegBase() bool {
	if q.providers == nil || uniqCount(q.providers) <= 1 {
		return false
	}
	_, pegged := currency.FindPeg(q.base)
	return pegged
}

func (q *Query) pivotPathBlend(rows []rates.Row) []currency.Blended {
	// One provider is that provider's own view, pegged base or not: its cross
	// is the answer the caller asked for.
	if q.single() {
		return q.singleProviderBlend(rows)
	}
	// Restricting the source set to several providers bypasses the peg layer
	// entirely, so a pegged request base has no anchor to rebase through.
	if q.providerPegBase() {
		return nil
	}
	blended := blendRows(rows, Pivot, q.today)
	if q.providers == nil {
		blended = currency.AnchorPegs(blended, Pivot)
	}
	if len(blended) == 0 || q.base == Pivot {
		return blended
	}
	return derive(blended, q.base)
}

// singleProviderBlend reaches the request base in one hop through the
// provider's own base rather than a round trip through USD. That keeps rows the
// provider never bridged to USD, and derives each pair from two rows instead of
// four (#645). A provider mid-transition between pivot currencies (LB around
// Lithuania's euro adoption) reaches one quote through two bridges dated
// differently; the newer observation wins, as everywhere else carry-forward
// applies.
func (q *Query) singleProviderBlend(rows []rates.Row) []currency.Blended {
	converted := blend.Convert(rows, q.base)
	var order []string
	newest := map[string]rates.Row{}
	for _, r := range converted {
		cur, ok := newest[r.Quote]
		if !ok {
			order = append(order, r.Quote)
		}
		if !ok || r.Date.After(cur.Date) {
			newest[r.Quote] = r
		}
	}
	out := make([]currency.Blended, len(order))
	for i, quote := range order {
		r := newest[quote]
		out[i] = currency.Blended{Date: r.Date, Base: r.Base, Quote: r.Quote, Rate: r.Rate,
			Providers: []currency.Contribution{{Key: r.Provider, Date: r.Date, Rate: r.Rate}}}
	}
	return out
}

type pair struct{ base, quote string }

// emitRecords turns blended rows into records: the quotes filter, rounding (or
// a single provider's own digits), the contributor lists, the base's identity
// row, and response order. rows are the raw contributors, for the passthrough.
func (q *Query) emitRecords(blended []currency.Blended, rows []rates.Row, yield func(Record) error) error {
	if len(blended) == 0 {
		return nil
	}
	// Echo a single provider's own published digits, but only for native daily
	// rows. Rollup buckets are time-averages, so their extra precision is
	// synthetic and stays rounded.
	passthrough := q.providers != nil && len(q.providers) == 1 && !q.rollup()
	lookup := map[pair]float64{}
	if passthrough {
		for _, r := range rows {
			lookup[pair{r.Base, r.Quote}] = r.Rate
		}
	}

	records := make([]Record, 0, len(blended)+1)
	for _, r := range blended {
		if q.quotes != nil && !slices.Contains(q.quotes, r.Quote) {
			continue
		}
		stored, hasStored := lookup[pair{r.Base, r.Quote}]
		hasStored = hasStored && !math.IsNaN(stored)
		rate := Float(stored)
		if !hasStored {
			var err error
			if rate, err = round(r.Rate); err != nil {
				return err
			}
		}
		record := Record{Date: db.FormatDate(r.Date), Base: r.Base, Quote: r.Quote, Rate: rate}
		if q.ExpandProviders() && r.Providers != nil {
			record.HasProviders = true
			record.Providers = make([]Contribution, len(r.Providers))
			for i, p := range r.Providers {
				pRate := Float(stored)
				if !(passthrough && p.Key == q.providers[0] && hasStored) {
					var err error
					if pRate, err = round(p.Rate); err != nil {
						return err
					}
				}
				record.Providers[i] = Contribution{Key: p.Key, Date: db.FormatDate(p.Date), Rate: pRate, Excluded: p.Excluded}
			}
		}
		records = append(records, record)
	}

	// Synthesize the base's identity rate (#538), subject to the quotes filter
	// like any other row. It takes the date of the newest visible record so it
	// never leaks a hidden pivot row the quotes filter dropped; when the filter
	// leaves no other rows, the blend's reference date.
	if q.quotes == nil || slices.Contains(q.quotes, q.base) {
		has := slices.ContainsFunc(records, func(r Record) bool { return r.Quote == q.base })
		if !has {
			ref := ""
			for _, r := range records {
				ref = max(ref, r.Date)
			}
			if ref == "" {
				newest := blended[0].Date
				for _, b := range blended[1:] {
					if b.Date.After(newest) {
						newest = b.Date
					}
				}
				ref = db.FormatDate(newest)
			}
			records = append(records, Record{Date: ref, Base: q.base, Quote: q.base, Rate: Float(1)})
		}
	}

	// Ranges stream chunks in date order, so records sort by date then quote
	// within a chunk. A snapshot is one batch where carry-forward mixes
	// observation dates, so it sorts by quote alone to stay alphabetical
	// (#360).
	if q.Range() {
		sort.Slice(records, func(i, j int) bool {
			if records[i].Date != records[j].Date {
				return records[i].Date < records[j].Date
			}
			return records[i].Quote < records[j].Quote
		})
	} else {
		sort.Slice(records, func(i, j int) bool { return records[i].Quote < records[j].Quote })
	}
	for _, r := range records {
		if err := yield(r); err != nil {
			return err
		}
	}
	return nil
}

// round is Roundable#round. Ruby returns an Integer above 5000 and raises on
// NaN and infinities.
func round(v float64) (Number, error) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return Number{}, fmt.Errorf("cannot round %v", v)
	}
	if v > 5000 {
		return Number{Value: math.Round(v), Int: true}, nil
	}
	return Float(rates.Round(v)), nil
}

// derive rebases rows blended in the pivot (one per quote) to target by
// division, dropping the target's own row and appending target -> pivot. It
// returns nothing when target is not among the quotes (no path to derive).
func derive(rows []currency.Blended, target string) []currency.Blended {
	if len(rows) == 0 {
		return nil
	}
	i := slices.IndexFunc(rows, func(r currency.Blended) bool { return r.Quote == target })
	if i < 0 {
		return nil
	}
	pivotToTarget := rows[i]
	targetRate := pivotToTarget.Rate
	out := make([]currency.Blended, 0, len(rows))
	for _, r := range rows {
		if r.Quote == target {
			continue
		}
		out = append(out, rebase(r, target, r.Quote, func(x float64) float64 { return x / targetRate }))
	}
	return append(out, rebase(pivotToTarget, target, pivotToTarget.Base, func(x float64) float64 { return 1.0 / x }))
}

// rebase applies f to a row's rate and to each contributor's, so the providers
// list stays consistent with the row's base.
func rebase(r currency.Blended, base, quote string, f func(float64) float64) currency.Blended {
	out := currency.Blended{Date: r.Date, Base: base, Quote: quote, Rate: f(r.Rate)}
	if r.Providers != nil {
		out.Providers = make([]currency.Contribution, len(r.Providers))
		for i, p := range r.Providers {
			p.Rate = f(p.Rate)
			out.Providers[i] = p
		}
	}
	return out
}
