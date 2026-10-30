package bomu

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func cassette(t *testing.T) *http.Client {
	t.Helper()
	return vcrtest.Client(t, "bomu", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI))
}

func fetch(t *testing.T, client *http.Client, after, upto time.Time) []adapter.Rate {
	t.Helper()
	rates, err := New(client).Fetch(context.Background(), after, upto)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func mustParse(t *testing.T, html string) []adapter.Rate {
	t.Helper()
	rates, err := parse([]byte(html))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func page(rows ...string) string {
	return `<div class="view-display-id-page">
  <div class="view-content"><div class="table-responsive"><table><thead><tr><th>Country</th><th>Code</th><th>T.T</th><th>D.D</th>
  <th>Notes</th><th>T.T/D.D</th><th>Notes</th><th>Date</th></tr></thead>
  <tbody>` + strings.Join(rows, "") + `</tbody></table></div></div>
</div>
`
}

type rowOpts struct{ code, buy, sell, date string }

// row renders a fixture row; edit overrides the defaults.
func row(edit func(*rowOpts)) string {
	o := rowOpts{code: "USD 1", buy: "28.9431", sell: "29.3634", date: "03-07-2001"}
	if edit != nil {
		edit(&o)
	}
	return fmt.Sprintf(`<tr class="tblConso">
  <td class="views-field-name">U.S.A.</td>
  <td class="views-field-field-currency">%s</td>
  <td class="views-field-php">%s</td>
  <td class="views-field-php-1">28.8117</td><td class="views-field-php-2">28.7649</td>
  <td class="views-field-php-3">%s</td><td class="views-field-php-4">.0000</td>
  <td class="views-field-field-transaction-date">%s</td>
</tr>
`, o.code, o.buy, o.sell, o.date)
}

func find(rates []adapter.Rate, base string, date time.Time) *adapter.Rate {
	for i, r := range rates {
		if r.Base == base && (date.IsZero() || r.Date.Equal(date)) {
			return &rates[i]
		}
	}
	return nil
}

func TestFetchBoundedRecentArchiveNativeDirection(t *testing.T) {
	rates := fetch(t, cassette(t), adapter.Date(2026, 9, 23), adapter.Date(2026, 9, 25))

	if len(rates) != 36 {
		t.Fatalf("got %d rates, want 36", len(rates))
	}
	var dates []time.Time
	var quotes []string
	for _, r := range rates {
		if !slices.ContainsFunc(dates, r.Date.Equal) {
			dates = append(dates, r.Date)
		}
		if !slices.Contains(quotes, r.Quote) {
			quotes = append(quotes, r.Quote)
		}
	}
	slices.SortFunc(dates, time.Time.Compare)
	want := []time.Time{adapter.Date(2026, 9, 23), adapter.Date(2026, 9, 24), adapter.Date(2026, 9, 25)}
	if !slices.EqualFunc(dates, want, time.Time.Equal) {
		t.Errorf("dates = %v, want %v", dates, want)
	}
	if !slices.Equal(quotes, []string{"MUR"}) {
		t.Errorf("quotes = %v", quotes)
	}
	aud := find(rates, "AUD", adapter.Date(2026, 9, 25))
	if aud == nil || aud.Rate != 34.28315 {
		t.Errorf("AUD = %+v, want rate 34.28315", aud)
	}
}

func TestFetchHistoricalTransferQuotes(t *testing.T) {
	rates := fetch(t, cassette(t), adapter.Date(2005, 1, 4), adapter.Date(2005, 1, 4))

	if len(rates) != 10 {
		t.Fatalf("got %d rates, want 10", len(rates))
	}
	usd := find(rates, "USD", time.Time{})
	if usd == nil {
		t.Fatal("no USD")
	}
	if !usd.Date.Equal(adapter.Date(2005, 1, 4)) {
		t.Errorf("USD date = %v", usd.Date)
	}
	if usd.Rate != 28.00555 {
		t.Errorf("USD rate = %v, want 28.00555", usd.Rate)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFetchChunksLongerRangesInclusiveBounds(t *testing.T) {
	stubs := map[[2]string][]string{
		{"04-01-2005", "03-02-2005"}: {"03-01-2005", "04-01-2005", "03-02-2005"},
		{"04-02-2005", "04-02-2005"}: {"04-02-2005", "05-02-2005"},
	}
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		q := r.URL.Query()
		key := [2]string{q.Get("field_transaction_date_value[value][date]"), q.Get("field_transaction_date_value_1[value][date]")}
		dates, ok := stubs[key]
		base := *r.URL
		base.RawQuery = ""
		if !ok || base.String() != pageURL || len(q) != 2 {
			return nil, fmt.Errorf("unstubbed request %s", r.URL)
		}
		var rows []string
		for _, d := range dates {
			rows = append(rows, row(func(o *rowOpts) { o.date = d }))
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(page(rows...))), Request: r}, nil
	})}

	rates := fetch(t, client, adapter.Date(2005, 1, 4), adapter.Date(2005, 2, 4))

	var got []time.Time
	for _, r := range rates {
		got = append(got, r.Date)
	}
	want := []time.Time{adapter.Date(2005, 1, 4), adapter.Date(2005, 2, 3), adapter.Date(2005, 2, 4)}
	if !slices.EqualFunc(got, want, time.Time.Equal) {
		t.Errorf("dates = %v, want %v", got, want)
	}
}

