package api

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/fixtures"
	"github.com/lineofflight/frankfurter/go/internal/heavyslots"
	"github.com/lineofflight/frankfurter/go/internal/ratequery"
)

// spec/versions/v2_spec.rb

func historicalDate() string { return bday(30) }
func v2RangeStart() string   { return bday(60) }
func v2RangeEnd() string     { return bday(30) }
func yearStart() string      { return db.FormatDate(fixtures.LatestDate().AddDate(0, 0, -365)) }
func yearEnd() string        { return latest() }

func TestV2ReturnsLatestRates(t *testing.T) {
	a := newV2App(t)
	a.get("/rates")
	a.conform(200)
	if a.array()[0]["base"] != "EUR" {
		t.Fatal("base is not EUR")
	}
}

func TestV2ReturnsRatesForDate(t *testing.T) {
	a := newV2App(t)
	a.get("/rates?date=" + historicalDate())
	a.conform(200)
	if a.array()[0]["date"] != historicalDate() {
		t.Fatalf("date = %v", a.array()[0]["date"])
	}
}

func TestV2SnapsWeekendsToBusinessDay(t *testing.T) {
	a := newV2App(t)
	sunday := fixtures.RecentSunday()
	a.get("/rates?date=" + db.FormatDate(sunday))
	a.ok()
	if got := a.array()[0]["date"]; got != db.FormatDate(fixtures.PrecedingFriday(sunday)) {
		t.Fatalf("date = %v", got)
	}
}

func TestV2ReturnsRatesForRange(t *testing.T) {
	a := newV2App(t)
	a.get("/rates?from=" + v2RangeStart() + "&to=" + v2RangeEnd())
	a.conform(200)
	if n := len(uniqStrings(field(a.array(), "date"))); n <= 1 {
		t.Fatalf("%d dates", n)
	}
}

func TestV2OrdersRangeRowsWhenCarryForwardSurfacesOlderQuotes(t *testing.T) {
	a := newV2App(t)
	from, to := bday(40), bday(36)
	a.exec("DELETE FROM rates WHERE provider = 'ECB' AND quote = 'USD' AND date >= ? AND date <= ?", from, to)
	a.get("/rates?providers=ecb&quotes=USD,GBP&from=" + from + "&to=" + to)
	a.ok()
	rows := a.array()
	usd := findRow(rows, "quote", "USD")
	if usd == nil || usd["date"].(string) >= from {
		t.Fatalf("USD = %v", usd)
	}
	if dates := field(rows, "date"); !slices.IsSorted(dates) {
		t.Fatalf("dates = %v", dates)
	}
}

func TestV2OrdersWeeklyRollupRows(t *testing.T) {
	a := newV2App(t)
	a.get("/rates?from=" + yearStart() + "&to=" + yearEnd() + "&group=week")
	a.ok()
	if dates := field(a.array(), "date"); !slices.IsSorted(dates) {
		t.Fatalf("dates = %v", dates)
	}
}

type pairKey [3]string

func pairs(rows []map[string]any) []pairKey {
	out := make([]pairKey, len(rows))
	for i, r := range rows {
		out[i] = pairKey{r["date"].(string), r["base"].(string), r["quote"].(string)}
	}
	return out
}

func assertUniquePairs(t *testing.T, p []pairKey) {
	t.Helper()
	seen := map[pairKey]bool{}
	for _, k := range p {
		if seen[k] {
			t.Fatalf("duplicate %v", k)
		}
		seen[k] = true
	}
}

func TestV2DoesNotDuplicateFirstRowOfRange(t *testing.T) {
	a := newV2App(t)
	a.get("/rates?base=EUR&quotes=USD&providers=ecb&from=" + bday(60) + "&to=" + bday(56))
	a.ok()
	assertUniquePairs(t, pairs(a.array()))
}

func TestV2DoesNotDuplicateStartOnGapBoundary(t *testing.T) {
	a := newV2App(t)
	monday := fixtures.GapBoundaryMonday(60)
	a.get("/rates?base=EUR&quotes=USD&providers=ecb&from=" + db.FormatDate(monday) + "&to=" +
		db.FormatDate(monday.AddDate(0, 0, 2)))
	a.ok()
	n := 0
	for _, r := range a.array() {
		if r["date"] == db.FormatDate(monday) && r["quote"] == "USD" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d start rows", n)
	}
}

func TestV2DoesNotDuplicateFirstRowOfNDJSONRange(t *testing.T) {
	a := newV2App(t)
	a.get("/rates?base=EUR&quotes=USD&providers=ecb&from="+bday(60)+"&to="+bday(56), "Accept", "application/x-ndjson")
	a.ok()
	if !strings.Contains(a.header("Content-Type"), "application/x-ndjson") {
		t.Fatalf("content type %q", a.header("Content-Type"))
	}
	assertUniquePairs(t, pairs(ndjsonLines(t, a.body())))
}

func csvRows(t *testing.T, body string) ([]string, []map[string]string) {
	t.Helper()
	records, err := csv.NewReader(strings.NewReader(body)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) == 0 {
		return nil, nil
	}
	var rows []map[string]string
	for _, rec := range records[1:] {
		row := map[string]string{}
		for i, h := range records[0] {
			row[h] = rec[i]
		}
		rows = append(rows, row)
	}
	return records[0], rows
}

func TestV2DoesNotDuplicateFirstRowOfCSVRange(t *testing.T) {
	a := newV2App(t)
	a.get("/rates.csv?base=EUR&quotes=USD&providers=ecb&from=" + bday(60) + "&to=" + bday(56))
	a.ok()
	if !strings.Contains(a.header("Content-Type"), "text/csv") {
		t.Fatalf("content type %q", a.header("Content-Type"))
	}
	_, rows := csvRows(t, a.body())
	var p []pairKey
	for _, r := range rows {
		p = append(p, pairKey{r["date"], r["base"], r["quote"]})
	}
	assertUniquePairs(t, p)
}

