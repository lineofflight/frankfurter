package cbllr

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func fetch(t *testing.T) []adapter.Rate {
	t.Helper()
	client := vcrtest.Client(t, "cbllr", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host), vcrtest.AllowPlaybackRepeats)
	rates, err := New(client).Fetch(context.Background(), adapter.Date(2026, 5, 15), adapter.Date(2026, 5, 22))
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

func dates(rates []adapter.Rate) []time.Time {
	out := make([]time.Time, len(rates))
	for i, r := range rates {
		out[i] = r.Date
	}
	return out
}

func TestFetchNarrowDateRange(t *testing.T) {
	rates := fetch(t)
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	first := rates[0]
	if first.Base != "USD" || first.Quote != "LRD" {
		t.Errorf("pair = %s/%s, want USD/LRD", first.Base, first.Quote)
	}
	if first.Rate <= 0 {
		t.Errorf("rate = %v, want > 0", first.Rate)
	}
}

func TestFetchFiltersDatesToRange(t *testing.T) {
	ds := dates(fetch(t))
	if len(ds) == 0 {
		t.Fatal("no rates")
	}
	if lo := slices.MinFunc(ds, time.Time.Compare); !lo.After(adapter.Date(2026, 5, 15)) {
		t.Errorf("min date = %v, want after 2026-05-15", lo)
	}
	if hi := slices.MaxFunc(ds, time.Time.Compare); hi.After(adapter.Date(2026, 5, 22)) {
		t.Errorf("max date = %v, want on or before 2026-05-22", hi)
	}
}

func TestFetchSortedByDateAscending(t *testing.T) {
	ds := dates(fetch(t))
	if !slices.IsSortedFunc(ds, time.Time.Compare) {
		t.Errorf("dates not ascending: %v", ds)
	}
}

func TestParseCoercesBuySellToMid(t *testing.T) {
	rates := mustParse(t, `<div class="view-content">
  <table>
    <tbody>
      <tr>
        <td class="views-field views-field-field-content-post-date">
          <time datetime="2026-05-21T12:00:00Z">Thursday, May 21, 2026</time>
        </td>
        <td class="views-field views-field-field-buying-us">L$182.0000/US$1.00</td>
        <td class="views-field views-field-field-selling-us">L$184.0000/US$1.00</td>
      </tr>
      <tr>
        <td class="views-field views-field-field-content-post-date">
          <time datetime="2026-05-20T12:00:00Z">Wednesday, May 20, 2026</time>
        </td>
        <td class="views-field views-field-field-buying-us">L$181.5000/US$1.00</td>
        <td class="views-field views-field-field-selling-us">L$183.5000/US$1.00</td>
      </tr>
    </tbody>
  </table>
</div>`)
	if len(rates) != 2 {
		t.Fatalf("len = %d, want 2", len(rates))
	}
	first := rates[0]
	if !first.Date.Equal(adapter.Date(2026, 5, 21)) {
		t.Errorf("date = %v, want 2026-05-21", first.Date)
	}
	if first.Base != "USD" || first.Quote != "LRD" {
		t.Errorf("pair = %s/%s, want USD/LRD", first.Base, first.Quote)
	}
	if first.Rate != 183.0 {
		t.Errorf("first rate = %v, want 183.0", first.Rate)
	}
	if rates[1].Rate != 182.5 {
		t.Errorf("last rate = %v, want 182.5", rates[1].Rate)
	}
}

// The pair that surfaced #579: (181.5264 + 181.76) / 2.0 => 181.64319999999998 in binary floats.
func TestParseComputesMidExactly(t *testing.T) {
	rates := mustParse(t, `<div class="view-content">
  <table>
    <tbody>
      <tr>
        <td class="views-field views-field-field-content-post-date">
          <time datetime="2026-08-11T12:00:00Z">Tuesday, August 11, 2026</time>
        </td>
        <td class="views-field views-field-field-buying-us">L$181.5264/US$1.00</td>
        <td class="views-field views-field-field-selling-us">L$181.7600/US$1.00</td>
      </tr>
    </tbody>
  </table>
</div>`)
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	if rates[0].Rate != 181.6432 {
		t.Errorf("rate = %v, want 181.6432", rates[0].Rate)
	}
}

func TestParseSkipsRowsMissingRateCells(t *testing.T) {
	rates := mustParse(t, `<div class="view-content">
  <table>
    <tbody>
      <tr>
        <td class="views-field views-field-field-content-post-date">
          <time datetime="2026-05-21T12:00:00Z">Thursday, May 21, 2026</time>
        </td>
      </tr>
      <tr>
        <td class="views-field views-field-field-content-post-date">
          <time datetime="2026-05-20T12:00:00Z">Wednesday, May 20, 2026</time>
        </td>
        <td class="views-field views-field-field-buying-us">L$181.5000/US$1.00</td>
        <td class="views-field views-field-field-selling-us">L$183.5000/US$1.00</td>
      </tr>
    </tbody>
  </table>
</div>`)
	if len(rates) != 1 {
		t.Fatalf("len = %d, want 1", len(rates))
	}
	if !rates[0].Date.Equal(adapter.Date(2026, 5, 20)) {
		t.Errorf("date = %v, want 2026-05-20", rates[0].Date)
	}
}

func TestParseSkipsUnparseableRateValues(t *testing.T) {
	rates := mustParse(t, `<div class="view-content">
  <table>
    <tbody>
      <tr>
        <td class="views-field views-field-field-content-post-date">
          <time datetime="2026-05-21T12:00:00Z">Thursday, May 21, 2026</time>
        </td>
        <td class="views-field views-field-field-buying-us">N/A</td>
        <td class="views-field views-field-field-selling-us">N/A</td>
      </tr>
    </tbody>
  </table>
</div>`)
	if len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	a := New(g.Client(t))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 5, 15), adapter.Date(2026, 5, 22))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
