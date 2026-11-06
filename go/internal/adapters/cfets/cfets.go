// Package cfets fetches rates from the China Foreign Exchange Trade System, the
// PBOC's interbank platform, which publishes the daily RMB central parity rate
// (the official fixing, 09:15 Beijing) for 25 pairs.
//
// The history endpoint returns every pair when currency is blank, names them in
// data.head (e.g. "USD/CNY", "100JPY/CNY", "CNY/THB") and aligns each record's
// values to it. Direction is per label: the first ten pairs are
// foreign-per-CNY, the rest CNY-per-foreign. Missing values are "---". The
// endpoint refuses spans of a year or more and any pageSize above 50. As in
// Ruby, rows dated on after are kept: the query's startDate is inclusive and
// the result is not windowed.
package cfets

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const (
	baseURL   = "https://www.chinamoney.com.cn/ags/ms/cm-u-bk-ccpr/CcprHisNew"
	chunkDays = 60
	pageSize  = 50
)

func init() {
	adapter.Register("CFETS", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches CFETS rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter.
func (a *Adapter) BackfillRange() int { return chunkDays }

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	end := upto
	if end.IsZero() {
		end = a.Today()
	}
	start := after
	if start.IsZero() {
		start = end.AddDate(0, 0, -chunkDays+1)
	}

	var rates []adapter.Rate
	for page := 1; ; page++ {
		if page > 1 {
			if err := a.Sleep(ctx, 500*time.Millisecond); err != nil {
				return nil, err
			}
		}
		body, err := a.fetchPage(ctx, start, end, page)
		if err != nil {
			return nil, err
		}
		resp, err := decode(body)
		if err != nil {
			return nil, err
		}
		pageRates, err := resp.rates()
		if err != nil {
			return nil, err
		}
		rates = append(rates, pageRates...)
		if page >= toInt(resp.Data.PageTotal) {
			break
		}
	}
	return rates, nil
}

// fetchPage posts with the parameters in the query string and no body, as
// http.rb's post(url, params:) does.
func (a *Adapter) fetchPage(ctx context.Context, start, end time.Time, page int) ([]byte, error) {
	q := url.Values{
		"startDate": {start.Format(time.DateOnly)},
		"endDate":   {end.Format(time.DateOnly)},
		"currency":  {""},
		"pageNum":   {strconv.Itoa(page)},
		"pageSize":  {strconv.Itoa(pageSize)},
	}
	req, err := a.NewRequest(ctx, http.MethodPost, baseURL+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.Do(req)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

type response struct {
	Data struct {
		Head        []any  `json:"head"`
		PageTotal   any    `json:"pageTotal"`
		FlagMessage string `json:"flagMessage"`
	} `json:"data"`
	Records *[]struct {
		Date   string `json:"date"`
		Values []any  `json:"values"`
	} `json:"records"`
}

func decode(data []byte) (*response, error) {
	var r response
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("expected JSON from %s: %w", baseURL, err)
	}
	return &r, nil
}

func parse(data []byte) ([]adapter.Rate, error) {
	r, err := decode(data)
	if err != nil {
		return nil, err
	}
	return r.rates()
}

var valuePattern = regexp.MustCompile(`\A\d+(\.\d+)?\z`)

func (r *response) rates() ([]adapter.Rate, error) {
	if r.Records == nil {
		return nil, fmt.Errorf("query refused: %s", r.Data.FlagMessage)
	}

	pairs := make([]*pair, len(r.Data.Head))
	for i, label := range r.Data.Head {
		s, _ := label.(string)
		pairs[i] = parseLabel(s)
	}

	var rates []adapter.Rate
	for _, rec := range *r.Records {
		date, err := time.Parse(time.DateOnly, rec.Date)
		if err != nil {
			return nil, err
		}
		for i, p := range pairs {
			if p == nil || i >= len(rec.Values) {
				continue
			}
			s, ok := rec.Values[i].(string)
			if !ok || !valuePattern.MatchString(s) {
				continue
			}
			value, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return nil, err
			}
			rate := value / float64(p.unit)
			if rate == 0 {
				continue
			}
			rates = append(rates, adapter.Rate{Date: date, Base: p.base, Quote: p.quote, Rate: rate})
		}
	}
	return rates, nil
}

type pair struct {
	base, quote string
	unit        int
}

var labelPattern = regexp.MustCompile(`\A(\d*)([A-Z]{3})/(\d*)([A-Z]{3})\z`)

// parseLabel reads "100JPY/CNY" as base JPY, quote CNY, unit 100. Labels with a
// quote unit are not understood.
func parseLabel(label string) *pair {
	m := labelPattern.FindStringSubmatch(label)
	if m == nil || m[3] != "" {
		return nil
	}
	unit := 1
	if m[1] != "" {
		unit, _ = strconv.Atoi(m[1])
	}
	if unit == 0 {
		return nil
	}
	return &pair{base: m[2], quote: m[4], unit: unit}
}

// toInt mirrors Ruby's to_i on a JSON number, string or null.
func toInt(v any) int {
	switch v := v.(type) {
	case float64:
		return int(v)
	case string:
		v = strings.TrimLeft(v, " \t\n\v\f\r")
		sign := 1
		if v != "" && (v[0] == '+' || v[0] == '-') {
			if v[0] == '-' {
				sign = -1
			}
			v = v[1:]
		}
		n := 0
		for _, c := range v {
			if c < '0' || c > '9' {
				break
			}
			n = n*10 + int(c-'0')
		}
		return sign * n
	}
	return 0
}