func TestV2RebasesToAnotherCurrency(t *testing.T) {
	a := newV2App(t)
	a.get("/rates?base=USD")
	a.conform(200)
	rows := a.array()
	if rows[0]["base"] != "USD" {
		t.Fatal("base is not USD")
	}
	if _, ok := findRow(rows, "quote", "EUR")["rate"].(float64); !ok {
		t.Fatal("EUR rate is not a number")
	}
}

func TestV2DoesNotDuplicateRowsWhenBlending(t *testing.T) {
	a := newV2App(t)
	a.get("/rates?base=CAD")
	a.ok()
	seen := map[[2]string]bool{}
	for _, r := range a.array() {
		k := [2]string{r["date"].(string), r["quote"].(string)}
		if seen[k] {
			t.Fatalf("duplicate %v", k)
		}
		seen[k] = true
	}
}

func sortedQuotes(rows []map[string]any) []string {
	q := uniqStrings(field(rows, "quote"))
	sort.Strings(q)
	return q
}

func TestV2FiltersQuotes(t *testing.T) {
	a := newV2App(t)
	a.get("/rates?quotes=USD,GBP")
	a.conform(200)
	if q := sortedQuotes(a.array()); !slices.Equal(q, []string{"GBP", "USD"}) {
		t.Fatalf("quotes = %v", q)
	}
}

func TestV2IncludesIdentityRateForBase(t *testing.T) {
	a := newV2App(t)
	a.get("/rates")
	a.conform(200)
	rows := a.array()
	eur := findRow(rows, "quote", "EUR")
	if eur == nil || eur["base"] != "EUR" || eur["rate"] != 1.0 || eur["date"] != slices.Max(field(rows, "date")) {
		t.Fatalf("identity = %v", eur)
	}
}

func TestV2IncludesIdentityWhenBaseInQuotes(t *testing.T) {
	a := newV2App(t)
	a.get("/rates?quotes=EUR,USD")
	a.ok()
	rows := a.array()
	if q := sortedQuotes(rows); !slices.Equal(q, []string{"EUR", "USD"}) || findRow(rows, "quote", "EUR")["rate"] != 1.0 {
		t.Fatalf("rows = %v", rows)
	}
}

func TestV2IncludesIdentityPerDateInRanges(t *testing.T) {
	a := newV2App(t)
	a.get("/rates?providers=ecb&quotes=EUR,USD&from=" + bday(60) + "&to=" + bday(56))
	a.ok()
	var usdDates, eurDates []string
	for _, r := range a.array() {
		switch r["quote"] {
		case "USD":
			usdDates = append(usdDates, r["date"].(string))
		case "EUR":
			eurDates = append(eurDates, r["date"].(string))
			if r["rate"] != 1.0 {
				t.Fatalf("identity rate %v", r["rate"])
			}
		}
	}
	if !slices.Equal(eurDates, usdDates) {
		t.Fatalf("EUR %v, USD %v", eurDates, usdDates)
	}
}

func TestV2AnchorsIdentityToVisibleRows(t *testing.T) {
	a := newV2App(t)
	a.exec("DELETE FROM rates WHERE quote = 'GBP' AND date = ?", latest())
	a.get("/rates?quotes=EUR,GBP")
	a.ok()
	rows := a.array()
	gbp, eur := findRow(rows, "quote", "GBP"), findRow(rows, "quote", "EUR")
	if gbp["date"].(string) >= latest() || eur["date"] != gbp["date"] {
		t.Fatalf("GBP %v, EUR %v", gbp, eur)
	}
}

func TestV2IncludesIdentityForPeggedBase(t *testing.T) {
	a := newV2App(t)
	a.get("/rates?base=BMD")
	a.ok()
	if id := findRow(a.array(), "quote", "BMD"); id == nil || id["base"] != "BMD" || id["rate"] != 1.0 {
		t.Fatalf("identity = %v", id)
	}
}

func TestV2OmitsProvidersOnIdentityWhenExpanded(t *testing.T) {
	a := newV2App(t)
	a.get("/rates?expand=providers&quotes=EUR,USD")
	a.ok()
	rows := a.array()
	eur, usd := findRow(rows, "quote", "EUR"), findRow(rows, "quote", "USD")
	if _, ok := eur["providers"]; eur == nil || ok {
		t.Fatalf("EUR = %v", eur)
	}
	if _, ok := usd["providers"].([]any); !ok {
		t.Fatalf("USD = %v", usd)
	}
}

func TestV2ReturnsIdentityForSameCurrencyPair(t *testing.T) {
	a := newV2App(t)
	a.get("/rate/USD/USD")
	a.ok()
	r := a.object()
	if r["base"] != "USD" || r["quote"] != "USD" || r["rate"] != 1.0 || r["date"] == nil {
		t.Fatalf("record = %v", r)
	}
}

func TestV2FiltersByProviders(t *testing.T) {
	a := newV2App(t)
	for _, p := range []string{"ecb", "ecb,boc"} {
		a.get("/rates?providers=" + p)
		a.conform(200)
	}
}

// BOA observes a daily fixing but releases a month of them at once, so its newest row can be weeks old with nothing
// missed. BOE publishes every day, so a row that old means the feed has stalled.
func TestV2CarriesArrearsFixingAcrossTheWait(t *testing.T) {
	a := newV2App(t)
	date := db.FormatDate(fixtures.Today().AddDate(0, 0, -20))
	a.insert("BOA", date, "EUR", "DZD", 145.0)
	a.get("/providers/boa/rates")
	a.ok()
	if row := findRow(a.array(), "quote", "DZD"); row == nil || row["date"] != date {
		t.Fatalf("DZD = %v", row)
	}
}

