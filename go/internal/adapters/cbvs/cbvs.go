// Package cbvs fetches Centrale Bank van Suriname's indicative quotes in SRD
// for USD, EUR, GBP, CNY and seven Caribbean and South American currencies,
// published as PDF notices ("wisselkoersnoteringen"). Each notice carries buy
// and sell columns for transfers (wissels, cheques en overmakingen) and for
// banknotes; we take the midpoint of the transfer pair. GYD is quoted per 100
// and is normalised to a per-unit rate.
//
// The archive page lists one PDF per year for 2009-2023, one per month from
// 2024 and one per fixing for the current month (three a day, at 10:00, 12:30
// and 15:00 local). Fetch scrapes the page, classifies each link by the span it
// covers and downloads only those overlapping the requested window. Yearly PDFs
// are large (up to 10 MB, 1500 pages) and slow to parse, so history is fetched
// in a single pass rather than in chunks that would redownload the same file.
//
// One page per fixing, in the same text layout since 2009: a Dutch date line
// ("08 SEPTEMBER 2026", from 2021 with "VASTGESTELD OMSTREEKS 15:00U"), then
// one row per currency with the ISO code in parentheses and four numbers. Pages
// without a Dutch date line (gold-certificate valuations, an occasional English
// rendition of a notice, the sell-only extended overview appended to the 2022
// file) are skipped. 2013 uses a dot as the decimal separator; every other year
// uses a comma.
//
// A day with several fixings yields the last one. For the current day that
// means waiting for the closing fixing: storing the 10:00 quote would freeze
// it, since backfill never rewrites a stored row. In March 2021 the notice
// appended USD and EUR quotes at the maximum selling rate below the main table;
// the main table comes first and wins.
//
// Direction: foreign currency in base, SRD in quote (1 USD = X SRD).
//
// Unlike most adapters, `after` is inclusive, as in the Ruby adapter.
package cbvs

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/pdftext"
)

const (
	host       = "https://www.cbvs.sr"
	archiveURL = host + "/statistieken/financiele-markten-statistieken/dagelijkse-publicaties"

	closingFixing = "15:00"
	title         = "WISSELKOERSNOTERINGEN"
)

var (
	months = []string{"JANUARI", "FEBRUARI", "MAART", "APRIL", "MEI", "JUNI", "JULI", "AUGUSTUS", "SEPTEMBER",
		"OKTOBER", "NOVEMBER", "DECEMBER"}
	monthPattern = strings.Join(months, "|")

	pdfHref = regexp.MustCompile(`(?i)href="([^"]*/Wisselkoersen/[^"]*\.pdf)"`)

	// Filename shapes on the archive page, one per generation. Daily: "DO260908
	// 15.00 uur.pdf" (yymmdd, then the fixing time; "uu" typos occur). Monthly:
	// "WK_JANUARI_2024.pdf" or "WisselkoersnoteringMaarti2025.pdf" (typos
	// occur). Yearly: "Jaar_2010.pdf", "Jaar2014.pdf", "jaar-2009sep-dec.pdf",
	// "Jaar_2023_WK.pdf".
	dailyFile   = regexp.MustCompile(`\ADO(\d{2})(\d{2})(\d{2})\b`)
	monthlyFile = regexp.MustCompile(`(?i)(` + monthPattern + `)[A-Z]*?_?(\d{4})\.pdf\z`)
	yearlyFile  = regexp.MustCompile(`(?i)JAAR[_ -]?(\d{4})`)

	header = regexp.MustCompile(`(\d{1,2})\s+(` + monthPattern + `)\s+(\d{4})(?:\s+VASTGESTELD\s+OMSTREEKS\s+(\d{1,2})[.:](\d{2}))?`)

	// "U.S. DOLLAR (USD)", "GUYANA DOLLAR (PER 100 GYD)", "GUYANA DOLLAR (GYD
	// PER 100 )", "CHINESE YUAN RENMINBI (PER CNY)". A few rows lose the
	// closing parenthesis to the text extraction.
	row = regexp.MustCompile(`\A[A-Z][A-Z .&]*?\s*\((?:PER\s+)?(?:(\d+)\s+)?([A-Z]{3})(?:\s+PER\s+(\d+))?\s*\)?\s+` +
		`(\d[\d.,]*)\s+(\d[\d.,]*)\s+(\d[\d.,]*)\s+(\d[\d.,]*)\z`)
)

