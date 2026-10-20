package boc

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

func TestFetch(t *testing.T) {
	a := New(vcrtest.Client(t, "boc", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2025, 1, 1), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
}

func TestParseStoresForeignAsBaseAndCADAsQuote(t *testing.T) {
	rates, err := parse([]byte(`{"observations": [{"d": "2026-03-20", "FXUSDCAD": {"v": "1.3728"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	want := adapter.Rate{Date: adapter.Date(2026, 3, 20), Base: "USD", Quote: "CAD", Rate: 1.3728}
	if rates[0] != want {
		t.Errorf("first rate = %+v, want %+v", rates[0], want)
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2025, 1, 1), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFetchRequestParams(t *testing.T) {
	tests := []struct {
		name        string
		after, upto time.Time
		want        url.Values
	}{
		{"after only", adapter.Date(2025, 1, 1), time.Time{}, url.Values{"start_date": {"2025-01-01"}}},
		{"after and upto", adapter.Date(2025, 1, 1), adapter.Date(2025, 1, 31),
			url.Values{"start_date": {"2025-01-01"}, "end_date": {"2025-01-31"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got *url.URL
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				got = r.URL
				return &http.Response{StatusCode: 200, Header: http.Header{},
					Body: io.NopCloser(strings.NewReader(`{"observations": []}`)), Request: r}, nil
			})}
			if _, err := New(client).Fetch(context.Background(), tt.after, tt.upto); err != nil {
				t.Fatal(err)
			}
			if base := got.Scheme + "://" + got.Host + got.Path; base != baseURL {
				t.Errorf("url = %s, want %s", base, baseURL)
			}
			if q := got.Query(); q.Encode() != tt.want.Encode() {
				t.Errorf("query = %v, want %v", q, tt.want)
			}
		})
	}
}

func TestParseSkipsNonFXSeriesAndRejectsBadValues(t *testing.T) {
	rates, err := parse([]byte(`{"observations": [{"d": "2026-03-20", "FXUSDCAD": {"v": "1.3728"}, "OTHER": {"v": "x"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 1 || rates[0].Base != "USD" {
		t.Errorf("rates = %+v, want only USD", rates)
	}

	for _, v := range []string{`{"v": ""}`, `{"v": "n/a"}`, `{}`} {
		if _, err := parse([]byte(`{"observations": [{"d": "2026-03-20", "FXUSDCAD": ` + v + `}]}`)); err == nil {
			t.Errorf("parse with FXUSDCAD %s: want error, as Ruby's Float raises", v)
		}
	}
}
