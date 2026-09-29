package cbvs

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
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
	a := New(vcrtest.Client(t, "cbvs", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI)))
	rates, err := a.Fetch(context.Background(), after, upto)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func find(t *testing.T, rates []adapter.Rate, base string) adapter.Rate {
	t.Helper()
	i := slices.IndexFunc(rates, func(r adapter.Rate) bool { return r.Base == base })
	if i < 0 {
		t.Fatalf("no %s rate", base)
	}
	return rates[i]
}

func mustParsePage(t *testing.T, text string) fixing {
	t.Helper()
	f, ok, err := parsePage(text)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("page skipped")
	}
	return f
}

type baseRate struct {
	base string
	rate float64
}

func baseRates(rates []adapter.Rate) []baseRate {
	out := make([]baseRate, len(rates))
	for i, r := range rates {
		out[i] = baseRate{r.Base, r.Rate}
	}
	return out
}

func TestFetchSRDQuote(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 9, 7), adapter.Date(2026, 9, 8))

	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	var dates []time.Time
	for _, r := range rates {
		if r.Quote != "SRD" {
			t.Errorf("quote = %s, want SRD", r.Quote)
		}
		if !slices.ContainsFunc(dates, r.Date.Equal) {
			dates = append(dates, r.Date)
		}
	}
	slices.SortFunc(dates, time.Time.Compare)
	want := []time.Time{adapter.Date(2026, 9, 7), adapter.Date(2026, 9, 8)}
	if !slices.EqualFunc(dates, want, time.Time.Equal) {
		t.Errorf("dates = %v, want %v", dates, want)
	}
}

func TestFetchCoversQuotedCurrencies(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 9, 8), adapter.Date(2026, 9, 8))

	var bases []string
	for _, r := range rates {
		bases = append(bases, r.Base)
	}
	slices.Sort(bases)
	want := []string{"AWG", "BBD", "BRL", "CNY", "EUR", "GBP", "GYD", "TTD", "USD", "XCD", "XCG"}
	if !slices.Equal(bases, want) {
		t.Errorf("bases = %v, want %v", bases, want)
	}
}

func TestFetchTransferMidpointOfClosingFixing(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 9, 8), adapter.Date(2026, 9, 8))

	// 15:00 fixing: buy 37,803, sell 37,923. The 10:00 fixing that day had USD at 37,612 / 37,682.
	if got := find(t, rates, "USD").Rate; got != 37.863 {
		t.Errorf("USD = %v, want 37.863", got)
	}
}

func TestFetchNormalisesGYD(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 9, 8), adapter.Date(2026, 9, 8))

	if got := find(t, rates, "GYD").Rate; got != 0.18139 {
		t.Errorf("GYD = %v, want 0.18139", got)
	}
}

// Ruby stubs PDF::Reader so one page raises MalformedPDFError. pdftext has no per-page failure (it fails the whole
// document), so the equivalent is a page whose text extraction errors.
func TestParseSkipsUnextractablePage(t *testing.T) {
	good := `                                     WISSELKOERSNOTERINGEN IN SRD
             18 JUNI 2025 VASTGESTELD OMSTREEKS 15:00U EN GELDIG TOT NADER ORDER
  U.S. DOLLAR (USD)                                        37,500             37,700         37,300       37,400
`
	pages := []pageText{
		func() (string, error) { return "", errors.New("current font is invalid") },
		func() (string, error) { return good, nil },
	}

	fixings, err := parsePages(pages)
	if err != nil {
		t.Fatal(err)
	}
	var times []string
	for _, f := range fixings {
		times = append(times, f.time)
	}
	if !slices.Equal(times, []string{"15:00"}) {
		t.Errorf("times = %v, want [15:00]", times)
	}
}

func TestParsePageLegacyLayout(t *testing.T) {
	text := `                           C E N T R A L E B A N K V A N S U R I N A M E
                                     WISSELKOERSNOTERINGEN IN SRD
                                 01 SEPTEMBER 2009 EN TOT NADER ORDER
  GELDSOORT                                       AANKOOP*           VERKOOP*      AANKOOP*        VERKOOP*
  U.S. DOLLAR (USD)                                     2,710             2,780         2,710           2,780
  GUYANA DOLLAR (PER 100 GYD)                           1,290             1,370         1,290           1,370
`
	f := mustParsePage(t, text)

	date := adapter.Date(2009, 9, 1)
	if !f.date.Equal(date) {
		t.Errorf("date = %v, want %v", f.date, date)
	}
	if f.time != "" {
		t.Errorf("time = %q, want none", f.time)
	}
	want := []adapter.Rate{
		{Date: date, Base: "USD", Quote: "SRD", Rate: 2.745, Bid: adapter.Float(2.71), Ask: adapter.Float(2.78)},
		{Date: date, Base: "GYD", Quote: "SRD", Rate: 0.0133, Bid: adapter.Float(0.0129), Ask: adapter.Float(0.0137)},
	}
	if !reflect.DeepEqual(f.records, want) {
		t.Errorf("records = %+v, want %+v", f.records, want)
	}
}

