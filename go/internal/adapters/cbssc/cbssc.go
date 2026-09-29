// Package cbssc fetches rates from the Central Bank of Seychelles, which publishes the Consolidated Average Rates of
// Authorised Dealers: a daily buy, sell and mid rate for USD, EUR and GBP against the Seychellois rupee (SCR), expressed
// as rupees per unit of foreign currency (1 USD = 14.6 SCR). The pivot sits in quote, as with NBG.
//
// Two sources, both carrying the mid rate:
//
//   - A statistics workbook ("Exchange Rates-Daily.xlsx") with one sheet per currency, a "Date | SCR/USD" header row
//     and one row per business day from 2000-01-04, regenerated daily around 12:00 UTC. The FXArchivedRates.jsp feed
//     the site itself reads only reaches back to 2012, carries buy/sell alone and lags the live day by about two
//     months, so the workbook is the archive.
//   - The live CAR endpoint, which has only the current day. It runs ahead of the workbook by a day, so it fills the
//     head of the series.
//
// The workbook stores the raw dealer average to full float precision (14.616577838181803) while every published view
// of the same figure shows four decimals. Round to four so a day caught live and the same day read from the workbook
// agree.
//
// Unlike most adapters, Fetch treats after as inclusive, as the Ruby adapter does.
package cbssc

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const (
	archiveURL = "https://www.cbs.sc/Downloads/StaExcel/Exchange%20Rates-Daily.xlsx"
	liveURL    = "https://www.cbs.sc/Controller/MarketinfoController.jsp"
)

var (
	excelEpoch = adapter.Date(1899, 12, 30)
	header     = regexp.MustCompile(`\ASCR/([A-Z]{3})\z`)
	sheetPath  = regexp.MustCompile(`\Axl/worksheets/sheet\d+\.xml\z`)
	trailingN  = regexp.MustCompile(`\d+\z`)
)

func init() {
	adapter.Register("CBSSC", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches CBSSC rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	archive, err := a.Get(ctx, archiveURL, nil)
	if err != nil {
		return nil, err
	}
	rates, err := parse(archive)
	if err != nil {
		return nil, err
	}
	live, err := a.Get(ctx, liveURL, url.Values{"type": {"car"}})
	if err != nil {
		return nil, err
	}
	liveRates, err := parseLive(live)
	if err != nil {
		return nil, err
	}
	rates = append(rates, liveRates...)

	type key struct {
		date time.Time
		base string
	}
	seen := map[key]bool{}
	var out []adapter.Rate
	for _, r := range rates {
		if !after.IsZero() && r.Date.Before(after) {
			continue
		}
		if !upto.IsZero() && r.Date.After(upto) {
			continue
		}
		k := key{r.Date, r.Base}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, r)
	}
	return out, nil
}

func parse(data []byte) ([]adapter.Rate, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	files := map[string]*zip.File{}
	var paths []string
	for _, f := range zr.File {
		files[f.Name] = f
		if sheetPath.MatchString(f.Name) {
			paths = append(paths, f.Name)
		}
	}
	sort.Strings(paths)

	ss, ok := files["xl/sharedStrings.xml"]
	if !ok {
		return nil, errors.New("xl/sharedStrings.xml missing from workbook")
	}
	strs, err := sharedStrings(ss)
	if err != nil {
		return nil, err
	}

	var rates []adapter.Rate
	for _, p := range paths {
		root, err := readXML(files[p])
		if err != nil {
			return nil, err
		}
		sheetData := root.find("sheetData")
		if sheetData == nil {
			continue
		}
		rates = parseSheet(sheetData.children, strs, rates)
	}
	if len(rates) == 0 {
		return nil, fmt.Errorf("no rates in workbook at %s", archiveURL)
	}
	return rates, nil
}

// parseSheet reads a currency sheet, which opens with a "Date | SCR/USD" header; rows beneath carry an Excel serial in
// A and the mid in B. Sheets without such a header (the notes, the hidden yearly averages) emit nothing. A day with no
// fixing holds a text placeholder in B instead of a number (GBP on 2020-04-09), which numericCell skips.
func parseSheet(rows []*node, strs []string, rates []adapter.Rate) []adapter.Rate {
	iso := ""
	for _, row := range rows {
		if iso == "" {
			if s, ok := stringCell(row, "B", strs); ok {
				if m := header.FindStringSubmatch(s); m != nil {
					iso = m[1]
				}
			}
		}
		if iso == "" {
			continue
		}
		serial, ok := numericCell(row, "A")
		if !ok {
			continue
		}
		rate, ok := numericCell(row, "B")
		if !ok || rate <= 0 {
			continue
		}
		if serial <= 30_000 || serial >= 80_000 {
			continue
		}
		rates = append(rates, adapter.Rate{
			Date:  excelEpoch.AddDate(0, 0, int(serial)),
			Base:  iso,
			Quote: "SCR",
			Rate:  round4(rate),
		})
	}
	return rates
}

