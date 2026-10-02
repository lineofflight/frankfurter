package brb

import (
	"context"
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
	a := New(vcrtest.Client(t, "brb", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI)))
	rates, err := a.Fetch(context.Background(), after, upto)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func fetchDay(t *testing.T) []adapter.Rate {
	t.Helper()
	return fetch(t, adapter.Date(2026, 5, 22), adapter.Date(2026, 5, 22))
}

func wantClose(t *testing.T, rates []adapter.Rate, base string, want, delta float64) {
	t.Helper()
	i := slices.IndexFunc(rates, func(r adapter.Rate) bool { return r.Base == base })
	if i < 0 {
		t.Fatalf("no %s rate", base)
	}
	if got := rates[i].Rate; math.Abs(got-want) > delta {
		t.Errorf("%s = %v, want %v ± %v", base, got, want, delta)
	}
}

func TestFetchBIFQuote(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 5, 20), adapter.Date(2026, 5, 22))
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	for _, r := range rates {
		if r.Quote != "BIF" {
			t.Fatalf("quote %s, want BIF", r.Quote)
		}
	}
}

func TestFetchCoversAll19Currencies(t *testing.T) {
	var bases []string
	for _, r := range fetchDay(t) {
		if !slices.Contains(bases, r.Base) {
			bases = append(bases, r.Base)
		}
	}
	if len(bases) != 19 {
		t.Errorf("got %d bases %v, want 19", len(bases), bases)
	}
	for _, want := range []string{"USD", "EUR", "KES"} {
		if !slices.Contains(bases, want) {
			t.Errorf("missing %s", want)
		}
	}
}

func TestFetchMapsDTSToXDR(t *testing.T) {
	wantClose(t, fetchDay(t), "XDR", 4086.83, 1.0)
}

func TestFetchEmitsMidRate(t *testing.T) {
	wantClose(t, fetchDay(t), "USD", 2990.36, 0.01)
}

func TestFetchFiltersByDateRange(t *testing.T) {
	after, upto := adapter.Date(2026, 5, 20), adapter.Date(2026, 5, 22)
	for _, r := range fetch(t, after, upto) {
		if r.Date.Before(after) || r.Date.After(upto) {
			t.Errorf("date %s outside %s..%s", r.Date.Format(time.DateOnly), after.Format(time.DateOnly), upto.Format(time.DateOnly))
		}
	}
}

func TestFetchUSDPlausible(t *testing.T) {
	wantClose(t, fetchDay(t), "USD", 2990.0, 500.0)
}

func TestFetchAsteriskCurrencies(t *testing.T) {
	// KES carries the asterisk; the rate is still authoritative.
	wantClose(t, fetchDay(t), "KES", 23.06, 0.05)
}

// stub replays WebMock's sequenced responses: each URL prefix serves its bodies
// in order, repeating the last.
type stub map[string][]string

func (s stub) RoundTrip(req *http.Request) (*http.Response, error) {
	u := req.URL.String()
	for prefix, bodies := range s {
		if strings.HasPrefix(u, prefix) {
			body := bodies[0]
			if len(bodies) > 1 {
				s[prefix] = bodies[1:]
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{},
				Body:       io.NopCloser(strings.NewReader(body)),
				Request:    req,
			}, nil
		}
	}
	return nil, &unmatched{u}
}

type unmatched struct{ url string }

func (e *unmatched) Error() string { return "unstubbed request " + e.url }

func TestFetchSkipsNonPDFBodies(t *testing.T) {
	// Some archive dates return a zero-byte body. The adapter must treat the
	// date as missing rather than failing on bad input.
	index := `<a href="/sites/default/files/2026-05/Cours%20de%20change%20du%2021-05-2026.pdf">empty</a>` + "\n"
	client := &http.Client{Transport: stub{
		indexURL: {index, ""},
		host + "/sites/default/files/2026-05/Cours%20de%20change%20du%2021-05-2026.pdf": {""},
	}}
	rates, err := New(client).Fetch(context.Background(), adapter.Date(2026, 5, 21), adapter.Date(2026, 5, 21))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
}

func TestGolden(t *testing.T) {
	for _, tc := range []struct {
		file        string
		after, upto time.Time
	}{
		{"fetch.json", adapter.Date(2026, 5, 20), adapter.Date(2026, 5, 22)},
		{"single_day.json", adapter.Date(2026, 5, 22), adapter.Date(2026, 5, 22)},
	} {
		t.Run(tc.file, func(t *testing.T) {
			g := golden.Load(t, "testdata/golden/"+tc.file)
			rates, err := New(g.Client(t)).Fetch(context.Background(), tc.after, tc.upto)
			if err != nil {
				t.Fatal(err)
			}
			g.Check(t, rates)
		})
	}
}

func TestFetchRejectsImpossibleLinkDate(t *testing.T) {
	// Ruby's Date.new raises on 31-02; the port returns an error rather than
	// normalising to March.
	index := `<a href="/sites/default/files/2026-02/Cours%20de%20change%20du%2031-02-2026.pdf">bad</a>`
	client := &http.Client{Transport: stub{indexURL: {index, ""}}}
	if _, err := New(client).Fetch(context.Background(), adapter.Date(2026, 2, 1), adapter.Date(2026, 3, 31)); err == nil {
		t.Fatal("want error for impossible date")
	}
}
