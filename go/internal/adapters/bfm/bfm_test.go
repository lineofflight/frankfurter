package bfm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func newAdapter(t *testing.T) *Adapter {
	return New(vcrtest.Client(t, "bfm", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI, vcrtest.Body)))
}

// response builds the API envelope around rates, as the spec's helper does.
func response(t *testing.T, rates any, extra map[string]any) []byte {
	t.Helper()
	data := map[string]any{"coursMid": rates}
	for k, v := range extra {
		data[k] = v
	}
	body, err := json.Marshal(map[string]any{
		"code": "cours-de-mid-en-ar-filter",
		"data": map[string]any{"status": 200, "data": data},
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func find(rates []adapter.Rate, base string, date time.Time) *adapter.Rate {
	for i, r := range rates {
		if r.Base == base && r.Date.Equal(date) {
			return &rates[i]
		}
	}
	return nil
}

func uniqueDates(rates []adapter.Rate) []time.Time {
	var dates []time.Time
	for _, r := range rates {
		if !slices.ContainsFunc(dates, r.Date.Equal) {
			dates = append(dates, r.Date)
		}
	}
	slices.SortFunc(dates, time.Time.Compare)
	return dates
}

func TestFetchAllPublishedCurrenciesFromRecentArchive(t *testing.T) {
	rates, err := newAdapter(t).Fetch(context.Background(), adapter.Date(2026, 9, 21), adapter.Date(2026, 9, 23))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 57 {
		t.Errorf("got %d rates, want 57", len(rates))
	}
	wantDates := []time.Time{adapter.Date(2026, 9, 21), adapter.Date(2026, 9, 22), adapter.Date(2026, 9, 23)}
	if got := uniqueDates(rates); !reflect.DeepEqual(got, wantDates) {
		t.Errorf("dates = %v, want %v", got, wantDates)
	}
	var bases, quotes []string
	for _, r := range rates {
		if !slices.Contains(bases, r.Base) {
			bases = append(bases, r.Base)
		}
		if !slices.Contains(quotes, r.Quote) {
			quotes = append(quotes, r.Quote)
		}
	}
	slices.Sort(bases)
	wantBases := []string{"AUD", "CAD", "CHF", "CNY", "DJF", "DKK", "EUR", "GBP", "HKD", "INR", "JPY", "MUR",
		"NOK", "NZD", "SEK", "SGD", "USD", "XDR", "ZAR"}
	if !slices.Equal(bases, wantBases) {
		t.Errorf("bases = %v, want %v", bases, wantBases)
	}
	if !slices.Equal(quotes, []string{"MGA"}) {
		t.Errorf("quotes = %v, want [MGA]", quotes)
	}
	usd := find(rates, "USD", adapter.Date(2026, 9, 23))
	if usd == nil || usd.Rate != 4391.53 {
		t.Errorf("USD on 2026-09-23 = %+v, want 4391.53", usd)
	}
}

func TestFetchEarliestArchiveInNativeAriaryUnits(t *testing.T) {
	rates, err := newAdapter(t).Fetch(context.Background(), adapter.Date(2018, 1, 2), adapter.Date(2018, 1, 5))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 76 {
		t.Errorf("got %d rates, want 76", len(rates))
	}
	usd := find(rates, "USD", adapter.Date(2018, 1, 2))
	if usd == nil || usd.Rate != 3220.45 || usd.Quote != "MGA" {
		t.Errorf("USD on 2018-01-02 = %+v, want 3220.45 MGA", usd)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFetchBoundsRequestsToAYearAndKeepsDateLimits(t *testing.T) {
	windows := [][3]string{{"2025/01/01", "2025/12/31", "2025-01-01"}, {"2026/01/01", "2026/01/02", "2026-01-02"}}
	stubs := map[string][]byte{}
	for _, code := range currencies {
		for _, w := range windows {
			form := url.Values{"dateFilterDebut": {w[0]}, "dateFilterFin": {w[1]}, "filterData": {code}}
			stubs[form.Encode()] = response(t, map[string]string{"2024-12-31": "100", w[2]: "123,45", "2026-01-03": "200"}, nil)
		}
	}
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		form, err := url.ParseQuery(string(raw))
		if err != nil {
			return nil, err
		}
		body, ok := stubs[form.Encode()]
		if r.Method != http.MethodPost || r.URL.String() != baseURL || !ok {
			t.Errorf("unexpected request %s %s %s", r.Method, r.URL, raw)
			return &http.Response{StatusCode: http.StatusNotFound, Body: http.NoBody, Request: r}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(body))), Request: r}, nil
	})}

	rates, err := New(client).Fetch(context.Background(), adapter.Date(2025, 1, 1), adapter.Date(2026, 1, 2))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 38 {
		t.Errorf("got %d rates, want 38", len(rates))
	}
	want := []time.Time{adapter.Date(2025, 1, 1), adapter.Date(2026, 1, 2)}
	if got := uniqueDates(rates); !reflect.DeepEqual(got, want) {
		t.Errorf("dates = %v, want %v", got, want)
	}
}