func TestParsePageDotDecimal2013(t *testing.T) {
	text := `                                      WISSELKOERSNOTERINGEN IN SRD
                                     02 JANUARI 2013 EN TOT NADER ORDER
  U.S. DOLLAR (USD)                                      3.250              3.350          3.250           3.350
`
	if got := mustParsePage(t, text).records[0].Rate; got != 3.3 {
		t.Errorf("rate = %v, want 3.3", got)
	}
}

func TestParsePageFixingTimeTypo(t *testing.T) {
	text := `                                   WISSELKOERSNOTERINGEN IN SRD
         20 NOVEMBER 2023 VASTGESTELD OMSTREEKS 12.30:00U EN GELDIG TOT NADER ORDER
  U.S. DOLLAR (USD)                                    38,019           38,072        37,369        37,700
`
	f := mustParsePage(t, text)

	if want := adapter.Date(2023, 11, 20); !f.date.Equal(want) {
		t.Errorf("date = %v, want %v", f.date, want)
	}
	if f.time != "12:30" {
		t.Errorf("time = %q, want 12:30", f.time)
	}
}

func TestParsePageCurrentLabels(t *testing.T) {
	text := `                                     WISSELKOERSNOTERINGEN IN SRD
             08 SEPTEMBER 2026 VASTGESTELD OMSTREEKS 15:00U EN GELDIG TOT NADER ORDER
  CARIBISCHE GULDEN (XCG)                                  20,771             21,178         20,517       20,923
  EASTERN CARIBBEAN DOLLAR (XCD                            14,001             14,276         13,830       14,104
  GUYANA DOLLAR (GYD PER 100 )                             17,963             18,315         17,743       18,094
  CHINESE YUAN RENMINBI (PER CNY)                           5,633              5,744          5,564        5,675
`
	f := mustParsePage(t, text)

	if f.time != "15:00" {
		t.Errorf("time = %q, want 15:00", f.time)
	}
	want := []baseRate{{"XCG", 20.9745}, {"XCD", 14.1385}, {"GYD", 0.18139}, {"CNY", 5.6885}}
	if got := baseRates(f.records); !slices.Equal(got, want) {
		t.Errorf("records = %v, want %v", got, want)
	}
}

func TestParsePageKeepsMainTable(t *testing.T) {
	text := `                                WISSELKOERSNOTERINGEN IN SRD
                                 02 MAART 2021 EN TOT NADER ORDER
  U.S. DOLLAR (USD)                               14,018           14,290        14,018        14,290
  EURO (EUR)                                      16,896           17,224        16,890        17,226
  N.B. Bovenstaande koersen zijn de koersnoteringen op basis van de minimumverkoopkoers voor de USD.
  Op basis van de maximumverkoopkoers voor de USD, zijn de USD- en EUR-koersnoteringen als volgt:
  U.S. DOLLAR (USD)                               15,990           16,300        15,990        16,300
  EURO (EUR)                                      19,273           19,646        19,265        19,648
`
	want := []baseRate{{"USD", 14.154}, {"EUR", 17.06}}
	if got := baseRates(mustParsePage(t, text).records); !slices.Equal(got, want) {
		t.Errorf("records = %v, want %v", got, want)
	}
}

func TestParsePageSkips(t *testing.T) {
	tests := []struct {
		name, text string
	}{
		{"English rendition", `                                             EXCHANGE RATES IN SRD
            February 10, 2022 determined around 15:00h and valid until further notice
  U.S. DOLLAR (USD)                                     21.421      21.561       20.645        20.687
`},
		{"sell-only extended overview", `                       UITGEBREID WISSELKOERSENOVERZICHT
             VERKOOPKOERSEN SRD VAN KRACHT M.I.V.:  01-03-2022
  U.S. DOLLAR       (PER USD 1)                 SRD       21,57
`},
		{"gold-certificate page with a date line", `                                WAARDE POWISI GOUDCERTIFICATEN
                              UITSLUITEND VOOR INTERN GEBRUIK CBvS
  CHINESE YUAN RENMINBI (PER CNY)                 0,531            0,547
  DATUM:             02 OKTOBER 2013 EN TOT NADER ORDER
`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, ok, err := parsePage(tt.text)
			if err != nil {
				t.Fatal(err)
			}
			if ok {
				t.Errorf("parsed %+v, want skip", f)
			}
		})
	}
}