func init() {
	adapter.Register("CBVS", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches CBVS rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// fixing is one notice page. Time is "HH:MM" from 2021 and empty before.
type fixing struct {
	date    time.Time
	time    string
	records []adapter.Rate
}

// span is the inclusive range of dates a listed PDF covers.
type span struct {
	begin, end time.Time
}

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	today := a.Today()
	if upto.IsZero() {
		upto = today
	}
	urls, err := a.documents(ctx, after, upto)
	if err != nil {
		return nil, err
	}
	var fixings []fixing
	for i, u := range urls {
		if i > 0 {
			if err := a.Sleep(ctx, time.Second); err != nil {
				return nil, err
			}
		}
		body, err := a.Get(ctx, u, nil)
		if err != nil {
			return nil, err
		}
		fs, err := parse(body)
		if err != nil {
			return nil, err
		}
		fixings = append(fixings, fs...)
	}

	var rates []adapter.Rate
	for _, f := range closing(fixings, after, upto, today) {
		rates = append(rates, f.records...)
	}
	return rates, nil
}

// closing keeps the last fixing per date within the window, in the order given
// for ties. Today's date is held back until its closing fixing is out.
func closing(fixings []fixing, after, upto, today time.Time) []fixing {
	var order []time.Time
	latest := map[time.Time]fixing{}
	for _, f := range fixings {
		if !after.IsZero() && f.date.Before(after) {
			continue
		}
		if f.date.After(upto) {
			continue
		}
		if !f.date.Before(today) && f.time != "" && f.time < closingFixing {
			continue
		}
		current, ok := latest[f.date]
		if !ok {
			order = append(order, f.date)
		} else if current.time > f.time {
			continue
		}
		latest[f.date] = f
	}
	selected := make([]fixing, len(order))
	for i, d := range order {
		selected[i] = latest[d]
	}
	return selected
}

// pageText extracts one page's text. It stands in for pdf-reader's page.text,
// which can raise on a malformed page.
type pageText func() (string, error)

// parse reads one fixing per notice page.
func parse(data []byte) ([]fixing, error) {
	pages, err := pdftext.Pages(data)
	if err != nil {
		return nil, err
	}
	texts := make([]pageText, len(pages))
	for i, p := range pages {
		texts[i] = func() (string, error) { return p.Text(), nil }
	}
	return parsePages(texts)
}

func parsePages(pages []pageText) ([]fixing, error) {
	var fixings []fixing
	for _, page := range pages {
		text, err := page()
		if err != nil {
			// One page of the June 2025 monthly file has an invalid font. The
			// archive is static, so failing would block the provider at that
			// file for good; the day keeps its other fixings.
			continue
		}
		f, ok, err := parsePage(text)
		if err != nil {
			return nil, err
		}
		if ok {
			fixings = append(fixings, f)
		}
	}
	return fixings, nil
}

func parsePage(text string) (fixing, bool, error) {
	if !strings.Contains(text, title) {
		return fixing{}, false, nil
	}
	m := header.FindStringSubmatch(text)
	if m == nil {
		return fixing{}, false, nil
	}
	date, ok := validDate(atoi(m[3]), slices.Index(months, m[2])+1, atoi(m[1]))
	if !ok {
		return fixing{}, false, fmt.Errorf("invalid date %q", m[0])
	}
	f := fixing{date: date}
	if m[4] != "" {
		f.time = fmt.Sprintf("%02d:%s", atoi(m[4]), m[5])
	}
	for line := range strings.Lines(text) {
		r, ok, err := record(strings.TrimSpace(line), date)
		if err != nil {
			return fixing{}, false, err
		}
		if ok && !slices.ContainsFunc(f.records, func(x adapter.Rate) bool { return x.Base == r.Base }) {
			f.records = append(f.records, r)
		}
	}
	if len(f.records) == 0 {
		return fixing{}, false, nil
	}
	return f, true, nil
}

func record(line string, date time.Time) (adapter.Rate, bool, error) {
	m := row.FindStringSubmatch(line)
	if m == nil {
		return adapter.Rate{}, false, nil
	}
	unit := 1
	switch {
	case m[1] != "":
		unit = atoi(m[1])
	case m[3] != "":
		unit = atoi(m[3])
	}
	if unit == 0 {
		return adapter.Rate{}, false, nil
	}
	bid, err := number(m[4])
	if err != nil {
		return adapter.Rate{}, false, err
	}
	ask, err := number(m[5])
	if err != nil {
		return adapter.Rate{}, false, err
	}
	u := float64(unit)
	rate := adapter.PerUnit(adapter.Midpoint(bid, ask), u)
	if rate == 0 {
		return adapter.Rate{}, false, nil
	}
	return adapter.Rate{
		Date:  date,
		Base:  m[2],
		Quote: "SRD",
		Rate:  rate,
		Bid:   adapter.Float(adapter.PerUnit(bid, u)),
		Ask:   adapter.Float(adapter.PerUnit(ask, u)),
	}, true, nil
}

// number reads Dutch notation ("1.073,45") from 2014; a bare dot ("3.250") is
// the decimal separator in 2013.
func number(text string) (float64, error) {
	if strings.Contains(text, ",") {
		text = strings.ReplaceAll(text, ".", "")
	}
	return strconv.ParseFloat(strings.ReplaceAll(text, ",", "."), 64)
}

// coverage is the span of dates a listed PDF covers; ok is false for a link
// that is not a rate notice. An impossible date in a daily filename is an
// error, as Ruby's Date.new raises on it.
func coverage(href string) (span, bool, error) {
	name := path.Base(href)
	if m := dailyFile.FindStringSubmatch(name); m != nil {
		date, ok := validDate(2000+atoi(m[1]), atoi(m[2]), atoi(m[3]))
		if !ok {
			return span{}, false, fmt.Errorf("invalid date in %q", name)
		}
		return span{date, date}, true, nil
	}
	if m := monthlyFile.FindStringSubmatch(name); m != nil {
		first := adapter.Date(atoi(m[2]), time.Month(slices.Index(months, strings.ToUpper(m[1]))+1), 1)
		return span{first, first.AddDate(0, 1, -1)}, true, nil
	}
	if m := yearlyFile.FindStringSubmatch(name); m != nil {
		year := atoi(m[1])
		return span{adapter.Date(year, time.January, 1), adapter.Date(year, time.December, 31)}, true, nil
	}
	return span{}, false, nil
}

type listing struct {
	span span
	href string
}

// documents lists the archive links overlapping the window, oldest first. Of a
// day's several fixing PDFs only the latest is fetched.
func (a *Adapter) documents(ctx context.Context, after, upto time.Time) ([]string, error) {
	body, err := a.Get(ctx, archiveURL, nil)
	if err != nil {
		return nil, err
	}
	var hrefs []string
	for _, m := range pdfHref.FindAllSubmatch(body, -1) {
		href := strings.ReplaceAll(strings.ReplaceAll(string(m[1]), "%20", " "), "\t", "")
		if !slices.Contains(hrefs, href) {
			hrefs = append(hrefs, href)
		}
	}
	if len(hrefs) == 0 {
		return nil, errors.New("no rate PDFs on " + archiveURL)
	}

	var bulk []listing
	var dailyOrder []time.Time
	daily := map[time.Time]listing{}
	for _, href := range hrefs {
		s, ok, err := coverage(href)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		if !after.IsZero() && s.end.Before(after) {
			continue
		}
		if s.begin.After(upto) {
			continue
		}
		l := listing{s, href}
		if !s.begin.Equal(s.end) {
			bulk = append(bulk, l)
			continue
		}
		current, seen := daily[s.begin]
		if !seen {
			dailyOrder = append(dailyOrder, s.begin)
		}
		if !seen || path.Base(href) > path.Base(current.href) {
			daily[s.begin] = l
		}
	}
	listed := bulk
	for _, d := range dailyOrder {
		listed = append(listed, daily[d])
	}
	sort.SliceStable(listed, func(i, j int) bool { return listed[i].span.begin.Before(listed[j].span.begin) })

	base, _ := url.Parse(host)
	urls := make([]string, len(listed))
	for i, l := range listed {
		ref, err := url.Parse(escape(l.href))
		if err != nil {
			return nil, err
		}
		urls[i] = base.ResolveReference(ref).String()
	}
	return urls, nil
}

// escape percent-encodes the characters URI::RFC2396_PARSER.escape treats as
// unsafe.
func escape(s string) string {
	const safe = "-_.!~*'();/?:@&=+$,[]"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || strings.IndexByte(safe, c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// validDate builds a date, rejecting one Ruby's Date.new would reject rather
// than normalising it.
func validDate(year, month, day int) (time.Time, bool) {
	d := adapter.Date(year, time.Month(month), day)
	return d, d.Year() == year && int(d.Month()) == month && d.Day() == day
}

// atoi parses digits the regexps have already matched.
func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