func TestParseRelaysReferenceWithoutAveragingExtremes(t *testing.T) {
	body := response(t, map[string]string{"2018-01-02": "3 220,45"}, map[string]any{
		"coursMidMin": map[string]string{"2018-01-02": "3 210,00"},
		"coursMidMax": map[string]string{"2018-01-02": "3 252,00"},
	})
	rates, err := parse(body, "USD")
	if err != nil {
		t.Fatal(err)
	}
	want := []adapter.Rate{{Date: adapter.Date(2018, 1, 2), Base: "USD", Quote: "MGA", Rate: 3220.45}}
	if !reflect.DeepEqual(rates, want) {
		t.Errorf("got %+v, want %+v", rates, want)
	}
}

// The Ruby spec checks RateComponents.attributes keeps every digit as mid. Here
// rates are float64, so the check is that the parsed rate is the nearest float
// to the published decimal.
func TestParsePreservesLongPublishedDigits(t *testing.T) {
	rates, err := parse(response(t, map[string]string{"2026-09-23": "4 391,53123456789"}, nil), "USD")
	if err != nil {
		t.Fatal(err)
	}
	want, _ := strconv.ParseFloat("4391.53123456789", 64)
	if len(rates) != 1 || rates[0].Rate != want {
		t.Errorf("got %+v, want rate %v", rates, want)
	}
}

func TestParseAcceptsNonbreakingGroupingSpacesWithoutRescalingYen(t *testing.T) {
	// A raw literal keeps the published key order, which json.Marshal of a map
	// would sort anyway.
	body := []byte(`{"code":"cours-de-mid-en-ar-filter","data":{"status":200,"data":{"coursMid":` +
		`{"2026-09-22":"1` + "\u00a0" + `234,56","2026-09-23":"1` + "\u202f" + `234,57"}}}}`)
	rates, err := parse(body, "JPY")
	if err != nil {
		t.Fatal(err)
	}
	var got []float64
	for _, r := range rates {
		got = append(got, r.Rate)
	}
	if !slices.Equal(got, []float64{1234.56, 1234.57}) {
		t.Errorf("rates = %v, want [1234.56 1234.57]", got)
	}
}

func TestParseSkipsMissingInvalidAndNonpositiveValues(t *testing.T) {
	body := response(t, map[string]any{"2026-09-21": nil, "2026-09-22": "", "2026-09-23": "N/A", "2026-09-24": "0",
		"2026-09-25": "-1"}, nil)
	rates, err := parse(body, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 0 {
		t.Errorf("got %+v, want none", rates)
	}
}

func TestParseAcceptsExplicitEmptyArchive(t *testing.T) {
	rates, err := parse(response(t, []any{}, nil), "USD")
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 0 {
		t.Errorf("got %+v, want none", rates)
	}
}

func TestParseRaisesOnSemanticErrorsOrMissingData(t *testing.T) {
	for _, body := range [][]byte{
		[]byte(`{}`),
		[]byte(`{"data":{"status":500,"data":{"coursMid":[]}}}`),
		response(t, []string{"unexpected"}, nil),
		response(t, nil, nil),
		[]byte(`{"data":{"status":"200","data":{"coursMid":[]}}}`),
	} {
		if _, err := parse(body, "USD"); err == nil {
			t.Errorf("parse(%s): want an error", body)
		}
	}
}

func TestGolden(t *testing.T) {
	for _, tc := range []struct {
		file        string
		after, upto time.Time
	}{
		{"testdata/golden/recent.json", adapter.Date(2026, 9, 21), adapter.Date(2026, 9, 23)},
		{"testdata/golden/earliest.json", adapter.Date(2018, 1, 2), adapter.Date(2018, 1, 5)},
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