func usd(date time.Time, rate float64) []adapter.Rate {
	return []adapter.Rate{{Date: date, Base: "USD", Quote: "SRD", Rate: rate}}
}

func TestClosingKeepsLastFixing(t *testing.T) {
	d := adapter.Date(2026, 9, 7)
	fixings := []fixing{
		{d, "10:00", usd(d, 1.0)},
		{d, "15:00", usd(d, 3.0)},
		{d, "12:30", usd(d, 2.0)},
	}

	selected := closing(fixings, adapter.Date(2026, 9, 1), adapter.Date(2026, 9, 8), adapter.Date(2026, 9, 9))

	if len(selected) != 1 || selected[0].time != "15:00" {
		t.Errorf("selected = %+v, want the 15:00 fixing", selected)
	}
}

func TestClosingLaterPageWinsTie(t *testing.T) {
	d := adapter.Date(2010, 2, 4)
	fixings := []fixing{
		{d, "", usd(d, 1.0)},
		{d, "", usd(d, 2.0)},
	}

	selected := closing(fixings, time.Time{}, adapter.Date(2010, 12, 31), adapter.Date(2026, 9, 9))

	if got := selected[0].records[0].Rate; got != 2.0 {
		t.Errorf("rate = %v, want 2", got)
	}
}

func TestClosingHoldsTodayBack(t *testing.T) {
	today := adapter.Date(2026, 9, 9)
	yesterday := today.AddDate(0, 0, -1)
	fixings := []fixing{
		{yesterday, "12:30", usd(yesterday, 1.0)},
		{today, "10:00", usd(today, 2.0)},
		{today, "12:30", usd(today, 3.0)},
	}
	dates := func(fs []fixing) []time.Time {
		var ds []time.Time
		for _, f := range fs {
			ds = append(ds, f.date)
		}
		return ds
	}

	if got, want := dates(closing(fixings, yesterday, today, today)), []time.Time{yesterday}; !slices.EqualFunc(got, want, time.Time.Equal) {
		t.Errorf("dates = %v, want %v", got, want)
	}

	fixings = append(fixings, fixing{today, "15:00", usd(today, 4.0)})

	if got, want := dates(closing(fixings, yesterday, today, today)), []time.Time{yesterday, today}; !slices.EqualFunc(got, want, time.Time.Equal) {
		t.Errorf("dates = %v, want %v", got, want)
	}
}

func TestClosingFiltersWindow(t *testing.T) {
	var fixings []fixing
	for d := 1; d <= 5; d++ {
		date := adapter.Date(2026, 9, d)
		fixings = append(fixings, fixing{date, "", usd(date, 1.0)})
	}

	selected := closing(fixings, adapter.Date(2026, 9, 2), adapter.Date(2026, 9, 4), adapter.Date(2026, 9, 9))

	var days []int
	for _, f := range selected {
		days = append(days, f.date.Day())
	}
	if !slices.Equal(days, []int{2, 3, 4}) {
		t.Errorf("days = %v, want [2 3 4]", days)
	}
}

func TestCoverageClassifiesLinks(t *testing.T) {
	tests := []struct {
		href       string
		begin, end time.Time
	}{
		{"/Wisselkoersen/2026/DO260908 15.00 uur.pdf", adapter.Date(2026, 9, 8), adapter.Date(2026, 9, 8)},
		{"/Wisselkoersen/2026/DO260814 12.30 uu.pdf", adapter.Date(2026, 8, 14), adapter.Date(2026, 8, 14)},
		{"/Wisselkoersen/2024/Maandoverzichten_2024/WK_FEBRUARI_2024.pdf", adapter.Date(2024, 2, 1), adapter.Date(2024, 2, 29)},
		{"/Wisselkoersen/2025/Maandoverzichten_2025/NL/WisselkoersnoteringMaarti2025.pdf", adapter.Date(2025, 3, 1), adapter.Date(2025, 3, 31)},
		{"/Wisselkoersen/2025/Maandoverzichten_2025/NL/WisselkoersnoteringjJuli2025.pdf", adapter.Date(2025, 7, 1), adapter.Date(2025, 7, 31)},
		{"/Wisselkoersen/2009/jaar-2009sep-dec.pdf", adapter.Date(2009, 1, 1), adapter.Date(2009, 12, 31)},
		{"/Wisselkoersen/2023/Jaar_2023_WK.pdf", adapter.Date(2023, 1, 1), adapter.Date(2023, 12, 31)},
		{"/Wisselkoersen/ALL/Jaar_2015.pdf", adapter.Date(2015, 1, 1), adapter.Date(2015, 12, 31)},
	}
	for _, tt := range tests {
		s, ok, err := coverage(tt.href)
		if err != nil || !ok || !s.begin.Equal(tt.begin) || !s.end.Equal(tt.end) {
			t.Errorf("coverage(%q) = %v..%v (%v), want %v..%v", tt.href, s.begin, s.end, ok, tt.begin, tt.end)
		}
	}
}

