// Package cbi fetches rates from the Central Bank of Iraq, which publishes daily reference buy and sell rates for the
// Iraqi dinar (IQD) against about 16 currencies plus gold in an XLSX file linked from cbi.iq/page/144.
//
// The page hosts three XLSX files (USD-only, multi-currency daily, and a 1995-present archive of foreign currencies
// against USD). We consume the multi-currency daily file: it gives IQD-pivoted rates back to 2009 for every currency
// CBI publishes. File URLs rotate when CBI re-uploads, so each fetch scrapes the page and picks the .xlsx link whose
// anchor text mentions gold.
//
// The workbook has one sheet per calendar year. The column layout has evolved: 2-column Buy/Sell groups in 2009-2024
// and 3-column Buy/Sell/Sell2 groups from 2025. Rows carry buy and sell as bid and ask with their mean as the rate, and
// the secondary sell column is ignored. Rates keep CBI's direction: foreign currency as base, IQD as quote.
package cbi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/PuerkitoBio/goquery"
	"github.com/xuri/excelize/v2"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const pageURL = "https://cbi.iq/page/144"

// codeAliases maps CBI's non-ISO labels to ISO 4217 codes.
var codeAliases = map[string]string{
	"S.FR": "CHF",
	"UAE":  "AED",
	"SDR":  "XDR",
	"Gold": "XAU",
}

var monthIndex = map[string]time.Month{
	"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
	"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
}

// CBI wrote "Spet.YYYY" throughout 2016-2024 before switching to "Sept" in 2025.
var monthRegex = regexp.MustCompile(`(?i)^\*?\s*(Jan|Feb|Mar|Apr|May|June?|July?|Aug|Sept?|Spet|Oct|Nov|Dec)[a-z]*[.,\s]*(?:\d{4})?$`)

var xlsxHref = regexp.MustCompile(`^https?://.+\.xlsx$`)

// goldMarker is Arabic for "gold". It appears only in the multi-currency daily file's link text, a selector that
// survives re-ordering or new files added to the page.
const goldMarker = "الذهب"

func init() {
	adapter.Register("CBI", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches CBI rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	page, err := a.Get(ctx, pageURL, nil)
	if err != nil {
		return nil, err
	}
	fileURL, err := discoverFileURL(page)
	if err != nil {
		return nil, err
	}
	body, err := a.Get(ctx, fileURL, nil)
	if err != nil {
		return nil, err
	}
	rates, err := parse(body)
	if err != nil {
		return nil, err
	}
	return adapter.Window(rates, after, upto), nil
}

func discoverFileURL(page []byte) (string, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(page))
	if err != nil {
		return "", err
	}
	var anchors []*goquery.Selection
	doc.Find("a").Each(func(_ int, s *goquery.Selection) {
		if href, ok := s.Attr("href"); ok && xlsxHref.MatchString(href) {
			anchors = append(anchors, s)
		}
	})
	if len(anchors) == 0 {
		return "", errors.New("no XLSX links found on page/144")
	}
	for _, s := range anchors {
		if strings.Contains(s.Text(), goldMarker) {
			href, _ := s.Attr("href")
			return href, nil
		}
	}
	return "", errors.New("could not find multi-currency daily XLSX on page/144")
}

var raw = excelize.Options{RawCellValue: true}

func parse(data []byte) ([]adapter.Rate, error) {
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var rates []adapter.Rate
	for _, name := range f.GetSheetList() {
		year, ok := rubyInteger(rubyStrip(name))
		if !ok {
			continue
		}
		rows, err := f.GetRows(name, raw)
		if err != nil {
			return nil, err
		}
		sheetRates, err := parseSheet(rows, int(year))
		if err != nil {
			return nil, err
		}
		rates = append(rates, sheetRates...)
	}
	return rates, nil
}

type column struct {
	buy, sell int
	code      string
}

// cell returns the cell at 0-based column i, or "" past the end of the row.
func cell(row []string, i int) string {
	if i < len(row) {
		return row[i]
	}
	return ""
}

func parseSheet(rows [][]string, year int) ([]adapter.Rate, error) {
	if len(rows) == 0 {
		return nil, nil // tolerate a not-yet-populated year sheet
	}
	layout, ok := detectLayout(rows)
	if !ok {
		return nil, fmt.Errorf("could not detect Buy/Sell layout on sheet %d", year)
	}

	var rates []adapter.Rate
	var month time.Month
	for _, row := range rows {
		a := cell(row, 0)
		if m, ok := monthFromLabel(a); ok {
			month = m
			continue
		}
		if month == 0 {
			continue
		}
		day, ok := rubyInteger(a)
		if !ok || day < 1 || day > 31 {
			continue
		}
		date := adapter.Date(year, month, int(day))
		if date.Day() != int(day) {
			continue // no such day in the month
		}

		for _, col := range layout {
			buy, okBuy := adapter.ParseFloat(cell(row, col.buy))
			sell, okSell := adapter.ParseFloat(cell(row, col.sell))
			if !okBuy || !okSell || buy <= 0 || sell <= 0 {
				continue
			}
			rates = append(rates, adapter.Rate{
				Date:  date,
				Base:  col.code,
				Quote: "IQD",
				Rate:  adapter.Midpoint(buy, sell),
				Bid:   adapter.Float(buy),
				Ask:   adapter.Float(sell),
			})
		}
	}
	return rates, nil
}

