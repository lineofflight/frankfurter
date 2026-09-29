package bdp

import (
	"context"
	"math"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
	"gopkg.in/dnaeon/go-vcr.v4/pkg/cassette"
)

// webmockURI matches the way VCR matched this cassette under Ruby. WebMock normalizes the query into a hash, so the
// three repeated dim_cats parameters were recorded (and matched) as the last one alone. vcrtest.URI compares every
// value, which rejects the full request, so this matcher collapses repeated keys to their last value on both sides.
func webmockURI(r *http.Request, _ []byte, rec cassette.Request) bool {
	u, err := url.Parse(rec.URL)
	if err != nil {
		return false
	}
	return strings.EqualFold(r.URL.Hostname(), u.Hostname()) && strings.EqualFold(r.URL.Scheme, u.Scheme) &&
		r.URL.Path == u.Path && reflect.DeepEqual(lastValues(r.URL.Query()), lastValues(u.Query()))
}

func lastValues(q url.Values) map[string]string {
	m := make(map[string]string, len(q))
	for k, v := range q {
		m[k] = v[len(v)-1]
	}
	return m
}

func client(t *testing.T, cassette string, repeats bool) *http.Client {
	opts := []vcrtest.Option{vcrtest.MatchOn(vcrtest.Method, webmockURI)}
	if repeats {
		opts = append(opts, vcrtest.AllowPlaybackRepeats)
	}
	return vcrtest.Client(t, cassette, opts...)
}