func TestV2StillDropsDailyPublishersRowAfterTwoWeeks(t *testing.T) {
	a := newV2App(t)
	a.insert("BOE", db.FormatDate(fixtures.Today().AddDate(0, 0, -20)), "EUR", "GBP", 0.86)
	a.get("/providers/boe/rates")
	a.ok()
	if rows := a.array(); len(rows) != 0 {
		t.Fatalf("rows = %v", rows)
	}
}

func TestV2Downsamples(t *testing.T) {
	a := newV2App(t)
	for group, limit := range map[string]int{"week": 55, "month": 13} {
		a.get("/rates?from=" + yearStart() + "&to=" + yearEnd() + "&group=" + group)
		a.ok()
		if n := len(uniqStrings(field(a.array(), "date"))); n > limit {
			t.Errorf("group=%s: %d dates", group, n)
		}
	}
}

func TestV2RejectsInvalidGroupAndConflictingParams(t *testing.T) {
	a := newV2App(t)
	a.get("/rates?from=" + v2RangeStart() + "&to=" + v2RangeEnd() + "&group=day")
	a.status(422)
	a.get("/rates?date=" + historicalDate() + "&from=" + v2RangeStart())
	a.status(422)
}

func TestV2Returns422ForInvalidDates(t *testing.T) {
	a := newV2App(t)
	invalid := []string{
		"04-01-1994", "2026-2-03", "2026-02-3", "20260203", "2026-02", "2026-02-03T12:00:00Z",
		"2026-02-03junk", "2026-02-03\n", "", "not-a-date", "2026-02-30", "2026-02-29",
	}
	for _, param := range []string{"date", "from", "to"} {
		for _, value := range invalid {
			a.getParams("/rates", param, value)
			if a.res.Code != 422 || a.object()["message"] != "invalid date" {
				t.Errorf("%s=%q: %d %s", param, value, a.res.Code, a.body())
			}
		}
	}
}

func TestV2AcceptsValidLeapDay(t *testing.T) {
	a := newV2App(t)
	for _, q := range []string{"date=2000-02-29", "from=2000-02-29&to=2000-02-29"} {
		a.get("/rates?" + q)
		a.ok()
	}
}

func TestV2ReturnsETagForRanges(t *testing.T) {
	a := newV2App(t)
	a.get("/rates?from=" + v2RangeStart() + "&to=" + v2RangeEnd())
	a.ok()
	if a.header("ETag") == "" {
		t.Fatal("no ETag")
	}
}

func TestV2LetsCachesServeStale(t *testing.T) {
	a := newV2App(t)
	a.get("/rates?from=" + v2RangeStart() + "&to=" + v2RangeEnd())
	cc := a.header("Cache-Control")
	for _, s := range []string{"public", "max-age=86400", "stale-while-revalidate", "stale-if-error"} {
		if !strings.Contains(cc, s) {
			t.Errorf("Cache-Control %q lacks %s", cc, s)
		}
	}
}

func secondsToUTCMidnight() int {
	now := time.Now().UTC()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).Add(24 * time.Hour)
	return int(math.Ceil(midnight.Sub(now).Seconds()))
}

func TestV2ExpiresDateRelativeResponsesAtUTCMidnight(t *testing.T) {
	a := newV2App(t)
	for _, path := range []string{"/rates", "/rates?from=" + v2RangeStart(), "/rates?to=" + v2RangeEnd()} {
		before := secondsToUTCMidnight()
		a.get(path)
		after := secondsToUTCMidnight()
		cc := a.header("Cache-Control")
		if !strings.Contains(cc, "public") || !strings.Contains(cc, "stale-if-error") ||
			strings.Contains(cc, "stale-while-revalidate") {
			t.Errorf("%s: Cache-Control %q", path, cc)
		}
		m := regexp.MustCompile(`max-age=(\d+)`).FindStringSubmatch(cc)
		maxAge, _ := strconv.Atoi(m[1])
		if maxAge > before || maxAge < after {
			t.Errorf("%s: max-age %d outside [%d, %d]", path, maxAge, after, before)
		}
	}
}

func TestV2KeepsFixedMaxAgeForExplicitDates(t *testing.T) {
	a := newV2App(t)
	a.get("/rates?date=" + historicalDate())
	if !strings.Contains(a.header("Cache-Control"), "max-age=86400") {
		t.Fatalf("Cache-Control %q", a.header("Cache-Control"))
	}
}

func TestV2Returns422ForUnknownParameters(t *testing.T) {
	a := newV2App(t)
	a.get("/rates?provider=ecb")
	a.status(422)
	if !strings.Contains(a.object()["message"].(string), "unknown parameter") {
		t.Fatalf("message %v", a.object()["message"])
	}
}

func TestV2Returns422ForLongExpandedDailyRanges(t *testing.T) {
	a := newV2App(t)
	a.get("/rates?from=2000-01-01&expand=providers")
	a.conform(422)
	msg := a.object()["message"].(string)
	for _, s := range []string{"quotes=", "group=week or group=month", "split the range"} {
		if !strings.Contains(msg, s) {
			t.Errorf("message %q lacks %q", msg, s)
		}
	}
}

func TestV2ServesLongSingleProviderRanges(t *testing.T) {
	a := newV2App(t)
	a.get("/rates?from=2000-01-01&providers=ecb")
	a.ok()
	if len(a.array()) == 0 {
		t.Fatal("empty")
	}
}

func TestV2KeepsCapForLongMultiProviderRanges(t *testing.T) {
	a := newV2App(t)
	a.get("/rates?from=2000-01-01&providers=ecb,boc")
	a.status(422)
	if !strings.Contains(a.object()["message"].(string), "quotes=") {
		t.Fatal(a.body())
	}
}