func TestCoverageIgnoresOtherPDFs(t *testing.T) {
	if s, ok, err := coverage("/pdf/Richtlijnen/Circulaire_dagelijkse_vaststelling_van_de_wisselkoersen.pdf"); ok || err != nil {
		t.Errorf("coverage = %v, %v, want none", s, err)
	}
}

func TestCoverageRejectsImpossibleDailyDate(t *testing.T) {
	if s, ok, err := coverage("/Wisselkoersen/2026/DO260231 15.00 uur.pdf"); err == nil {
		t.Errorf("coverage = %v (%v), want error", s, ok)
	}
}

func TestParsePageRejectsImpossibleDate(t *testing.T) {
	text := `WISSELKOERSNOTERINGEN IN SRD
31 FEBRUARI 2026 VASTGESTELD OMSTREEKS 15:00U
U.S. DOLLAR (USD)   37,500   37,700   37,300   37,400
`
	if _, _, err := parsePage(text); err == nil {
		t.Error("parsePage succeeded, want error")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDocumentsSelectsAndOrdersLinks(t *testing.T) {
	body := `<a href="/images/Wisselkoersen/2026/DO260908 10.00 uur.pdf">
<a href="/images/Wisselkoersen/2026/DO260908%2015.00%20uur.pdf">
<a href="/images/Wisselkoersen/2026/DO260907 15.00 uur.pdf">
<a href="/images/Wisselkoersen/2026/DO260909 15.00 uur.pdf">
<a href="/images/Wisselkoersen/2026/Maandoverzichten/WK_AUGUSTUS_2026.pdf">
<a href="/images/Wisselkoersen/2017/` + "\t" + `jaar_2017.pdf">
<a href="/images/Wisselkoersen/2026/DO260801 15.00 uur.pdf">
<a href="/pdf/Wisselkoersen/Circulaire.pdf">`
	var got string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		got = r.URL.String()
		return &http.Response{StatusCode: 200, Header: http.Header{},
			Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}

	urls, err := New(client).documents(context.Background(), adapter.Date(2026, 8, 1), adapter.Date(2026, 9, 8))
	if err != nil {
		t.Fatal(err)
	}
	if got != archiveURL {
		t.Errorf("GET %s, want %s", got, archiveURL)
	}
	want := []string{
		host + "/images/Wisselkoersen/2026/Maandoverzichten/WK_AUGUSTUS_2026.pdf",
		host + "/images/Wisselkoersen/2026/DO260801%2015.00%20uur.pdf",
		host + "/images/Wisselkoersen/2026/DO260907%2015.00%20uur.pdf",
		host + "/images/Wisselkoersen/2026/DO260908%2015.00%20uur.pdf",
	}
	if !slices.Equal(urls, want) {
		t.Errorf("urls = %q, want %q", urls, want)
	}
}

func TestDocumentsFailsWithoutLinks(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{},
			Body: io.NopCloser(strings.NewReader("<html></html>")), Request: r}, nil
	})}
	if _, err := New(client).documents(context.Background(), time.Time{}, adapter.Date(2026, 9, 8)); err == nil {
		t.Error("documents succeeded, want error")
	}
}

func TestGolden(t *testing.T) {
	tests := []struct {
		file        string
		after, upto time.Time
	}{
		{"testdata/golden/fetch.json", adapter.Date(2026, 9, 7), adapter.Date(2026, 9, 8)},
		{"testdata/golden/single_day.json", adapter.Date(2026, 9, 8), adapter.Date(2026, 9, 8)},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			g := golden.Load(t, tt.file)
			rates, err := New(g.Client(t)).Fetch(context.Background(), tt.after, tt.upto)
			if err != nil {
				t.Fatal(err)
			}
			g.Check(t, rates)
		})
	}
}
