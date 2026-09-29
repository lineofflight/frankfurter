package cbkkw

import (
	"context"
	"fmt"
	"io"
	"maps"
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
)

func fragment(rows ...[2]string) string {
	var body []string
	for _, r := range rows {
		body = append(body, fmt.Sprintf("<tr><td>%s</td><td>%s</td></tr>", r[0], r[1]))
	}
	return `<div class="tab-content">
<script type="application/json" id="currencyJSON">{"dateMin": "02/01/2008","dataSet": []}</script>
<table class="table table-bordered table-striped ctable">
  <thead class="thead-dark">
    <tr class="heading-row"><th>Date</th><th>KWD / US Dollar</th></tr>
  </thead>
  <tbody>
    ` + strings.Join(body, "\n") + `
  </tbody>
</table>
</div>
`
}

func mustParse(t *testing.T, html, code string) []adapter.Rate {
	t.Helper()
	rates, err := parse(html, code)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestParseFilsIntoKWDWithForeignBase(t *testing.T) {
	rates := mustParse(t, fragment([2]string{"08.09.2026", "306.650"}), "USD")
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	r := rates[0]
	if !r.Date.Equal(adapter.Date(2026, 9, 8)) || r.Base != "USD" || r.Quote != "KWD" || r.Rate != 0.30665 {
		t.Errorf("rate = %+v", r)
	}
}

func TestParseKeepsSubFilsQuotesExact(t *testing.T) {
	rates := mustParse(t, fragment([2]string{"09.09.2026", "0.018"}), "IDR")
	if rates[0].Rate != 0.000018 {
		t.Errorf("rate = %v, want 0.000018", rates[0].Rate)
	}
}

func TestParseSkipsZeroRates(t *testing.T) {
	if rates := mustParse(t, fragment([2]string{"09.09.2026", "0.000"}), "VEF"); len(rates) != 0 {
		t.Errorf("rates = %+v, want none", rates)
	}
}

func TestParseEmptyTable(t *testing.T) {
	if rates := mustParse(t, fragment(), "USD"); len(rates) != 0 {
		t.Errorf("rates = %+v, want none", rates)
	}
}

func TestParseFailsWithoutFragment(t *testing.T) {
	if _, err := parse("<html><body>Request Rejected</body></html>", "USD"); err == nil {
		t.Error("want error")
	}
}

func TestParseFormIDAndCurrencyIDs(t *testing.T) {
	formID, currencies, err := parseForm(`<input id="formId" name="formId" type="hidden" value="127906" />
<select id="selCurrency" name="selCurrency">
  <option value="" selected>Select</option>
  <option value="USD:128735"  selected>US Dollar</option>
  <option value="EUR:128674" >EURO</option>
</select>
`)
	if err != nil {
		t.Fatal(err)
	}
	if formID != "127906" {
		t.Errorf("formID = %q", formID)
	}
	if want := []currency{{"USD", "128735"}, {"EUR", "128674"}}; !slices.Equal(currencies, want) {
		t.Errorf("currencies = %v, want %v", currencies, want)
	}
}

func TestParseFormFailsWithoutOptions(t *testing.T) {
	if _, _, err := parseForm(`<input name="formId" value="1" />`); err == nil {
		t.Error("want error")
	}
}

func TestFetchDateRange(t *testing.T) {
	a := New(vcrtest.Client(t, "cbkkw", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 9, 6), adapter.Date(2026, 9, 8))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) == 0 {
		t.Fatal("no rates")
	}

	quotes, bases := map[string]bool{}, map[string]bool{}
	dates := map[time.Time]bool{}
	for _, r := range rates {
		quotes[r.Quote], bases[r.Base], dates[r.Date] = true, true, true
	}
	if got := slices.Collect(maps.Keys(quotes)); !slices.Equal(got, []string{"KWD"}) {
		t.Errorf("quotes = %v, want [KWD]", got)
	}
	if len(bases) <= 100 {
		t.Errorf("got %d bases, want more than 100", len(bases))
	}
	gotDates := slices.SortedFunc(maps.Keys(dates), time.Time.Compare)
	wantDates := []time.Time{adapter.Date(2026, 9, 6), adapter.Date(2026, 9, 7), adapter.Date(2026, 9, 8)}
	if !slices.EqualFunc(gotDates, wantDates, time.Time.Equal) {
		t.Errorf("dates = %v, want %v", gotDates, wantDates)
	}

	find := func(base string) adapter.Rate {
		i := slices.IndexFunc(rates, func(r adapter.Rate) bool {
			return r.Base == base && r.Date.Equal(adapter.Date(2026, 9, 8))
		})
		if i < 0 {
			t.Fatalf("no %s rate on 2026-09-08", base)
		}
		return rates[i]
	}
	if got := find("USD").Rate; got != 0.30665 {
		t.Errorf("USD = %v, want 0.30665", got)
	}
	if got := find("ECS").Rate; got != 0.000012 {
		t.Errorf("ECS = %v, want 0.000012", got)
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2026, 9, 6), adapter.Date(2026, 9, 8))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFetchPostsDateWindowPerCurrency(t *testing.T) {
	var forms []url.Values
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := `<input name="formId" value="127906" /><option value="USD:128735"><option value="EUR:128674">`
		if r.Method == http.MethodPost {
			if r.URL.String() != lookupURL {
				t.Errorf("POST %s, want %s", r.URL, lookupURL)
			}
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			forms = append(forms, r.PostForm)
			body = fragment([2]string{"08.09.2026", "306.650"})
		} else if r.URL.String() != formURL {
			t.Errorf("GET %s, want %s", r.URL, formURL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})}

	rates, err := New(client).Fetch(context.Background(), adapter.Date(2026, 9, 6), adapter.Date(2026, 9, 8))
	if err != nil {
		t.Fatal(err)
	}
	want := []url.Values{
		{"formId": {"127906"}, "selCurrency": {"USD:128735"}, "txtDateFrom": {"05/09/2026"}, "txtDateTo": {"08/09/2026"}},
		{"formId": {"127906"}, "selCurrency": {"EUR:128674"}, "txtDateFrom": {"05/09/2026"}, "txtDateTo": {"08/09/2026"}},
	}
	if !reflect.DeepEqual(forms, want) {
		t.Errorf("forms = %v, want %v", forms, want)
	}
	if len(rates) != 2 || rates[0].Base != "USD" || rates[1].Base != "EUR" {
		t.Errorf("rates = %+v", rates)
	}
}

func TestFetchEmptyWindowMakesNoRequests(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL)
		return nil, io.EOF
	})}
	rates, err := New(client).Fetch(context.Background(), adapter.Date(2026, 9, 9), adapter.Date(2026, 9, 8))
	if err != nil || rates != nil {
		t.Errorf("rates, err = %v, %v; want nil, nil", rates, err)
	}
}

func TestParseFormFailsWithoutFormID(t *testing.T) {
	if _, _, err := parseForm(`<option value="USD:128735">`); err == nil {
		t.Error("want error")
	}
}

func TestParseFormDuplicateCodeKeepsFirstPositionLastID(t *testing.T) {
	_, currencies, err := parseForm(`<input name="formId" value="1" />
<option value="USD:1"><option value="EUR:2"><option value="USD:3">`)
	if err != nil {
		t.Fatal(err)
	}
	if want := []currency{{"USD", "3"}, {"EUR", "2"}}; !slices.Equal(currencies, want) {
		t.Errorf("currencies = %v, want %v", currencies, want)
	}
}
