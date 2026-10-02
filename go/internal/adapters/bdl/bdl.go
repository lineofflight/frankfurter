// Package bdl fetches rates from Banque du Liban, which publishes daily
// official rates of the Lebanese pound (LBP) against seven currencies (USD,
// EUR, GBP, JPY, CHF, AUD, CAD).
//
// They come as a single rolling XLS with one sheet per calendar year (current
// year and the two before it). Each sheet has columns Period | Currency | Bid |
// Ask | Mid, newest row first, with Period as an Excel date cell. Rows before
// the 2024-03-28 step to 89,500 carry a real bid/ask spread; since then bid,
// ask and mid coincide. We emit the published Mid directly in both regimes, as
// "1 foreign = X LBP": foreign currency is the base, LBP the quote.
//
// The workbook is the only archive: years drop off the file as it rolls
// forward, so anything older than the window must already be stored. The site's
// HTML pages sit behind a Cloudflare JS challenge, but the XLS itself is served
// plainly.
//
// Unlike most adapters, Fetch treats after as inclusive, as the Ruby adapter
// does.
package bdl

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/xls"
)

const dataURL = "https://www.bdl.gov.lb/CB%20Com/Statistics%20And%20Research/Daily/BDL_DailyExchangeRates.xls"

var isoCode = regexp.MustCompile(`^[A-Z]{3}$`)

func init() {
	adapter.Register("BDL", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches BDL rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter: a single XLS holds every year on
// offer, so one fetch covers it.
func (a *Adapter) BackfillRange() int { return 36_525 }

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	body, err := a.Get(ctx, dataURL, nil)
	if err != nil {
		return nil, err
	}
	return parse(body, after, upto)
}

type key struct {
	date time.Time
	base string
}

// parse reads every sheet, keeping rows dated from after through upto, both
// inclusive; zero bounds are open.
func parse(data []byte, after, upto time.Time) ([]adapter.Rate, error) {
	sheets, err := xls.Open(data)
	if err != nil {
		return nil, err
	}
	if len(sheets) == 0 {
		return nil, errors.New("workbook has no worksheets")
	}

	// The file occasionally repeats a day verbatim (2025-11-17 appears twice in
	// the 2025 sheet). A repeat with a different mid is a conflict we cannot
	// resolve by row order, so fail loudly rather than pick one.
	var rates []adapter.Rate
	seen := map[key]float64{}
	var conflicts []string
	conflicted := map[key]bool{}
	for _, sheet := range sheets {
		for _, row := range sheet.Rows {
			cell := row.At(0)
			if cell.Kind != xls.Date {
				continue
			}
			date := time.Date(cell.Date.Year(), cell.Date.Month(), cell.Date.Day(), 0, 0, 0, 0, time.UTC)
			if !after.IsZero() && date.Before(after) || !upto.IsZero() && date.After(upto) {
				continue
			}

			base := strings.ToUpper(strings.TrimSpace(row.At(1).Text()))
			if !isoCode.MatchString(base) {
				continue
			}

			mid := row.At(4)
			if mid.Kind != xls.Number || mid.Number <= 0 {
				continue
			}

			k := key{date, base}
			if prev, ok := seen[k]; ok {
				if prev != mid.Number && !conflicted[k] {
					conflicted[k] = true
					conflicts = append(conflicts, base+" "+date.Format(time.DateOnly))
				}
				continue
			}
			seen[k] = mid.Number
			rates = append(rates, adapter.Rate{Date: date, Base: base, Quote: "LBP", Rate: mid.Number})
		}
	}
	if len(conflicts) > 0 {
		return nil, fmt.Errorf("conflicting rates for %s", strings.Join(conflicts, ", "))
	}
	return rates, nil
}
