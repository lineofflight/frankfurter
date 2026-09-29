// Package blend blends exchange rates from many providers into one series, and keeps the materialized blends
// (blended_rates, blended_weekly_rates, blended_monthly_rates) in step with the rates they are computed from.
//
// The pipeline is Blend: rebase each provider to a common base (Convert), flag cross-provider outliers (Annotate),
// then take a recency-weighted average (WeightedAverage). Sums use Ruby's compensated Array#sum so values match the
// Ruby app to the last bit wherever the inputs come in the same order.
package blend

import (
	"math"
	"sort"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/currency"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

// Blend is Blender#blend: rows rebased to base, outliers flagged, then averaged per quote with recency weights.
// today caps the reference date, as Ruby's Date.today does.
func Blend(rows []rates.Row, base string, today time.Time) []currency.Blended {
	return WeightedAverage(Annotate(Convert(rows, base)), today)
}

// Outliers is Blender#outliers: the rebased rows the consensus filter flags.
func Outliers(rows []rates.Row, base string) []rates.Row {
	return ConsensusOutliers(Convert(rows, base))
}

// kbSum adds values the way Ruby's Array#sum adds floats: Kahan-Babuska compensated summation.
func kbSum(values []float64) float64 {
	f, c := 0.0, 0.0
	for _, x := range values {
		switch {
		case math.IsNaN(f):
			continue
		case math.IsNaN(x):
			f = x
			continue
		case math.IsInf(x, 0):
			if math.IsInf(f, 0) && math.Signbit(x) != math.Signbit(f) {
				f = math.NaN()
			} else {
				f = x
			}
			continue
		case math.IsInf(f, 0):
			continue
		}
		t := f + x
		if math.Abs(f) >= math.Abs(x) {
			c += (f - t) + x
		} else {
			c += (x - t) + f
		}
		f = t
	}
	return f + c
}

// Recency weighting: rates within the grace period carry full weight; beyond it, weight decays exponentially so stale
// rates contribute less without a hard cutoff.
const (
	DecayGraceDays = 3
	DecayRate      = 0.5
)

// Annotated is a rebased row with the consensus verdict. Excluded rows are listed among a blend's providers but do
// not contribute to its rate.
type Annotated struct {
	rates.Row
	Excluded bool
}

func recencyWeight(daysOld int) float64 {
	return math.Exp(-DecayRate * float64(max(daysOld-DecayGraceDays, 0)))
}

func days(from, to time.Time) int { return int(math.Round(to.Sub(from).Hours() / 24)) }

// WeightedAverage is WeightedAverage#calculate: one blended row per quote, sorted by quote. Each takes the date,
// base and quote of its newest contributor, the recency-weighted mean of the contributors' rates, and the latest row
// of every provider (excluded ones included, marked) sorted by key. A quote whose rows are all excluded is dropped.
func WeightedAverage(rows []Annotated, today time.Time) []currency.Blended {
	if len(rows) == 0 {
		return []currency.Blended{}
	}
	reference := rows[0].Date
	for _, r := range rows[1:] {
		if r.Date.After(reference) {
			reference = r.Date
		}
	}
	if today.Before(reference) {
		reference = today
	}

	var quotes []string
	groups := map[string][]Annotated{}
	for _, r := range rows {
		if _, ok := groups[r.Quote]; !ok {
			quotes = append(quotes, r.Quote)
		}
		groups[r.Quote] = append(groups[r.Quote], r)
	}
	sort.Strings(quotes)

	out := make([]currency.Blended, 0, len(quotes))
	for _, quote := range quotes {
		group := groups[quote]
		var contributors []Annotated
		for _, r := range group {
			if !r.Excluded {
				contributors = append(contributors, r)
			}
		}
		if len(contributors) == 0 {
			continue
		}

		weights := make([]float64, len(contributors))
		products := make([]float64, len(contributors))
		newest := contributors[0]
		for i, r := range contributors {
			weights[i] = recencyWeight(days(r.Date, reference))
			products[i] = r.Rate * weights[i]
			if r.Date.After(newest.Date) {
				newest = r
			}
		}
		rate := kbSum(products) / kbSum(weights)

		latest := map[string]Annotated{}
		var keys []string
		for _, r := range group {
			l, ok := latest[r.Provider]
			if !ok {
				keys = append(keys, r.Provider)
			}
			if !ok || r.Date.After(l.Date) {
				latest[r.Provider] = r
			}
		}
		sort.Strings(keys)
		providers := make([]currency.Contribution, len(keys))
		for i, k := range keys {
			l := latest[k]
			providers[i] = currency.Contribution{Key: k, Date: l.Date, Rate: l.Rate, Excluded: l.Excluded}
		}

		out = append(out, currency.Blended{
			Date: newest.Date, Base: newest.Base, Quote: newest.Quote, Rate: rate, Providers: providers,
		})
	}
	return out
}
