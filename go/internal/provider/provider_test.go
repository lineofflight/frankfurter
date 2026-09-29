package provider

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/adhocore/gronx"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/dbtest"
	"github.com/lineofflight/frankfurter/go/internal/fixtures"
	"github.com/lineofflight/frankfurter/go/internal/rates"
	"github.com/lineofflight/frankfurter/go/internal/seeds"
)

func TestSchemaHasPublishScheduleAndCadence(t *testing.T) {
	conn := dbtest.New(t)
	rows, err := conn.Query("SELECT name FROM pragma_table_info('providers')")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatal(err)
		}
		cols = append(cols, c)
	}
	for _, c := range []string{"publish_schedule", "publish_cadence"} {
		if !slices.Contains(cols, c) {
			t.Errorf("missing column %s", c)
		}
	}
	for _, c := range []string{"publish_time", "publish_days"} {
		if slices.Contains(cols, c) {
			t.Errorf("unexpected column %s", c)
		}
	}
}

func TestSeedsEveryProviderWithScheduleCadenceAndValidCron(t *testing.T) {
	paths, err := fs.Glob(seeds.FS, "providers/*.json")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no provider seeds: %v", err)
	}
	valid := []any{nil, "daily", "weekly", "monthly", "quarterly"}
	for _, path := range paths {
		b, err := fs.ReadFile(seeds.FS, path)
		if err != nil {
			t.Fatal(err)
		}
		var data map[string]any
		if err := json.Unmarshal(b, &data); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		for _, k := range []string{"publish_schedule", "publish_cadence"} {
			if _, ok := data[k]; !ok {
				t.Errorf("%s: missing %s", path, k)
			}
		}
		for _, k := range []string{"publish_time", "publish_days"} {
			if _, ok := data[k]; ok {
				t.Errorf("%s: unexpected %s", path, k)
			}
		}
		if !slices.Contains(valid, data["publish_cadence"]) {
			t.Errorf("%s: publish_cadence %v", path, data["publish_cadence"])
		}
		if !slices.Contains(valid, data["frequency"]) {
			t.Errorf("%s: frequency %v", path, data["frequency"])
		}
		if (data["publish_cadence"] == nil) != (data["publish_schedule"] == nil) {
			t.Errorf("%s: publish_cadence and publish_schedule must both be set or both null", path)
		}
		if s, ok := data["publish_schedule"].(string); ok && !gronx.IsValid(s) {
			t.Errorf("%s: invalid cron %q", path, s)
		}
	}
}

type stubAdapter struct{ adapter.Base }

func (*stubAdapter) Fetch(context.Context, time.Time, time.Time) ([]adapter.Rate, error) {
	return nil, nil
}

