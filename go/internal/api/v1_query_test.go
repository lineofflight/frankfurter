package api

import (
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

// spec/versions/v1/query_spec.rb. Query.new(...).x without date parameters becomes the matching accessor on v1Params,
// since buildV1Query also parses dates.

func TestQueryBuildsQuery(t *testing.T) {
	q, err := buildV1Query(v1Params{"date": "2014-01-01"})
	if err != nil {
		t.Fatal(err)
	}
	if q.IsInterval || !q.Date.Equal(adapter.Date(2014, 1, 1)) {
		t.Fatalf("query = %+v", q)
	}
}

func TestQueryReturnsGivenAmount(t *testing.T) {
	q, err := buildV1Query(v1Params{"amount": "100", "date": "2014-01-01"})
	if err != nil {
		t.Fatal(err)
	}
	if !q.HasAmount || q.Amount != 100.0 {
		t.Fatalf("amount = %v", q.Amount)
	}
}

func TestQueryRequiresPositiveAmount(t *testing.T) {
	for _, s := range []string{"0", "-1"} {
		if _, err := buildV1Query(v1Params{"amount": s, "date": "2014-01-01"}); !errors.Is(err, errInvalidAmount) {
			t.Errorf("amount %s: err = %v", s, err)
		}
	}
}

func TestQueryDefaultsAmountToNothing(t *testing.T) {
	q, err := buildV1Query(v1Params{"date": "2014-01-01"})
	if err != nil {
		t.Fatal(err)
	}
	if q.HasAmount {
		t.Fatalf("amount = %v", q.Amount)
	}
}

func TestQueryBase(t *testing.T) {
	for _, c := range []struct {
		name   string
		params v1Params
		want   string
		ok     bool
	}{
		{"returns given base", v1Params{"base": "USD"}, "USD", true},
		{"upcases given base", v1Params{"base": "usd"}, "USD", true},
		{"defaults base to nothing", v1Params{}, "", false},
		{"aliases base with from", v1Params{"from": "USD"}, "USD", true},
	} {
		if got, ok := c.params.base(); got != c.want || ok != c.ok {
			t.Errorf("%s: base = %q, %v", c.name, got, ok)
		}
	}
}

func TestQuerySymbols(t *testing.T) {
	for _, c := range []struct {
		name   string
		params v1Params
		want   []string
	}{
		{"returns given symbols", v1Params{"symbols": "USD,GBP"}, []string{"USD", "GBP"}},
		{"upcases given symbols", v1Params{"symbols": "usd,gbp"}, []string{"USD", "GBP"}},
		{"aliases symbols with to", v1Params{"to": "USD"}, []string{"USD"}},
		{"defaults symbols to nothing", v1Params{}, nil},
		{"splits like Ruby", v1Params{"to": ",USD,,GBP,,"}, []string{"", "USD", "", "GBP"}},
		{"empty list", v1Params{"to": ""}, []string{}},
	} {
		if got := c.params.symbols(); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: symbols = %#v", c.name, got)
		}
	}
}

func TestQueryReturnsGivenDate(t *testing.T) {
	q, err := buildV1Query(v1Params{"date": "2014-01-01"})
	if err != nil {
		t.Fatal(err)
	}
	if !q.Date.Equal(adapter.Date(2014, 1, 1)) {
		t.Fatalf("date = %v", q.Date)
	}
}

func TestQueryRequiresValidDate(t *testing.T) {
	if _, err := buildV1Query(v1Params{"date": "2014-01-32"}); !errors.Is(err, errInvalidDate) {
		t.Fatalf("err = %v", err)
	}
}

func TestQueryReturnsGivenDateInterval(t *testing.T) {
	q, err := buildV1Query(v1Params{"start_date": "2014-01-01", "end_date": "2014-12-31"})
	if err != nil {
		t.Fatal(err)
	}
	if !q.IsInterval || !q.Start.Equal(adapter.Date(2014, 1, 1)) || !q.End.Equal(adapter.Date(2014, 12, 31)) {
		t.Fatalf("query = %+v", q)
	}
}