func fetchLate1998(t *testing.T) []adapter.Rate {
	t.Helper()
	rates, err := New(client(t, "bdp", false)).Fetch(context.Background(), adapter.Date(1998, 12, 28), adapter.Date(1998, 12, 31))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func mustParse(t *testing.T, data string) []adapter.Rate {
	t.Helper()
	rates, err := parse([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func bases(rates []adapter.Rate) []string {
	var out []string
	for _, r := range rates {
		if !slices.Contains(out, r.Base) {
			out = append(out, r.Base)
		}
	}
	return out
}

func TestParseForeignBasePTEQuote(t *testing.T) {
	rates := mustParse(t, `{
		"size": [1, 1, 1, 1, 1, 1, 1, 2],
		"value": [184.244, 185.487],
		"extension": {"series": [{"id": 180121, "label": "US, Dollars (USD) against Escudo - daily",
			"dimension_category": [{"dimension_id": 12, "category_id": 826}]}]},
		"dimension": {
			"12": {"category": {"index": ["826"]}},
			"reference_date": {"category": {"index": ["1998-01-02", "1998-01-05"]}}
		}
	}`)
	if len(rates) != 2 {
		t.Fatalf("got %d rates, want 2", len(rates))
	}
	want := adapter.Rate{Date: adapter.Date(1998, 1, 2), Base: "USD", Quote: "PTE", Rate: 184.244}
	if rates[0] != want {
		t.Errorf("first rate = %+v, want %+v", rates[0], want)
	}
}

func TestParseInterleavesCounterparties(t *testing.T) {
	rates := mustParse(t, `{
		"value": [184.244, 185.487, 288.532, 288.186],
		"extension": {"series": [
			{"label": "US, Dollars (USD) against Escudo - daily",
				"dimension_category": [{"dimension_id": 12, "category_id": 826}]},
			{"label": "United Kingdom, Pounds (GBP) against Escudo - daily",
				"dimension_category": [{"dimension_id": 12, "category_id": 824}]}
		]},
		"dimension": {
			"12": {"category": {"index": ["826", "824"]}},
			"reference_date": {"category": {"index": ["1998-12-30", "1998-12-31"]}}
		}
	}`)
	got := map[string][]float64{}
	for _, r := range rates {
		got[r.Base] = append(got[r.Base], r.Rate)
	}
	want := map[string][]float64{"USD": {184.244, 185.487}, "GBP": {288.532, 288.186}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rates = %v, want %v", got, want)
	}
}

func TestParseSkipsNullObservations(t *testing.T) {
	rates := mustParse(t, `{
		"value": [184.244, null],
		"extension": {"series": [{"label": "US, Dollars (USD) against Escudo - daily",
			"dimension_category": [{"dimension_id": 12, "category_id": 826}]}]},
		"dimension": {
			"12": {"category": {"index": ["826"]}},
			"reference_date": {"category": {"index": ["1998-01-02", "1998-01-03"]}}
		}
	}`)
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	if !rates[0].Date.Equal(adapter.Date(1998, 1, 2)) {
		t.Errorf("date = %v, want 1998-01-02", rates[0].Date)
	}
}

func TestParseRemapsECUToXEU(t *testing.T) {
	rates := mustParse(t, `{
		"value": [122.93],
		"extension": {"series": [{"label": "ECU - Banco do Portugal (ECU) against Escudo - daily",
			"dimension_category": [{"dimension_id": 12, "category_id": 2777}]}]},
		"dimension": {
			"12": {"category": {"index": ["2777"]}},
			"reference_date": {"category": {"index": ["1998-12-31"]}}
		}
	}`)
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	if rates[0].Base != "XEU" {
		t.Errorf("base = %q, want XEU", rates[0].Base)
	}
}

func TestParseSkipsSeriesWithoutISOCode(t *testing.T) {
	// BdP labels usually carry a code; a series that omits it is dropped rather than emitted malformed.
	rates := mustParse(t, `{
		"value": [42.0, 180.5],
		"extension": {"series": [
			{"label": "Some unlabelled series against Escudo - daily",
				"dimension_category": [{"dimension_id": 12, "category_id": 999}]},
			{"label": "Exchange rate of Escudo against US dollar (USD) - daily",
				"dimension_category": [{"dimension_id": 12, "category_id": 1}]}
		]},
		"dimension": {
			"12": {"category": {"index": ["999", "1"]}},
			"reference_date": {"category": {"index": ["1998-01-02"]}}
		}
	}`)
	if got := bases(rates); !slices.Equal(got, []string{"USD"}) {
		t.Errorf("bases = %v, want [USD]", got)
	}
}

func TestParseRaises(t *testing.T) {
	tests := []struct {
		name, json, want string
	}{
		{"no series label is recognizable", `{
			"value": [42.0],
			"extension": {"series": [{"label": "Some unlabelled series against Escudo - daily",
				"dimension_category": [{"dimension_id": 12, "category_id": 999}]}]},
			"dimension": {
				"12": {"category": {"index": ["999"]}},
				"reference_date": {"category": {"index": ["1998-01-02"]}}
			}
		}`, "no recognizable currency series"},
		{"dateless response is not the observed empty shape",
			`{"value": [42.0], "extension": {"series": []}, "dimension": {}}`, "not the observed empty shape"},
		{"dateless response omits the value array",
			`{"extension": {"series": []}, "dimension": {}}`, "not the observed empty shape"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parse([]byte(tt.json))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestParseEmptyWhenNoSeries(t *testing.T) {
	rates := mustParse(t, `{"size": [0], "value": [], "extension": {"series": []}, "dimension": {}}`)
	if len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
}

func TestFetchHistoricalRange(t *testing.T) {
	rates := fetchLate1998(t)
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	for _, r := range rates {
		if r.Quote != "PTE" {
			t.Fatalf("quote = %q, want PTE", r.Quote)
		}
	}
}

func TestFetchMultipleCurrenciesFollowsPagination(t *testing.T) {
	// Pagination across all source-filtered series must surface BEF/ATS (page 3) too.
	got := bases(fetchLate1998(t))
	for _, want := range []string{"USD", "ATS"} {
		if !slices.Contains(got, want) {
			t.Errorf("bases %v lack %s", got, want)
		}
	}
}

func TestFetchUSDPlausibleLate1998(t *testing.T) {
	for _, r := range fetchLate1998(t) {
		if r.Base == "USD" && r.Date.Equal(adapter.Date(1998, 12, 30)) {
			if math.Abs(r.Rate-171.0) > 5.0 {
				t.Errorf("USD/PTE = %v, want 171 +/- 5", r.Rate)
			}
			return
		}
	}
	t.Error("no USD rate on 1998-12-30")
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	// g.Client would match with vcrtest.URI; replay with the recorded cassette and repeat setting under the
	// WebMock-style URI matcher instead (see webmockURI).
	if !slices.Equal(g.MatchRequestsOn, []string{"method", "uri"}) {
		t.Fatalf("golden recorded with %v, want method,uri", g.MatchRequestsOn)
	}
	a := New(client(t, g.Cassette, g.AllowPlaybackRepeats))
	rates, err := a.Fetch(context.Background(), adapter.Date(1998, 12, 28), adapter.Date(1998, 12, 31))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}

// The cassette matcher collapses repeated dim_cats, so pin the full request shape here.
func TestQuery(t *testing.T) {
	got := query(adapter.Date(1998, 12, 28), adapter.Date(1998, 12, 31), 2)
	want := url.Values{
		"lang":      {"EN"},
		"page":      {"2"},
		"dim_cats":  {"13:794", "18:35", "40:4263"},
		"obs_since": {"1998-12-28"},
		"obs_to":    {"1998-12-31"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("query = %v, want %v", got, want)
	}
	if q := query(time.Time{}, adapter.Date(1998, 12, 31), 1); q.Has("obs_since") {
		t.Errorf("query without after = %v, want no obs_since", q)
	}
}

func TestDecodeAcceptsFalseNextPage(t *testing.T) {
	res, err := decode([]byte(`{"value": [], "extension": {"series": [], "next_page": false}, "dimension": {}}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Extension.NextPage != false {
		t.Errorf("next_page = %v, want false", res.Extension.NextPage)
	}
}
