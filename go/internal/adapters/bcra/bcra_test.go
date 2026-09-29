package bcra

import (
	"bytes"
	"context"
	"io"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFetchRequestsEachWeekdayInclusive(t *testing.T) {
	var got []string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		got = append(got, r.Method+" "+r.URL.String())
		body := `{"results": {"fecha": null, "detalle": []}}`
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	rates, err := New(client).Fetch(context.Background(), adapter.Date(2026, 3, 13), adapter.Date(2026, 3, 17))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
	want := []string{
		"GET " + baseURL + "?fecha=2026-03-13",
		"GET " + baseURL + "?fecha=2026-03-16",
		"GET " + baseURL + "?fecha=2026-03-17",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("requests =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestFetchRequiresAfter(t *testing.T) {
	if _, err := New(http.DefaultClient).Fetch(context.Background(), time.Time{}, adapter.Date(2026, 3, 17)); err == nil {
		t.Error("want error for zero after")
	}
}

func TestParseRowFiltering(t *testing.T) {
	rates, err := parse([]byte(`{"results": {"fecha": "2026-03-20", "detalle": [
		{"descripcion": "NO CODE", "tipoCotizacion": "1.0"},
		{"codigoMoneda": "ARS", "tipoCotizacion": "1.0"},
		{"codigoMoneda": "EUR", "tipoCotizacion": "0.0000"},
		{"codigoMoneda": " JPY ", "descripcion": "YEN (c/100 unidades)", "tipoCotizacion": 7.5},
		{"codigoMoneda": "XXX", "descripcion": "OCTAL (C/010 UNIDADES)", "tipoCotizacion": "8"}
	]}}`))
	if err != nil {
		t.Fatal(err)
	}
	want := []adapter.Rate{
		{Date: adapter.Date(2026, 3, 20), Base: "JPY", Quote: "ARS", Rate: 0.075},
		{Date: adapter.Date(2026, 3, 20), Base: "XXX", Quote: "ARS", Rate: 1},
	}
	if len(rates) != len(want) {
		t.Fatalf("rates = %+v, want %+v", rates, want)
	}
	for i := range want {
		if rates[i] != want[i] {
			t.Errorf("rates[%d] = %+v, want %+v", i, rates[i], want[i])
		}
	}
}

func TestParseSkipsDateWithDuplicateCodes(t *testing.T) {
	rates, err := parse([]byte(`{"results": {"fecha": "2026-03-20", "detalle": [
		{"codigoMoneda": "USD", "tipoCotizacion": "1075"},
		{"codigoMoneda": "EUR", "tipoCotizacion": "1200"},
		{"codigoMoneda": "USD ", "tipoCotizacion": "1076"}
	]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
}

func TestParseRejectsBadValues(t *testing.T) {
	for name, row := range map[string]string{
		"null rate":        `{"codigoMoneda": "USD"}`,
		"text rate":        `{"codigoMoneda": "USD", "tipoCotizacion": "n/a"}`,
		"bool rate":        `{"codigoMoneda": "USD", "tipoCotizacion": true}`,
		"empty multiplier": `{"codigoMoneda": "USD", "descripcion": "X (C/. UNIDADES)", "tipoCotizacion": "1"}`,
		"bad octal":        `{"codigoMoneda": "USD", "descripcion": "X (C/08 UNIDADES)", "tipoCotizacion": "1"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parse([]byte(`{"results": {"fecha": "2026-03-20", "detalle": [` + row + `]}}`)); err == nil {
				t.Error("want error")
			}
		})
	}
}
