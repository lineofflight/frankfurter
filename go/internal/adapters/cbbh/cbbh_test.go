package cbbh

import (
	"context"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func newClient(t *testing.T) *http.Client {
	return vcrtest.Client(t, "cbbh", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI))
}

func fetch(t *testing.T, a *Adapter, after, upto time.Time) []adapter.Rate {
	t.Helper()
	rates, err := a.Fetch(context.Background(), after, upto)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func find(rates []adapter.Rate, date time.Time, base string) (adapter.Rate, bool) {
	for _, r := range rates {
		if r.Date.Equal(date) && r.Base == base {
			return r, true
		}
	}
	return adapter.Rate{}, false
}

func uniqueDates(rates []adapter.Rate) []time.Time {
	var dates []time.Time
	for _, r := range rates {
		if !slices.ContainsFunc(dates, r.Date.Equal) {
			dates = append(dates, r.Date)
		}
	}
	return dates
}

func bases(rates []adapter.Rate) []string {
	var out []string
	for _, r := range rates {
		out = append(out, r.Base)
	}
	return out
}

func TestFetchPublishedEffectiveDates(t *testing.T) {
	rates := fetch(t, New(newClient(t)), adapter.Date(2026, 9, 24), adapter.Date(2026, 9, 26))

	if len(rates) != 51 {
		t.Errorf("got %d rates, want 51", len(rates))
	}
	want := []time.Time{adapter.Date(2026, 9, 24), adapter.Date(2026, 9, 25), adapter.Date(2026, 9, 26)}
	if got := uniqueDates(rates); !slices.EqualFunc(got, want, time.Time.Equal) {
		t.Errorf("dates = %v, want %v", got, want)
	}
	for _, r := range rates {
		if r.Quote != "BAM" {
			t.Errorf("quote = %q, want BAM", r.Quote)
		}
	}
	for base, rate := range map[string]float64{"USD": 1.720621, "JPY": 0.01083142} {
		r, ok := find(rates, adapter.Date(2026, 9, 25), base)
		if !ok || r.Rate != rate {
			t.Errorf("%s on 2026-09-25 = %v (found %v), want %v", base, r.Rate, ok, rate)
		}
	}
	if !slices.Contains(bases(rates), "XDR") {
		t.Error("want XDR among bases")
	}
}

// The Ruby spec also backfills through Provider and reads the rows back through the API. Core packages are not
// available here, so the same values are asserted on the fetched rows. Its unknown_currencies check needs the
// currency catalogue and is not ported.
func TestFetchEarliestListsPreserveHistoricalUnits(t *testing.T) {
	rates := fetch(t, New(newClient(t)), adapter.Date(1998, 1, 6), adapter.Date(1998, 1, 8))

	want := []time.Time{adapter.Date(1998, 1, 6), adapter.Date(1998, 1, 8)}
	if got := uniqueDates(rates); !slices.EqualFunc(got, want, time.Time.Equal) {
		t.Errorf("dates = %v, want %v", got, want)
	}
	for base, rate := range map[string]float64{"ATS": 0.1421367521, "ESP": 0.0118233618, "XEU": 1.97509972} {
		r, ok := find(rates, adapter.Date(1998, 1, 6), base)
		if !ok || r.Rate != rate {
			t.Errorf("%s on 1998-01-06 = %v (found %v), want %v", base, r.Rate, ok, rate)
		}
	}
	for _, r := range rates {
		if r.Base == "ESB" || r.Base == "XBA" {
			t.Errorf("unexpected source alias %s on %s", r.Base, r.Date.Format(time.DateOnly))
		}
	}
}

func TestFetchKeepsPesetaAndECUAcrossLabelCorrection(t *testing.T) {
	rates := fetch(t, New(newClient(t)), adapter.Date(1998, 7, 14), adapter.Date(1998, 7, 16))

	var got []adapter.Rate
	for _, r := range rates {
		if slices.Contains([]string{"ESP", "XEU", "ESB", "XBA"}, r.Base) {
			got = append(got, r)
		}
	}
	row := func(y, m, d int, base string, rate float64) adapter.Rate {
		return adapter.Rate{Date: adapter.Date(y, time.Month(m), d), Base: base, Quote: "BAM", Rate: rate}
	}
	want := []adapter.Rate{
		row(1998, 7, 14, "XEU", 1.97741014),
		row(1998, 7, 14, "ESP", 0.011789601),
		row(1998, 7, 16, "ESP", 0.0117867372),
		row(1998, 7, 16, "XEU", 1.97491187),
	}
	if !slices.Equal(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestFetchConfirmsEmptySundayMondayRange(t *testing.T) {
	if rates := fetch(t, New(newClient(t)), adapter.Date(2025, 1, 5), adapter.Date(2025, 1, 6)); len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
}

// stubPeriod answers the period export for 2026-09-25 with the export-error string, as the Ruby spec's WebMock stub
// does, and sends everything else to the cassette.
type stubPeriod struct{ next http.RoundTripper }

func (s stubPeriod) RoundTrip(req *http.Request) (*http.Response, error) {
	q := req.URL.Query()
	if strings.HasPrefix(req.URL.String(), periodURL) && q.Get("dateFrom") == "2026-09-25" && q.Get("dateTo") == "2026-09-25" {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader(`"Problem with export"`)),
			Request:    req,
		}, nil
	}
	return s.next.RoundTrip(req)
}

func TestFetchDoesNotHideFailedPeriodExport(t *testing.T) {
	client := &http.Client{Transport: stubPeriod{newClient(t).Transport}}
	_, err := New(client).Fetch(context.Background(), adapter.Date(2026, 9, 25), adapter.Date(2026, 9, 25))
	if err == nil || !strings.Contains(err.Error(), "despite an available list") {
		t.Errorf("err = %v, want a failed export error", err)
	}
}

func TestFetchSkipsReversedAndPreCoverageRanges(t *testing.T) {
	// No cassette: any request would fail.
	a := New(&http.Client{Transport: failTransport{t}})
	for _, r := range [][2]time.Time{
		{adapter.Date(2025, 1, 2), adapter.Date(2025, 1, 1)},
		{adapter.Date(1997, 1, 1), adapter.Date(1998, 1, 5)},
	} {
		if rates := fetch(t, a, r[0], r[1]); len(rates) != 0 {
			t.Errorf("%v: got %d rates, want none", r, len(rates))
		}
	}
}

type failTransport struct{ t *testing.T }

func (f failTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	f.t.Errorf("unexpected request %s", req.URL)
	return nil, http.ErrNotSupported
}

func entry(code, units, middle string) string {
	return `{"AlphaCode": "` + code + `", "Units": "` + units + `", "Middle": "` + middle + `", "Buy": "1", "Sell": "3"}`
}

func list(items ...string) []byte {
	return []byte(`[{"Date": "2025-01-04T00:00:00", "CurrencyExchangeItems": [` + strings.Join(items, ",") + `]}]`)
}

func TestParseUsesPublishedMiddleAndDecimalArithmetic(t *testing.T) {
	rates, err := parse(list(entry("JPY", "100", "1,23456789"), entry("USD", "1", "1.9876543212345")))
	if err != nil {
		t.Fatal(err)
	}
	want := []adapter.Rate{
		{Date: adapter.Date(2025, 1, 4), Base: "JPY", Quote: "BAM", Rate: 0.0123456789},
		{Date: adapter.Date(2025, 1, 4), Base: "USD", Quote: "BAM", Rate: 1.9876543212345},
	}
	if !slices.Equal(rates, want) {
		t.Errorf("got %+v, want %+v", rates, want)
	}
}

func TestParseRetainsSourceLegacyCodes(t *testing.T) {
	rates, err := parse(list(entry("TRL", "100", "0.000107"), entry("ESB", "100", "1.18233618")))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 2 || rates[0].Base != "TRL" || rates[0].Rate != 0.00000107 ||
		rates[1].Base != "ESB" || rates[1].Rate != 0.0118233618 {
		t.Errorf("got %+v", rates)
	}
}

func TestParseSkipsInvalidEntries(t *testing.T) {
	rates, err := parse(list(entry("KWD", "", ""), entry("USD", "1", "0"), entry("JPY", "0", "1"),
		entry("EUR", "1", "NaN"), entry("CHF", "1", "-1"), entry("?", "1", "2")))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 0 {
		t.Errorf("got %+v, want none", rates)
	}
}

func TestParseRaisesOnMalformedExport(t *testing.T) {
	for _, data := range []string{
		`"Problem with export"`,
		`{}`,
		`[{"Date": "2025-01-04"}]`,
		`[{"Date": "invalid", "CurrencyExchangeItems": []}]`,
	} {
		if _, err := parse([]byte(data)); err == nil {
			t.Errorf("parse(%s): want an error", data)
		}
	}
}

func TestGolden(t *testing.T) {
	for _, tc := range []struct {
		file        string
		after, upto time.Time
	}{
		{"recent", adapter.Date(2026, 9, 24), adapter.Date(2026, 9, 26)},
		{"earliest", adapter.Date(1998, 1, 6), adapter.Date(1998, 1, 8)},
		{"relabel", adapter.Date(1998, 7, 14), adapter.Date(1998, 7, 16)},
		{"empty", adapter.Date(2025, 1, 5), adapter.Date(2025, 1, 6)},
	} {
		t.Run(tc.file, func(t *testing.T) {
			g := golden.Load(t, "testdata/golden/"+tc.file+".json")
			g.Check(t, fetch(t, New(g.Client(t)), tc.after, tc.upto))
		})
	}
}
