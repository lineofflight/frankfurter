package sbi

import (
	"context"
	"testing"

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

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	a := New(g.Client(t))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 24), adapter.Date(2026, 3, 28))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
