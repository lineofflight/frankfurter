package sarb

import (
	"context"
	"testing"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func newAdapter(t *testing.T) *Adapter {
	return New(vcrtest.Client(t, "sarb", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host)))
}

func fetch(t *testing.T) []adapter.Rate {
	t.Helper()
	rates, err := newAdapter(t).Fetch(context.Background(), adapter.Date(2026, 3, 23), adapter.Date(2026, 3, 27))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestFetchWithDateRange(t *testing.T) {
	if len(fetch(t)) == 0 {
		t.Fatal("no rates")
	}
}

func TestFetchMultipleCurrenciesPerDate(t *testing.T) {
	rates := fetch(t)
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	first := rates[0].Date
	n := 0
	for _, r := range rates {
		if r.Date.Equal(first) {
			n++
		}
	}
	if n <= 1 {
		t.Errorf("got %d rates on %s, want more than 1", n, first.Format("2006-01-02"))
	}
}

func TestParseBaseAndQuote(t *testing.T) {
	rates, err := parse([]byte(`[{"Period":"2026-03-24T00:00:00","Value":18.2345}]`), "USD", "ZAR")
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	want := adapter.Rate{Date: adapter.Date(2026, 3, 24), Base: "USD", Quote: "ZAR", Rate: 18.2345}
	if rates[0] != want {
		t.Errorf("got %+v, want %+v", rates[0], want)
	}
}

func TestParseRestoresOldKwachaBeforeRebasing(t *testing.T) {
	rates, err := parse([]byte(`[{"Period":"2013-01-02T00:00:00","Value":0.6188},{"Period":"2012-12-31T00:00:00","Value":612.3404}]`), "ZAR", "ZMW")
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 2 || rates[0].Quote != "ZMW" || rates[1].Quote != "ZMK" {
		t.Fatalf("got %+v, want quotes ZMW, ZMK", rates)
	}
	for _, r := range rates {
		if r.Base != "ZAR" {
			t.Errorf("base = %s, want ZAR", r.Base)
		}
	}
}

func TestParseSkipsZeroAndNullValues(t *testing.T) {
	for name, data := range map[string]string{
		"zero": `[{"Period":"2026-03-24T00:00:00","Value":0}]`,
		"null": `[{"Period":"2026-03-24T00:00:00","Value":null}]`,
	} {
		t.Run(name, func(t *testing.T) {
			rates, err := parse([]byte(data), "ZAR", "AUD")
			if err != nil {
				t.Fatal(err)
			}
			if len(rates) != 0 {
				t.Errorf("got %+v, want none", rates)
			}
		})
	}
}

func TestParseStringValues(t *testing.T) {
	rates, err := parse([]byte(`[{"Period":"2026-03-24T00:00:00","Value":" 18.5 "},{"Period":"2026-03-25T00:00:00","Value":"  "},{"Period":"2026-03-26","Value":"0"}]`), "USD", "ZAR")
	if err != nil {
		t.Fatal(err)
	}
	want := adapter.Rate{Date: adapter.Date(2026, 3, 24), Base: "USD", Quote: "ZAR", Rate: 18.5}
	if len(rates) != 1 || rates[0] != want {
		t.Errorf("got %+v, want [%+v]", rates, want)
	}
}

func TestParseRejectsInvalidValues(t *testing.T) {
	for _, v := range []string{`"abc"`, `"NaN"`, `"Infinity"`, `true`} {
		t.Run(v, func(t *testing.T) {
			if _, err := parse([]byte(`[{"Period":"2026-03-24T00:00:00","Value":`+v+`}]`), "ZAR", "AUD"); err == nil {
				t.Error("want error")
			}
		})
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2026, 3, 23), adapter.Date(2026, 3, 27))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
