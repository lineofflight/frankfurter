package blend

import (
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

// Convert is BaseConversion#convert: each provider's rows rebased to base, bridging only through that provider's own
// rows. Rows a provider cannot bridge are dropped. Providers come out in order of first appearance.
func Convert(rows []rates.Row, base string) []rates.Row {
	var providers []string
	groups := map[string][]rates.Row{}
	for _, r := range rows {
		if _, ok := groups[r.Provider]; !ok {
			providers = append(providers, r.Provider)
		}
		groups[r.Provider] = append(groups[r.Provider], r)
	}
	var out []rates.Row
	for _, p := range providers {
		out = append(out, reconcile(convertGroup(groups[p], base))...)
	}
	return out
}

type pair struct{ base, quote string }

func convertGroup(group []rates.Row, base string) []rates.Row {
	// First occurrence per pair, as Array#find would pick.
	index := map[pair]rates.Row{}
	for _, r := range group {
		if _, ok := index[pair{r.Base, r.Quote}]; !ok {
			index[pair{r.Base, r.Quote}] = r
		}
	}

	var out []rates.Row
	for _, r := range group {
		row := rates.Row{Date: r.Date, Base: base, Provider: r.Provider}
		switch {
		case r.Base == base:
			row = r
		case r.Quote == base:
			row.Quote, row.Rate = r.Base, 1.0/r.Rate
		default:
			if b, ok := index[pair{base, r.Quote}]; ok {
				row.Quote, row.Rate = r.Base, b.Rate/r.Rate
			} else if b, ok := index[pair{base, r.Base}]; ok {
				row.Quote, row.Rate = r.Quote, r.Rate*b.Rate
			} else if b, ok := index[pair{r.Base, base}]; ok {
				row.Quote, row.Rate = r.Quote, r.Rate/b.Rate
			} else {
				continue
			}
		}
		out = append(out, row)
	}
	return out
}

type dateQuote struct {
	date  int64 // Unix seconds: time.Time map keys also compare locations
	quote string
}

// reconcile collapses rows a provider reached by more than one bridge into one averaged rate per date and quote. A
// provider can reach the same quote two ways during a pivot-currency transition (Banque du Liban quoting against both
// LTL and EUR around Lithuania's 2015 euro adoption); a failed query is never the right answer to that, and consensus
// guards against genuine outliers downstream.
func reconcile(rows []rates.Row) []rates.Row {
	var keys []dateQuote
	groups := map[dateQuote][]rates.Row{}
	for _, r := range rows {
		k := dateQuote{r.Date.Unix(), r.Quote}
		if _, ok := groups[k]; !ok {
			keys = append(keys, k)
		}
		groups[k] = append(groups[k], r)
	}
	out := make([]rates.Row, 0, len(keys))
	for _, k := range keys {
		group := groups[k]
		row := group[0]
		if len(group) > 1 {
			values := make([]float64, len(group))
			for i, r := range group {
				values[i] = r.Rate
			}
			row.Rate = kbSum(values) / float64(len(group))
		}
		out = append(out, row)
	}
	return out
}
