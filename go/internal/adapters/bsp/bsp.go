// Package bsp fetches Bangko Sentral ng Pilipinas' daily Reference Exchange
// Rate Bulletin (RERB), published every business day as a single PDF.
//
// We relay one figure from it: the "BSP Reference Rate", BSP's own official
// USD/PHP mid (1 USD = X PHP). It is the only rate in the bulletin BSP actually
// computes. The rest is third-party data BSP reprints (an LSEG peso table, the
// IMF SDR rate, LBMA gold and silver), already covered by official sources
// elsewhere in the blend, so we skip it. The peso table is anchored on LSEG's
// USD/PHP, which differs from the Reference Rate. See #533.
//
// The bulletins live in a SharePoint list named "RERB", exposed without
// authentication via the SharePoint REST API. Each item's Title holds the
// bulletin date as DDMMMYYYY (e.g. "29May2026") and its attachment is the PDF.
//
// Coverage starts 2017-11-06: earlier bulletins are image-only scans with no
// text layer. They fetch harmlessly and yield no records.
package bsp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/pdftext"
)

const (
	host     = "https://www.bsp.gov.ph"
	listURL  = host + "/_api/web/lists/getByTitle('RERB')/items"
	pageSize = 100
)

var referenceRate = regexp.MustCompile(`BSP Reference Rate:\s*PHP\s+([\d.,]+)`)

func init() {
	adapter.Register("BSP", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches BSP rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter. Each day is its own PDF, so small
// windows keep backfill progress durable.
func (a *Adapter) BackfillRange() int { return 30 }

type entry struct {
	date time.Time
	url  string
}

// Fetch implements adapter.Adapter. As in the Ruby adapter, `after` is
// inclusive: bulletins dated after..upto are kept.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	if upto.IsZero() {
		upto = a.Today()
	}
	entries, err := a.discover(ctx, after, upto)
	if err != nil {
		return nil, err
	}

	var rates []adapter.Rate
	for i, e := range entries {
		if i > 0 {
			if err := a.Sleep(ctx, 500*time.Millisecond); err != nil {
				return nil, err
			}
		}
		body, err := a.Get(ctx, host+e.url, nil)
		if err != nil {
			return nil, err
		}
		if !bytes.HasPrefix(body, []byte("%PDF")) {
			continue
		}
		parsed, err := parse(body, e.date)
		if err != nil {
			return nil, err
		}
		rates = append(rates, parsed...)
	}
	return rates, nil
}

type listPage struct {
	D struct {
		Results []struct {
			Title           *string
			AttachmentFiles struct {
				Results []struct {
					ServerRelativeURL string `json:"ServerRelativeUrl"`
				} `json:"results"`
			}
		} `json:"results"`
		Next string `json:"__next"`
	} `json:"d"`
}

// discover walks the SharePoint list newest first, collecting bulletins dated
// within after..upto, and stops paging once an item predates the window.
func (a *Adapter) discover(ctx context.Context, after, upto time.Time) ([]entry, error) {
	var entries []entry
	u := fmt.Sprintf("%s?$top=%d&$orderby=Id%%20desc&$expand=AttachmentFiles&$select=Id,Title,AttachmentFiles",
		listURL, pageSize)

	for {
		req, err := a.NewRequest(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		// SharePoint returns Atom/XML unless asked for verbose OData JSON.
		req.Header.Set("Accept", "application/json;odata=verbose")
		resp, err := a.Do(req)
		if err != nil {
			return nil, err
		}
		var page listPage
		if err := json.Unmarshal(resp.Body, &page); err != nil {
			return nil, err
		}
		if len(page.D.Results) == 0 {
			break
		}

		done := false
		for _, item := range page.D.Results {
			if item.Title == nil {
				continue
			}
			date, ok := parseTitleDate(*item.Title)
			if !ok {
				continue
			}
			if !after.IsZero() && date.Before(after) {
				done = true
				continue
			}
			if date.After(upto) {
				continue
			}
			files := item.AttachmentFiles.Results
			if len(files) == 0 || files[0].ServerRelativeURL == "" {
				continue
			}
			if !slices.ContainsFunc(entries, func(e entry) bool { return e.date.Equal(date) }) {
				entries = append(entries, entry{date, files[0].ServerRelativeURL})
			}
		}
		if done || page.D.Next == "" {
			break
		}
		u = page.D.Next
	}

	slices.SortFunc(entries, func(x, y entry) int { return x.date.Compare(y.date) })
	return entries, nil
}

// parseTitleDate reads a Title like "29May2026", as Ruby's Date.strptime(title,
// "%d%b%Y"), which also takes one-digit days and full month names.
func parseTitleDate(title string) (time.Time, bool) {
	date, err := adapter.ParseDate(strings.TrimSpace(title), "2Jan2006", "2January2006")
	return date, err == nil
}

func parse(pdf []byte, date time.Time) ([]adapter.Rate, error) {
	text, err := pdftext.Text(pdf)
	if err != nil {
		return nil, err
	}
	return parseText(text, date)
}

// parseText reads the bulletin's only BSP-computed figure, the Reference Rate.
func parseText(text string, date time.Time) ([]adapter.Rate, error) {
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	m := referenceRate.FindStringSubmatch(text)
	if m == nil {
		return nil, fmt.Errorf("Reference Rate line missing from bulletin text for %s", date.Format(time.DateOnly))
	}
	usd, ok := adapter.ParseFloat(strings.ReplaceAll(m[1], ",", ""))
	if !ok {
		return nil, fmt.Errorf("invalid Reference Rate %q for %s", m[1], date.Format(time.DateOnly))
	}
	// Never relay a placeholder 0.00; drop the day instead.
	if usd == 0 {
		return nil, nil
	}
	return []adapter.Rate{{Date: date, Base: "USD", Quote: "PHP", Rate: usd}}, nil
}
