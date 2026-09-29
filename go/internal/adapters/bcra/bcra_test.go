package bcra

import (
	"bytes"
	"context"
	"io"
	"math"
	"net/http"
	"strings"
	"testing"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func newAdapter(t *testing.T) *Adapter {
	return New(vcrClient(t, "bcra"))
}

// vcrClient replays a cassette matched on method and host with repeats allowed, the way Ruby's VCR does: once every
// match has played, VCR repeats the most recently used one, where go-vcr (behind vcrtest) repeats the first. The
// difference decides which day's body the later weekdays get, so the golden parity test depends on it. vcrtest has
// no option for this, so this wraps a no-repeat client and replays the last response itself.
func vcrClient(t *testing.T, cassette string) *http.Client {
	c := vcrtest.Client(t, cassette, vcrtest.MatchOn(vcrtest.Method, vcrtest.Host))
	c.Transport = &lastUsed{next: c.Transport}
	return c
}

type lastUsed struct {
	next   http.RoundTripper
	served []served
}

type served struct {
	method, host string
	resp         *http.Response
	body         []byte
}

func (l *lastUsed) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := l.next.RoundTrip(r)
	if err != nil {
		for i := len(l.served) - 1; i >= 0; i-- {
			s := l.served[i]
			if s.method == r.Method && s.host == r.URL.Host {
				replay := *s.resp
				replay.Body = io.NopCloser(bytes.NewReader(s.body))
				replay.Request = r
				return &replay, nil
			}
		}
		return nil, err
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	l.served = append(l.served, served{r.Method, r.URL.Host, resp, body})
	return resp, nil
}

func TestFetch(t *testing.T) {
	rates, err := newAdapter(t).Fetch(context.Background(), adapter.Date(2026, 3, 16), adapter.Date(2026, 3, 20))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
}

func TestParseStoresForeignCurrencyAsBase(t *testing.T) {
	rates, err := parse([]byte(`{"results": {"fecha": "2026-03-20", "detalle": [
		{"codigoMoneda": "USD", "descripcion": "DOLAR ESTADOUNIDENSE", "tipoPase": 1, "tipoCotizacion": "1075.0000"}
	]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	want := adapter.Rate{Date: adapter.Date(2026, 3, 20), Base: "USD", Quote: "ARS", Rate: 1075.0}
	if rates[0] != want {
		t.Errorf("first rate = %+v, want %+v", rates[0], want)
	}
}

func TestParseNormalizesPer1000Units(t *testing.T) {
	rates, err := parse([]byte(`{"results": {"fecha": "2026-03-20", "detalle": [
		{"codigoMoneda": "VND", "descripcion": "DONG VIETNAM (C/1.000 UNIDADES)", "tipoPase": 0.038036, "tipoCotizacion": "53.04096500"}
	]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	if rates[0].Base != "VND" {
		t.Errorf("base = %q, want VND", rates[0].Base)
	}
	if math.Abs(rates[0].Rate-0.05304) > 0.001 {
		t.Errorf("rate = %v, want about 0.05304", rates[0].Rate)
	}
}

func TestParseSkipsExcludedCodes(t *testing.T) {
	rates, err := parse([]byte(`{"results": {"fecha": "2026-03-20", "detalle": [
		{"codigoMoneda": "REF", "tipoCotizacion": "1.0"},
		{"codigoMoneda": "VEB", "tipoCotizacion": "1.0"},
		{"codigoMoneda": "MXP", "tipoCotizacion": "1.0"},
		{"codigoMoneda": "USD", "tipoCotizacion": "1075.0000"}
	]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	if rates[0].Base != "USD" {
		t.Errorf("base = %q, want USD", rates[0].Base)
	}
}

func TestParseReturnsEmptyForHolidayShape(t *testing.T) {
	rates, err := parse([]byte(`{"results": {"fecha": null, "detalle": []}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name, json, want string
	}{
		{"results envelope missing", `{}`, "results envelope missing"},
		{"detalle missing from the envelope", `{"results": {"fecha": "2026-03-20"}}`, "detalle missing"},
		{"detalle nonempty but fecha null", `{"results": {"fecha": null, "detalle": [{"codigoMoneda": "USD"}]}}`, "undated results"},
		{"fecha key absent entirely", `{"results": {"detalle": []}}`, "undated results"},
		{"fecha false rather than null", `{"results": {"fecha": false, "detalle": []}}`, "undated results"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parse([]byte(tt.json))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	if g.Cassette != "bcra" || !g.AllowPlaybackRepeats || strings.Join(g.MatchRequestsOn, ",") != "method,host" {
		t.Fatalf("golden file recorded with %s %v repeats=%v; vcrClient assumes bcra method,host with repeats",
			g.Cassette, g.MatchRequestsOn, g.AllowPlaybackRepeats)
	}
	rates, err := New(vcrClient(t, g.Cassette)).Fetch(context.Background(), adapter.Date(2026, 3, 16), adapter.Date(2026, 3, 20))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
