// Package pma fetches rates from the Palestine Monetary Authority. Palestine has no currency of its own; the PMA
// publishes daily buy, sell and mid rates for the currencies circulating in the territories (ILS, JOD, USD) plus USD
// crosses for the majors, the Gulf currencies, gold and silver. 25 pairs, Sunday to Thursday, archive from 2020-09-01.
//
// The public site embeds a small export tool at wcur.pma.ps. A GET on the export URL sets a PHP session cookie and
// renders a form whose hidden anti-forgery input has a per-session name and value; a POST with that input, the cookie
// and a from/to range returns an XLSX with Date, Pair, Buy, Sell, Mid columns. The whole archive fits in one request
// (about 1 MB), so there is no chunking.
//
// Every cell in the workbook is a shared string, dates as "YYYY/MM/DD" and numbers with thousands separators
// ("89,446.50") and the odd stray space. We take the published mid rather than synthesizing one from buy and sell.
//
// Direction is per pair, as labelled: "USD/ILS" is ILS per USD (USD in base), "GBP/USD" is USD per GBP (USD in quote),
// and "JOD/ILS", "EUR/ILS", "EGP/ILS" do not touch the pivot at all.
//
// As in Ruby, after is passed to the export as "from", so it is inclusive, and rows are not clipped further.
package pma

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const exportURL = "https://wcur.pma.ps/ar/webtools/currency/export"

var (
	archiveStart = adapter.Date(2020, 9, 1)

	tokenPattern = regexp.MustCompile(`<input\s+type="hidden"\s+name="([^"]+)"\s+value="([^"]+)"`)
	pairPattern  = regexp.MustCompile(`^([A-Z]{3})/([A-Z]{3})$`)
	datePattern  = regexp.MustCompile(`^(\d{4})[/-](\d{2})[/-](\d{2})$`)
)

func init() {
	adapter.Register("PMA", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches PMA rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	start, end := after, upto
	if start.IsZero() {
		start = archiveStart
	}
	if end.IsZero() {
		end = a.Today()
	}
	if start.After(end) {
		return nil, nil
	}
	body, err := a.export(ctx, start, end)
	if err != nil {
		return nil, err
	}
	return parse(body)
}

func (a *Adapter) export(ctx context.Context, start, end time.Time) ([]byte, error) {
	req, err := a.NewRequest(ctx, http.MethodGet, exportURL, nil)
	if err != nil {
		return nil, err
	}
	form, err := a.Do(req)
	if err != nil {
		return nil, err
	}
	token := tokenPattern.FindSubmatch(form.Body)
	if token == nil {
		return nil, fmt.Errorf("no request token on %s", exportURL)
	}

	values := url.Values{
		string(token[1]): {string(token[2])},
		"from":           {start.Format(time.DateOnly)},
		"to":             {end.Format(time.DateOnly)},
	}
	req, err = a.NewRequest(ctx, http.MethodPost, exportURL, strings.NewReader(values.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", adapter.CookieHeader(form.Header))
	req.Header.Set("Referer", exportURL)
	resp, err := a.Do(req)
	if err != nil {
		return nil, err
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "spreadsheetml") {
		return nil, fmt.Errorf("export returned %q instead of a workbook", ct)
	}
	return resp.Body, nil
}

type cell struct {
	Ref    string   `xml:"r,attr"`
	Type   string   `xml:"t,attr"`
	Value  *string  `xml:"v"`
	Inline []string `xml:"is>t"`
}

type worksheet struct {
	SheetData *struct {
		Rows []struct {
			Cells []cell `xml:"c"`
		} `xml:"row"`
	} `xml:"sheetData"`
}

type sst struct {
	Items []struct {
		T []string `xml:"t"`
	} `xml:"si"`
}

var trailingDigits = regexp.MustCompile(`\d+$`)

func parse(data []byte) ([]adapter.Rate, error) {
	strs, sheet, err := readWorkbook(data)
	if err != nil {
		return nil, err
	}
	var ws worksheet
	if err := xml.Unmarshal(sheet, &ws); err != nil {
		return nil, fmt.Errorf("sheet1.xml: %w", err)
	}
	if ws.SheetData == nil {
		return nil, errors.New("sheetData missing from export workbook")
	}

	var rates []adapter.Rate
	for _, row := range ws.SheetData.Rows {
		cells := map[string]string{}
		for _, c := range row.Cells {
			cells[trailingDigits.ReplaceAllString(c.Ref, "")] = c.value(strs)
		}
		if r, ok := parseRow(cells["A"], cells["B"], cells["E"]); ok {
			rates = append(rates, r)
		}
	}
	return rates, nil
}

func (c cell) value(strs []string) string {
	var v string
	if c.Value != nil {
		v = *c.Value
	}
	switch c.Type {
	case "s":
		i, _ := strconv.Atoi(strings.TrimSpace(v))
		if i >= 0 && i < len(strs) {
			return strs[i]
		}
		return ""
	case "inlineStr":
		return strings.Join(c.Inline, "")
	}
	return v
}

func parseRow(dateText, pairText, midText string) (adapter.Rate, bool) {
	date, ok := parseDate(dateText)
	if !ok {
		return adapter.Rate{}, false
	}
	pair := pairPattern.FindStringSubmatch(strings.TrimSpace(pairText))
	if pair == nil {
		return adapter.Rate{}, false
	}
	rate, ok := adapter.ParseFloat(strings.ReplaceAll(strings.TrimSpace(midText), ",", ""))
	if !ok || rate <= 0 {
		return adapter.Rate{}, false
	}
	return adapter.Rate{Date: date, Base: pair[1], Quote: pair[2], Rate: rate}, true
}

func parseDate(text string) (time.Time, bool) {
	m := datePattern.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return time.Time{}, false
	}
	y, _ := strconv.Atoi(m[1])
	mo, _ := strconv.Atoi(m[2])
	d, _ := strconv.Atoi(m[3])
	date := adapter.Date(y, time.Month(mo), d)
	// time.Date normalizes out-of-range parts; Ruby's Date.new rejects them.
	if date.Year() != y || int(date.Month()) != mo || date.Day() != d {
		return time.Time{}, false
	}
	return date, true
}

func readWorkbook(data []byte) ([]string, []byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, nil, err
	}
	sheet, err := readEntry(zr, "xl/worksheets/sheet1.xml")
	if err != nil {
		return nil, nil, err
	}
	if sheet == nil {
		return nil, nil, errors.New("sheet1.xml missing from export workbook")
	}
	stringsXML, err := readEntry(zr, "xl/sharedStrings.xml")
	if err != nil {
		return nil, nil, err
	}
	if stringsXML == nil {
		return nil, nil, errors.New("sharedStrings.xml missing from export workbook")
	}
	var s sst
	if err := xml.Unmarshal(stringsXML, &s); err != nil {
		return nil, nil, fmt.Errorf("sharedStrings.xml: %w", err)
	}
	strs := make([]string, len(s.Items))
	for i, si := range s.Items {
		strs[i] = strings.Join(si.T, "")
	}
	return strs, sheet, nil
}

func readEntry(zr *zip.Reader, name string) ([]byte, error) {
	f, err := zr.Open(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}
