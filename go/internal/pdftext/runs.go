package pdftext

import (
	"math"
	"slices"
	"sort"
)

// runs applies pdf-reader's PageTextReceiver#runs pipeline to raw glyphs: keep
// those whose origin lies in the crop box, drop zero-width and overlapping
// duplicates (text painted twice to fake bold), and merge the rest into runs.
func runs(glyphs []Run, crop Rect) []Run {
	kept := glyphs[:0:0]
	for _, g := range glyphs {
		if crop.contains(g.X, g.Y) && g.Width != 0 && g.Text != "" {
			kept = append(kept, g)
		}
	}
	return merge(withoutOverlaps(kept))
}

// withoutOverlaps is pdf-reader's OverlappingRunsFilter: a sweep over x that
// drops a run when an open run with the same text covers at least half of it.
func withoutOverlaps(glyphs []Run) []Run {
	type event struct {
		x     float64
		run   int
		start bool
	}
	events := make([]event, 0, 2*len(glyphs))
	for i, g := range glyphs {
		events = append(events, event{g.X, i, true}, event{g.EndX(), i, false})
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].x < events[j].x })

	excluded := make([]bool, len(glyphs))
	var open []int
	for _, e := range events {
		if !e.start {
			if i := slices.Index(open, e.run); i >= 0 {
				open = slices.Delete(open, i, i+1)
			}
			continue
		}
		run := glyphs[e.run]
		for _, o := range open {
			other := glyphs[o]
			if other.Text == run.Text && e.x >= other.X && e.x <= other.EndX() && overlapShare(other, run) >= 0.5 {
				excluded[e.run] = true
				break
			}
		}
		open = append(open, e.run)
	}

	kept := glyphs[:0:0]
	for i, g := range glyphs {
		if !excluded[i] {
			kept = append(kept, g)
		}
	}
	return kept
}

// overlapShare is the fraction of a's area that b covers.
func overlapShare(a, b Run) float64 {
	if !(a.X <= b.EndX() && a.EndX() >= b.X && a.EndY() >= b.Y && a.Y <= b.EndY()) {
		return 0
	}
	dx := math.Min(a.EndX(), b.EndX()) - math.Max(a.X, b.X)
	dy := math.Min(a.EndY(), b.EndY()) - math.Max(a.Y, b.Y)
	return dx * dy / ((a.EndX() - a.X) * (a.EndY() - a.Y))
}

// merge groups glyphs by truncated baseline, joins neighbours left to right,
// and returns the runs top to bottom, then left to right.
func merge(glyphs []Run) []Run {
	lines := map[int][]Run{}
	var order []int
	for _, g := range glyphs {
		y := int(g.Y) // Ruby's to_i truncates towards zero
		if _, ok := lines[y]; !ok {
			order = append(order, y)
		}
		lines[y] = append(lines[y], g)
	}

	var merged []Run
	for _, y := range order {
		line := lines[y]
		sortRuns(line)
		var out []Run
		for _, g := range line {
			if n := len(out); n > 0 && mergeable(out[n-1], g) {
				out[n-1] = join(out[n-1], g)
			} else {
				out = append(out, g)
			}
		}
		merged = append(merged, out...)
	}
	sortRuns(merged)
	return merged
}

// sortRuns orders runs as pdf-reader's TextRun#<=>: higher on the page first,
// then left to right.
func sortRuns(rs []Run) {
	sort.SliceStable(rs, func(i, j int) bool {
		if rs[i].Y != rs[j].Y {
			return rs[i].Y > rs[j].Y
		}
		return rs[i].X < rs[j].X
	})
}

func mergeable(a, b Run) bool {
	return int(a.Y) == int(b.Y) && a.FontSize == b.FontSize && b.X >= a.EndX()-3 && b.X <= a.EndX()+a.FontSize
}

func join(a, b Run) Run {
	text := a.Text + b.Text
	if b.X-a.EndX() >= a.FontSize*0.2 {
		text = a.Text + " " + b.Text
	}
	return Run{X: a.X, Y: a.Y, Width: b.EndX() - a.X, FontSize: a.FontSize, Text: text}
}