func TestV2ServesLongPlainRangesOnceTableReady(t *testing.T) {
	a := newV2App(t)
	a.rebuildDaily()
	a.get("/rates?from=2000-01-01")
	a.ok()
	if len(a.array()) == 0 {
		t.Fatal("empty")
	}
}

// fakeQuery stands in for a stubbed RateQuery.new.
type fakeQuery struct {
	each func(func(ratequery.Record) error) error
}

func (fakeQuery) Range() bool                              { return true }
func (fakeQuery) DateRelative() bool                       { return false }
func (fakeQuery) ExpandProviders() bool                    { return false }
func (fakeQuery) CacheKey(context.Context) (string, error) { return "x", nil }
func (fakeQuery) CSVFilename() string                      { return "x.csv" }
func (fakeQuery) ReleaseSlot()                             {}
func (f fakeQuery) Each(_ context.Context, yield func(ratequery.Record) error) error {
	return f.each(yield)
}

func stubRateQuery(t *testing.T, q rateQuery) {
	orig := newRateQuery
	newRateQuery = func(context.Context, *sql.DB, ratequery.Params, ratequery.Options) (rateQuery, error) {
		return q, nil
	}
	t.Cleanup(func() { newRateQuery = orig })
}

func TestV2Returns503WhenDeadlineExpiresBeforeStreaming(t *testing.T) {
	a := newV2App(t)
	stubRateQuery(t, fakeQuery{each: func(func(ratequery.Record) error) error {
		return &ratequery.DeadlineError{Timeout: 90 * time.Second}
	}})
	a.get("/rates?from=" + v2RangeStart() + "&to=" + v2RangeEnd())
	a.conform(503)
	if !strings.Contains(a.object()["message"].(string), "timeout") {
		t.Fatal(a.body())
	}
}

func heavyPath() string { return "/rates?providers=ecb&from=" + v2RangeStart() + "&to=" + v2RangeEnd() }

func TestV2Returns503WithRetryAfterWhenSlotsHeld(t *testing.T) {
	a := newV2App(t)
	a.s.HeavySlots = heavyslots.New(1)
	a.s.HeavySlots.TryAcquire()
	a.get(heavyPath())
	a.conform(503)
	if a.header("Retry-After") != "30" || !strings.Contains(a.header("Content-Type"), "application/json") {
		t.Fatalf("headers %v", a.res.Header())
	}
	body := a.object()
	if body["status"] != 503.0 || !strings.Contains(body["message"].(string), "retry") {
		t.Fatalf("body %v", body)
	}
}

func TestV2ServesRangeAndReturnsSlotWhenDrained(t *testing.T) {
	a := newV2App(t)
	a.s.HeavySlots = heavyslots.New(1)
	a.get(heavyPath())
	a.ok()
	if len(a.array()) == 0 || a.s.HeavySlots.Held() != 0 {
		t.Fatalf("held = %d", a.s.HeavySlots.Held())
	}
}

// goneWriter is a client that disconnects after the first chunk it receives.
type goneWriter struct {
	*httptest.ResponseRecorder
	writes    int
	heldAtCut int
	slots     *heavyslots.Slots
}

func (w *goneWriter) Write(b []byte) (int, error) {
	w.writes++
	if w.writes > 1 {
		return 0, errors.New("broken pipe")
	}
	w.heldAtCut = w.slots.Held()
	return w.ResponseRecorder.Write(b)
}

// A client that disconnects mid-stream stops the compute, and the slot comes back.
func TestV2ReturnsSlotWhenClientDisconnectsMidStream(t *testing.T) {
	a := newV2App(t)
	slots := heavyslots.New(1)
	a.s.HeavySlots = slots
	w := &goneWriter{ResponseRecorder: httptest.NewRecorder(), slots: slots}
	func() {
		defer func() {
			if r := recover(); r != nil && r != http.ErrAbortHandler {
				panic(r)
			}
		}()
		a.h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v2"+heavyPath(), nil))
	}()
	if w.heldAtCut != 1 {
		t.Fatalf("held while streaming = %d", w.heldAtCut)
	}
	if slots.Held() != 0 {
		t.Fatalf("held after disconnect = %d", slots.Held())
	}
}

func TestV2DoesNotTouchSlotsForTableServedRanges(t *testing.T) {
	a := newV2App(t)
	a.rebuildDaily()
	a.s.HeavySlots = heavyslots.New(1)
	a.s.HeavySlots.TryAcquire()
	a.get("/rates?from=" + v2RangeStart() + "&to=" + v2RangeEnd())
	a.ok()
	if len(a.array()) == 0 {
		t.Fatal("empty")
	}
}

func TestV2RoutesDeterministicRangeErrorsThroughErrorHandler(t *testing.T) {
	a := newV2App(t)
	stubRateQuery(t, fakeQuery{each: func(func(ratequery.Record) error) error { return errors.New("boom") }})
	a.get("/rates?from=" + v2RangeStart() + "&to=" + v2RangeEnd())
	a.status(500)
}

func TestV2ReturnsRatesAsCSV(t *testing.T) {
	a := newV2App(t)
	a.get("/rates.csv")
	a.ok()
	if !strings.Contains(a.header("Content-Type"), "text/csv") {
		t.Fatalf("content type %q", a.header("Content-Type"))
	}
	headers, rows := csvRows(t, a.body())
	if !slices.Equal(headers, []string{"date", "base", "quote", "rate"}) || len(rows) <= 1 {
		t.Fatalf("headers %v, %d rows", headers, len(rows))
	}
}

