package sbi

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func fetch(t *testing.T) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "sbi", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 24), adapter.Date(2026, 3, 28))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func mustParse(t *testing.T, xml string, currencies map[string]string) []adapter.Rate {
	t.Helper()
	rates, err := parse([]byte(xml), currencies)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestFetchWithDateRange(t *testing.T) {
	if rates := fetch(t); len(rates) == 0 {
		t.Fatal("no rates")
	}
}

func TestFetchMultipleCurrenciesPerDate(t *testing.T) {
	rates := fetch(t)
	first := rates[0].Date
	n := 0
	for _, r := range rates {
		if r.Date.Equal(first) {
			n++
		}
	}
	if n <= 1 {
		t.Errorf("got %d rates on %v, want several", n, first)
	}
}

func TestParseBaseAndQuote(t *testing.T) {
	rates := mustParse(t, `<?xml version="1.0" encoding="utf-8"?>
<Group ID="9">
  <TimeSeries ID="4055">
    <Name>Bandaríkjadalur</Name>
    <TimeSeriesData>
      <Entry><Date>3/24/2026 12:00:00 AM</Date><Value>124.440000</Value></Entry>
    </TimeSeriesData>
  </TimeSeries>
</Group>`, group9)

	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	r := rates[0]
	if r.Base != "USD" || r.Quote != "ISK" || r.Rate != 124.44 || !r.Date.Equal(adapter.Date(2026, 3, 24)) {
		t.Errorf("got %+v", r)
	}
}

func TestParseGroup7Currencies(t *testing.T) {
	rates := mustParse(t, `<?xml version="1.0" encoding="utf-8"?>
<Group ID="7">
  <TimeSeries ID="29">
    <Name>Kínverskt júan</Name>
    <TimeSeriesData>
      <Entry><Date>3/25/2026 12:00:00 AM</Date><Value>17.930000</Value></Entry>
    </TimeSeriesData>
  </TimeSeries>
</Group>`, group7)

	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	r := rates[0]
	if r.Base != "CNY" || r.Quote != "ISK" || r.Rate != 17.93 {
		t.Errorf("got %+v", r)
	}
}

// TestParseSkips covers "skips entries with missing values", "skips entries with zero rates" and "skips unknown
// TimeSeries IDs".
func TestParseSkips(t *testing.T) {
	for name, xml := range map[string]string{
		"missing values": `<?xml version="1.0" encoding="utf-8"?>
<Group ID="9">
  <TimeSeries ID="4055">
    <Name>Bandaríkjadalur</Name>
    <TimeSeriesData>
      <Entry><Date>3/24/2026 12:00:00 AM</Date></Entry>
    </TimeSeriesData>
  </TimeSeries>
</Group>`,
		"zero rates": `<?xml version="1.0" encoding="utf-8"?>
<Group ID="9">
  <TimeSeries ID="4055">
    <Name>Bandaríkjadalur</Name>
    <TimeSeriesData>
      <Entry><Date>3/24/2026 12:00:00 AM</Date><Value>0.000000</Value></Entry>
    </TimeSeriesData>
  </TimeSeries>
</Group>`,
		"unknown TimeSeries IDs": `<?xml version="1.0" encoding="utf-8"?>
<Group ID="9">
  <TimeSeries ID="9999">
    <Name>Unknown</Name>
    <TimeSeriesData>
      <Entry><Date>3/24/2026 12:00:00 AM</Date><Value>100.000000</Value></Entry>
    </TimeSeriesData>
  </TimeSeries>
</Group>`,
	} {
		t.Run(name, func(t *testing.T) {
			if rates := mustParse(t, xml, group9); len(rates) != 0 {
				t.Errorf("got %+v, want none", rates)
			}
		})
	}
}

func TestParseSkipsWhitespaceValue(t *testing.T) {
	rates := mustParse(t, `<Group ID="9"><TimeSeries ID="4055"><TimeSeriesData>
<Entry><Date>3/24/2026 12:00:00 AM</Date><Value> </Value></Entry>
<Entry><Date>3/25/2026 12:00:00 AM</Date><Value/></Entry>
</TimeSeriesData></TimeSeries></Group>`, group9)
	if len(rates) != 0 {
		t.Errorf("got %+v, want none", rates)
	}
}

// Ruby's Float() raises on text that isn't a finite number.
func TestParseRejectsBadValue(t *testing.T) {
	for _, v := range []string{"abc", "NaN", "Inf"} {
		xml := `<Group ID="9"><TimeSeries ID="4055"><TimeSeriesData><Entry><Date>3/24/2026 12:00:00 AM</Date><Value>` +
			v + `</Value></Entry></TimeSeriesData></TimeSeries></Group>`
		if _, err := parse([]byte(xml), group9); err == nil {
			t.Errorf("%s: want error", v)
		}
	}
}

type recorder struct{ urls []*url.URL }

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	r.urls = append(r.urls, req.URL)
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`<Group/>`)),
		Header:     http.Header{},
		Request:    req,
	}, nil
}

func TestFetchRequests(t *testing.T) {
	for _, tc := range []struct {
		name     string
		after    time.Time
		wantFrom string
	}{
		{"bounded", adapter.Date(2026, 3, 24), "2026-03-24"},
		{"open start", time.Time{}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recorder{}
			a := New(&http.Client{Transport: rec})
			if _, err := a.Fetch(context.Background(), tc.after, adapter.Date(2026, 3, 28)); err != nil {
				t.Fatal(err)
			}
			if len(rec.urls) != 2 {
				t.Fatalf("got %d requests, want 2", len(rec.urls))
			}
			for i, group := range []string{"9", "7"} {
				u := rec.urls[i]
				if got := u.Scheme + "://" + u.Host + u.Path; got != baseURL {
					t.Errorf("url %s", got)
				}
				q := u.Query()
				if !q.Has("DagsFra") || q.Get("DagsFra") != tc.wantFrom || q.Get("DagsTil") != "2026-03-28" ||
					q.Get("GroupID") != group || q.Get("Type") != "xml" {
					t.Errorf("query %v", q)
				}
			}
		})
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	a := New(g.Client(t))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 24), adapter.Date(2026, 3, 28))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