func cell(row *node, column string) *node {
	for _, c := range row.children {
		if r, ok := c.attrs["r"]; ok && trailingN.ReplaceAllString(r, "") == column {
			return c
		}
	}
	return nil
}

func cellValue(c *node) string {
	for _, n := range c.children {
		if n.name == "v" {
			return strings.TrimSpace(n.firstText())
		}
	}
	return ""
}

func stringCell(row *node, column string, strs []string) (string, bool) {
	c := cell(row, column)
	if c == nil || c.attrs["t"] != "s" {
		return "", false
	}
	i, _ := strconv.Atoi(cellValue(c))
	if i < 0 || i >= len(strs) {
		return "", false
	}
	return strs[i], true
}

func numericCell(row *node, column string) (float64, bool) {
	c := cell(row, column)
	if c == nil || c.attrs["t"] == "s" {
		return 0, false
	}
	return adapter.ParseFloat(cellValue(c))
}

func sharedStrings(f *zip.File) ([]string, error) {
	root, err := readXML(f)
	if err != nil {
		return nil, err
	}
	strs := make([]string, 0, len(root.children))
	for _, si := range root.children {
		var b strings.Builder
		for _, t := range si.children {
			b.WriteString(t.firstText())
		}
		strs = append(strs, b.String())
	}
	return strs, nil
}

// parseLive reads the CAR endpoint's JSON, which holds only the current day.
func parseLive(data []byte) ([]adapter.Rate, error) {
	var doc struct {
		Car []map[string]any `json:"car"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if len(doc.Car) == 0 {
		return nil, errors.New("no CAR rates in live response")
	}
	row := doc.Car[0]

	ds, ok := row["currentDate"].(string)
	if !ok {
		return nil, errors.New("live response has no currentDate")
	}
	date, err := time.Parse("02-Jan-2006", ds)
	if err != nil {
		return nil, err
	}

	var rates []adapter.Rate
	for _, iso := range []string{"USD", "EUR", "GBP"} {
		field := strings.ToLower(iso) + "mid"
		var rate float64
		switch v := row[field].(type) {
		case float64:
			rate = v
		case string:
			f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
			if err != nil {
				return nil, fmt.Errorf("live %s: %w", field, err)
			}
			rate = f
		default:
			return nil, fmt.Errorf("live response has no %s", field)
		}
		if rate == 0 {
			continue
		}
		rates = append(rates, adapter.Rate{Date: date, Base: iso, Quote: "SCR", Rate: rate})
	}
	return rates, nil
}

// round4 is Ruby's Float#round(4): round half up on x*10^4, corrected where the product lost precision.
func round4(x float64) float64 {
	const s = 1e4
	f := math.Round(x * s)
	if x > 0 {
		if (f+0.5)/s <= x {
			f++
		}
	} else if (f-0.5)/s >= x {
		f--
	}
	return f / s
}

// node is a minimal generic XML tree, standing in for Ox's generic mode.
type node struct {
	name     string
	attrs    map[string]string
	children []*node
	text     *string // set for text nodes
}

func (n *node) find(name string) *node {
	if n.name == name {
		return n
	}
	for _, c := range n.children {
		if found := c.find(name); found != nil {
			return found
		}
	}
	return nil
}

// firstText returns the first child's text, or "" when it is an element or absent.
func (n *node) firstText() string {
	if len(n.children) == 0 || n.children[0].text == nil {
		return ""
	}
	return *n.children[0].text
}

func readXML(f *zip.File) (*node, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	dec := xml.NewDecoder(rc)
	root := &node{}
	stack := []*node{root}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.Name, err)
		}
		top := stack[len(stack)-1]
		switch t := tok.(type) {
		case xml.StartElement:
			n := &node{name: t.Name.Local, attrs: map[string]string{}}
			for _, a := range t.Attr {
				n.attrs[a.Name.Local] = a.Value
			}
			top.children = append(top.children, n)
			stack = append(stack, n)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			// Ox skips whitespace-only text by default.
			if strings.TrimSpace(string(t)) == "" {
				continue
			}
			s := string(t)
			top.children = append(top.children, &node{text: &s})
		}
	}
	if len(root.children) == 0 {
		return nil, fmt.Errorf("%s has no root element", f.Name)
	}
	return root.children[0], nil
}