func TestV2ExpandsProviders(t *testing.T) {
	a := newV2App(t)
	a.get("/rates?expand=providers&quotes=USD")
	a.ok()
	rows := a.array()
	providers, ok := rows[0]["providers"].([]any)
	if !ok || len(providers) == 0 {
		t.Fatalf("providers = %v", rows[0]["providers"])
	}
	p := providers[0].(map[string]any)
	var keys []string
	for k := range p {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	_, isString := p["key"].(string)
	_, isDate := p["date"].(string)
	_, isNumber := p["rate"].(float64)
	if !slices.Equal(keys, []string{"date", "key", "rate"}) || !isString || !isDate || !isNumber {
		t.Fatalf("provider = %v", p)
	}
}

func TestV2PipeDelimitsProvidersInCSV(t *testing.T) {
	a := newV2App(t)
	from := db.FormatDate(fixtures.LatestDate().AddDate(0, 0, -7))
	a.get("/rates.csv?expand=providers&quotes=USD&from=" + from + "&to=" + latest())
	a.ok()
	headers, rows := csvRows(t, a.body())
	if !slices.Equal(headers, []string{"date", "base", "quote", "rate", "providers"}) {
		t.Fatalf("headers %v", headers)
	}
	if !regexp.MustCompile(`^[A-Z]+:[\d.]+\*?(\|[A-Z]+:[\d.]+\*?)*$`).MatchString(rows[0]["providers"]) {
		t.Fatalf("providers %q", rows[0]["providers"])
	}
}

func TestV2RejectsUnknownExpand(t *testing.T) {
	a := newV2App(t)
	a.get("/rates?expand=foo")
	a.status(422)
}

func TestV2ServesCSVAsNamedAttachment(t *testing.T) {
	a := newV2App(t)
	a.get("/rates.csv?quotes=USD&date=" + historicalDate())
	if want := `attachment; filename="EUR-USD_` + historicalDate() + `.csv"`; a.header("Content-Disposition") != want {
		t.Fatalf("Content-Disposition %q", a.header("Content-Disposition"))
	}
	a.get("/rates.csv?from=" + v2RangeStart() + "&to=" + v2RangeEnd())
	if want := `attachment; filename="EUR-rates_` + v2RangeStart() + "_" + v2RangeEnd() + `.csv"`; a.header("Content-Disposition") != want {
		t.Fatalf("Content-Disposition %q", a.header("Content-Disposition"))
	}
	a.get("/rates")
	if a.header("Content-Disposition") != "" {
		t.Fatal("JSON attached")
	}
}

func TestV2Returns406ForCSVOnUnsupportedEndpoints(t *testing.T) {
	a := newV2App(t)
	a.get("/currencies.csv")
	a.status(406)
}

func TestV2ReturnsEmptyArrayBeforeDataset(t *testing.T) {
	a := newV2App(t)
	a.get("/rates?date=1901-01-01")
	a.ok()
	if len(a.array()) != 0 {
		t.Fatal(a.body())
	}
}

func TestV2ReturnsCurrencies(t *testing.T) {
	a := newV2App(t)
	a.get("/currencies?scope=all")
	a.conform(200)
	usd := findRow(a.array(), "iso_code", "USD")
	if usd["name"] != "United States Dollar" || usd["symbol"] != "$" || usd["iso_numeric"] != "840" {
		t.Fatalf("USD = %v", usd)
	}
}

func TestV2ReturnsCOMESADollarAsNamedAccountingUnit(t *testing.T) {
	a := newV2App(t)
	a.insert("RBM", latest(), "CMD", "MWK", 100.0)
	a.insert("RBM", latest(), "USD", "MWK", 90.0)
	a.refreshSummaries("RBM", "CMD", "MWK")
	a.get("/currency/cmd")
	a.conform(200)
	c := a.object()
	peg, _ := c["peg"].(map[string]any)
	if c["iso_code"] != "CMD" || c["name"] != "COMESA Dollar" || c["iso_numeric"] != nil ||
		!slices.Equal(anyStrings(c["providers"]), []string{"RBM"}) || peg["base"] != "USD" || peg["rate"] != 1.0 ||
		peg["authority"] != "Common Market for Eastern and Southern Africa" {
		t.Fatalf("currency = %v", c)
	}
	if _, ok := c["iso_numeric"]; !ok {
		t.Fatal("iso_numeric missing")
	}
}

func anyStrings(v any) []string {
	list, _ := v.([]any)
	out := []string{}
	for _, x := range list {
		s, _ := x.(string)
		out = append(out, s)
	}
	return out
}

func TestV2RejectsUnsupportedCurrencyScopes(t *testing.T) {
	a := newV2App(t)
	for _, scope := range []string{"invalid", "", "ALL", "active"} {
		for _, extra := range [][]string{nil, {"providers", "ecb"}} {
			a.getParams("/currencies", append([]string{"scope", scope}, extra...)...)
			if a.res.Code != 422 || a.body() != `{"status":422,"message":"invalid scope"}` {
				t.Errorf("scope=%q %v: %d %s", scope, extra, a.res.Code, a.body())
			}
			a.conform(422)
		}
	}
}

func TestV2ReturnsActiveCurrenciesWhenScopeOmitted(t *testing.T) {
	a := newV2App(t)
	a.exec("INSERT INTO currencies (iso_code, start_date, end_date) VALUES ('DEM', '1999-01-04', '2001-12-31')")
	a.get("/currencies")
	a.conform(200)
	codes := field(a.array(), "iso_code")
	if !has(codes, "USD") || has(codes, "DEM") {
		t.Fatalf("codes = %v", codes)
	}
	a.get("/currencies?scope=all")
	a.conform(200)
	if !has(field(a.array(), "iso_code"), "DEM") {
		t.Fatal("DEM missing with scope=all")
	}
}

func TestV2ReturnsSingleCurrency(t *testing.T) {
	a := newV2App(t)
	a.get("/currency/usd")
	a.ok()
	c := a.object()
	if c["iso_code"] != "USD" || c["name"] != "United States Dollar" || !has(anyStrings(c["providers"]), "ECB") {
		t.Fatalf("currency = %v", c)
	}
	a.get("/currency/xyz")
	a.status(404)
}

func TestV2FiltersCurrenciesByProvider(t *testing.T) {
	a := newV2App(t)
	a.get("/currencies?providers=ecb")
	a.ok()
	codes := field(a.array(), "iso_code")
	if !has(codes, "USD") || !has(codes, "EUR") || has(codes, "BMD") {
		t.Fatalf("codes = %v", codes)
	}
}

func TestV2ListsMonthlyProvidersCoverageWithoutChangingGlobalCatalogue(t *testing.T) {
	a := newV2App(t)
	a.insert("INFOREURO", "1994-03-01", "XEU", "ADP", 160.0)
	a.insert("INFOREURO", "1998-01-01", "XEU", "ADP", 165.092)
	a.insert("INFOREURO", "1999-01-01", "EUR", "XYZ", 2.0)
	a.refreshSummaries("INFOREURO", "XEU", "ADP", "XYZ")
	a.get("/currencies?scope=all")
	globalBefore := a.body()

	a.get("/currencies?providers=inforeuro")
	a.conform(200)
	rows := a.array()
	codes := field(rows, "iso_code")
	sort.Strings(codes)
	if !slices.Equal(codes, []string{"ADP", "XEU"}) {
		t.Fatalf("codes = %v", codes)
	}
	adp := findRow(rows, "iso_code", "ADP")
	if adp["start_date"] != "1994-03-01" || adp["end_date"] != "1998-01-01" {
		t.Fatalf("ADP = %v", adp)
	}
	providerBody := a.body()
	a.get("/currencies?providers=inforeuro&scope=all")
	if a.body() != providerBody {
		t.Fatal("scope=all changed the provider listing")
	}
	a.get("/currencies?scope=all")
	if a.body() != globalBefore || has(field(a.array(), "iso_code"), "ADP") {
		t.Fatal("the global catalogue changed")
	}
}

func TestV2PreservesProviderFilteringWithScopeAll(t *testing.T) {
	a := newV2App(t)
	a.get("/currencies?providers=ecb")
	expected := a.body()
	a.get("/currencies?providers=ecb&scope=all")
	a.conform(200)
	if a.body() != expected {
		t.Fatal("bodies differ")
	}
}

func TestV2IncludesBaseCurrenciesInList(t *testing.T) {
	a := newV2App(t)
	a.get("/currencies?scope=all")
	if findRow(a.array(), "iso_code", "EUR") == nil {
		t.Fatal("no EUR")
	}
}

func TestV2ReturnsSingleRatePair(t *testing.T) {
	a := newV2App(t)
	for _, c := range []struct{ path, date string }{
		{"/rate/EUR/USD", ""},
		{"/rate/EUR/USD?date=" + historicalDate(), historicalDate()},
		{"/rate/eur/usd", ""},
		{"/rate/EUR/USD?providers=ECB", ""},
	} {
		a.get(c.path)
		a.ok()
		r := a.object()
		if _, isNumber := r["rate"].(float64); r["base"] != "EUR" || r["quote"] != "USD" || !isNumber || r["date"] == nil {
			t.Errorf("%s: %v", c.path, r)
		}
		if c.date != "" && r["date"] != c.date {
			t.Errorf("%s: date %v", c.path, r["date"])
		}
	}
	a.get("/rate/EUR/XYZ")
	a.status(422)
}

func TestV2SetsVaryOnNDJSON(t *testing.T) {
	a := newV2App(t)
	a.get("/rates", "Accept", "application/x-ndjson")
	a.ok()
	if a.header("Vary") != "Accept" {
		t.Fatalf("Vary %q", a.header("Vary"))
	}
}

func TestV2PrefersCSVExtensionOverNDJSONAccept(t *testing.T) {
	a := newV2App(t)
	a.get("/rates.csv", "Accept", "application/x-ndjson")
	a.ok()
	if !strings.Contains(a.header("Content-Type"), "text/csv") {
		t.Fatalf("content type %q", a.header("Content-Type"))
	}
}

func TestV2ReturnsNDJSON(t *testing.T) {
	a := newV2App(t)
	a.get("/rates?from="+v2RangeStart()+"&to="+v2RangeEnd(), "Accept", "application/x-ndjson")
	a.ok()
	if !strings.Contains(a.header("Content-Type"), "application/x-ndjson") {
		t.Fatalf("content type %q", a.header("Content-Type"))
	}
	lines := ndjsonLines(t, a.body())
	if len(lines) <= 1 || lines[0]["date"] == nil || lines[0]["rate"] == nil {
		t.Fatalf("lines = %v", lines)
	}
	a.get("/rates", "Accept", "application/x-ndjson")
	a.ok()
	if lines := ndjsonLines(t, a.body()); len(lines) == 0 || lines[0]["date"] == nil {
		t.Fatalf("single-date lines = %v", lines)
	}
}

func TestV2StreamsValidJSONArrayForRanges(t *testing.T) {
	a := newV2App(t)
	a.get("/rates?from=" + v2RangeStart() + "&to=" + v2RangeEnd())
	a.ok()
	if !strings.Contains(a.header("Content-Type"), "application/json") || a.array()[0]["date"] == nil {
		t.Fatal(a.body())
	}
}

func TestV2ReturnsRatesAlphabetically(t *testing.T) {
	a := newV2App(t)
	a.get("/rates")
	a.ok()
	if q := field(a.array(), "quote"); !slices.IsSorted(q) {
		t.Fatalf("quotes = %v", q)
	}
}

func TestV2KeepsLatestAlphabeticalWhenCarryForwardSurfacesOlderQuote(t *testing.T) {
	a := newV2App(t)
	a.exec("DELETE FROM rates WHERE quote = 'SEK' AND date = ?", latest())
	a.get("/rates")
	a.ok()
	rows := a.array()
	sek := findRow(rows, "quote", "SEK")
	if sek == nil || sek["date"].(string) >= latest() || !slices.IsSorted(field(rows, "quote")) {
		t.Fatalf("SEK = %v", sek)
	}
}

func TestV2ExcludesPeggedCurrenciesWithProvidersFilter(t *testing.T) {
	a := newV2App(t)
	a.get("/rates?providers=ecb")
	a.ok()
	q := field(a.array(), "quote")
	for _, code := range []string{"BMD", "FKP", "BTN"} {
		if has(q, code) {
			t.Errorf("%s listed", code)
		}
	}
}

func TestV2FiltersPeggedCurrenciesByQuotes(t *testing.T) {
	a := newV2App(t)
	a.get("/rates?quotes=USD,BMD")
	a.ok()
	if q := sortedQuotes(a.array()); !slices.Equal(q, []string{"BMD", "USD"}) {
		t.Fatalf("quotes = %v", q)
	}
}

func TestV2ReturnsProviders(t *testing.T) {
	a := newV2App(t)
	a.get("/providers")
	a.conform(200)
	rows := a.array()
	ecb := findRow(rows, "key", "ECB")
	if ecb["name"] != "European Central Bank" || ecb["start_date"] == nil || ecb["end_date"] == nil ||
		!has(anyStrings(ecb["currencies"]), "USD") {
		t.Fatalf("ECB = %v", ecb)
	}
	if ecb["rate_type"] != "reference rate" || ecb["pivot_currency"] != "EUR" || ecb["country_code"] != "EU" {
		t.Fatalf("ECB = %v", ecb)
	}
	if _, ok := ecb["description"]; ok {
		t.Fatal("description listed")
	}
	missed, ok := ecb["publishes_missed"].(float64)
	if !ok || missed < 0 || missed != math.Trunc(missed) {
		t.Fatalf("publishes_missed = %v", ecb["publishes_missed"])
	}
	if ecb["publish_cadence"] != "daily" {
		t.Fatalf("publish_cadence = %v", ecb["publish_cadence"])
	}
	if f := uniqStrings(field(rows, "frequency")); !slices.Equal(f, []string{"daily"}) {
		t.Fatalf("frequencies = %v", f)
	}
}

func TestV2ExcludesProvidersWithoutRates(t *testing.T) {
	a := newV2App(t)
	a.get("/providers")
	keys := field(a.array(), "key")
	rows, err := a.db.Query("SELECT key FROM providers WHERE key NOT IN (SELECT DISTINCT provider FROM rates)")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var k string
		rows.Scan(&k)
		n++
		if has(keys, k) {
			t.Errorf("%s listed without rates", k)
		}
	}
	if n == 0 {
		t.Fatal("every provider has rates")
	}
}