func TestQueryRejectsBadCurrencyPair(t *testing.T) {
	if _, err := buildV1Query(v1Params{"from": "usd", "to": "USD", "date": "2014-01-01"}); !errors.Is(err, errBadPair) {
		t.Fatalf("err = %v", err)
	}
	if _, err := buildV1Query(v1Params{"to": "EUR", "date": "2014-01-01"}); err != nil {
		t.Fatalf("to alone: err = %v", err)
	}
}

func TestRubyToF(t *testing.T) {
	for s, want := range map[string]float64{
		"100": 100, " 5": 5, "10abc": 10, "abc": 0, "": 0, "1e3": 1000, "1e": 1, "1_000": 1000, "1__0": 1,
		".5": 0.5, "5.": 5, "-1": -1, "+2.5": 2.5, "0x10": 0, "1.5e-2x": 0.015, "_1": 0, "Infinity": 0, "1_": 1, "1._5": 1, "1e_5": 1, "1e5_0": 1e50, "  \n7": 7,
	} {
		if got := rubyToF(s); got != want {
			t.Errorf("%q.to_f = %v, want %v", s, got, want)
		}
	}
	if got := rubyToF("1e400"); !math.IsInf(got, 1) {
		t.Errorf(`"1e400".to_f = %v`, got)
	}
}

func TestParseV1ParamsLastValueWins(t *testing.T) {
	p, _, err := parseV1Params("to=USD&to=GBP&base=USD?callback=?&amount=1+0")
	if err != nil {
		t.Fatal(err)
	}
	want := v1Params{"to": "GBP", "base": "USD?callback=?", "amount": "1 0"}
	if !reflect.DeepEqual(p, want) {
		t.Fatalf("params = %v", p)
	}
	if _, _, err := parseV1Params("to=%zz"); err == nil {
		t.Fatal("want an error on a bad escape")
	}
}

// Rack parses a key without '=' as nil, which V1::Query treats as absent, and nests bracketed keys.
func TestParseV1ParamsLikeRack(t *testing.T) {
	p, nested, err := parseV1Params("amount&from=USD&from&to[]=USD&base[x]=GBP&=x&foo[=1&[bar]=2")
	if err != nil {
		t.Fatal(err)
	}
	if want := (v1Params{"foo[": "1", "[bar]": "2"}); !reflect.DeepEqual(p, want) {
		t.Errorf("params = %v", p)
	}
	if want := (v1Nested{"to": '[', "base": '{'}); !reflect.DeepEqual(nested, want) {
		t.Errorf("nested = %v", nested)
	}
	for _, raw := range []string{"foo=1&foo[]=2", "foo[]=1&foo[x]=2", "foo[x]=1&foo[]=2"} {
		if _, _, err := parseV1Params(raw); err == nil {
			t.Errorf("%s: want a type conflict", raw)
		}
	}
	for _, raw := range []string{"foo&foo[]=1", "foo[]=1&foo=2", "foo[]=1&foo[]=2", "foo[x]=1&foo[y]=2"} {
		if _, _, err := parseV1Params(raw); err != nil {
			t.Errorf("%s: %v", raw, err)
		}
	}
}

func TestV1NestedCheck(t *testing.T) {
	cases := []struct {
		query string
		fails bool
	}{
		{"to[]=USD", true},
		{"symbols[]=USD", true},
		{"to=USD&symbols[]=GBP", false},
		{"from[x]=USD", true},
		{"from=USD&base[]=GBP", false},
		{"amount[]=1", true},
		{"date[]=2020-01-01", true},
		{"foo[]=1", false},
	}
	for _, c := range cases {
		p, nested, err := parseV1Params(c.query)
		if err != nil {
			t.Fatal(err)
		}
		if err := nested.check(p); (err != nil) != c.fails {
			t.Errorf("%s: check = %v", c.query, err)
		}
	}
}
