package blend

// The two spec/rate_scopes_spec.rb cases that assert on the grouped blend
// tables. internal/rates ports the rest and checks these through the blend
// inputs; here they run against the materialized tables themselves.

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/fixtures"
)

var terminalCases = []struct {
	r        Rollup
	code     string
	before   time.Time
	terminal time.Time
}{
	{Weekly, "BYR", time.Date(2016, 6, 30, 0, 0, 0, 0, time.UTC), time.Date(2016, 7, 1, 0, 0, 0, 0, time.UTC)},
	{Monthly, "VEF", time.Date(2018, 8, 19, 0, 0, 0, 0, time.UTC), time.Date(2018, 8, 20, 0, 0, 0, 0, time.UTC)},
}

func TestGroupedBlendsIgnoreExpiredRowsInSharedBucket(t *testing.T) {
	for _, c := range terminalCases {
		for _, side := range []string{"base", "quote"} {
			base, quote := c.code, "USD"
			if side == "quote" {
				base, quote = "USD", c.code
			}
			t.Run(fmt.Sprintf("%s %s", c.r.Table, side), func(t *testing.T) {
				conn := fixtures.New(t)
				bucket := bucketOf(t, conn, c.r.Source.Precision, c.before)
				insertRates(t, conn, []any{"ECB", day(c.before), base, quote, 15.123456789012})
				refreshProvider(t, conn, "ECB", c.before)
				original := snapshot(t, conn, c.r.Table, "bucket_date = ?", bucket)
				if len(original) == 0 {
					t.Fatal("no blend before the terminal date")
				}

				insertRates(t, conn, []any{"ECB", day(c.terminal), base, quote, 3000.0},
					[]any{"ECB", day(c.terminal), "USD", "SDR", 5000.0})
				refreshProvider(t, conn, "ECB", c.before, c.terminal)

				published := queryFloat(t, conn, "SELECT rate FROM "+c.r.Source.Name+" WHERE provider = 'ECB' AND "+
					"bucket_date = ? AND base = ? AND quote = ?", bucket, base, quote)
				if published != (15.123456789012+3000.0)/2 {
					t.Errorf("published %v", published)
				}
				if got := snapshot(t, conn, c.r.Table, "bucket_date = ?", bucket); !slices.Equal(got, original) {
					t.Errorf("blend changed:\n%v\n%v", original, got)
				}
			})
		}
	}
}

func TestGroupedBlendsOmitBucketsOfOnlyExpiredObservations(t *testing.T) {
	for _, c := range terminalCases {
		t.Run(c.r.Table, func(t *testing.T) {
			conn := fixtures.New(t)
			insertRates(t, conn, []any{"ECB", day(c.terminal), "USD", c.code, 30.0})
			refreshProvider(t, conn, "ECB", c.terminal)
			bucket := bucketOf(t, conn, c.r.Source.Precision, c.terminal)

			if n := queryInt(t, conn, "SELECT count(*) FROM "+c.r.Source.Name+" WHERE bucket_date = ? AND quote = ?",
				bucket, c.code); n != 1 {
				t.Errorf("source rows = %d", n)
			}
			if n := queryInt(t, conn, "SELECT count(*) FROM "+c.r.Table+" WHERE bucket_date = ? AND quote = ?", bucket,
				c.code); n != 0 {
				t.Errorf("blended rows = %d", n)
			}
		})
	}
}
