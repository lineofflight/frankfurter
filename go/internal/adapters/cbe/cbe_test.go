package cbe

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

type xlsxRow struct {
	serial    int
	name      string
	buy, sell float64
}

// buildXLSX writes a minimal export: shared strings are "", the four headers, then each currency name.
func buildXLSX(t *testing.T, rows []xlsxRow) []byte {
	t.Helper()
	strs := []string{"", "Date", "Currency", "Buy", "Sell"}
	for _, r := range rows {
		if !slices.Contains(strs, r.name) {
			strs = append(strs, r.name)
		}
	}

	var sheetRows strings.Builder
	for i, r := range rows {
		n := i + 3
		fmt.Fprintf(&sheetRows,
			`<row r="%[1]d"><c r="A%[1]d" s="4"><v>%[2]d</v></c><c r="B%[1]d" s="5" t="s"><v>%[3]d</v></c>`+
				`<c r="C%[1]d" s="6"><v>%[4]v</v></c><c r="D%[1]d" s="6"><v>%[5]v</v></c></row>`+"\n",
			n, r.serial, slices.Index(strs, r.name), r.buy, r.sell)
	}
	sheet := `<?xml version="1.0" encoding="utf-8"?>
<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
  <sheetData>
    <row r="1"><c r="A1" s="2" t="s"><v>0</v></c></row>
    <row r="2"><c r="A2" s="3" t="s"><v>1</v></c><c r="B2" s="3" t="s"><v>2</v></c><c r="C2" s="3" t="s"><v>3</v></c><c r="D2" s="3" t="s"><v>4</v></c></row>
    ` + sheetRows.String() + `
  </sheetData>
</worksheet>
`
	var items strings.Builder
	for _, s := range strs {
		items.WriteString("<si><t>" + s + "</t></si>")
	}
	sst := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" count="%[1]d" uniqueCount="%[1]d">%[2]s</sst>
`, len(strs), items.String())

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range map[string]string{"xl/worksheets/sheet1.xml": sheet, "xl/sharedStrings.xml": sst} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func mustParse(t *testing.T, rows []xlsxRow) []adapter.Rate {
	t.Helper()
	rates, err := parse(buildXLSX(t, rows))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestParseUSDRowsWithForeignBaseAndEGPQuote(t *testing.T) {
	rates := mustParse(t, []xlsxRow{{46142, "US Dollar", 53.5501, 53.6894}})
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	r := rates[0]
	if r.Base != "USD" || r.Quote != "EGP" {
		t.Errorf("got %s/%s, want USD/EGP", r.Base, r.Quote)
	}
	if !r.Date.Equal(adapter.Date(2026, 4, 30)) {
		t.Errorf("date = %v, want 2026-04-30", r.Date)
	}
	if math.Abs(r.Rate-53.61975) > 0.0001 {
		t.Errorf("rate = %v, want 53.61975", r.Rate)
	}
}

func TestParseCoercesBuyAndSellToMid(t *testing.T) {
	rates := mustParse(t, []xlsxRow{{46142, "Euro", 60.0, 61.0}})
	if math.Abs(rates[0].Rate-60.5) > 0.0001 {
		t.Errorf("rate = %v, want 60.5", rates[0].Rate)
	}
}

func TestParseNormalizesJPYPer100Quotes(t *testing.T) {
	rates := mustParse(t, []xlsxRow{{46142, "Japanese Yen 100", 34.0, 34.2}})
	if rates[0].Base != "JPY" {
		t.Errorf("base = %s, want JPY", rates[0].Base)
	}
	if math.Abs(rates[0].Rate-0.341) > 0.0001 {
		t.Errorf("rate = %v, want 0.341", rates[0].Rate)
	}
}

func TestParseMapsAll18CurrencyNames(t *testing.T) {
	var rows []xlsxRow
	for i, c := range currencies {
		rows = append(rows, xlsxRow{46142 - i, c.name, 10.0, 11.0})
	}
	rates := mustParse(t, rows)

	var bases, quotes []string
	for _, r := range rates {
		bases = append(bases, r.Base)
		if !slices.Contains(quotes, r.Quote) {
			quotes = append(quotes, r.Quote)
		}
	}
	slices.Sort(bases)
	want := []string{"AED", "AUD", "BHD", "CAD", "CHF", "CNY", "DKK", "EUR", "GBP", "JOD", "JPY", "KWD", "NOK",
		"OMR", "QAR", "SAR", "SEK", "USD"}
	if !slices.Equal(bases, want) {
		t.Errorf("bases = %v, want %v", bases, want)
	}
	if !slices.Equal(quotes, []string{"EGP"}) {
		t.Errorf("quotes = %v, want [EGP]", quotes)
	}
}

func TestParseSkipsUnknownCurrencyNames(t *testing.T) {
	if rates := mustParse(t, []xlsxRow{{46142, "Mystery Coin", 1.0, 1.0}}); len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
}

func TestParseConvertsExcelSerialDates(t *testing.T) {
	rates := mustParse(t, []xlsxRow{{45292, "US Dollar", 30.0, 30.5}})
	if !rates[0].Date.Equal(adapter.Date(2024, 1, 1)) {
		t.Errorf("date = %v, want 2024-01-01", rates[0].Date)
	}
}

func fetch(t *testing.T) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "cbe", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 4, 1), adapter.Date(2026, 4, 9))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestFetchDateRange(t *testing.T) {
	rates := fetch(t)
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	for _, r := range rates {
		if r.Quote != "EGP" {
			t.Fatalf("quote = %s, want EGP", r.Quote)
		}
	}
}

func TestFetchUSDInPlausiblePostFloatRange(t *testing.T) {
	rates := fetch(t)
	i := slices.IndexFunc(rates, func(r adapter.Rate) bool {
		return r.Base == "USD" && r.Date.Equal(adapter.Date(2026, 4, 1))
	})
	if i < 0 {
		t.Fatal("no USD rate on 2026-04-01")
	}
	if math.Abs(rates[i].Rate-53.0) > 5.0 {
		t.Errorf("rate = %v, want 53 +/- 5", rates[i].Rate)
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	a := New(g.Client(t))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 4, 1), adapter.Date(2026, 4, 9))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
