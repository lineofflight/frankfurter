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
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/PuerkitoBio/goquery"

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

var cellRef = regexp.MustCompile(`^[A-Z]+\d+$`)

// goldMarker is Arabic for "gold". It appears only in the multi-currency daily file's link text, a selector that
// survives re-ordering or new files added to the page.
const goldMarker = "الذهب"

const relsNS = "http://schemas.openxmlformats.org/package/2006/relationships"

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

func parse(data []byte) ([]adapter.Rate, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	sharedStrings, err := readSharedStrings(zr)
	if err != nil {
		return nil, err
	}
	sheets, err := readSheetTargets(zr)
	if err != nil {
		return nil, err
	}

	var rates []adapter.Rate
	for _, s := range sheets {
		year, ok := rubyInteger(strings.TrimSpace(s.name))
		if !ok {
			continue
		}
		sheetXML, err := readEntry(zr, "xl/"+s.target)
		if errors.Is(err, errNoEntry) {
			continue
		}
		if err != nil {
			return nil, err
		}
		sheetRates, err := parseSheet(sheetXML, sharedStrings, int(year))
		if err != nil {
			return nil, err
		}
		rates = append(rates, sheetRates...)
	}
	return rates, nil
}

type row struct {
	r     int
	cells map[string]*string
}

func (r row) cell(col string) (string, bool) {
	v, ok := r.cells[col]
	if !ok {
		return "", false
	}
	return *v, true
}

type column struct {
	buy, sell, code string
}