func TestV2ExpandsPeggedCurrenciesInRates(t *testing.T) {
	a := newV2App(t)
	a.get("/rates?base=EUR")
	a.ok()
	rows := a.array()
	q := field(rows, "quote")
	for _, code := range []string{"BMD", "FKP", "GGP", "BTN"} {
		if !has(q, code) {
			t.Errorf("%s missing", code)
		}
	}
	usd := findRow(rows, "quote", "USD")["rate"].(float64)
	if bmd := findRow(rows, "quote", "BMD")["rate"].(float64); bmd != usd {
		t.Errorf("BMD %v, USD %v", bmd, usd)
	}
	if ang := findRow(rows, "quote", "ANG")["rate"].(float64); math.Abs(ang-usd*1.79) > 0.01 {
		t.Errorf("ANG %v, USD %v", ang, usd)
	}
}

func TestV2ResolvesPeggedBase(t *testing.T) {
	a := newV2App(t)
	a.get("/rates?base=BMD")
	a.ok()
	rows := a.array()
	if len(rows) == 0 || rows[0]["base"] != "BMD" {
		t.Fatalf("rows = %v", rows)
	}
	if _, ok := findRow(rows, "quote", "EUR")["rate"].(float64); !ok {
		t.Fatal("no EUR rate")
	}
}

