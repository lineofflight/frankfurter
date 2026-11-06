package api

import (
	"context"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/fixtures"
)

// spec/versions/v1_spec.rb, through the full app (the Ruby spec mounts
// Versions::V1 alone, at /).

func TestV1ReturnsLatestQuotes(t *testing.T) {
	a := newTestApp(t)
	a.get("/v1/latest")
	a.status(http.StatusOK)
}

func TestV1SetsBaseCurrency(t *testing.T) {
	a := newTestApp(t)
	a.get("/v1/latest")
	res := a.json()
	a.get("/v1/latest?from=USD")
	if reflect.DeepEqual(a.json(), res) {
		t.Fatal("rebasing changed nothing")
	}
}

func TestV1SetsBaseAmount(t *testing.T) {
	a := newTestApp(t)
	a.get("/v1/latest?amount=10")
	if usd := rateMap(t, a.json()["rates"])["USD"].(float64); !(usd > 10) {
		t.Fatalf("USD = %v", usd)
	}
}

func TestV1FiltersSymbols(t *testing.T) {
	a := newTestApp(t)
	a.get("/v1/latest?to=USD")
	if got := keys(rateMap(t, a.json()["rates"])); !reflect.DeepEqual(got, []string{"USD"}) {
		t.Fatalf("rates = %v", got)
	}
}

func TestV1ReturnsHistoricalQuotes(t *testing.T) {
	a := newTestApp(t)
	a.get("/v1/" + bday(30))
	j := a.json()
	if len(rateMap(t, j["rates"])) == 0 {
		t.Fatal("no rates")
	}
	if j["date"] != bday(30) {
		t.Fatalf("date = %v", j["date"])
	}
}

func TestV1WorksAroundHolidays(t *testing.T) {
	a := newTestApp(t)
	a.get("/v1/" + db.FormatDate(fixtures.RecentSunday()))
	if len(rateMap(t, a.json()["rates"])) == 0 {
		t.Fatal("no rates")
	}
}

func TestV1ReturnsLatestQuotesForFutureDate(t *testing.T) {
	a := newTestApp(t)
	a.get("/v1/" + db.FormatDate(fixtures.Today().AddDate(0, 0, 1)))
	a.status(http.StatusOK)
}

func TestV1ReturnsETag(t *testing.T) {
	a := newTestApp(t)
	for _, path := range []string{"/v1/latest", "/v1/" + bday(30)} {
		a.get(path)
		if a.header("ETag") == "" {
			t.Errorf("%s: no ETag", path)
		}
	}
}

func TestV1ReturnsCacheControl(t *testing.T) {
	a := newTestApp(t)
	for _, path := range []string{"/v1/latest", "/v1/" + bday(30)} {
		a.get(path)
		if a.header("Cache-Control") == "" {
			t.Errorf("%s: no Cache-Control", path)
		}
	}
}

func TestV1ConvertsAmount(t *testing.T) {
	a := newTestApp(t)
	a.get("/v1/latest?from=GBP&to=USD&amount=100")
	if usd := rateMap(t, a.json()["rates"])["USD"].(float64); !(usd > 100) {
		t.Fatalf("USD = %v", usd)
	}
}

func rangeStart() string { return db.FormatDate(fixtures.LatestDate().AddDate(0, 0, -365)) }

func checkPeriod(t *testing.T, a *testApp) {
	t.Helper()
	j := a.json()
	if s, _ := j["start_date"].(string); s == "" {
		t.Error("empty start_date")
	}
	if s, _ := j["end_date"].(string); s == "" {
		t.Error("empty end_date")
	}
	if len(rateMap(t, j["rates"])) == 0 {
		t.Error("no rates")
	}
}

func TestV1ReturnsRatesForPeriod(t *testing.T) {
	a := newTestApp(t)
	a.get("/v1/" + rangeStart() + ".." + latest())
	checkPeriod(t, a)
}

func TestV1ReturnsRatesForOpenPeriod(t *testing.T) {
	a := newTestApp(t)
	a.get("/v1/" + rangeStart() + "..")
	checkPeriod(t, a)
}

func TestV1ReturnsCurrencies(t *testing.T) {
	a := newTestApp(t)
	a.get("/v1/currencies")
	if got := a.json()["USD"]; got != "United States Dollar" {
		t.Fatalf("USD = %v", got)
	}
}

func TestV1ReturnsEmptyCurrenciesWhenNoData(t *testing.T) {
	a := newTestApp(t)
	if _, err := a.db.ExecContext(context.Background(), "DELETE FROM rates"); err != nil {
		t.Fatal(err)
	}
	a.get("/v1/currencies")
	a.status(http.StatusOK)
	if j := a.json(); len(j) != 0 {
		t.Fatalf("currencies = %v", j)
	}
}

func TestV1SetsCharsetUTF8(t *testing.T) {
	a := newTestApp(t)
	a.get("/v1/currencies")
	if got := a.header("Content-Type"); !strings.HasSuffix(got, "charset=utf-8") {
		t.Fatalf("Content-Type = %q", got)
	}
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
