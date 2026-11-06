package rates_test

import (
	"context"
	"database/sql"
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/dbtest"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

var ctx = context.Background()

func TestNormalize(t *testing.T) {
	for _, c := range []struct {
		name     string
		in, want float64
	}{
		{"strips noise from a synthesized midpoint", (181.5264 + 181.76) / 2.0, 181.6432},
		{"strips noise from per-unit scaling", 744.92 / 100.0, 7.4492},
		{"strips noise from a cross rate", 0.9136230000000001, 0.913623},
		{"leaves a clean rate untouched", 1.0876, 1.0876},
		{"keeps every digit of a rate published to ten significant digits", 0.0001234567891, 0.0001234567891},
		{"keeps the magnitude of very large rates", 260988505.32818, 260988505.328},
		{"keeps the magnitude of very small rates", 1.2e-9, 1.2e-9},
		{"passes integers through", 3, 3},
		{"breaks a decimal tie to even", 1.234567890125, 1.23456789012},
		{"breaks a decimal tie to even upwards", 1.234567890135, 1.23456789014},
		{"passes non-finite values through", math.Inf(1), math.Inf(1)},
	} {
		if got := rates.Normalize(c.in); got != c.want {
			t.Errorf("%s: Normalize(%v) = %v, want %v", c.name, c.in, got, c.want)
		}
	}
	if !math.IsNaN(rates.Normalize(math.NaN())) {
		t.Error("NaN did not pass through")
	}
}

// Ruby's "passes nil through" has no Go counterpart: a float64 is never nil.

func TestNormalizeRoundTripsThroughText(t *testing.T) {
	v := rates.Normalize(0.9136230000000001)
	if back, _ := strconv.ParseFloat(strconv.FormatFloat(v, 'g', -1, 64), 64); back != v {
		t.Errorf("%v round-tripped to %v", v, back)
	}
}

func TestPrecisionSQLStripsNoise(t *testing.T) {
	conn := dbtest.New(t)
	var got float64
	if err := conn.QueryRow("SELECT "+rates.PrecisionSQL("?"), (181.5264+181.76)/2.0).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != 181.6432 {
		t.Errorf("got %v", got)
	}
}

// storedRate inserts one TST USD/GBP row with the given components and reads
// back its resolved rate and mid.
func storedRate(t *testing.T, provider string, mid, bid, ask any) (rate, storedMid sql.NullFloat64) {
	t.Helper()
	conn := dbtest.New(t)
	if _, err := conn.Exec(`INSERT INTO rates (provider, date, base, quote, mid, bid, ask)
		VALUES (?, '2026-09-01', 'USD', 'GBP', ?, ?, ?)`, provider, mid, bid, ask); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow("SELECT rate, mid FROM rates").Scan(&rate, &storedMid); err != nil {
		t.Fatal(err)
	}
	return rate, storedMid
}

func TestRateIsVirtualColumn(t *testing.T) {
	conn := dbtest.New(t)
	var hidden int
	if err := conn.QueryRow("SELECT hidden FROM pragma_table_xinfo('rates') WHERE name = 'rate'").Scan(&hidden); err != nil {
		t.Fatal(err)
	}
	if hidden != 2 {
		t.Errorf("hidden = %d, want 2", hidden)
	}
}

func TestPrefersPublishedMid(t *testing.T) {
	if rate, _ := storedRate(t, "TST", 100, 99, 103); rate.Float64 != 100 {
		t.Errorf("rate = %v", rate)
	}
}

func TestReproducesDecimalMidpoint(t *testing.T) {
	if rate, _ := storedRate(t, "TST", nil, 181.5264, 181.76); rate.Float64 != 181.6432 {
		t.Errorf("rate = %v", rate)
	}
}

func TestNormalizesMidpointPrecisionInSQL(t *testing.T) {
	if rate, _ := storedRate(t, "TST", nil, 1830.59054685, 1831.5063); rate.Float64 != 1831.04842343 {
		t.Errorf("rate = %v", rate)
	}
}

func TestNoEffectiveRateForSingleSide(t *testing.T) {
	if rate, _ := storedRate(t, "TST", nil, nil, 103); rate.Valid {
		t.Errorf("rate = %v", rate)
	}
}

func TestKeepsBOJAZeroBuyConvention(t *testing.T) {
	rate, mid := storedRate(t, "BOJA", nil, 0, 103)
	if rate.Float64 != 103 || mid.Valid {
		t.Errorf("rate = %v, mid = %v", rate, mid)
	}
}

func TestComponentsRetainLegacyReference(t *testing.T) {
	c := rates.ComponentsOf(adapter.Rate{Date: time.Now(), Base: "USD", Quote: "GBP", Rate: 0.9136230000000001})
	if c.Mid == nil || *c.Mid != 0.913623 || c.Bid != nil || c.Ask != nil {
		t.Errorf("components = %+v", c)
	}
}

func TestComponentsKeepDerivedMidpointOut(t *testing.T) {
	c := rates.ComponentsOf(adapter.Rate{Base: "USD", Quote: "GBP", Rate: 101, Bid: adapter.Float(99), Ask: adapter.Float(103)})
	if c.Mid != nil || *c.Bid != 99 || *c.Ask != 103 {
		t.Errorf("components = %+v", c)
	}
}

func TestMidpoint(t *testing.T) {
	if got := rates.Midpoint(adapter.Float(181.5264), adapter.Float(181.76)); *got != 181.6432 {
		t.Errorf("got %v", *got)
	}
	if rates.Midpoint(nil, adapter.Float(1)) != nil {
		t.Error("midpoint of one side")
	}
}

func TestRound(t *testing.T) {
	for _, c := range []struct{ in, want float64 }{
		{12345.6, 12346},
		{150.456, 150.46},
		{25.12345, 25.123},
		{1.234567, 1.2346},
		{0.1234567, 0.12346},
		{0.00001234567, 0.000012},
		// Ruby rounds the shortest decimal half to even, not the exact double.
		{214.415, 214.42},
		{643.965, 643.96},
		{25.1235, 25.124},
		{25.1245, 25.124},
		{1.00005, 1.0},
		{0.0000125, 0.000012},
		{0.0001235, 0.00012},
	} {
		if got := rates.Round(c.in); got != c.want {
			t.Errorf("Round(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestBucketMatchesSQL(t *testing.T) {
	conn := dbtest.New(t)
	for d := time.Date(2015, 12, 20, 0, 0, 0, 0, time.UTC); d.Year() < 2018; d = d.AddDate(0, 0, 1) {
		for _, p := range []rates.Precision{rates.Week, rates.Month} {
			var want string
			if err := conn.QueryRow("SELECT "+rates.BucketSQL(p, "?1"), d.Format(time.DateOnly)).Scan(&want); err != nil {
				t.Fatal(err)
			}
			if got := rates.Bucket(p, d).Format(time.DateOnly); got != want {
				t.Fatalf("%s bucket of %s = %s, SQL says %s", p, d.Format(time.DateOnly), got, want)
			}
		}
	}
}