func TestV2ReturnsAnchorAsQuoteForPeggedBase(t *testing.T) {
	a := newV2App(t)
	a.get("/rate/GGP/GBP")
	a.ok()
	if r := a.object(); r["base"] != "GGP" || r["quote"] != "GBP" || r["rate"] != 1.0 {
		t.Fatalf("record = %v", r)
	}
	a.get("/rate/ANG/USD")
	a.ok()
	if r := a.object(); r["base"] != "ANG" || r["quote"] != "USD" || math.Abs(r["rate"].(float64)-1.0/1.79) > 0.001 {
		t.Fatalf("record = %v", r)
	}
}

// BMD pegs 1:1 to USD; ECB does not publish BMD. Pegs are a source of rate data, so scoping ?providers= to ECB
// excludes pegs along with all other unlisted sources.
func TestV2ExcludesPegsWithProvidersFilter(t *testing.T) {
	a := newV2App(t)
	a.get("/rates?base=BMD&providers=ecb")
	a.ok()
	if len(a.array()) != 0 {
		t.Fatal(a.body())
	}
}

func TestV2IncludesPeggedCurrenciesInCatalogue(t *testing.T) {
	a := newV2App(t)
	a.get("/currencies?scope=all")
	a.ok()
	bmd := findRow(a.array(), "iso_code", "BMD")
	if bmd == nil || bmd["name"] != "Bermudian Dollar" || bmd["start_date"] == nil || bmd["end_date"] == nil {
		t.Fatalf("BMD = %v", bmd)
	}
}

