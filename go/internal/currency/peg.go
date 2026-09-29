package currency

import (
	"sync"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/seeds"
)

// Peg fixes Quote at Rate units per one Base since Since.
type Peg = seeds.Peg

var pegs = sync.OnceValue(func() []Peg {
	all, err := seeds.Pegs()
	if err != nil {
		panic("currency: " + err.Error())
	}
	return all
})

// Pegs returns every peg, sorted by seed file name. Callers must not modify it.
func Pegs() []Peg { return pegs() }

// FindPeg returns the peg whose quote is code.
func FindPeg(code string) (Peg, bool) {
	for _, p := range pegs() {
		if p.Quote == code {
			return p, true
		}
	}
	return Peg{}, false
}

// Contribution is one provider's part in a blended rate.
type Contribution struct {
	Key      string
	Date     time.Time
	Rate     float64
	Excluded bool
}

// Blended is one blended rate: Quote units per one Base on Date. Providers is nil for a row no provider contributed
// to (Ruby's row without a :providers key), such as a peg-synthesised one.
type Blended struct {
	Date      time.Time
	Base      string
	Quote     string
	Rate      float64
	Providers []Contribution
}

// AnchorPegs is PegAnchor.apply: peg-aware post-processing of blended rates that share one base.
//
// Pegs are a source of rate data alongside providers, so callers apply this only when the source set is unrestricted
// (no providers filter). Two interactions:
//
//  1. A blended quote may be pegged. From the peg's start its rate becomes the peg value, directly when base is the
//     peg's base, or through the peg's base as a bridge. Its providers stay listed, all marked excluded: the peg, not
//     the providers, defined the rate.
//  2. A peg's quote may have no provider row. One is synthesised from the peg's anchor at the newest row date, with
//     no providers.
//
// Rebasing to the caller's base is the caller's job.
func AnchorPegs(rows []Blended, base string) []Blended {
	if len(rows) == 0 {
		return []Blended{}
	}
	out := make([]Blended, 0, len(rows))
	for _, r := range rows {
		out = append(out, anchorQuote(rows, r, base))
	}

	reference := rows[0].Date
	for _, r := range rows[1:] {
		if r.Date.After(reference) {
			reference = r.Date
		}
	}
	emitted := make(map[string]bool, len(out))
	for _, r := range out {
		emitted[r.Quote] = true
	}
	anchored := out
	for _, peg := range pegs() {
		if peg.Quote == base || emitted[peg.Quote] || reference.Before(peg.Since) {
			continue
		}
		rate := peg.Rate
		if peg.Base != base {
			anchor, ok := findQuote(anchored, peg.Base)
			if !ok {
				continue
			}
			rate = anchor.Rate * peg.Rate
		}
		out = append(out, Blended{Date: reference, Base: base, Quote: peg.Quote, Rate: rate})
	}
	return out
}

func anchorQuote(rows []Blended, row Blended, base string) Blended {
	peg, ok := FindPeg(row.Quote)
	if !ok || row.Date.Before(peg.Since) {
		return row
	}
	rate := peg.Rate
	if peg.Base != base {
		bridge, ok := findQuote(rows, peg.Base)
		if !ok {
			return row
		}
		rate = bridge.Rate * peg.Rate
	}
	row.Rate = rate
	if row.Providers != nil {
		providers := make([]Contribution, len(row.Providers))
		for i, p := range row.Providers {
			p.Excluded = true
			providers[i] = p
		}
		row.Providers = providers
	}
	return row
}

func findQuote(rows []Blended, quote string) (Blended, bool) {
	for _, r := range rows {
		if r.Quote == quote {
			return r, true
		}
	}
	return Blended{}, false
}
