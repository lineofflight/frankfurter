package adapter_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

// stub is an adapter whose Fetch records its windows and returns canned rows.
type stub struct {
	adapter.Base
	backfillRange int
	rows          []adapter.Rate
	windows       [][2]time.Time
}

func (s *stub) BackfillRange() int { return s.backfillRange }

func (s *stub) Fetch(_ context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	s.windows = append(s.windows, [2]time.Time{after, upto})
	return s.rows, nil
}

var today = adapter.Date(2026, time.March, 20)

func TestFetchEach(t *testing.T) {
	row := adapter.Rate{Date: adapter.Date(2099, 1, 1), Base: "EUR", Quote: "USD", Rate: 1.1}

	t.Run("yields records from fetch", func(t *testing.T) {
		s := &stub{rows: []adapter.Rate{row}}
		var batches [][]adapter.Rate
		err := adapter.FetchEach(context.Background(), s, time.Time{}, today, func(rows []adapter.Rate) error {
			batches = append(batches, rows)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(batches) != 1 || len(batches[0]) != 1 {
			t.Fatalf("batches = %v, want one batch of one", batches)
		}
	})

	t.Run("skips empty results", func(t *testing.T) {
		s := &stub{}
		called := false
		err := adapter.FetchEach(context.Background(), s, time.Time{}, today, func([]adapter.Rate) error {
			called = true
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if called {
			t.Error("yield called for an empty fetch")
		}
	})

	t.Run("chunks by backfill range", func(t *testing.T) {
		after := today.AddDate(0, 0, -90)
		s := &stub{backfillRange: 30, rows: []adapter.Rate{row}}
		if err := adapter.FetchEach(context.Background(), s, after, today, func([]adapter.Rate) error { return nil }); err != nil {
			t.Fatal(err)
		}
		if len(s.windows) != 4 {
			t.Fatalf("got %d windows, want 4", len(s.windows))
		}
		if !s.windows[0][0].Equal(after) || !s.windows[0][1].Equal(after.AddDate(0, 0, 29)) {
			t.Errorf("first window = %v, want %v..%v", s.windows[0], after, after.AddDate(0, 0, 29))
		}
		if !s.windows[3][1].IsZero() {
			t.Errorf("last window upto = %v, want open", s.windows[3][1])
		}
	})

	t.Run("does nothing when up to date", func(t *testing.T) {
		s := &stub{}
		if err := adapter.FetchEach(context.Background(), s, today, today, nil); err != nil {
			t.Fatal(err)
		}
		if len(s.windows) != 0 {
			t.Errorf("fetched %d windows, want none", len(s.windows))
		}
	})
}

func TestMidpoint(t *testing.T) {
	tests := []struct {
		name      string
		bid, ask  float64
		wantExact float64
	}{
		{"exact mid of two published prices", 181.5264, 181.76, 181.6432},
		{"one decimal beyond its inputs", 1.1, 1.3, 1.2},
		{"small prices", 0.0001234, 0.0001236, 0.0001235},
		{"both sides agree", 3.14, 3.14, 3.14},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := adapter.Midpoint(tt.bid, tt.ask); got != tt.wantExact {
				t.Errorf("Midpoint(%v, %v) = %v, want %v", tt.bid, tt.ask, got, tt.wantExact)
			}
		})
	}
}

func TestPerUnit(t *testing.T) {
	if got := adapter.PerUnit(744.92, 100); got != 7.4492 {
		t.Errorf("PerUnit(744.92, 100) = %v, want 7.4492", got)
	}
	if got := adapter.PerUnit(11.41, 100); got != 0.1141 {
		t.Errorf("PerUnit(11.41, 100) = %v, want 0.1141", got)
	}
}

func TestHistoricalCode(t *testing.T) {
	preds := map[string]adapter.Predecessor{"AZN": {Code: "AZM", Cutover: adapter.Date(2006, 1, 1)}}
	if got := adapter.HistoricalCode(preds, "AZN", adapter.Date(2005, 12, 31)); got != "AZM" {
		t.Errorf("before cutover = %s, want AZM", got)
	}
	if got := adapter.HistoricalCode(preds, "AZN", adapter.Date(2006, 1, 1)); got != "AZN" {
		t.Errorf("on cutover = %s, want AZN", got)
	}
	if got := adapter.HistoricalCode(preds, "USD", adapter.Date(1990, 1, 1)); got != "USD" {
		t.Errorf("unmapped = %s, want USD", got)
	}
}

func TestWindow(t *testing.T) {
	var rows []adapter.Rate
	for d := 1; d <= 5; d++ {
		rows = append(rows, adapter.Rate{Date: adapter.Date(2026, 4, d)})
	}
	got := adapter.Window(rows, adapter.Date(2026, 4, 1), adapter.Date(2026, 4, 3))
	if len(got) != 2 || got[0].Date.Day() != 2 || got[1].Date.Day() != 3 {
		t.Errorf("Window = %v, want April 2 and 3", got)
	}
}

func TestParseFloat(t *testing.T) {
	for in, want := range map[string]float64{"4181.69": 4181.69, " 5.0978\n": 5.0978, "0": 0, "-1.5": -1.5} {
		if got, ok := adapter.ParseFloat(in); !ok || got != want {
			t.Errorf("ParseFloat(%q) = %v, %v", in, got, ok)
		}
	}
	for _, in := range []string{"", " ", "abc", "NaN", "Inf", "1,5"} {
		if _, ok := adapter.ParseFloat(in); ok {
			t.Errorf("ParseFloat(%q) accepted", in)
		}
	}
}

func TestParseDate(t *testing.T) {
	got, err := adapter.ParseDate("26-Aug-25", "2 January 2006", "02-Jan-06")
	if err != nil || !got.Equal(adapter.Date(2025, 8, 26)) {
		t.Errorf("ParseDate = %v, %v", got, err)
	}
	if _, err := adapter.ParseDate("not a date", "2 January 2006"); err == nil {
		t.Error("want an error for an unparseable date")
	}
}

func TestToday(t *testing.T) {
	b := adapter.NewBase(nil)
	b.Now = func() time.Time { return time.Date(2026, 9, 24, 23, 30, 0, 0, time.UTC) }
	if got := b.Today(); !got.Equal(adapter.Date(2026, 9, 24)) {
		t.Errorf("Today = %v", got)
	}
}

func TestHTTPClient(t *testing.T) {
	serve := func(t *testing.T, h http.HandlerFunc) (*adapter.Base, string) {
		t.Helper()
		srv := httptest.NewServer(h)
		t.Cleanup(srv.Close)
		b := adapter.NewBase(srv.Client())
		return &b, srv.URL + "/rates"
	}

	t.Run("returns 2xx bodies", func(t *testing.T) {
		b, u := serve(t, func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "ok") })
		body, err := b.Get(context.Background(), u, nil)
		if err != nil || string(body) != "ok" {
			t.Fatalf("Get = %q, %v", body, err)
		}
	})

	for _, code := range []int{http.StatusForbidden, http.StatusInternalServerError, http.StatusMovedPermanently} {
		t.Run("fails on "+http.StatusText(code), func(t *testing.T) {
			b, u := serve(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Location", "https://example.test/new")
				w.WriteHeader(code)
			})
			_, err := b.Get(context.Background(), u, nil)
			var se *adapter.StatusError
			if !errors.As(err, &se) || se.StatusCode != code {
				t.Fatalf("err = %v, want StatusError %d", err, code)
			}
		})
	}

	t.Run("passes allowed statuses", func(t *testing.T) {
		b, u := serve(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })
		req, _ := b.NewRequest(context.Background(), http.MethodGet, u, nil)
		resp, err := b.Do(req, http.StatusNotFound)
		if err != nil || resp.StatusCode != http.StatusNotFound {
			t.Fatalf("Do = %v, %v", resp, err)
		}
	})

	t.Run("identifies as Frankfurter and accepts anything", func(t *testing.T) {
		var got http.Header
		b, u := serve(t, func(_ http.ResponseWriter, r *http.Request) { got = r.Header })
		if _, err := b.Get(context.Background(), u, nil); err != nil {
			t.Fatal(err)
		}
		if got.Get("User-Agent") != adapter.UserAgent || got.Get("Accept") != "*/*" {
			t.Errorf("headers = %v", got)
		}
	})

	t.Run("merges query parameters", func(t *testing.T) {
		var got url.Values
		b, u := serve(t, func(_ http.ResponseWriter, r *http.Request) { got = r.URL.Query() })
		if _, err := b.Get(context.Background(), u+"?a=1", url.Values{"$limit": {"5"}}); err != nil {
			t.Fatal(err)
		}
		if got.Get("a") != "1" || got.Get("$limit") != "5" {
			t.Errorf("query = %v", got)
		}
	})

	t.Run("retries 429 as Retry-After says", func(t *testing.T) {
		calls := 0
		b, u := serve(t, func(w http.ResponseWriter, r *http.Request) {
			calls++
			body, _ := io.ReadAll(r.Body)
			if calls < 3 {
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			w.Write(body)
		})
		body, err := b.PostForm(context.Background(), u, url.Values{"q": {"1"}})
		if err != nil || string(body) != "q=1" || calls != 3 {
			t.Fatalf("PostForm = %q, %v after %d calls", body, err, calls)
		}
	})

	t.Run("gives up after five attempts", func(t *testing.T) {
		calls := 0
		b, u := serve(t, func(w http.ResponseWriter, _ *http.Request) {
			calls++
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
		})
		if _, err := b.Get(context.Background(), u, nil); err == nil || calls != 5 {
			t.Fatalf("err = %v after %d calls, want failure after 5", err, calls)
		}
	})
}

func TestCookieHeader(t *testing.T) {
	h := http.Header{}
	h.Add("Set-Cookie", "session=abc; Path=/; HttpOnly")
	h.Add("Set-Cookie", "token=xyz; Secure")
	if got := adapter.CookieHeader(h); got != "session=abc; token=xyz" {
		t.Errorf("CookieHeader = %q", got)
	}
}

func TestRegistry(t *testing.T) {
	adapter.Register("TESTONLY", func(c *http.Client) adapter.Adapter { return &stub{Base: adapter.NewBase(c)} })
	c, ok := adapter.Lookup("TESTONLY")
	if !ok || c(nil) == nil {
		t.Fatal("TESTONLY not registered")
	}
	found := false
	for _, k := range adapter.All() {
		found = found || k == "TESTONLY"
	}
	if !found {
		t.Error("All() misses TESTONLY")
	}
	defer func() {
		if recover() == nil {
			t.Error("duplicate Register did not panic")
		}
	}()
	adapter.Register("TESTONLY", nil)
}
