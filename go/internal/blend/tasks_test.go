package blend

import (
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/dbtest"
)

func TestScanConsensusCountsOutliersPerProviderAndQuote(t *testing.T) {
	conn := dbtest.New(t)
	for i, date := range []time.Time{adapter.Date(2024, 1, 15), adapter.Date(2024, 1, 16)} {
		for _, p := range []struct {
			key  string
			rate float64
		}{{"A", 1.08}, {"B", 1.09}, {"C", 1.08}, {"D", 9.99}} {
			rate := p.rate
			if i == 1 && p.key == "D" {
				rate = 1.085 // back in line on the second day
			}
			insertRates(t, conn, []any{p.key, day(date), "EUR", "USD", rate})
		}
	}

	all, err := ScanConsensus(ctx, conn, time.Time{}, time.Time{}, adapter.Date(2024, 2, 1))
	if err != nil {
		t.Fatal(err)
	}
	if all.Dates != 2 || all.Total != 1 || len(all.Counts) != 1 || all.Counts[0] != (OutlierCount{"D", "USD", 1}) {
		t.Errorf("report = %+v", all)
	}

	year, err := ScanYearConsensus(ctx, conn, 2023, adapter.Date(2024, 2, 1))
	if err != nil {
		t.Fatal(err)
	}
	if year.Dates != 0 || year.Total != 0 {
		t.Errorf("2023 report = %+v", year)
	}
}