func TestV2ReturnsPegMetadataForPeggedCurrency(t *testing.T) {
	a := newV2App(t)
	a.get("/currency/bmd")
	a.ok()
	c := a.object()
	peg, _ := c["peg"].(map[string]any)
	if c["iso_code"] != "BMD" || peg["base"] != "USD" || peg["rate"] != 1.0 || peg["authority"] != "Bermuda Monetary Authority" {
		t.Fatalf("BMD = %v", c)
	}
	if _, ok := c["providers"].([]any); !ok {
		t.Fatal("providers missing")
	}
	a.get("/currency/usd")
	a.ok()
	c = a.object()
	if _, ok := c["providers"].([]any); !ok {
		t.Fatal("providers missing")
	}
	if _, ok := c["peg"]; ok {
		t.Fatal("USD has a peg")
	}
}

// Provider routes: /providers/<key>/<path> is an alias of /<path>?providers=<key>, same bytes and headers.

func assertAlias(t *testing.T, a *v2App, path, query string, headers ...string) {
	t.Helper()
	sep := ""
	if query != "" {
		sep = "&"
	}
	canonical := a.get("/"+path+"?providers=ecb"+sep+query, headers...)
	aliased := "/providers/ecb/" + path
	if query != "" {
		aliased += "?" + query
	}
	a.get(aliased, headers...)
	if a.res.Code != canonical.Code || a.body() != canonical.Body.String() {
		t.Fatalf("%s: %d %.200s, canonical %d %.200s", aliased, a.res.Code, a.body(), canonical.Code, canonical.Body.String())
	}
	for _, h := range []string{"Content-Type", "Content-Disposition", "Cache-Control", "ETag", "Vary"} {
		if a.header(h) != canonical.Header().Get(h) {
			t.Errorf("%s: %s %q, canonical %q", aliased, h, a.header(h), canonical.Header().Get(h))
		}
	}
}

func TestV2ProviderRoutesAliasRates(t *testing.T) {
	a := newV2App(t)
	for _, c := range []struct {
		path, query string
		conform     bool
	}{
		{"rates", "", true},
		{"rates", "date=" + historicalDate(), true},
		{"rates", "from=" + v2RangeStart() + "&to=" + v2RangeEnd(), true},
		{"rates", "base=USD&quotes=EUR,GBP", true},
		{"rates", "from=" + yearStart() + "&to=" + yearEnd() + "&group=week", true},
		{"rates.csv", "from=" + v2RangeStart() + "&to=" + v2RangeEnd(), false},
		{"rate/EUR/USD", "", true},
		{"rate/EUR/USD", "date=" + historicalDate(), false},
	} {
		assertAlias(t, a, c.path, c.query)
		a.ok()
		if c.conform {
			a.conform(200)
		}
	}
	if !strings.Contains(a.header("Content-Type"), "json") || a.object()["date"] != historicalDate() {
		t.Fatalf("pair on date = %s", a.body())
	}
	assertAlias(t, a, "rates", "from="+v2RangeStart()+"&to="+v2RangeEnd(), "Accept", "application/x-ndjson")
	if !strings.Contains(a.header("Content-Type"), "application/x-ndjson") {
		t.Fatalf("content type %q", a.header("Content-Type"))
	}
	assertAlias(t, a, "rates.csv", "from="+v2RangeStart()+"&to="+v2RangeEnd())
	if !strings.Contains(a.header("Content-Type"), "text/csv") {
		t.Fatalf("content type %q", a.header("Content-Type"))
	}
}

func TestV2ServesSingleProviderEntry(t *testing.T) {
	a := newV2App(t)
	a.get("/providers")
	entry := findRow(a.array(), "key", "ECB")
	a.get("/providers/ecb")
	a.conform(200)
	got := a.object()
	for k, v := range entry {
		if !equalJSON(got[k], v) {
			t.Errorf("%s = %v, listing %v", k, got[k], v)
		}
	}
	if len(got) != len(entry) {
		t.Fatalf("entry %v, listing %v", got, entry)
	}
	a.get("/providers/nope")
	a.status(404)
}

func equalJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func TestV2ProviderRoutesAreCaseInsensitive(t *testing.T) {
	a := newV2App(t)
	a.get("/providers/ecb/rates")
	lower := a.body()
	a.get("/providers/ECB/rates")
	if a.body() != lower {
		t.Fatal("bodies differ")
	}
}

func TestV2ProviderRoutes404(t *testing.T) {
	a := newV2App(t)
	a.get("/providers/nope/rates")
	a.conform(404)
	a.get("/providers/boc/rate/CAD/SEK")
	a.status(404)
}

func TestV2ProviderRoutesRejectProvidersParam(t *testing.T) {
	a := newV2App(t)
	a.get("/providers/ecb/rates?providers=boc")
	a.status(422)
	if !strings.Contains(a.object()["message"].(string), "providers") {
		t.Fatal(a.body())
	}
}

func TestV2RejectsTrailingPathSegments(t *testing.T) {
	a := newV2App(t)
	for _, path := range []string{"/rates", "/rate/EUR/USD", "/currency/usd", "/currencies", "/providers/ecb/rates",
		"/providers/ecb/rate/EUR/USD"} {
		a.get(path)
		a.ok()
		for _, extra := range []string{path + "/latest", path + "/"} {
			a.get(extra)
			if a.res.Code != 404 || a.body() != `{"status":404,"message":"not found"}` {
				t.Errorf("%s: %d %s", extra, a.res.Code, a.body())
			}
		}
	}
}
