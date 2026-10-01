package provider

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"time"
)

// Backfiller is what BackfillTask drives; *Ingester is one.
type Backfiller interface {
	Backfill(ctx context.Context, p Provider)
	BackfillAfter(ctx context.Context, p Provider, after time.Time)
}

// BackfillTask is the backfill rake task. With a name it backfills that
// provider (matched case-insensitively); otherwise every provider, in random
// order, on up to workers goroutines. full starts each provider at its own
// coverage_start instead of after its newest stored rate.
func BackfillTask(ctx context.Context, b Backfiller, providers []Provider, name string, full bool, workers int) error {
	run := func(p Provider) {
		if full {
			b.BackfillAfter(ctx, p, p.coverageCursor())
		} else {
			b.Backfill(ctx, p)
		}
	}

	if name != "" {
		for _, p := range providers {
			if strings.EqualFold(p.Key, name) {
				run(p)
				return nil
			}
		}
		return fmt.Errorf("unknown provider: %s", name)
	}

	queue := make(chan Provider, len(providers))
	for _, i := range rand.Perm(len(providers)) {
		queue <- providers[i]
	}
	close(queue)
	var wg sync.WaitGroup
	for range max(1, min(len(providers), workers)) {
		wg.Go(func() {
			for p := range queue {
				run(p)
			}
		})
	}
	wg.Wait()
	return nil
}
