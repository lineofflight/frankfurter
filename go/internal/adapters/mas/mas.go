// Package mas fetches rates from the Monetary Authority of Singapore, which
// publishes daily exchange rates for 21 currencies against the Singapore dollar
// through an ASP.NET statistics page that serves CSV downloads. Rates are
// quoted as SGD per unit (or per 100 units) of foreign currency. Data is
// available from 1988.
//
// Fetch keeps rows dated on or after `after`, as the Ruby adapter does, rather
// than strictly after it.
package mas

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const baseURL = "https://eservices.mas.gov.sg/statistics/msb/ExchangeRates.aspx"

// coverageStart is the first year in the form's year list. An earlier year
// renders the form again instead of a CSV.
var coverageStart = adapter.Date(1988, 1, 1)

func init() {
	adapter.Register("MAS", func(c *http.Client) adapter.Adapter { return New(c) })
}

type column struct {
	name string
	code string
	unit float64
}

// columns maps header substrings to currencies, in the order they are tried.
var columns = []column{
	{"Euro", "EUR", 1},
	{"Pound Sterling", "GBP", 1},
	{"US Dollar", "USD", 1},
	{"Australian Dollar", "AUD", 100},
	{"Canadian Dollar", "CAD", 100},
	{"Chinese Renminbi", "CNY", 100},
	{"Hong Kong Dollar", "HKD", 100},
	{"Indian Rupee", "INR", 100},
	{"Indonesian Rupiah", "IDR", 100},
	{"Japanese Yen", "JPY", 100},
	{"Korean Won", "KRW", 100},
	{"Malaysian Ringgit", "MYR", 100},
	{"New Taiwan Dollar", "TWD", 100},
	{"New Zealand Dollar", "NZD", 100},
	{"Philippine Peso", "PHP", 100},
	{"Qatar Riyal", "QAR", 100},
	{"Saudi Arabia Riyal", "SAR", 100},
	{"Swiss Franc", "CHF", 100},
	{"Thai Baht", "THB", 100},
	{"UAE Dirham", "AED", 100},
	{"Vietnamese Dong", "VND", 100},
}

var months = map[string]int{
	"Jan": 1, "Feb": 2, "Mar": 3, "Apr": 4, "May": 5, "Jun": 6,
	"Jul": 7, "Aug": 8, "Sep": 9, "Oct": 10, "Nov": 11, "Dec": 12,
}

var tokenPatterns = map[string]*regexp.Regexp{
	"__VIEWSTATE":          regexp.MustCompile(`name="__VIEWSTATE"[^>]*value="([^"]*)"`),
	"__VIEWSTATEGENERATOR": regexp.MustCompile(`name="__VIEWSTATEGENERATOR"[^>]*value="([^"]*)"`),
	"__EVENTVALIDATION":    regexp.MustCompile(`name="__EVENTVALIDATION"[^>]*value="([^"]*)"`),
}

// Adapter fetches MAS rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter.
func (a *Adapter) BackfillRange() int { return 365 }

