package ratequery

import (
	"database/sql"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/blend"
	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/fixtures"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

// spec/rate_spike_spec.rb, "the CBG leone typo" once stored. The Central Bank
// of the Gambia published the leone at 43.26 dalasi on 2024-01-22, 3.26 either
// side. Three sources quote the leone, too few for Consensus to screen an
// outlier.

var (
	leoneDays = []time.Time{adapter.Date(2024, 1, 17), adapter.Date(2024, 1, 18), adapter.Date(2024, 1, 19),
		adapter.Date(2024, 1, 22), adapter.Date(2024, 1, 23), adapter.Date(2024, 1, 24)}
	leoneTypoDay  = adapter.Date(2024, 1, 22)
	leoneScreened = ((70.0 / 3.26) + 21.5 + 21.6) / 3
)

// leoneDatabase stores CBG's leone with the typo and T1's and T2's quotes,
// rolls them up and screens CBG.
func leoneDatabase(t *testing.T) *sql.DB {
	t.Helper()
	conn := fixtures.New(t)
	for _, day := range leoneDays {
		leone := 3.26
		if day.Equal(leoneTypoDay) {
			leone = 43.26
		}
		insert(t, conn, "CBG", day, "USD", "GMD", 70.0)
		insert(t, conn, "CBG", day, "SLE", "GMD", leone)
		insert(t, conn, "T1", day, "USD", "SLE", 21.5)
		insert(t, conn, "T2", day, "USD", "SLE", 21.6)
	}
	list := make([]string, len(leoneDays))
	for i, day := range leoneDays {
		list[i] = d(day)
	}
	for _, table := range rates.Rollups {
		b := rates.BucketSQL(table.Precision, "date")
		exec(t, conn, "INSERT INTO "+table.Name+" (bucket_date, provider, base, quote, rate) SELECT "+b+
			", provider, base, quote, avg(rate) FROM rates WHERE provider IN ('CBG', 'T1', 'T2') AND date IN "+
			db.LitList(list)+" GROUP BY "+b+", provider, base, quote")
	}
	if _, err := rates.RefreshSpikes(ctx, conn, "CBG", leoneDays); err != nil {
		t.Fatal(err)
	}
	return conn
}

func TestSpikeScreensTheTypoOutOfTheDailyBlendAndCarriesCBGsPreviousObservation(t *testing.T) {
	conn := leoneDatabase(t)
	rebuildDaily(t, conn)

	rows, err := conn.Query("SELECT provider, base, quote FROM rate_spikes")
	if err != nil {
		t.Fatal(err)
	}
	var flagged [][3]string
	for rows.Next() {
		var f [3]string
		if err := rows.Scan(&f[0], &f[1], &f[2]); err != nil {
			t.Fatal(err)
		}
		flagged = append(flagged, f)
	}
	rows.Close()
	if want := [][3]string{{"CBG", "SLE", "GMD"}}; !reflect.DeepEqual(flagged, want) {
		t.Fatalf("flagged %v, want %v", flagged, want)
	}
	var blended float64
	if err := conn.QueryRow("SELECT rate FROM blended_rates WHERE quote = 'SLE' AND date = ?", d(leoneTypoDay)).
		Scan(&blended); err != nil {
		t.Fatal(err)
	}
	if math.Abs(blended-leoneScreened) > 1e-9 {
		t.Errorf("SLE blend %v, want %v", blended, leoneScreened)
	}

	records := query(t, conn, "date", d(leoneTypoDay), "base", "USD", "quotes", "SLE", "expand", "providers")
	if len(records) == 0 {
		t.Fatal("no records")
	}
	var cbg *Contribution
	for i := range records[0].Providers {
		if records[0].Providers[i].Key == "CBG" {
			cbg = &records[0].Providers[i]
		}
	}
	if cbg == nil || cbg.Date != "2024-01-19" {
		t.Errorf("CBG contribution %+v, want dated 2024-01-19", cbg)
	}
}

func TestSpikeServesThePublishedValueToProviderQueries(t *testing.T) {
	conn := leoneDatabase(t)

	records := query(t, conn, "date", d(leoneTypoDay), "base", "SLE", "quotes", "GMD", "providers", "CBG")
	if len(records) == 0 {
		t.Fatal("no records")
	}
	if records[0].Date != d(leoneTypoDay) {
		t.Errorf("date %s, want %s", records[0].Date, d(leoneTypoDay))
	}
	if records[0].Rate.Value != 43.26 {
		t.Errorf("rate %v, want 43.26", records[0].Rate.Value)
	}
}

func TestSpikeServesTheSameBlendFromTheTableAndTheLivePath(t *testing.T) {
	conn := leoneDatabase(t)
	rebuildDaily(t, conn)
	kv := []string{"from", d(leoneDays[0]), "to", d(leoneDays[len(leoneDays)-1]), "base", "USD", "quotes", "SLE"}

	stored, live := query(t, conn, kv...), liveQuery(t, conn, kv...)
	if len(stored) == 0 {
		t.Fatal("no records")
	}
	if !reflect.DeepEqual(stored, live) {
		t.Errorf("table %+v\nlive  %+v", stored, live)
	}
}

func TestSpikeScreensTheTypoOutOfWeeklyAndMonthlyBlendsButNotOutOfCBGsOwnAverages(t *testing.T) {
	conn := leoneDatabase(t)
	filter, err := rates.LoadBlendFilter(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	value := func(query string) float64 {
		t.Helper()
		var v float64
		if err := conn.QueryRow(query).Scan(&v); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		return v
	}
	for _, r := range blend.Rollups {
		if err := r.Rebuild(ctx, conn, today()); err != nil {
			t.Fatal(err)
		}
		bucket := db.Lit(db.FormatDate(rates.Bucket(r.Source.Precision, leoneTypoDay)))
		cbg := "provider = 'CBG' AND base = 'SLE' AND bucket_date = " + bucket

		if got := value(r.Source.Dataset().Filter(cbg).Columns("rate").SQL()); got <= 3.26 {
			t.Errorf("%s: CBG average %v, want above 3.26", r.Source.Name, got)
		}
		if got := value(r.Source.Blendable(filter).Filter(cbg).Columns("rate").SQL()); math.Abs(got-3.26) > 1e-9 {
			t.Errorf("%s: CBG blendable average %v, want 3.26", r.Source.Name, got)
		}
		got := value("SELECT rate FROM " + r.Table + " WHERE quote = 'SLE' AND bucket_date = " + bucket)
		if math.Abs(got-leoneScreened) > 1e-9 {
			t.Errorf("%s: SLE blend %v, want %v", r.Table, got, leoneScreened)
		}
	}
}