func parseSheet(data []byte, sharedStrings []string, year int) ([]adapter.Rate, error) {
	rows, err := parseRows(data, sharedStrings)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil // tolerate a not-yet-populated year sheet
	}
	layout, ok := detectLayout(rows)
	if !ok {
		return nil, fmt.Errorf("could not detect Buy/Sell layout on sheet %d", year)
	}

	var rates []adapter.Rate
	var month time.Month
	for _, rw := range rows {
		a, hasA := rw.cell("A")
		if hasA {
			if m, ok := monthFromLabel(a); ok {
				month = m
				continue
			}
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
			bs, _ := rw.cell(col.buy)
			ss, _ := rw.cell(col.sell)
			buy, okBuy := adapter.ParseFloat(bs)
			sell, okSell := adapter.ParseFloat(ss)
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

func detectLayout(rows []row) ([]column, bool) {
	var bs *row
	for i := range rows {
		for _, v := range rows[i].cells {
			if strings.Contains(*v, "Buy") {
				bs = &rows[i]
				break
			}
		}
		if bs != nil {
			break
		}
	}
	if bs == nil {
		return nil, false
	}

	var buyCols []string
	for c, v := range bs.cells {
		if strings.Contains(*v, "Buy") {
			buyCols = append(buyCols, c)
		}
	}
	slices.SortFunc(buyCols, func(x, y string) int { return colToNum(x) - colToNum(y) })

	var headerRows []row
	for _, rw := range rows {
		if rw.r < bs.r {
			headerRows = append(headerRows, rw)
		}
	}

	layout := []column{}
	for _, buy := range buyCols {
		// The sell column is the next Sell label to the right, excluding "Sell 2".
		sell := ""
		for c, v := range bs.cells {
			if colToNum(c) <= colToNum(buy) || !strings.Contains(*v, "Sell") || strings.Contains(*v, "2") {
				continue
			}
			if sell == "" || colToNum(c) < colToNum(sell) {
				sell = c
			}
		}
		if sell == "" {
			continue
		}
		code := findCurrencyCode(headerRows, buy, sell)
		if code == "" {
			continue
		}
		layout = append(layout, column{buy, sell, code})
	}
	return layout, true
}

// findCurrencyCode searches the header rows for a currency code near the buy/sell group. Headers can land on the buy
// column (2009 layout) or any nearby column (2025-2026, where merged headers can land on the middle column).
func findCurrencyCode(headerRows []row, buy, sell string) string {
	for _, rw := range headerRows {
		for n := colToNum(buy); n <= colToNum(sell)+1; n++ {
			text, ok := rw.cell(numToCol(n))
			if !ok || text == "" {
				continue
			}
			if code := extractCode(text); code != "" {
				return code
			}
		}
	}
	return ""
}

// extractCode finds an ISO 4217 code at the end of a header ("Saudi Arabian Riyal SAR") or alone in a cell ("USD",
// "S.FR", "Gold"), rewriting CBI's aliases to ISO codes.
func extractCode(text string) string {
	if code, ok := codeAliases[strings.TrimSpace(text)]; ok {
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
	stripped := strings.TrimSpace(label)
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

// rubyInteger is Ruby's Integer(s, exception: false): surrounding whitespace, a sign, base prefixes, a leading-zero
// octal and underscores between digits are accepted.
func rubyInteger(s string) (int64, bool) {
	n, err := strconv.ParseInt(strings.Trim(s, " \t\n\v\f\r"), 0, 64)
	return n, err == nil
}

// rubyToI is Ruby's String#to_i: the leading decimal digits, or 0.
func rubyToI(s string) int {
	s = strings.TrimLeft(s, " \t\n\v\f\r")
	end := 0
	if end < len(s) && (s[end] == '+' || s[end] == '-') {
		end++
	}
	for end < len(s) && (s[end] >= '0' && s[end] <= '9' || s[end] == '_' && end > 0 && s[end-1] >= '0' && s[end-1] <= '9') {
		end++
	}
	n, err := strconv.Atoi(strings.ReplaceAll(strings.TrimRight(s[:end], "_"), "_", ""))
	if err != nil {
		return 0
	}
	return n
}

func colToNum(col string) int {
	n := 0
	for _, b := range []byte(col) {
		n = n*26 + int(b-'A'+1)
	}
	return n
}

func numToCol(n int) string {
	var col []byte
	for n > 0 {
		n--
		col = append([]byte{byte('A' + n%26)}, col...)
		n /= 26
	}
	return string(col)
}

var errNoEntry = errors.New("zip entry missing")

func readEntry(zr *zip.Reader, name string) ([]byte, error) {
	f, err := zr.Open(name)
	if err != nil {
		return nil, errNoEntry
	}
	defer f.Close()
	return io.ReadAll(f)
}

func readSharedStrings(zr *zip.Reader) ([]string, error) {
	data, err := readEntry(zr, "xl/sharedStrings.xml")
	if errors.Is(err, errNoEntry) {
		return nil, errors.New("sharedStrings.xml missing from workbook")
	}
	if err != nil {
		return nil, err
	}
	doc, err := loadXML(data)
	if err != nil {
		return nil, err
	}
	var root *node
	for _, n := range doc.kids {
		if n.name == "sst" {
			root = n
			break
		}
	}
	if root == nil {
		return nil, errors.New("malformed sharedStrings.xml (no sst root)")
	}
	strs := make([]string, len(root.kids))
	for i, si := range root.kids {
		strs[i] = collectText(si)
	}
	return strs, nil
}

type sheetTarget struct {
	name, target string
}

func readSheetTargets(zr *zip.Reader) ([]sheetTarget, error) {
	workbookXML, err := readEntry(zr, "xl/workbook.xml")
	if err != nil {
		return nil, fmt.Errorf("read workbook.xml: %w", err)
	}
	relsXML, err := readEntry(zr, "xl/_rels/workbook.xml.rels")
	if err != nil {
		return nil, fmt.Errorf("read workbook.xml.rels: %w", err)
	}

	rels := map[string]string{}
	dec := xml.NewDecoder(bytes.NewReader(relsXML))
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Space != relsNS || se.Name.Local != "Relationship" {
			continue
		}
		rels[attr(se.Attr, "Id")] = attr(se.Attr, "Target")
	}

	doc, err := loadXML(workbookXML)
	if err != nil {
		return nil, err
	}
	var sheets []sheetTarget
	doc.walk(func(n *node) bool {
		if n.name != "sheet" {
			return true
		}
		name, rid := n.attr("name"), n.attr("r:id")
		if rid == "" {
			rid = n.attr("id")
		}
		if name == "" || rid == "" {
			return true
		}
		if target := rels[rid]; target != "" {
			sheets = append(sheets, sheetTarget{name, target})
		}
		return true
	})
	return sheets, nil
}

func parseRows(data []byte, sharedStrings []string) ([]row, error) {
	doc, err := loadXML(data)
	if err != nil {
		return nil, err
	}
	var sheetData *node
	doc.walk(func(n *node) bool {
		if n.name == "sheetData" {
			sheetData = n
			return false
		}
		return true
	})
	if sheetData == nil {
		return nil, errors.New("sheetData missing from worksheet XML")
	}

	var rows []row
	for _, rn := range sheetData.kids {
		if rn.name != "row" {
			continue
		}
		rw := row{r: rubyToI(rn.attr("r")), cells: map[string]*string{}}
		for _, c := range rn.kids {
			if c.name != "c" {
				continue
			}
			ref := c.attr("r")
			if !cellRef.MatchString(ref) {
				continue
			}
			col := strings.TrimRightFunc(ref, unicode.IsDigit)
			if v := cellValue(c, sharedStrings); v != nil {
				rw.cells[col] = v
			}
		}
		rows = append(rows, rw)
	}
	return rows, nil
}

func cellValue(c *node, sharedStrings []string) *string {
	typ := c.attr("t")
	if typ == "inlineStr" {
		is := c.child("is")
		if is == nil {
			return nil
		}
		s := collectText(is)
		return &s
	}
	v := c.child("v")
	if v == nil {
		return nil
	}
	raw := v.firstText()
	if typ != "s" {
		return &raw
	}
	i := rubyToI(raw)
	if i < 0 || i >= len(sharedStrings) {
		return nil
	}
	return &sharedStrings[i]
}

func collectText(n *node) string {
	var b strings.Builder
	for _, k := range n.kids {
		switch k.name {
		case "t":
			b.WriteString(k.firstText())
		case "r":
			b.WriteString(collectText(k))
		}
	}
	return b.String()
}

// node is a generic XML element or text node shaped like Ox's generic mode, which the Ruby adapter reads: element names
// keep their prefix, whitespace runs in text collapse to one space, and whitespace-only text is dropped.
type node struct {
	name  string // "" for text
	text  string
	attrs []xml.Attr
	kids  []*node
}

func (n *node) attr(name string) string { return attr(n.attrs, name) }

func (n *node) child(name string) *node {
	for _, k := range n.kids {
		if k.name == name {
			return k
		}
	}
	return nil
}

func (n *node) firstText() string {
	if len(n.kids) == 0 || n.kids[0].name != "" {
		return ""
	}
	return n.kids[0].text
}

// walk visits the elements below n depth first, stopping when visit returns false.
func (n *node) walk(visit func(*node) bool) bool {
	for _, k := range n.kids {
		if k.name == "" {
			continue
		}
		if !visit(k) || !k.walk(visit) {
			return false
		}
	}
	return true
}

func attr(attrs []xml.Attr, name string) string {
	for _, a := range attrs {
		if qualified(a.Name) == name {
			return a.Value
		}
	}
	return ""
}

func qualified(n xml.Name) string {
	if n.Space == "" {
		return n.Local
	}
	return n.Space + ":" + n.Local
}

var whitespaceRun = regexp.MustCompile(`[ \t\r\n]+`)

func loadXML(data []byte) (*node, error) {
	doc := &node{name: "#document"}
	stack := []*node{doc}
	dec := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := dec.RawToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		top := stack[len(stack)-1]
		switch t := tok.(type) {
		case xml.StartElement:
			el := &node{name: qualified(t.Name), attrs: t.Attr}
			top.kids = append(top.kids, el)
			stack = append(stack, el)
		case xml.EndElement:
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			text := whitespaceRun.ReplaceAllString(string(t), " ")
			if text == " " || text == "" {
				continue
			}
			top.kids = append(top.kids, &node{text: text})
		}
	}
	return doc, nil
}