// Fetch implements adapter.Adapter. It downloads one CSV per calendar year in
// the range.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	if after.IsZero() {
		return nil, errors.New("fetch needs a start date")
	}
	end := upto
	if end.IsZero() {
		end = a.Today()
	}

	start := after
	if start.Before(coverageStart) {
		start = coverageStart
	}
	var dataset []adapter.Rate
	for date := start; !date.After(end); {
		yearEnd := adapter.Date(date.Year(), 12, 31)
		chunkEnd := yearEnd
		if end.Before(chunkEnd) {
			chunkEnd = end
		}
		body, err := a.download(ctx, int(date.Month()), date.Year(), int(chunkEnd.Month()), chunkEnd.Year())
		if err != nil {
			return nil, err
		}
		rates, err := parse(body)
		if err != nil {
			return nil, err
		}
		dataset = append(dataset, rates...)
		if err := a.Sleep(ctx, time.Second); err != nil {
			return nil, err
		}
		date = yearEnd.AddDate(0, 0, 1)
	}

	var out []adapter.Rate
	for _, r := range dataset {
		if r.Date.Before(after) || r.Date.After(end) {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// download reads the page for its ASP.NET tokens and cookies, then posts the
// form back to download the CSV.
func (a *Adapter) download(ctx context.Context, startMonth, startYear, endMonth, endYear int) ([]byte, error) {
	req, err := a.NewRequest(ctx, http.MethodGet, baseURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.Do(req)
	if err != nil {
		return nil, err
	}
	page := resp.Body
	cookie := adapter.CookieHeader(resp.Header)

	form := url.Values{}
	for name, re := range tokenPatterns {
		if m := re.FindSubmatch(page); m != nil {
			form.Set(name, string(m[1]))
		}
	}
	const prefix = "ctl00$ContentPlaceHolder1$"
	form.Set(prefix+"StartYearDropDownList", strconv.Itoa(startYear))
	form.Set(prefix+"EndYearDropDownList", strconv.Itoa(endYear))
	form.Set(prefix+"StartMonthDropDownList", strconv.Itoa(startMonth))
	form.Set(prefix+"EndMonthDropDownList", strconv.Itoa(endMonth))
	form.Set(prefix+"FrequencyDropDownList", "D")
	form.Set(prefix+"DownloadButton", "Download")
	for i := range 3 {
		form.Set(fmt.Sprintf("%sEndOfPeriodPerUnitCheckBoxList$%d", prefix, i), "on")
	}
	for i := range 18 {
		form.Set(fmt.Sprintf("%sEndOfPeriodPer100UnitsCheckBoxList$%d", prefix, i), "on")
	}

	post, err := a.NewRequest(ctx, http.MethodPost, baseURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.Header.Set("Cookie", cookie)
	resp, err = a.Do(post)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func parse(data []byte) ([]adapter.Rate, error) {
	text := string(data)
	lines := strings.SplitAfter(text, "\n")
	header := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "End of Period") {
			header = i
			break
		}
	}

	// A window with no data yet (e.g. a new year before the first fixing)
	// re-renders the page with a "No Results Found" panel.
	if strings.Contains(text, "No Results Found") {
		return nil, nil
	}
	if header < 0 {
		return nil, errors.New("no 'End of Period' header in CSV download")
	}

	parts, err := parseLine(lines[header])
	if err != nil {
		return nil, err
	}
	cols := map[int]column{}
	for i, part := range parts {
		for _, c := range columns {
			if strings.Contains(part, c.name) {
				cols[i] = c
				break
			}
		}
	}
	if len(cols) == 0 {
		return nil, errors.New("no recognized currency columns in CSV header")
	}

	var rates []adapter.Rate
	var year, month int
	for _, line := range lines[header+1:] {
		row, err := parseLine(line)
		if err != nil {
			return nil, err
		}
		if len(row) <= 3 {
			continue
		}
		if s := strings.TrimSpace(row[0]); s != "" {
			year = toI(s)
		}
		if s := strings.TrimSpace(row[1]); s != "" {
			month = months[s]
		}
		day := toI(strings.TrimSpace(row[2]))
		if year <= 0 || month <= 0 || day <= 0 {
			continue
		}
		date := adapter.Date(year, time.Month(month), day)
		if date.Day() != day {
			return nil, fmt.Errorf("invalid date %d-%02d-%02d", year, month, day)
		}

		for i, value := range row {
			c, ok := cols[i]
			if !ok {
				continue
			}
			rate, ok := adapter.ParseFloat(value)
			if !ok || rate <= 0 {
				continue
			}
			rates = append(rates, adapter.Rate{Date: date, Base: c.code, Quote: "SGD", Rate: rate / c.unit})
		}
	}
	return rates, nil
}

// parseLine is Ruby's CSV.parse_line; a blank line gives no fields.
func parseLine(line string) ([]string, error) {
	r := csv.NewReader(strings.NewReader(line))
	r.FieldsPerRecord = -1
	row, err := r.Read()
	if err == io.EOF {
		return nil, nil
	}
	return row, err
}

// toI is Ruby's String#to_i on unsigned text: the leading digits, or 0.
func toI(s string) int {
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	n, _ := strconv.Atoi(s[:end])
	return n
}
