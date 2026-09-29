// Package bcccd fetches rates from the Banque Centrale du Congo, which publishes a daily indicative mid ("cours
// indicatif moyen") for a basket of currencies against the Congolese franc (CDF). The bare acronym "BCC" already names
// Banco Central de Cuba, so the key smushes in the country code: BCCCD.
//
// The site, a Next.js relaunch, exposes the fixing two ways and neither is complete on its own:
//
//   - A dated page per publication day, /cours-de-change/YYYY-MM-DD, back to 2020-10-12. It renders the mid for ten
//     majors as <dt>/<dd> pairs. Days without a fixing 404 (weekends never have one); a few early nodes 200 with no
//     rows.
//   - The landing page embeds the explorer's history as JSON in its React Server Components payload: buy, mid and sell
//     for the whole basket (21 codes today, the African neighbours among them), but only weekly before late August
//     2025.
//
// A fetch reads both: dated pages for the daily majors, the landing page for everything else. Where the two overlap
// they agree, and the dated page wins. The history occasionally lists a date twice with different values; the later
// entry is the one the dated page shows, so later entries win within the history too.
//
// Unlike most adapters, Fetch treats after as inclusive, as the Ruby adapter does.
package bcccd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/PuerkitoBio/goquery"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const quote = "CDF"

// pageURL is a variable so tests can point it at a local server.
var pageURL = "https://www.bcc.cd/marche-des-changes/cours-de-change"

var (
	midLabel = regexp.MustCompile(`^([A-Z]{3}) \(cours moyen\)$`)
	// Each currency's history is an array of flat objects, so nothing inside it opens a bracket.
	history = regexp.MustCompile(`"history":\{((?:"[A-Z]{3}":\[[^\]]*\],?)+)\}`)
)

func init() {
	adapter.Register("BCCCD", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches BCCCD rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter.
func (a *Adapter) BackfillRange() int { return 31 }

type key struct {
	date        time.Time
	base, quote string
}

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	end := upto
	if end.IsZero() {
		end = a.Today()
	}
	start := after
	if start.IsZero() {
		start = end
	}

	records := map[key]adapter.Rate{}
	add := func(r adapter.Rate) { records[key{r.Date, r.Base, r.Quote}] = r }

	payload, err := a.fetchHistory(ctx)
	if err != nil {
		return nil, err
	}
	hist, err := parseHistory(payload)
	if err != nil {
		return nil, err
	}
	for _, r := range hist {
		if !r.Date.Before(start) && !r.Date.After(end) {
			add(r)
		}
	}

	for date := start; !date.After(end); date = date.AddDate(0, 0, 1) {
		if wd := date.Weekday(); wd == time.Saturday || wd == time.Sunday {
			continue
		}
		html, err := a.fetchDay(ctx, date)
		if err != nil {
			return nil, err
		}
		day, err := parseDay(html, date)
		if err != nil {
			return nil, err
		}
		for _, r := range day {
			add(r)
		}
		if err := a.Sleep(ctx, 500*time.Millisecond); err != nil {
			return nil, err
		}
	}

	rates := make([]adapter.Rate, 0, len(records))
	for _, r := range records {
		rates = append(rates, r)
	}
	slices.SortFunc(rates, func(x, y adapter.Rate) int {
		if c := x.Date.Compare(y.Date); c != 0 {
			return c
		}
		return strings.Compare(x.Base, y.Base)
	})
	return rates, nil
}

func (a *Adapter) fetchDay(ctx context.Context, date time.Time) ([]byte, error) {
	req, err := a.NewRequest(ctx, http.MethodGet, pageURL+"/"+date.Format(time.DateOnly), nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.Do(req, http.StatusNotFound)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	return resp.Body, nil
}

// fetchHistory sends the RSC header, which asks Next.js for the component payload alone, without the HTML shell that
// splits the same JSON across script chunks.
func (a *Adapter) fetchHistory(ctx context.Context) ([]byte, error) {
	req, err := a.NewRequest(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("RSC", "1")
	resp, err := a.Do(req)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func parseDay(html []byte, date time.Time) ([]adapter.Rate, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(html))
	if err != nil {
		return nil, err
	}

	var rates []adapter.Rate
	doc.Find("dt").Each(func(_ int, dt *goquery.Selection) {
		m := midLabel.FindStringSubmatch(strings.TrimSpace(dt.Text()))
		if m == nil {
			return
		}
		dd := dt.Next()
		if dd.Length() == 0 || goquery.NodeName(dd) != "dd" {
			return
		}
		rate, ok := parseNumber(dd.Text())
		if !ok || rate <= 0 {
			return
		}
		rates = append(rates, adapter.Rate{Date: date, Base: m[1], Quote: quote, Rate: rate})
	})
	return rates, nil
}

type historyRow struct {
	Date    string   `json:"date"`
	Average *float64 `json:"average"`
	Unit    *float64 `json:"unit"`
}

type series struct {
	code string
	rows []historyRow
}

func parseHistory(payload []byte) ([]adapter.Rate, error) {
	matches := history.FindAllSubmatch(payload, -1)
	if len(matches) == 0 {
		return nil, errors.New("no rate history on " + pageURL)
	}

	var rates []adapter.Rate
	for _, m := range matches {
		all, err := decodeSeries(m[1])
		if err != nil {
			return nil, err
		}
		for _, s := range all {
			for _, row := range s.rows {
				r, ok, err := historyRecord(s.code, row)
				if err != nil {
					return nil, err
				}
				if ok {
					rates = append(rates, r)
				}
			}
		}
	}
	return rates, nil
}

// decodeSeries decodes the history object in key order. A repeated code keeps its first position and its last
// value, as a Ruby Hash does.
func decodeSeries(body []byte) ([]series, error) {
	dec := json.NewDecoder(bytes.NewReader(slices.Concat([]byte("{"), body, []byte("}"))))
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	var all []series
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		code, _ := tok.(string)
		var rows []historyRow
		if err := dec.Decode(&rows); err != nil {
			return nil, err
		}
		if i := slices.IndexFunc(all, func(s series) bool { return s.code == code }); i >= 0 {
			all[i].rows = rows
		} else {
			all = append(all, series{code, rows})
		}
	}
	return all, nil
}

func historyRecord(code string, row historyRow) (adapter.Rate, bool, error) {
	unit := 1.0
	if row.Unit != nil {
		unit = *row.Unit
	}
	if row.Average == nil || *row.Average <= 0 || unit <= 0 {
		return adapter.Rate{}, false, nil
	}
	rate := *row.Average
	if unit != 1 {
		rate /= unit
	}
	date, err := time.Parse(time.DateOnly, row.Date)
	if err != nil {
		return adapter.Rate{}, false, err
	}
	return adapter.Rate{Date: date, Base: code, Quote: quote, Rate: rate}, true, nil
}

// parseNumber reads "2 263,0000" on older pages and "2265.71" on newer ones.
func parseNumber(text string) (float64, bool) {
	cleaned := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, text)
	if strings.Contains(cleaned, ",") {
		cleaned = strings.ReplaceAll(strings.ReplaceAll(cleaned, ".", ""), ",", ".")
	}
	return adapter.ParseFloat(cleaned)
}
