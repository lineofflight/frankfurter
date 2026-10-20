package provider

import (
	"context"
	"log/slog"
	"math"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/fixtures"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

// spec/rate_spike_spec.rb, "the CBG leone typo" as CBG's latest observation.
// The Central Bank of the Gambia published the leone at 43.26 dalasi on
// 2024-01-22, 3.26 either side. Three sources quote the leone, too few for
// Consensus to screen an outlier.
func TestSpikeBlendsUntilTheNextObservationArrivesThenIsScreenedAndItsBlendsRefreshed(t *testing.T) {
	ctx := context.Background()
	days := []time.Time{adapter.Date(2024, 1, 17), adapter.Date(2024, 1, 18), adapter.Date(2024, 1, 19),
		adapter.Date(2024, 1, 22), adapter.Date(2024, 1, 23), adapter.Date(2024, 1, 24)}
	typoDay := days[3]
	published := ((70.0 / 43.26) + 21.5 + 21.6) / 3
	screened := ((70.0 / 3.26) + 21.5 + 21.6) / 3

	conn := fixtures.New(t)
	var list []string
	for _, day := range days {
		list = append(list, d(day))
		for _, r := range []struct {
			provider string
			rate     float64
		}{{"T1", 21.5}, {"T2", 21.6}} {
			if _, err := conn.Exec("INSERT INTO rates (provider, date, base, quote, mid) VALUES (?, ?, 'USD', 'SLE', ?)",
				r.provider, d(day), r.rate); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, table := range rates.Rollups {
		b := rates.BucketSQL(table.Precision, "date")
		if _, err := conn.Exec("INSERT INTO " + table.Name + " (bucket_date, provider, base, quote, rate) SELECT " + b +
			", provider, base, quote, avg(rate) FROM rates WHERE provider IN ('T1', 'T2') AND date IN " +
			db.LitList(list) + " GROUP BY " + b + ", provider, base, quote"); err != nil {
			t.Fatal(err)
		}
	}

	cbg, err := Find(ctx, conn, "CBG")
	if err != nil || cbg == nil {
		t.Fatalf("find CBG: %v", err)
	}
	// The adapter serves CBG's rows through upto, dated after after.
	var upto time.Time
	a := &fakeAdapter{fetch: func(after, _ time.Time) ([]adapter.Rate, error) {
		var out []adapter.Rate
		for _, day := range days {
			if day.After(upto) || (!after.IsZero() && !day.After(after)) {
				continue
			}
			leone := 3.26
			if day.Equal(typoDay) {
				leone = 43.26
			}
			out = append(out, rate(day, "USD", "GMD", 70.0), rate(day, "SLE", "GMD", leone))
		}
		return out, nil
	}}
	in := &Ingester{
		DB:      conn,
		Logger:  slog.New(newLogRecorder()),
		Today:   fixtures.Today,
		Adapter: func(string) (adapter.Adapter, error) { return a, nil },
	}
	value := func(query string, args ...any) float64 {
		t.Helper()
		var v float64
		if err := conn.QueryRow(query, args...).Scan(&v); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		return v
	}
	spikes := func() []string {
		t.Helper()
		rows, err := conn.Query("SELECT date FROM rate_spikes ORDER BY date")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var day db.NullDate
			if err := rows.Scan(&day); err != nil {
				t.Fatal(err)
			}
			out = append(out, d(day.Time))
		}
		return out
	}

	upto = typoDay
	in.BackfillAfter(ctx, *cbg, days[0].AddDate(0, 0, -1))

	if got := spikes(); len(got) != 0 {
		t.Fatalf("spikes %v before the next observation", got)
	}
	if got := value("SELECT rate FROM blended_rates WHERE quote = 'SLE' AND date = ?", d(typoDay)); math.Abs(got-published) > 1e-9 {
		t.Fatalf("SLE blend %v, want the published %v", got, published)
	}

	upto = days[len(days)-1]
	in.BackfillAfter(ctx, *cbg, typoDay)

	if got := spikes(); len(got) != 1 || got[0] != d(typoDay) {
		t.Fatalf("spikes %v, want [%s]", got, d(typoDay))
	}
	if got := value("SELECT rate FROM blended_rates WHERE quote = 'SLE' AND date = ?", d(typoDay)); math.Abs(got-screened) > 1e-9 {
		t.Errorf("SLE blend %v, want the screened %v", got, screened)
	}
	for _, r := range []struct {
		table string
		p     rates.Precision
	}{{"blended_weekly_rates", rates.Week}, {"blended_monthly_rates", rates.Month}} {
		got := value("SELECT rate FROM "+r.table+" WHERE quote = 'SLE' AND bucket_date = ?",
			d(rates.Bucket(r.p, typoDay)))
		if math.Abs(got-screened) > 1e-9 {
			t.Errorf("%s: SLE blend %v, want %v", r.table, got, screened)
		}
	}
}
