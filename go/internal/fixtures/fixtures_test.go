package fixtures

import (
	"testing"
	"time"
)

func TestRoundHalfUpMatchesRuby(t *testing.T) {
	// Values from Ruby's Float#round.
	for _, c := range []struct {
		in   float64
		n    int
		want float64
	}{
		{1.0005, 3, 1.001},
		{2.675, 2, 2.68},
		{1.08 * 0.975, 4, 1.053},
		{0.86 * 1.049, 4, 0.9021},
		{81.0193 * 0.951, 4, 77.0494},
	} {
		if got := roundHalfUp(c.in, c.n); got != c.want {
			t.Errorf("roundHalfUp(%v, %d) = %v, want %v", c.in, c.n, got, c.want)
		}
	}
}

func TestJulianDay(t *testing.T) {
	if got := julianDay(time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)); got != 2460325 {
		t.Errorf("got %d", got)
	}
}

func TestHelpers(t *testing.T) {
	if wd := LatestDate().Weekday(); wd == time.Saturday || wd == time.Sunday {
		t.Errorf("latest date is a %s", wd)
	}
	if RecentSunday().Weekday() != time.Sunday || RecentSunday().After(Today()) {
		t.Errorf("recent Sunday %v", RecentSunday())
	}
	if f := PrecedingFriday(RecentSunday()); f.Weekday() != time.Friday || !f.Equal(RecentSunday().AddDate(0, 0, -2)) {
		t.Errorf("preceding Friday %v", f)
	}
	if m := GapBoundaryMonday(60); m.Weekday() != time.Monday || m.After(Today().AddDate(0, 0, -60)) {
		t.Errorf("gap boundary Monday %v", m)
	}
	if b := BusinessDay(30); b.After(Today().AddDate(0, 0, -30)) || b.Weekday() == time.Sunday {
		t.Errorf("business day %v", b)
	}
}

func TestNewSeedsFixture(t *testing.T) {
	conn := New(t)
	for _, c := range []struct {
		query string
		want  int
	}{
		{"SELECT count(*) FROM rates", BusinessDays * (11 + 4 + 2)},
		{"SELECT count(*) FROM rates WHERE provider = 'ECB' AND date = '" + LatestDate().Format(time.DateOnly) + "'", 11},
		{"SELECT count(DISTINCT provider) FROM weekly_rates", 3},
		{"SELECT count(*) FROM blended_rates", 0},
		{"SELECT count(*) FROM providers WHERE key IN ('ECB', 'BOC', 'BOJ')", 3},
		{"SELECT count(*) FROM currency_coverages WHERE provider_key = 'BOC'", 5},
	} {
		var n int
		if err := conn.QueryRow(c.query).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != c.want {
			t.Errorf("%s = %d, want %d", c.query, n, c.want)
		}
	}
}

func TestNewIsolatesTests(t *testing.T) {
	a, b := New(t), New(t)
	if _, err := a.Exec("DELETE FROM rates"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := b.QueryRow("SELECT count(*) FROM rates").Scan(&n); err != nil || n == 0 {
		t.Errorf("second database saw the delete: %d, %v", n, err)
	}
}
