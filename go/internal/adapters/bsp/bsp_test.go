package bsp

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func fetch(t *testing.T, after, upto time.Time) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "bsp", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI)))
	rates, err := a.Fetch(context.Background(), after, upto)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestFetchOnlyReferenceRateAsUSDOverPHP(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 5, 27), adapter.Date(2026, 5, 29))
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	for _, r := range rates {
		if r.Base != "USD" || r.Quote != "PHP" {
			t.Errorf("pair %s/%s, want USD/PHP", r.Base, r.Quote)
		}
	}
}

func TestFetchEmitsReferenceRateNotReutersEquivalent(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 5, 29), adapter.Date(2026, 5, 29))
	i := slices.IndexFunc(rates, func(r adapter.Rate) bool { return r.Base == "USD" })
	if i < 0 {
		t.Fatal("no USD rate")
	}
	// BSP Reference Rate is 61.600; the USD row's peso equivalent is 61.6540.
	if got := rates[i].Rate; math.Abs(got-61.600) > 0.001 {
		t.Errorf("USD = %v, want 61.600", got)
	}
}

func TestFetchFiltersByDateRange(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 5, 27), adapter.Date(2026, 5, 29))
	for _, r := range rates {
		if r.Date.Before(adapter.Date(2026, 5, 27)) || r.Date.After(adapter.Date(2026, 5, 29)) {
			t.Errorf("date %s outside 2026-05-27..2026-05-29", r.Date.Format(time.DateOnly))
		}
	}
}

const bulletin = `   1 UNITED STATES                       DOLLAR             USD             0.858222     1.000000      61.6540
   2 JAPAN                               YEN                JPY             0.005390     0.006280       0.3872
  16 EUROPEAN MONETARY UNION             EURO               EUR             1.000000     1.165200      71.8392
     BSP Buying Rate (T/T)PHP            61.350      GOLD BUYING:   $      4,495.00
     BSP Selling Rate (T/TPHP            61.850      SILVER BUYING: $         75.80
     BSP Reference Rate:  PHP            61.600
     SDR Rate:            $               1.36668    /SDR
`

