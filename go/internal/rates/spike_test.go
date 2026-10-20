package rates_test

import (
	"database/sql"
	"slices"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/dbtest"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

// spec/rate_spike_spec.rb

type observation struct {
	date time.Time
	mid  float64
}

// daily lays values on consecutive days from from (2024-01-15 when zero).
func daily(from time.Time, values ...float64) []observation {
	if from.IsZero() {
		from = adapter.Date(2024, 1, 15)
	}
	out := make([]observation, len(values))
	for i, v := range values {
		out[i] = observation{from.AddDate(0, 0, i), v}
	}
	return out
}

func insertSeries(t *testing.T, conn *sql.DB, provider, base, quote string, series []observation) {
	t.Helper()
	for _, o := range series {
		if _, err := conn.Exec("INSERT INTO rates (provider, date, base, quote, mid) VALUES (?, ?, ?, ?, ?)",
			provider, db.FormatDate(o.date), base, quote, o.mid); err != nil {
			t.Fatal(err)
		}
	}
}

func spikeDates(t *testing.T, conn *sql.DB, query string) []string {
	t.Helper()
	rows, err := conn.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var d db.NullDate
		if err := rows.Scan(&d); err != nil {
			t.Fatal(err)
		}
		out = append(out, db.FormatDate(d.Time))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	slices.Sort(out)
	return out
}

// detect stores series as T1's USD/SLE and returns the dates DetectSpikes
// flags among T1's rows.
func detect(t *testing.T, conn *sql.DB, series []observation) []string {
	t.Helper()
	insertSeries(t, conn, "T1", "USD", "SLE", series)
	return spikeDates(t, conn, rates.DetectSpikes(rates.Daily.Dataset().Filter("provider = 'T1'")).Columns("date").SQL())
}

func TestDetectFlagsAOneDayTypoAboveOrBelowBothNeighbours(t *testing.T) {
	got := detect(t, dbtest.New(t), daily(time.Time{}, 3.26, 3.26, 43.26, 3.26, 0.326, 3.26))
	if want := []string{"2024-01-17", "2024-01-19"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDetectKeepsAMoveShortOfTheFactor(t *testing.T) {
	if got := detect(t, dbtest.New(t), daily(time.Time{}, 3.26, 9.7, 3.26)); len(got) > 0 {
		t.Errorf("got %v, want none", got)
	}
}

func TestDetectKeepsADevaluationOrRedenominationThatPersists(t *testing.T) {
	if got := detect(t, dbtest.New(t), daily(time.Time{}, 3.26, 32.6, 32.6)); len(got) > 0 {
		t.Errorf("devaluation: got %v, want none", got)
	}
	if got := detect(t, dbtest.New(t), daily(adapter.Date(2024, 2, 1), 3260.0, 3.26, 3.26)); len(got) > 0 {
		t.Errorf("redenomination: got %v, want none", got)
	}
}

func TestDetectKeepsAValueWhoseNeighboursDisagree(t *testing.T) {
	// BDI's euro rate for the Zimbabwe dollar across the 2009 redenomination:
	// no level to revert to.
	if got := detect(t, dbtest.New(t), daily(time.Time{}, 47_219_839.4304, 15_741_267_667.1, 28.2678)); len(got) > 0 {
		t.Errorf("got %v, want none", got)
	}
}

func TestDetectLeavesTheLatestObservationToBlendUntilItsSuccessorArrives(t *testing.T) {
	if got := detect(t, dbtest.New(t), daily(time.Time{}, 3.26, 3.26, 43.26)); len(got) > 0 {
		t.Errorf("got %v, want none", got)
	}
}

func TestDetectJudgesOnlyAgainstNeighboursWithinTheGap(t *testing.T) {
	day := adapter.Date(2024, 1, 22)
	gap := rates.SpikeMaxGapDays

	got := detect(t, dbtest.New(t), []observation{{day.AddDate(0, 0, -gap), 3.26}, {day, 43.26},
		{day.AddDate(0, 0, gap), 3.26}})
	if want := []string{db.FormatDate(day)}; !slices.Equal(got, want) {
		t.Errorf("within the gap: got %v, want %v", got, want)
	}

	got = detect(t, dbtest.New(t), []observation{{day.AddDate(0, 0, -gap-1), 3.26}, {day, 43.26},
		{day.AddDate(0, 0, gap), 3.26}})
	if len(got) > 0 {
		t.Errorf("past the gap: got %v, want none", got)
	}
}

func TestDetectJudgesEachPairAgainstItsOwnNeighbours(t *testing.T) {
	conn := dbtest.New(t)
	insertSeries(t, conn, "T1", "USD", "GMD", daily(time.Time{}, 43.26, 43.26, 43.26))

	if got := detect(t, conn, daily(time.Time{}, 3.26, 3.26, 3.26)); len(got) > 0 {
		t.Errorf("got %v, want none", got)
	}
}

// "the CBG leone typo" / "as CBG's latest observation": "is cleared when a
// late arrival turns the typo into a run". The Central Bank of the Gambia
// published the leone at 43.26 dalasi on 2024-01-22, 3.26 either side.
func TestRefreshSpikesClearsATypoALateArrivalTurnsIntoARun(t *testing.T) {
	conn := dbtest.New(t)
	days := []time.Time{adapter.Date(2024, 1, 17), adapter.Date(2024, 1, 18), adapter.Date(2024, 1, 19),
		adapter.Date(2024, 1, 22), adapter.Date(2024, 1, 23), adapter.Date(2024, 1, 24)}
	typoDay := days[3]
	for _, day := range days {
		if day.Equal(days[4]) {
			continue
		}
		leone := 3.26
		if day.Equal(typoDay) {
			leone = 43.26
		}
		insertSeries(t, conn, "CBG", "USD", "GMD", []observation{{day, 70.0}})
		insertSeries(t, conn, "CBG", "SLE", "GMD", []observation{{day, leone}})
	}
	if _, err := rates.RefreshSpikes(ctx, conn, "CBG", days); err != nil {
		t.Fatal(err)
	}
	if got := spikeDates(t, conn, "SELECT date FROM rate_spikes"); len(got) != 1 {
		t.Fatalf("spikes %v, want one", got)
	}

	insertSeries(t, conn, "CBG", "SLE", "GMD", []observation{{days[4], 43.26}})

	changed, err := rates.RefreshSpikes(ctx, conn, "CBG", []time.Time{days[4]})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.EqualFunc(changed, []time.Time{typoDay}, time.Time.Equal) {
		t.Errorf("changed %v, want [%s]", changed, db.FormatDate(typoDay))
	}
	if got := spikeDates(t, conn, "SELECT date FROM rate_spikes"); len(got) != 0 {
		t.Errorf("spikes %v, want none", got)
	}
}

func TestSpanSQLHoldsEveryDateOfTheBucket(t *testing.T) {
	conn := dbtest.New(t)
	for _, p := range []rates.Precision{rates.Week, rates.Month} {
		var outside int
		err := conn.QueryRow(`WITH RECURSIVE days(d) AS (SELECT '2015-12-25' UNION ALL
			SELECT date(d, '+1 day') FROM days WHERE d < '2018-01-07')
			SELECT count(*) FROM (SELECT d AS date, ` + rates.BucketSQL(p, "d") + ` AS bucket FROM days)
			WHERE NOT (` + rates.SpanSQL(p, "bucket", "date") + `)`).Scan(&outside)
		if err != nil {
			t.Fatal(err)
		}
		if outside != 0 {
			t.Errorf("%s: %d dates outside their bucket's span", p, outside)
		}
	}
}