func detectLayout(rows [][]string) ([]column, bool) {
	bs := slices.IndexFunc(rows, func(row []string) bool {
		return slices.ContainsFunc(row, func(v string) bool { return strings.Contains(v, "Buy") })
	})
	if bs < 0 {
		return nil, false
	}
	header, labels := rows[:bs], rows[bs]

	layout := []column{}
	for buy, v := range labels {
		if !strings.Contains(v, "Buy") {
			continue
		}
		// The sell column is the next Sell label to the right, excluding "Sell 2".
		sell := -1
		for c := buy + 1; c < len(labels); c++ {
			if strings.Contains(labels[c], "Sell") && !strings.Contains(labels[c], "2") {
				sell = c
				break
			}
		}
		if sell < 0 {
			continue
		}
		if code := findCurrencyCode(header, buy, sell); code != "" {
			layout = append(layout, column{buy, sell, code})
		}
	}
	return layout, true
}

// findCurrencyCode searches the header rows for a currency code near the buy/sell group. Headers can land on the buy
// column (2009 layout) or any nearby column (2025-2026, where merged headers can land on the middle column).
func findCurrencyCode(header [][]string, buy, sell int) string {
	for _, row := range header {
		for c := buy; c <= sell+1; c++ {
			if text := cell(row, c); text != "" {
				if code := extractCode(text); code != "" {
					return code
				}
			}
		}
	}
	return ""
}

// extractCode finds an ISO 4217 code at the end of a header ("Saudi Arabian Riyal SAR") or alone in a cell ("USD",
// "S.FR", "Gold"), rewriting CBI's aliases to ISO codes.
func extractCode(text string) string {
	if code, ok := codeAliases[rubyStrip(text)]; ok {
		return code
	}
	token := lastCodeToken(text)
	if token == "" {
		return ""
	}
	if code, ok := codeAliases[token]; ok {
		return code
	}
	return token
}

// lastCodeToken is Ruby's text.scan(/\b([A-Z]{3,4}|S\.FR)\b/).last. Ruby's \b treats any Unicode letter or digit as a
// word character, where Go's regexp \b knows only ASCII, so the boundaries are checked by hand.
func lastCodeToken(text string) string {
	rs := []rune(text)
	isWord := func(i int) bool {
		if i < 0 || i >= len(rs) {
			return false
		}
		r := rs[i]
		return r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r)
	}
	upper := func(i int) bool { return i < len(rs) && rs[i] >= 'A' && rs[i] <= 'Z' }

	last := ""
	for i := 0; i < len(rs); {
		matched := 0
		if !isWord(i - 1) {
			for _, n := range []int{4, 3} {
				ok := true
				for k := range n {
					if !upper(i + k) {
						ok = false
						break
					}
				}
				if ok && isWord(i+n-1) && !isWord(i+n) {
					matched = n
					break
				}
			}
			if matched == 0 && i+4 <= len(rs) && string(rs[i:i+4]) == "S.FR" && !isWord(i+4) {
				matched = 4
			}
		}
		if matched > 0 {
			last = string(rs[i : i+matched])
			i += matched
		} else {
			i++
		}
	}
	return last
}

func monthFromLabel(label string) (time.Month, bool) {
	stripped := rubyStrip(label)
	if serial, ok := rubyInteger(stripped); ok && serial >= 30000 {
		return adapter.Date(1899, 12, 30).AddDate(0, 0, int(serial)).Month(), true
	}
	m := monthRegex.FindStringSubmatch(stripped)
	if m == nil {
		return 0, false
	}
	token := strings.Replace(strings.ToLower(m[1]), "spet", "sep", 1)[:3]
	month, ok := monthIndex[token]
	return month, ok
}

// rubyStrip is Ruby's String#strip, which leaves non-ASCII spaces such as U+00A0 alone, unlike strings.TrimSpace.
func rubyStrip(s string) string { return strings.Trim(s, " \t\n\v\f\r\x00") }

// rubyInteger is Ruby's Integer(s, exception: false): surrounding whitespace, a sign, base prefixes, a leading-zero
// octal and underscores between digits are accepted.
func rubyInteger(s string) (int64, bool) {
	n, err := strconv.ParseInt(strings.Trim(s, " \t\n\v\f\r"), 0, 64)
	return n, err == nil
}