func parseBulletin(t *testing.T) []adapter.Rate {
	t.Helper()
	rates, err := parseText(bulletin, adapter.Date(2026, 5, 29))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func bases(rates []adapter.Rate) []string {
	var bs []string
	for _, r := range rates {
		bs = append(bs, r.Base)
	}
	return bs
}

func TestParseTextEmitsOnlyReferenceRate(t *testing.T) {
	want := []adapter.Rate{{Date: adapter.Date(2026, 5, 29), Base: "USD", Quote: "PHP", Rate: 61.600}}
	got := parseBulletin(t)
	if !slices.EqualFunc(got, want, func(x, y adapter.Rate) bool {
		return x.Date.Equal(y.Date) && x.Base == y.Base && x.Quote == y.Quote && x.Rate == y.Rate &&
			x.Bid == nil && x.Ask == nil && x.Mid == nil
	}) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestParseTextSkipsLSEGTable(t *testing.T) {
	bs := bases(parseBulletin(t))
	for _, code := range []string{"JPY", "EUR"} {
		if slices.Contains(bs, code) {
			t.Errorf("relayed %s", code)
		}
	}
}

func TestParseTextSkipsSDRAndMetals(t *testing.T) {
	bs := bases(parseBulletin(t))
	for _, code := range []string{"XDR", "XAU", "XAG"} {
		if slices.Contains(bs, code) {
			t.Errorf("relayed %s", code)
		}
	}
}

func TestParseTextSkipsUSDRowReutersEquivalent(t *testing.T) {
	for _, r := range parseBulletin(t) {
		if r.Rate == 61.6540 {
			t.Errorf("relayed the USD row's peso equivalent")
		}
	}
}

func TestParseTextImageOnlyScan(t *testing.T) {
	rates, err := parseText("  \n ", adapter.Date(2016, 5, 29))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
}

func TestParseTextMissingReferenceRate(t *testing.T) {
	_, err := parseText("no reference rate in this text", adapter.Date(2026, 5, 29))
	if err == nil || !strings.Contains(err.Error(), "Reference Rate line missing") {
		t.Errorf("err = %v, want Reference Rate line missing", err)
	}
}

func TestGolden(t *testing.T) {
	for _, tc := range []struct {
		file        string
		after, upto time.Time
	}{
		{"testdata/golden/fetch.json", adapter.Date(2026, 5, 27), adapter.Date(2026, 5, 29)},
		{"testdata/golden/single_day.json", adapter.Date(2026, 5, 29), adapter.Date(2026, 5, 29)},
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func item(title, url string) string {
	return fmt.Sprintf(`{"Title":%q,"AttachmentFiles":{"results":[{"ServerRelativeUrl":%q}]}}`, title, url)
}

func TestDiscoverPagesUntilWindowStart(t *testing.T) {
	pages := map[string]string{
		"page1": `{"d":{"results":[` + item("30May2026", "/30") + `,` + item("29May2026", "/29a") + `,` +
			item("29May2026", "/29b") + `,` + item("junk", "/junk") + `],"__next":"https://www.bsp.gov.ph/page2"}}`,
		"/page2": `{"d":{"results":[` + item("28May2026", "/28") + `,` + item("26May2026", "/26") +
			`],"__next":"https://www.bsp.gov.ph/page3"}}`,
	}
	var fetched []string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if got := r.Header.Get("Accept"); got != "application/json;odata=verbose" {
			t.Errorf("Accept = %q", got)
		}
		key := r.URL.Path
		if strings.HasSuffix(key, "/items") {
			key = "page1"
		}
		fetched = append(fetched, key)
		body, ok := pages[key]
		if !ok {
			return nil, fmt.Errorf("unexpected request %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})}

	entries, err := New(client).discover(context.Background(), adapter.Date(2026, 5, 27), adapter.Date(2026, 5, 29))
	if err != nil {
		t.Fatal(err)
	}
	want := []entry{{adapter.Date(2026, 5, 28), "/28"}, {adapter.Date(2026, 5, 29), "/29a"}}
	if !slices.Equal(entries, want) {
		t.Errorf("entries = %v, want %v", entries, want)
	}
	if !slices.Equal(fetched, []string{"page1", "/page2"}) {
		t.Errorf("fetched %v, want page1 and page2 only", fetched)
	}
}

func TestParseTitleDate(t *testing.T) {
	for title, want := range map[string]time.Time{
		"29May2026":      adapter.Date(2026, 5, 29),
		" 6Nov2017 ":     adapter.Date(2017, 11, 6),
		"06November2017": adapter.Date(2017, 11, 6),
	} {
		if got, ok := parseTitleDate(title); !ok || !got.Equal(want) {
			t.Errorf("parseTitleDate(%q) = %v, %v", title, got, ok)
		}
	}
	for _, title := range []string{"", "RERB", "2026-05-29"} {
		if _, ok := parseTitleDate(title); ok {
			t.Errorf("parseTitleDate(%q) parsed", title)
		}
	}
}

func TestParseTextDropsZeroReferenceRate(t *testing.T) {
	rates, err := parseText("BSP Reference Rate:  PHP            0.000", adapter.Date(2026, 5, 29))
	if err != nil || len(rates) != 0 {
		t.Errorf("got %v, %v, want no rates", rates, err)
	}
}

func TestParseTextStripsThousandsSeparator(t *testing.T) {
	rates, err := parseText("BSP Reference Rate:  PHP  1,061.600", adapter.Date(2026, 5, 29))
	if err != nil || len(rates) != 1 || rates[0].Rate != 1061.6 {
		t.Errorf("got %v, %v, want 1061.6", rates, err)
	}
}

func TestParseTextRejectsMalformedReferenceRate(t *testing.T) {
	if _, err := parseText("BSP Reference Rate:  PHP  61.6.0", adapter.Date(2026, 5, 29)); err == nil {
		t.Error("want error")
	}
}
