package api

import (
	"errors"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/db"
)

// v1Params are a v1 request's parameters: the query string, with the route's captures written over it
// (date, start_date, end_date), as Roda's indifferent params. A repeated key keeps its last value, as Rack does.
type v1Params map[string]string

// parseV1Params reads a raw query string the way Rack does: pairs split on '&' only, '+' as space, last value wins.
func parseV1Params(raw string) (v1Params, error) {
	p := v1Params{}
	for pair := range strings.SplitSeq(raw, "&") {
		if pair == "" {
			continue
		}
		k, v, _ := strings.Cut(pair, "=")
		key, err := url.QueryUnescape(k)
		if err != nil {
			return nil, errors.New("invalid %-encoding (" + pair + ")")
		}
		val, err := url.QueryUnescape(v)
		if err != nil {
			return nil, errors.New("invalid %-encoding (" + pair + ")")
		}
		p[key] = val
	}
	return p, nil
}

// v1Query is Versions::V1::Query: the validated request. Nil Symbols means none given; an empty slice is a given but
// empty list (to=), which matches nothing.
type v1Query struct {
	Amount    float64 // 0 when not given
	HasAmount bool
	Base      string // "" when not given
	HasBase   bool
	Symbols   []string

	// Date is set for a single date; Start and End for an interval.
	Date       time.Time
	Start, End time.Time
	IsInterval bool
}

var (
	errInvalidAmount = errors.New("invalid amount")
	errBadPair       = errors.New("bad currency pair")
	errInvalidDate   = errors.New("invalid date")
)

// buildV1Query is Query.build: it parses every parameter and rejects a conversion from a currency to itself.
func buildV1Query(p v1Params) (v1Query, error) {
	var q v1Query
	if s, ok := p["amount"]; ok {
		v := rubyToF(s)
		if !(v > 0) {
			return q, errInvalidAmount
		}
		q.Amount, q.HasAmount = v, true
	}
	q.Base, q.HasBase = p.base()
	q.Symbols = p.symbols()

	if s, ok := p["date"]; ok {
		d, err := parseV1Date(s)
		if err != nil {
			return q, err
		}
		q.Date = d
	} else {
		start, err := parseV1Date(p["start_date"])
		if err != nil {
			return q, err
		}
		end, err := parseV1Date(p["end_date"])
		if err != nil {
			return q, err
		}
		q.Start, q.End, q.IsInterval = start, end, true
	}

	if q.HasBase && len(q.Symbols) == 1 && q.Symbols[0] == q.Base {
		return q, errBadPair
	}
	return q, nil
}

// base is from, else base, upcased.
func (p v1Params) base() (string, bool) {
	for _, k := range []string{"from", "base"} {
		if v, ok := p[k]; ok {
			return strings.ToUpper(v), true
		}
	}
	return "", false
}

// symbols is to, else symbols, upcased and split on commas as Ruby's String#split does (trailing empty fields
// dropped, so an empty string gives an empty list).
func (p v1Params) symbols() []string {
	for _, k := range []string{"to", "symbols"} {
		if v, ok := p[k]; ok {
			parts := strings.Split(strings.ToUpper(v), ",")
			for len(parts) > 0 && parts[len(parts)-1] == "" {
				parts = parts[:len(parts)-1]
			}
			if parts == nil {
				parts = []string{}
			}
			return parts
		}
	}
	return nil
}

// parseV1Date parses the YYYY-MM-DD dates the routes capture. Ruby's Date.parse accepts more, but only a query's own
// date= on an interval route ever reaches it with anything else, and that request fails either way.
func parseV1Date(s string) (time.Time, error) {
	d, err := db.ParseDate(s)
	if err != nil {
		return time.Time{}, errInvalidDate
	}
	return d, nil
}

// rubyToF is Ruby's String#to_f: the longest leading decimal number, ignoring leading whitespace and single
// underscores between digits; 0 when there is none. Out-of-range values overflow to infinity, as in Ruby.
func rubyToF(s string) float64 {
	s = strings.TrimLeft(s, " \t\n\v\f\r")
	var b strings.Builder
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		b.WriteByte(s[i])
		i++
	}
	digits := func() int {
		n := 0
		for i < len(s) {
			switch {
			case s[i] >= '0' && s[i] <= '9':
				b.WriteByte(s[i])
				n++
				i++
			case s[i] == '_' && n > 0 && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9':
				i++
			default:
				return n
			}
		}
		return n
	}
	mantissa := digits()
	if i+1 < len(s) && s[i] == '.' && s[i+1] >= '0' && s[i+1] <= '9' {
		b.WriteByte('.')
		i++
		mantissa += digits()
	}
	if mantissa == 0 {
		return 0
	}
	if i+1 < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		if j < len(s) && s[j] >= '0' && s[j] <= '9' {
			b.WriteString(s[i:j])
			i = j
			digits()
		}
	}
	v, err := strconv.ParseFloat(b.String(), 64)
	if err != nil && !math.IsInf(v, 0) {
		return 0
	}
	return v
}
