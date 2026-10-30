package cbe

import (
	"bytes"
	"context"
	"io"
	"math"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

type xlsxRow struct {
	serial    int
	name      string
	buy, sell float64
}

// buildWorkbook writes an export shaped like CBE's: a title row, a header row,
// then the data rows from row 3.
func buildWorkbook(t *testing.T, rows [][]any) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	sh := f.GetSheetName(0)
	all := append([][]any{{"CBE Exchange Rates"}, {"Date", "Currency", "Buy", "Sell"}}, rows...)
	for i, r := range all {
		ref, _ := excelize.CoordinatesToCellName(1, i+1)
		if err := f.SetSheetRow(sh, ref, &r); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	if _, err := f.WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func buildXLSX(t *testing.T, rows []xlsxRow) []byte {
	t.Helper()
	var cells [][]any
	for _, r := range rows {
		cells = append(cells, []any{r.serial, r.name, r.buy, r.sell})
	}
	return buildWorkbook(t, cells)
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

func TestParseSkipsBlankTextAndZeroPrices(t *testing.T) {
	rates, err := parse(buildWorkbook(t, [][]any{
		{46142, "US Dollar", "-", 50},
		{46142, "US Dollar", nil, 50},
		{46142, "US Dollar", 0, 0},
		{46142, "US Dollar", "50", 51}, // numeric text is not a price
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 0 {
		t.Errorf("got %v, want none", rates)
	}
}

func TestParseSkipsRowsWithoutNumericDate(t *testing.T) {
	rates, err := parse(buildWorkbook(t, [][]any{
		{"Total", "US Dollar", 50, 51},
		{"46142", "US Dollar", 50, 51},
		{46142, "US Dollar", 50},
		{46142, "Euro", 60, 61},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 1 || rates[0].Base != "EUR" {
		t.Errorf("got %v, want one EUR rate", rates)
	}
}

func TestParseReadsFirstSheetOnly(t *testing.T) {
	f, err := excelize.OpenReader(bytes.NewReader(buildXLSX(t, []xlsxRow{{46142, "US Dollar", 50, 51}})))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.NewSheet("Other"); err != nil {
		t.Fatal(err)
	}
	if err := f.SetSheetRow("Other", "A1", &[]any{46142, "Euro", 60, 61}); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := f.WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	rates, err := parse(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 1 || rates[0].Base != "USD" {
		t.Errorf("got %v, want one USD rate", rates)
	}
}

func TestParseErrorsWithoutDataRows(t *testing.T) {
	if _, err := parse(buildWorkbook(t, nil)); err == nil {
		t.Error("want an error for an export without data rows")
	}
}

func TestParseErrorsOnNonWorkbook(t *testing.T) {
	if _, err := parse([]byte("<html><body>Request Rejected</body></html>")); err == nil {
		t.Error("want an error for an HTML page instead of a workbook")
	}
}

// fakeSite serves the historical-data page (with a token but no Set-Cookie) and
// the XLSX export.
type fakeSite struct {
	xlsx  []byte
	gets  int
	posts []*http.Request
	forms []string
}

func (f *fakeSite) RoundTrip(req *http.Request) (*http.Response, error) {
	body := `<input name="__RequestVerificationToken" type="hidden" value="tok123" />`
	if req.Method == http.MethodGet {
		f.gets++
	} else {
		b, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		f.posts = append(f.posts, req)
		f.forms = append(f.forms, string(b))
		body = string(f.xlsx)
	}
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
}

func TestFetchPostsFormAndReusesSession(t *testing.T) {
	site := &fakeSite{xlsx: buildXLSX(t, []xlsxRow{{46142, "US Dollar", 50, 51}})}
	a := New(&http.Client{Transport: site})
	for range 2 {
		if _, err := a.Fetch(context.Background(), adapter.Date(2026, 4, 1), adapter.Date(2026, 4, 9)); err != nil {
			t.Fatal(err)
		}
	}
	if site.gets != 1 || len(site.posts) != 2 {
		t.Fatalf("gets = %d, posts = %d, want 1 and 2", site.gets, len(site.posts))
	}
	if got := site.posts[0].URL.String(); got != apiURL {
		t.Errorf("POST to %s, want %s", got, apiURL)
	}
	wantPrefix := "__RequestVerificationToken=tok123&DataSourceId=" + dataSourceID +
		"&FallbackUrl=%2Fen%2Feconomic-research%2Fstatistics%2Fcbe-exchange-rates%2Fhistorical-data&LanguageName=en" +
		"&FromDateRaw=01%2F04%2F2026&ToDateRaw=09%2F04%2F2026&SelectedSelectOptions=US+Dollar&SelectedSelectOptions=Euro&"
	form := site.forms[0]
	if !strings.HasPrefix(form, wantPrefix) || !strings.HasSuffix(form, "&SelectedSelectOptions=Chinese+Yuan&SubmitAction=2") {
		t.Errorf("form = %s", form)
	}
	if n := strings.Count(form, "SelectedSelectOptions="); n != 18 {
		t.Errorf("form selects %d currencies, want 18", n)
	}
	if h := site.posts[0].Header; h.Get("User-Agent") != userAgent || h.Get("Referer") != historicalURL {
		t.Errorf("headers = %v", h)
	}
}

func TestFetchNothingWhenWindowIsEmpty(t *testing.T) {
	site := &fakeSite{}
	a := New(&http.Client{Transport: site})
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 4, 10), adapter.Date(2026, 4, 9))
	if err != nil || rates != nil {
		t.Fatalf("got %v, %v; want nothing", rates, err)
	}
	if site.gets != 0 || len(site.posts) != 0 {
		t.Errorf("made %d requests, want none", site.gets+len(site.posts))
	}
}
