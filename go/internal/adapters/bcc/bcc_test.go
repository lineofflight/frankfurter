package bcc

import (
	"context"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func fetch(t *testing.T) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "bcc", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI), vcrtest.AllowPlaybackRepeats))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 5, 19), adapter.Date(2026, 5, 22))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestFetch(t *testing.T) {
	if len(fetch(t)) == 0 {
		t.Fatal("no rates")
	}
}

func TestFetchCoversMultipleCurrenciesPerDate(t *testing.T) {
	rates := fetch(t)
	first := rates[0].Date
	n := 0
	for _, r := range rates {
		if r.Date.Equal(first) {
			n++
		}
	}
	if n <= 1 {
		t.Errorf("got %d rates on %s, want more than 1", n, first.Format(time.DateOnly))
	}
}

func TestParseTasaEspecialAsHeadlineRate(t *testing.T) {
	rates, err := parse([]byte(`[{"fecha":"2026-05-22","tasaOficial":24,"tasaPublica":120,"tasaEspecial":507}]`), "USD")
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	want := adapter.Rate{Date: adapter.Date(2026, 5, 22), Base: "USD", Quote: "CUP", Rate: 507.0}
	if rates[0] != want {
		t.Errorf("rate = %+v, want %+v", rates[0], want)
	}
}

// One case per Ruby it: "skips entries with nil tasaEspecial", "skips zero
// rates", "handles empty response".
func TestParseSkips(t *testing.T) {
	for name, body := range map[string]string{
		"nil tasaEspecial": `[{"fecha":"2026-05-22","tasaOficial":24,"tasaPublica":120,"tasaEspecial":null}]`,
		"zero rate":        `[{"fecha":"2026-05-22","tasaOficial":24,"tasaPublica":120,"tasaEspecial":0}]`,
		"empty response":   `[]`,
	} {
		t.Run(name, func(t *testing.T) {
			rates, err := parse([]byte(body), "USD")
			if err != nil {
				t.Fatal(err)
			}
			if len(rates) != 0 {
				t.Errorf("got %d rates, want none", len(rates))
			}
		})
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2026, 5, 19), adapter.Date(2026, 5, 22))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}

func TestParseNumericStringRate(t *testing.T) {
	rates, err := parse([]byte(`[{"fecha":"2026-05-22","tasaEspecial":"507.5"}]`), "USD")
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 1 || rates[0].Rate != 507.5 {
		t.Errorf("rates = %+v, want one at 507.5", rates)
	}
}

func TestParseErrors(t *testing.T) {
	for name, body := range map[string]string{
		"object":       `{"error":"bad"}`,
		"null":         `null`,
		"invalid rate": `[{"fecha":"2026-05-22","tasaEspecial":"n/a"}]`,
		"boolean rate": `[{"fecha":"2026-05-22","tasaEspecial":true}]`,
		"bad date":     `[{"fecha":"22/05/2026","tasaEspecial":507}]`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parse([]byte(body), "USD"); err == nil {
				t.Error("want error")
			}
		})
	}
}

func TestFetchEmptyWindow(t *testing.T) {
	a := New(vcrtest.Client(t, "bcc", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 5, 23), adapter.Date(2026, 5, 22))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
}
