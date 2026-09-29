package blend

import (
	"math"
	"sort"

	"github.com/lineofflight/frankfurter/go/internal/rates"
)

// Consensus thresholds: a quote needs MinProviders distinct providers before
// any is judged. A rate is an outlier when it sits further from the median than
// MADMultiplier median absolute deviations, and at least MinDeviation of the
// median away.
const (
	MinProviders  = 4
	MADMultiplier = 10
	MinDeviation  = 0.05
)

type pairKey struct{ provider, quote string }

// flagged compares each provider's rebased rates against the median of their
// quote and returns the provider and quote pairs that deviate significantly.
func flagged(rows []rates.Row) map[pairKey]bool {
	out := map[pairKey]bool{}
	var quotes []string
	groups := map[string][]rates.Row{}
	for _, r := range rows {
		if _, ok := groups[r.Quote]; !ok {
			quotes = append(quotes, r.Quote)
		}
		groups[r.Quote] = append(groups[r.Quote], r)
	}
	for _, quote := range quotes {
		group := groups[quote]
		providers := map[string]bool{}
		for _, r := range group {
			providers[r.Provider] = true
		}
		if len(providers) < MinProviders {
			continue
		}

		values := make([]float64, len(group))
		for i, r := range group {
			values[i] = r.Rate
		}
		med := median(values)
		deviations := make([]float64, len(values))
		for i, v := range values {
			deviations[i] = math.Abs(v - med)
		}
		threshold := math.Max(MADMultiplier*median(deviations), MinDeviation*math.Abs(med))
		for _, r := range group {
			if math.Abs(r.Rate-med) > threshold {
				out[pairKey{r.Provider, r.Quote}] = true
			}
		}
	}
	return out
}

// median is the upper median, as Consensus#median picks sorted[size / 2].
func median(values []float64) float64 {
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	return sorted[len(sorted)/2]
}

// ConsensusOutliers is Consensus#outliers: every row whose provider and quote
// pair deviates from its quote's consensus.
func ConsensusOutliers(rows []rates.Row) []rates.Row {
	flags := flagged(rows)
	var out []rates.Row
	for _, r := range rows {
		if flags[pairKey{r.Provider, r.Quote}] {
			out = append(out, r)
		}
	}
	return out
}

// ConsensusFind is Consensus#find: the rows that are not outliers.
func ConsensusFind(rows []rates.Row) []rates.Row {
	flags := flagged(rows)
	var out []rates.Row
	for _, r := range rows {
		if !flags[pairKey{r.Provider, r.Quote}] {
			out = append(out, r)
		}
	}
	return out
}

// Annotate is Consensus#annotated: every row, with outliers marked excluded.
func Annotate(rows []rates.Row) []Annotated {
	flags := flagged(rows)
	out := make([]Annotated, len(rows))
	for i, r := range rows {
		out[i] = Annotated{Row: r, Excluded: flags[pairKey{r.Provider, r.Quote}]}
	}
	return out
}
