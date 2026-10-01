package provider

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/fixtures"
)

type call struct {
	key   string
	after *time.Time // nil: incremental Backfill
}

type recordingBackfiller struct {
	mu    sync.Mutex
	calls []call
}

func (r *recordingBackfiller) Backfill(_ context.Context, p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, call{key: p.Key})
}

func (r *recordingBackfiller) BackfillAfter(_ context.Context, p Provider, after time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, call{key: p.Key, after: &after})
}

func seeded(t *testing.T, keys ...string) []Provider {
	t.Helper()
	conn := fixtures.New(t)
	var out []Provider
	for _, k := range keys {
		p, err := Find(context.Background(), conn, k)
		if err != nil || p == nil {
			t.Fatalf("%s: %v", k, err)
		}
		out = append(out, *p)
	}
	return out
}

func TestBackfillTaskStartsANamedProviderAtCoverageStartForFullHistory(t *testing.T) {
	providers := seeded(t, "BOC", "ECB")
	b := &recordingBackfiller{}
	if err := BackfillTask(context.Background(), b, providers, "ecb", true, 4); err != nil {
		t.Fatal(err)
	}
	// The cursor is exclusive, so the day before fetches coverage_start itself.
	if len(b.calls) != 1 || b.calls[0].key != "ECB" || b.calls[0].after == nil ||
		!b.calls[0].after.Equal(providers[1].CoverageStart.AddDate(0, 0, -1)) || providers[1].CoverageStart.IsZero() {
		t.Fatalf("calls %+v", b.calls)
	}
}

func TestBackfillTaskStartsEveryProviderAtItsOwnCoverageStartForFullHistory(t *testing.T) {
	providers := seeded(t, "ECB", "BOC")
	b := &recordingBackfiller{}
	if err := BackfillTask(context.Background(), b, providers, "", true, 4); err != nil {
		t.Fatal(err)
	}
	got := map[string]time.Time{}
	for _, c := range b.calls {
		if c.after == nil {
			t.Fatalf("%s backfilled incrementally", c.key)
		}
		got[c.key] = *c.after
	}
	if len(b.calls) != 2 || !got["ECB"].Equal(providers[0].CoverageStart.AddDate(0, 0, -1)) ||
		!got["BOC"].Equal(providers[1].CoverageStart.AddDate(0, 0, -1)) {
		t.Fatalf("calls %+v", b.calls)
	}
}

func TestBackfillTaskKeepsTheIncrementalDefault(t *testing.T) {
	providers := seeded(t, "ECB")
	b := &recordingBackfiller{}
	if err := BackfillTask(context.Background(), b, providers, "ecb", false, 4); err != nil {
		t.Fatal(err)
	}
	if len(b.calls) != 1 || b.calls[0] != (call{key: "ECB"}) {
		t.Fatalf("calls %+v", b.calls)
	}
}

func TestBackfillTaskRejectsAnUnknownProvider(t *testing.T) {
	b := &recordingBackfiller{}
	if err := BackfillTask(context.Background(), b, seeded(t, "ECB"), "nope", false, 4); err == nil {
		t.Fatal("want an error")
	}
	if len(b.calls) != 0 {
		t.Fatalf("calls %+v", b.calls)
	}
}