func TestAdapterFindsAdapterByKey(t *testing.T) {
	adapter.Register("STUB", func(c *http.Client) adapter.Adapter { return &stubAdapter{adapter.NewBase(c)} })

	a, err := Lookup("STUB", http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := a.(*stubAdapter); !ok {
		t.Fatalf("got %T", a)
	}
	if _, err := Lookup("NOSUCH", http.DefaultClient); err == nil {
		t.Fatal("want an error for an unregistered key")
	}
}

func TestSeedKeepsAKeyOnlySomeSeedFilesCarry(t *testing.T) {
	ctx := context.Background()
	conn := fixtures.New(t)
	if err := rates.SeedProviders(ctx, conn); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"UST": "quarterly", "ECB": "daily"} {
		p, err := Find(ctx, conn, key)
		if err != nil || p == nil {
			t.Fatalf("%s: %v", key, err)
		}
		if p.ObservationFrequency() != want {
			t.Errorf("%s frequency = %q, want %q", key, p.ObservationFrequency(), want)
		}
	}
	keys, err := rates.NonBlendingKeys(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(keys, "UST") {
		t.Errorf("non-blending keys %v lack UST", keys)
	}
}

func TestFindAndAllReadEveryColumn(t *testing.T) {
	ctx := context.Background()
	conn := fixtures.New(t)
	p, err := Find(ctx, conn, "BCB")
	if err != nil {
		t.Fatal(err)
	}
	want := Provider{Key: "BCB", Name: "Banco Central do Brasil", DataURL: "https://olinda.bcb.gov.br/olinda/servico/PTAX/versao/v1/odata/",
		CoverageStart: adapter.Date(2000, 1, 1), PivotCurrency: "BRL", RateType: "PTAX closing", CountryCode: "BR",
		PublishSchedule: "*/30 16-18 * * 1-5", PublishCadence: "daily", Frequency: "daily"}
	if *p != want {
		t.Fatalf("got %+v\nwant %+v", *p, want)
	}
	if p, err := Find(ctx, conn, "NOSUCH"); err != nil || p != nil {
		t.Fatalf("Find(NOSUCH) = %v, %v", p, err)
	}
	all, err := All(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	all2, _ := seeds.Providers()
	if len(all) != len(all2) {
		t.Fatalf("All returned %d providers, seeds have %d", len(all), len(all2))
	}
	if !slices.IsSortedFunc(all, func(a, b Provider) int { return compare(a.Key, b.Key) }) {
		t.Error("All is not ordered by key")
	}
}

func compare(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func TestFrequencyDefaultsToDailyBlendsAndCarriesForwardTwoWeeks(t *testing.T) {
	p := Provider{Key: "EXAMPLE"}
	if p.ObservationFrequency() != "daily" || !p.Blends() || p.Lookback() != 14 {
		t.Fatalf("got %q, blends %v, lookback %d", p.ObservationFrequency(), p.Blends(), p.Lookback())
	}
}

func TestFrequencyKeepsMonthlyAndQuarterlyOutOfTheBlend(t *testing.T) {
	monthly := Provider{Frequency: "monthly"}
	quarterly := Provider{Frequency: "quarterly"}
	if monthly.Blends() || monthly.Lookback() != 45 {
		t.Errorf("monthly: blends %v, lookback %d", monthly.Blends(), monthly.Lookback())
	}
	if quarterly.Blends() || quarterly.Lookback() != 120 {
		t.Errorf("quarterly: blends %v, lookback %d", quarterly.Blends(), quarterly.Lookback())
	}
}

func TestFrequencyWidensLookbackToPublishCadence(t *testing.T) {
	inArrears := Provider{PublishCadence: "monthly"}
	weekly := Provider{PublishCadence: "weekly"}
	unscheduled := Provider{}
	if !inArrears.Blends() || inArrears.Lookback() != 45 {
		t.Errorf("in arrears: blends %v, lookback %d", inArrears.Blends(), inArrears.Lookback())
	}
	if weekly.Lookback() != 14 || unscheduled.Lookback() != 14 {
		t.Errorf("weekly %d, unscheduled %d", weekly.Lookback(), unscheduled.Lookback())
	}
}

func TestFrequencyListsTheKeysOfProvidersThatDoNotBlend(t *testing.T) {
	ctx := context.Background()
	conn := fixtures.New(t)
	if _, err := conn.Exec("INSERT INTO providers (key, name, frequency) VALUES ('TST', 'Test', 'monthly')"); err != nil {
		t.Fatal(err)
	}
	keys, err := rates.NonBlendingKeys(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(keys, "TST") {
		t.Fatalf("non-blending keys %v lack TST", keys)
	}
}

func TestUnknownCurrenciesSkipsNamedAndReviewedCodes(t *testing.T) {
	ctx := context.Background()
	conn := dbtest.New(t)
	for _, row := range [][2]string{{"NB", "TWI"}, {"NB", "ZZZ"}, {"NB", "I44"}, {"NB", "ABC"}, {"NB", "USD"}, {"RBA", "TWI"}} {
		if _, err := conn.Exec("INSERT INTO currency_exclusions VALUES (?, ?, '2020-01-01', '2020-02-01')",
			row[0], row[1]); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Provider{Key: "NB"}.UnknownCurrencies(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"ABC", "ZZZ"}) {
		t.Fatalf("got %v", got)
	}
	got, _ = Provider{Key: "RBA"}.UnknownCurrencies(ctx, conn)
	if !slices.Equal(got, []string{"TWI"}) {
		t.Fatalf("RBA got %v", got)
	}
}

func TestStartAndEndDatesSpanCoveragesAndExclusions(t *testing.T) {
	ctx := context.Background()
	conn := dbtest.New(t)
	p := Provider{Key: "X"}
	if _, ok, err := p.EndDate(ctx, conn); ok || err != nil {
		t.Fatalf("EndDate on no coverage: ok %v, err %v", ok, err)
	}
	for _, stmt := range []string{
		"INSERT INTO currency_coverages VALUES ('X', 'USD', '2020-03-01', '2021-01-01')",
		"INSERT INTO currency_exclusions VALUES ('X', 'ZZZ', '2019-05-01', '2022-06-30')",
		"INSERT INTO currency_coverages VALUES ('Y', 'USD', '2010-01-01', '2030-01-01')",
	} {
		if _, err := conn.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	start, _, _ := p.StartDate(ctx, conn)
	end, _, _ := p.EndDate(ctx, conn)
	if start != "2019-05-01" || end != "2022-06-30" {
		t.Fatalf("got %s..%s", start, end)
	}
	if _, err := conn.Exec("INSERT INTO currency_coverages VALUES ('X', 'EUR', NULL, NULL)"); err != nil {
		t.Fatal(err)
	}
	if start, _, _ := p.StartDate(ctx, conn); start != "" {
		t.Fatalf("a NULL start should win as Ruby's nil.to_s, got %q", start)
	}
}

func TestLastSynced(t *testing.T) {
	ctx := context.Background()
	conn := fixtures.New(t)
	got, err := Provider{Key: "ECB"}.LastSynced(ctx, conn)
	if err != nil || !got.Equal(fixtures.LatestDate()) {
		t.Fatalf("ECB: %v, %v, want %v", got, err, fixtures.LatestDate())
	}
	got, err = Provider{Key: "BCB"}.LastSynced(ctx, conn)
	if err != nil || !got.IsZero() {
		t.Fatalf("BCB: %v, %v", got, err)
	}
}

func day(y int, m time.Month, d int) time.Time { return adapter.Date(y, m, d) }

func build(schedule, cadence string) Provider {
	return Provider{Key: "EXAMPLE", Name: "Example", PublishSchedule: schedule, PublishCadence: cadence}
}

func TestPublishesMissedNilWithoutSchedule(t *testing.T) {
	if _, ok, err := build("", "").MissedSince("2026-04-01", day(2026, 4, 20)); ok || err != nil {
		t.Fatalf("ok %v, err %v", ok, err)
	}
}

func TestPublishesMissedNilWithoutEndDate(t *testing.T) {
	conn := dbtest.New(t)
	if _, ok, err := build("*/30 14-16 * * 1-5", "daily").PublishesMissed(context.Background(), conn,
		day(2026, 4, 20)); ok || err != nil {
		t.Fatalf("ok %v, err %v", ok, err)
	}
}

func TestPublishesMissedReadsStoredEndDate(t *testing.T) {
	conn := dbtest.New(t)
	if _, err := conn.Exec("INSERT INTO currency_coverages VALUES ('EXAMPLE', 'USD', '2026-01-01', '2026-04-13')"); err != nil {
		t.Fatal(err)
	}
	n, ok, err := build("*/30 14-16 * * 1-5", "daily").PublishesMissed(context.Background(), conn, day(2026, 4, 17))
	if !ok || err != nil || n != 3 {
		t.Fatalf("got %d, ok %v, err %v", n, ok, err)
	}
}

func TestPublishesMissedRejectsUnknownCadence(t *testing.T) {
	if _, _, err := build("*/30 14-16 * * 1-5", "biweekly").MissedSince("2026-04-01", day(2026, 4, 20)); err == nil {
		t.Fatal("want an error")
	}
}

func TestPublishesMissed(t *testing.T) {
	const (
		monFri  = "*/30 14-16 * * 1-5"
		daily   = "*/30 14-16 * * *"
		mondays = "*/30 21-23 * * 1"
		hkma    = "*/30 1-10 3-12 * *"
		ust     = "0 12 1-10 1,4,7,10 *"
		cbc     = "*/30 8-10 * * 1-5"
	)
	tests := []struct {
		name              string
		schedule, cadence string
		end               string
		ref               time.Time
		want              int
	}{
		// Daily cadence, Mon-Fri publishing (ECB-style).
		{"Friday to Monday misses nothing", monFri, "daily", "2026-04-17", day(2026, 4, 20), 0},
		{"counts weekdays missed", monFri, "daily", "2026-04-13", day(2026, 4, 17), 3},
		{"ignores weekends", monFri, "daily", "2026-04-10", day(2026, 4, 20), 5},
		// Daily cadence, seven-day publishing.
		{"counts every day", daily, "daily", "2026-04-10", day(2026, 4, 20), 9},
		// Daily cadence, Mondays only.
		{"counts only Mondays", mondays, "daily", "2026-04-06", day(2026, 4, 24), 2},
		// Weekly cadence (FRED-style): the Monday batch covers the prior ISO week.
		{"weekly: end in the week the last batch covered", mondays, "weekly", "2026-04-19", day(2026, 4, 21), 0},
		{"weekly: Saturday of the covered week", mondays, "weekly", "2026-04-18", day(2026, 4, 21), 0},
		{"weekly: one ISO week behind", mondays, "weekly", "2026-04-12", day(2026, 4, 21), 1},
		{"weekly: Sunday before the Monday batch", mondays, "weekly", "2026-04-12", day(2026, 4, 19), 0},
		{"weekly: Monday morning without last week's batch", mondays, "weekly", "2026-04-12", day(2026, 4, 20), 1},
		// Monthly cadence (HKMA-style): a day-of-month window covers the prior month.
		{"monthly: current, past the window", hkma, "monthly", "2026-03-31", day(2026, 4, 20), 0},
		{"monthly: current, before the window", hkma, "monthly", "2026-03-31", day(2026, 4, 2), 0},
		{"monthly: last business day is not the last calendar day", hkma, "monthly", "2025-11-29", day(2025, 12, 15), 0},
		{"monthly: April batch missing past May's window", hkma, "monthly", "2026-03-31", day(2026, 5, 15), 1},
		{"monthly: two batches missed", hkma, "monthly", "2026-01-31", day(2026, 4, 20), 2},
		{"monthly: first day of the window without last month", hkma, "monthly", "2026-02-28", day(2026, 4, 3), 1},
		// Quarterly cadence (Treasury-style).
		{"quarterly: latest quarter-end is the last one due", ust, "quarterly", "2026-03-31", day(2026, 5, 20), 0},
		{"quarterly: counts each quarter missed", ust, "quarterly", "2025-09-30", day(2026, 5, 20), 2},
		// Monthly cadence on a daily weekday schedule (CBC-style).
		{"monthly cadence on a weekday schedule", cbc, "monthly", "2026-04-30", day(2026, 5, 29), 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok, err := build(tt.schedule, tt.cadence).MissedSince(tt.end, tt.ref)
			if err != nil || !ok || got != tt.want {
				t.Fatalf("got %d (ok %v, err %v), want %d", got, ok, err, tt.want)
			}
		})
	}
}

func TestPublishesMissedWouldReportALargeCountUnderDailyCadence(t *testing.T) {
	got, _, err := build("*/30 8-10 * * 1-5", "daily").MissedSince("2026-04-30", day(2026, 5, 29))
	if err != nil || got < 20 {
		t.Fatalf("got %d, err %v", got, err)
	}
}
