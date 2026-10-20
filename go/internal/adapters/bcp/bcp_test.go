package bcp

import (
	"context"
	"fmt"
	"math"
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
	a := New(vcrtest.Client(t, "bcp", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI)))
	rates, err := a.Fetch(context.Background(), after, upto)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func mustParse(t *testing.T, html string, year int, currency string) []adapter.Rate {
	t.Helper()
	rates, err := parse([]byte(html), year, currency)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

type cell struct {
	day, month int
	value      string
}

// buildYearHTML mirrors the real BCP table: a 31-day x 12-month matrix, cells
// outside the list default to ND.
func buildYearHTML(currency string, year int, cells []cell) string {
	var rows strings.Builder
	for day := 1; day <= 31; day++ {
		fmt.Fprintf(&rows, "<tr><th>%d</th>", day)
		for month := 1; month <= 12; month++ {
			value := "ND"
			for _, c := range cells {
				if c.day == day && c.month == month {
					value = c.value
				}
			}
			fmt.Fprintf(&rows, `<td style="text-align:center;">%s</td>`, value)
		}
		rows.WriteString("</tr>")
	}
	return fmt.Sprintf(`<table id="cotizacion-interbancaria">
  <thead><tr><th colspan="5">PLANILLA DE COTIZACIONES DEL A&Ntilde;O %d DE MONEDA %s</th></tr></thead>
</table>
<table id="cotizacion-interbancaria"><tbody>%s</tbody></table>
`, year, currency, rows.String())
}

func TestFetchWithDateRange(t *testing.T) {
	if len(fetch(t, adapter.Date(2024, 6, 2), adapter.Date(2024, 6, 5))) == 0 {
		t.Fatal("no rates")
	}
}

func TestFetchMultipleCurrenciesPerDate(t *testing.T) {
	rates := fetch(t, adapter.Date(2024, 6, 2), adapter.Date(2024, 6, 5))
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
	if n <= 5 {
		t.Errorf("got %d rates on %s, want more than 5", n, first.Format(time.DateOnly))
	}
}

func TestFetchForeignBasePYGQuote(t *testing.T) {
	rates := fetch(t, adapter.Date(2024, 6, 2), adapter.Date(2024, 6, 5))
	var bases []string
	for _, r := range rates {
		if r.Quote != "PYG" {
			t.Fatalf("quote = %q, want PYG", r.Quote)
		}
		bases = append(bases, r.Base)
	}
	if !slices.Contains(bases, "USD") {
		t.Error("no USD base")
	}
}

func TestFetchUSDPlausibleMid2024(t *testing.T) {
	rates := fetch(t, adapter.Date(2024, 6, 2), adapter.Date(2024, 6, 5))
	i := slices.IndexFunc(rates, func(r adapter.Rate) bool {
		return r.Base == "USD" && r.Date.Equal(adapter.Date(2024, 6, 3))
	})
	if i < 0 {
		t.Fatal("no USD rate on 2024-06-03")
	}
	if got := rates[i].Rate; math.Abs(got-7530.0) > 100.0 {
		t.Errorf("USD rate = %v, want 7530 +/- 100", got)
	}
}

func TestFetchFiltersByDateRange(t *testing.T) {
	after, upto := adapter.Date(2024, 6, 2), adapter.Date(2024, 6, 4)
	for _, r := range fetch(t, after, upto) {
		if !r.Date.After(after) || r.Date.After(upto) {
			t.Errorf("date %s outside (%s, %s]", r.Date.Format(time.DateOnly), after.Format(time.DateOnly), upto.Format(time.DateOnly))
		}
	}
}

func TestParsePtBRDecimals(t *testing.T) {
	rates := mustParse(t, buildYearHTML("USD", 2024, []cell{
		{1, 1, "7.271,63"},
		{2, 1, "ND"},
		{3, 1, "7.281,04"},
	}), 2024, "USD")
	if len(rates) != 2 {
		t.Fatalf("got %d rates, want 2", len(rates))
	}
	i := slices.IndexFunc(rates, func(r adapter.Rate) bool { return r.Date.Equal(adapter.Date(2024, 1, 1)) })
	if i < 0 {
		t.Fatal("no rate on 2024-01-01")
	}
	first := rates[i]
	if first.Base != "USD" || first.Quote != "PYG" {
		t.Errorf("pair = %s/%s, want USD/PYG", first.Base, first.Quote)
	}
	if math.Abs(first.Rate-7271.63) > 0.001 {
		t.Errorf("rate = %v, want 7271.63", first.Rate)
	}
}

func TestParseSkipsNDCells(t *testing.T) {
	rates := mustParse(t, buildYearHTML("EUR", 2024, []cell{
		{1, 2, "ND"},
		{2, 2, "7.264,14"},
	}), 2024, "EUR")
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	if !rates[0].Date.Equal(adapter.Date(2024, 2, 2)) {
		t.Errorf("date = %s, want 2024-02-02", rates[0].Date.Format(time.DateOnly))
	}
	if math.Abs(rates[0].Rate-7264.14) > 0.001 {
		t.Errorf("rate = %v, want 7264.14", rates[0].Rate)
	}
}

func TestParseSkipsInvalidDayMonth(t *testing.T) {
	rates := mustParse(t, buildYearHTML("USD", 2024, []cell{
		{30, 2, "7.500,00"},
		{30, 4, "7.500,25"},
	}), 2024, "USD")
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	if !rates[0].Date.Equal(adapter.Date(2024, 4, 30)) {
		t.Errorf("date = %s, want 2024-04-30", rates[0].Date.Format(time.DateOnly))
	}
}

func TestCurrenciesExcludeXDR(t *testing.T) {
	if slices.Contains(currencies, "XDR") {
		t.Error("currencies include XDR")
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	a := New(g.Client(t))
	rates, err := a.Fetch(context.Background(), adapter.Date(2024, 6, 2), adapter.Date(2024, 6, 5))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
