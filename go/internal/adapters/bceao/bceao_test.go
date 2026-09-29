package bceao

import (
	"context"
	"strings"
	"testing"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func newAdapter(t *testing.T) *Adapter {
	return New(vcrtest.Client(t, "bceao", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host), vcrtest.AllowPlaybackRepeats))
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
	html := `<h2>Cours des devises du jeudi 20 mars 2026</h2>
<table><tbody>
<tr><th>Devise</th><th>CFA</th></tr>
<tr><td>Dollar us</td><td>605,5200</td></tr>
</tbody></table>
`
	rates, err := parse(html, adapter.Date(2026, 3, 20))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	if r := rates[0]; r.Base != "USD" || r.Quote != "XOF" || r.Rate != 605.52 {
		t.Errorf("got %+v, want USD/XOF 605.52", r)
	}
}

func TestParseThousandsSeparator(t *testing.T) {
	html := `<h2>Cours des devises du jeudi 20 mars 2026</h2>
<table><tbody>
<tr><th>Devise</th><th>CFA</th></tr>
<tr><td>Dinar Koweitien</td><td>1.953,0200</td></tr>
</tbody></table>
`
	rates, err := parse(html, adapter.Date(2026, 3, 20))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	if r := rates[0]; r.Base != "KWD" || r.Rate != 1953.02 {
		t.Errorf("got %+v, want KWD 1953.02", r)
	}
}

func TestParseWeekendResponse(t *testing.T) {
	rates, err := parse("<h2>Cours des devises du saturday 22 mars 2026</h2>", adapter.Date(2026, 3, 22))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
}

func TestParseRaisesWithoutTableOrHeader(t *testing.T) {
	_, err := parse("<html>Maintenance</html>", adapter.Date(2026, 3, 23))
	if err == nil || !strings.Contains(err.Error(), "neither rates table nor day header") {
		t.Errorf("got %v, want a missing table error", err)
	}
}

// The golden file is recorded with method,uri matching. Under the spec's method,host with repeats, go-vcr replays the
// first match for every day, where Ruby VCR prefers unused interactions; Ruby's output is identical either way.
func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2026, 3, 16), adapter.Date(2026, 3, 20))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}

func TestParseSkipsZeroAndUnknownRows(t *testing.T) {
	html := "<table><tbody>\n" +
		"<tr><td>Euro</td><td>0,0000</td></tr>\n" +
		"<tr><td>Peso mexicain</td><td>32,1000</td></tr>\n" +
		"<tr><td>Euro </td><td>655,9570</td></tr>\n" +
		"<tr><td> Livre sterling\n</td><td>780.12</td></tr>\n" +
		"</tbody></table>"
	rates, err := parse(html, adapter.Date(2026, 3, 20))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 1 || rates[0].Base != "GBP" || rates[0].Rate != 780.12 {
		t.Errorf("got %+v, want only GBP 780.12", rates)
	}
}
