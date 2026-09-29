package provider

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/lineofflight/frankfurter/go/internal/db"
)

// testdata/publishes_missed.json holds Ruby's Provider#publishes_missed for every seeded schedule and cadence over a
// grid of end dates and reference offsets, recorded under UTC by go/scripts/publishes_missed.rb.
func TestPublishesMissedMatchesRuby(t *testing.T) {
	b, err := os.ReadFile("testdata/publishes_missed.json")
	if err != nil {
		t.Fatal(err)
	}
	var grid struct {
		Start   string
		Step    int
		Offsets []int
		Combos  []struct {
			Schedule, Cadence string
			Counts            [][]int
		}
	}
	if err := json.Unmarshal(b, &grid); err != nil {
		t.Fatal(err)
	}
	start, err := db.ParseDate(grid.Start)
	if err != nil {
		t.Fatal(err)
	}
	cases, failures := 0, 0
	for _, c := range grid.Combos {
		p := build(c.Schedule, c.Cadence)
		for i, counts := range c.Counts {
			end := start.AddDate(0, 0, i*grid.Step)
			for j, want := range counts {
				cases++
				ref := end.AddDate(0, 0, grid.Offsets[j])
				got, ok, err := p.MissedSince(db.FormatDate(end), ref)
				if err == nil && ok && got == want {
					continue
				}
				if failures++; failures <= 20 {
					t.Errorf("%s %s end %s ref %s: got %d (ok %v, err %v), Ruby %d", c.Schedule, c.Cadence,
						db.FormatDate(end), db.FormatDate(ref), got, ok, err, want)
				}
			}
		}
	}
	if failures > 0 {
		t.Errorf("%d of %d cases differ", failures, cases)
	}
	if cases < 60000 {
		t.Errorf("only %d cases", cases)
	}
}
