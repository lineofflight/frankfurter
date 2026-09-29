// Package jpc fetches Japan Customs' weekly exchange rates for customs valuation: JPY per 1 or 100 foreign units,
// effective Sunday through Saturday. These weekly observations never blend with daily reference rates. The linked PDFs
// form a continuous archive from January 2002.
package jpc

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/pdftext"
)

const (
	indexURL   = "https://www.customs.go.jp/tetsuzuki/kawase/"
	archiveURL = indexURL + "list.htm"
)

var (
	coverageStart     = adapter.Date(2002, time.January, 6)
	currentIndexStart = adapter.Date(2008, time.January, 1)

	// ISO amendment 119 replaced YUM with CSD effective February 2003.
	csdCutover = adapter.Date(2003, time.February, 1)

	pdfHref = regexp.MustCompile(`(?i)href=["']([^"']*kouji-rate(\d{8})-[^"']+\.pdf)["']`)
	number  = regexp.MustCompile(`^[\d,]+\.\d+$`)
	isoCode = regexp.MustCompile(`^[A-Z]{3}$`)

	// The archive retains PLZ/BGL/SUR labels for the redenominated PLN (1995), BGN (1999) and RUB (1998). Values
	// already price the successor units.
	aliases = map[string]string{"PLZ": "PLN", "BGL": "BGN", "SUR": "RUB"}
)

func init() {
	adapter.Register("JPC", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches JPC rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter.
func (a *Adapter) BackfillRange() int { return 91 }

// LeadDays implements adapter.Adapter: the next Sunday's table is published during the preceding week.
func (a *Adapter) LeadDays() int { return 7 }

type report struct {
	date time.Time
	url  string
}

// Fetch implements adapter.Adapter. Unlike most adapters, `after` is inclusive: a report is kept when its effective
// Sunday falls within after..upto.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	horizon := a.Today().AddDate(0, 0, a.LeadDays())
	start := coverageStart
	if after.After(start) {
		start = after
	}
	end := horizon
	if !upto.IsZero() && upto.Before(end) {
		end = upto
	}
	if start.After(end) {
		return nil, nil
	}

	var indexes []string
	if start.Before(currentIndexStart) {
		indexes = append(indexes, archiveURL)
	}
	if !end.Before(currentIndexStart) {
		indexes = append(indexes, indexURL)
	}
	var reports []report
	for _, index := range indexes {
		listed, err := a.listing(ctx, index)
		if err != nil {
			return nil, err
		}
		for _, r := range listed {
			seen := slices.ContainsFunc(reports, func(o report) bool { return o.date.Equal(r.date) })
			if !seen && !r.date.Before(start) && !r.date.After(end) {
				reports = append(reports, r)
			}
		}
	}
	slices.SortFunc(reports, func(x, y report) int { return x.date.Compare(y.date) })

	var rates []adapter.Rate
	for i, r := range reports {
		if i > 0 {
			if err := a.Sleep(ctx, 100*time.Millisecond); err != nil {
				return nil, err
			}
		}
		body, err := a.Get(ctx, r.url, nil)
		if err != nil {
			return nil, err
		}
		parsed, err := parse(body, r.date)
		if err != nil {
			return nil, err
		}
		rates = append(rates, parsed...)
	}
	return rates, nil
}

func (a *Adapter) listing(ctx context.Context, index string) ([]report, error) {
	body, err := a.Get(ctx, index, nil)
	if err != nil {
		return nil, err
	}
	base, err := url.Parse(index)
	if err != nil {
		return nil, err
	}
	matches := pdfHref.FindAllStringSubmatch(string(body), -1)
	if len(matches) == 0 {
		return nil, fmt.Errorf("no weekly PDF links on %s", index)
	}
	reports := make([]report, 0, len(matches))
	for _, m := range matches {
		date, err := time.Parse("20060102", m[2])
		if err != nil {
			return nil, err
		}
		ref, err := url.Parse(m[1])
		if err != nil {
			return nil, err
		}
		reports = append(reports, report{date, base.ResolveReference(ref).String()})
	}
	return reports, nil
}

func parse(pdf []byte, date time.Time) ([]adapter.Rate, error) {
	pages, err := pdftext.Pages(pdf)
	if err != nil {
		return nil, err
	}
	runs := make([][]pdftext.Run, len(pages))
	for i, p := range pages {
		runs[i] = p.Runs
	}
	return parseRuns(runs, date)
}

type pair struct {
	code  string
	value pdftext.Run
}

func parseRuns(pages [][]pdftext.Run, date time.Time) ([]adapter.Rate, error) {
	day := date.Format(time.DateOnly)

	// Older PDFs lack usable Japanese Unicode mappings. ISO codes and numbers remain readable, but flattened text can
	// split a row across lines. Pair them by their PDF coordinates instead of interpreting garbled headers.
	var pairs []pair
	for _, runs := range pages {
		for _, code := range runs {
			if !isoCode.MatchString(code.Text) || code.Text == "ISO" {
				continue
			}
			var values []pdftext.Run
			for _, v := range runs {
				if v.X > code.EndX() && math.Abs(v.Y-code.Y) < 3 && number.MatchString(v.Text) {
					values = append(values, v)
				}
			}
			if len(values) > 1 {
				return nil, fmt.Errorf("multiple rates for %s on %s", code.Text, day)
			}
			// Some rows publish only an equivalence statement (e.g. BND equals SGD), not a numerical customs rate.
			if len(values) == 1 {
				pairs = append(pairs, pair{code.Text, values[0]})
			}
		}
	}

	// The two numeric columns are right-aligned: the left is JPY per 1 unit, the right JPY per 100. Identify their
	// right edges across all pages; layouts shift horizontally between historical editions.
	if len(pairs) == 0 {
		return nil, fmt.Errorf("missing unit columns on %s", day)
	}
	lo, hi := pairs[0].value.EndX(), pairs[0].value.EndX()
	for _, p := range pairs {
		lo, hi = min(lo, p.value.EndX()), max(hi, p.value.EndX())
	}
	if hi-lo < 40 {
		return nil, fmt.Errorf("missing unit columns on %s", day)
	}
	for _, p := range pairs {
		if edge := p.value.EndX(); min(edge-lo, hi-edge) >= 3 {
			return nil, fmt.Errorf("unexpected numeric column on %s", day)
		}
	}

	boundary := (lo + hi) / 2
	var rates []adapter.Rate
	for _, p := range pairs {
		unit := 1.0
		if p.value.EndX() > boundary {
			unit = 100
		}
		published, ok := adapter.ParseFloat(strings.ReplaceAll(p.value.Text, ",", ""))
		if !ok {
			return nil, fmt.Errorf("unreadable rate %q for %s on %s", p.value.Text, p.code, day)
		}
		rate := adapter.PerUnit(published, unit)
		if rate <= 0 {
			continue
		}

		base := p.code
		if alias, ok := aliases[base]; ok {
			base = alias
		}
		// YUN prices the post-1994 dinar. The source eventually adopts RSD in its own right.
		if base == "YUN" {
			base = "CSD"
			if date.Before(csdCutover) {
				base = "YUM"
			}
		}
		rates = append(rates, adapter.Rate{Date: date, Base: base, Quote: "JPY", Rate: rate})
	}
	return rates, nil
}