func TestParseTransferBuySellWithoutFloatNoise(t *testing.T) {
	r := mustParse(t, page(row(nil)))[0]

	if !r.Date.Equal(adapter.Date(2001, 7, 3)) || r.Base != "USD" || r.Quote != "MUR" || r.Rate != 29.15325 ||
		r.Bid == nil || *r.Bid != 28.9431 || r.Ask == nil || *r.Ask != 29.3634 || r.Mid != nil {
		t.Errorf("got %+v", r)
	}
}

func TestParseNormalizesPer100Yen(t *testing.T) {
	r := mustParse(t, page(row(func(o *rowOpts) { o.code, o.buy, o.sell = "JPY 100", "26.9610", "27.8600" })))[0]

	if r.Base != "JPY" {
		t.Errorf("base = %q", r.Base)
	}
	if r.Rate != 0.274105 {
		t.Errorf("rate = %v", r.Rate)
	}
	if *r.Bid != 0.26961 {
		t.Errorf("bid = %v", *r.Bid)
	}
	if *r.Ask != 0.2786 {
		t.Errorf("ask = %v", *r.Ask)
	}
}

func TestParseRemovesSourceNUL(t *testing.T) {
	if n := len(mustParse(t, "<nav>Menu\x00</nav>"+page(row(nil)))); n != 1 {
		t.Errorf("got %d rates, want 1", n)
	}
}

func TestParseIgnoresAttachmentAndSidebar(t *testing.T) {
	attachment := strings.Replace(page(row(nil)), "view-display-id-page", "view-display-id-attachment_1", 1)
	html := regexp.MustCompile(`</div>\s*\z`).ReplaceAllLiteralString(page(row(nil)), attachment+"</div>")

	if n := len(mustParse(t, html)); n != 1 {
		t.Errorf("got %d rates, want 1", n)
	}
}

func TestParseSkipsIncompleteNonpositiveMalformed(t *testing.T) {
	html := page(row(func(o *rowOpts) { o.buy = "" }), row(func(o *rowOpts) { o.sell = "N/A" }), row(func(o *rowOpts) { o.buy = "0" }), row(func(o *rowOpts) { o.sell = "-1" }),
		row(func(o *rowOpts) { o.code = "JPY 0" }), row(func(o *rowOpts) { o.code = "Not a currency" }))

	if rates := mustParse(t, html); len(rates) != 0 {
		t.Errorf("got %+v, want none", rates)
	}
}

// Ruby's strip and Float leave a non-breaking space in place, so such a cell is
// not a number.
func TestParseSkipsPriceWithNonBreakingSpace(t *testing.T) {
	html := page(row(func(o *rowOpts) { o.buy = "28.9431&nbsp;" }), row(func(o *rowOpts) { o.sell = " 29.3634" }))

	if rates := mustParse(t, html); len(rates) != 0 {
		t.Errorf("got %+v, want none", rates)
	}
}

func TestParseAcceptsVerticalTabInLabel(t *testing.T) {
	if n := len(mustParse(t, page(row(func(o *rowOpts) { o.code = "USD\v1" })))); n != 1 {
		t.Errorf("got %d rates, want 1", n)
	}
}

// strptime's %d-%m-%Y takes unpadded days and months.
func TestParseUnpaddedDate(t *testing.T) {
	r := mustParse(t, page(row(func(o *rowOpts) { o.date = "3-7-2001" })))[0]

	if !r.Date.Equal(adapter.Date(2001, 7, 3)) {
		t.Errorf("date = %v", r.Date)
	}
}

func TestParseRaisesOnBadDate(t *testing.T) {
	if _, err := parse([]byte(page(row(func(o *rowOpts) { o.date = "2001-07-03" })))); err == nil {
		t.Error("parse succeeded, want error")
	}
}

func TestParseAcceptsEmptyFilteredView(t *testing.T) {
	if rates := mustParse(t, `<div class="view-display-id-page"></div>`); len(rates) != 0 {
		t.Errorf("got %+v, want none", rates)
	}
}

func TestParseRaisesWhenTableWrapperLost(t *testing.T) {
	body, err := New(cassette(t)).Get(context.Background(), pageURL, url.Values{
		"field_transaction_date_value[value][date]":   {"04-01-2005"},
		"field_transaction_date_value_1[value][date]": {"04-01-2005"},
	})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(strings.ReplaceAll(string(body), "\x00", "")))
	if err != nil {
		t.Fatal(err)
	}
	wrapper := doc.Find(".view-display-id-page > .view-content > .table-responsive").First()
	if wrapper.Length() == 0 {
		t.Fatal("recorded page has no table wrapper")
	}
	wrapper.ReplaceWithSelection(wrapper.Children())
	html, err := goquery.OuterHtml(doc.Selection)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := parse([]byte(html)); err == nil {
		t.Error("parse succeeded, want error")
	}
}

func TestParseRaisesWhenViewMissing(t *testing.T) {
	if _, err := parse([]byte("<html>Request rejected</html>")); err == nil {
		t.Error("parse succeeded, want error")
	}
}

func TestGolden(t *testing.T) {
	for _, tc := range []struct {
		file        string
		after, upto time.Time
	}{
		{"testdata/golden/recent.json", adapter.Date(2026, 9, 23), adapter.Date(2026, 9, 25)},
		{"testdata/golden/historical.json", adapter.Date(2005, 1, 4), adapter.Date(2005, 1, 4)},
	} {
		t.Run(tc.file, func(t *testing.T) {
			g := golden.Load(t, tc.file)
			rates, err := New(g.Client(t)).Fetch(context.Background(), tc.after, tc.upto)
			if err != nil {
				t.Fatal(err)
			}
			g.Check(t, rates)
		})
	}
}
