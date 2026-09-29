package schedule

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var quiet = slog.New(slog.DiscardHandler)

func TestSchedulerRunsAnInJobOnce(t *testing.T) {
	s := New(2, quiet)
	ctx, cancel := context.WithCancel(context.Background())
	var runs atomic.Int32
	s.In("once", 0, func(context.Context, *Job) error {
		if runs.Add(1) == 1 {
			cancel()
		}
		return nil
	})
	s.Run(ctx)
	if runs.Load() != 1 {
		t.Fatalf("ran %d times", runs.Load())
	}
}

func TestSchedulerStopsAnEveryJobThatUnschedulesItself(t *testing.T) {
	s := New(2, quiet)
	var runs atomic.Int32
	done := make(chan struct{})
	s.Every("repeat", time.Millisecond, Options{FirstIn: time.Millisecond, NoOverlap: true},
		func(_ context.Context, job *Job) error {
			if runs.Add(1) == 3 {
				job.Unschedule()
				close(done)
			}
			return errors.New("logged and retried")
		})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-done; cancel() }()
	s.Run(ctx)
	if runs.Load() != 3 {
		t.Fatalf("ran %d times", runs.Load())
	}
}

// With NoOverlap a slow run makes the scheduler skip ticks instead of starting a second copy.
func TestSchedulerNeverOverlapsANoOverlapJob(t *testing.T) {
	s := New(4, quiet)
	var active, peak, runs atomic.Int32
	release := make(chan struct{})
	s.Every("slow", time.Millisecond, Options{NoOverlap: true}, func(context.Context, *Job) error {
		n := active.Add(1)
		for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
		}
		<-release
		active.Add(-1)
		runs.Add(1)
		return nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for range 5 {
			release <- struct{}{}
		}
		cancel()
		close(release)
	}()
	s.Run(ctx)
	if peak.Load() != 1 || runs.Load() < 5 {
		t.Fatalf("peak %d, runs %d", peak.Load(), runs.Load())
	}
}

func TestSchedulerRunsAtMostWorkersJobsAtOnce(t *testing.T) {
	s := New(1, quiet)
	var active, peak atomic.Int32
	var wg sync.WaitGroup
	wg.Add(3)
	for range 3 {
		s.In("job", 0, func(context.Context, *Job) error {
			defer wg.Done()
			n := active.Add(1)
			for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
			}
			active.Add(-1)
			return nil
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { wg.Wait(); cancel() }()
	s.Run(ctx)
	if peak.Load() != 1 {
		t.Fatalf("peak %d", peak.Load())
	}
}

func TestSchedulerSurvivesAPanickingJob(t *testing.T) {
	s := New(1, quiet)
	ctx, cancel := context.WithCancel(context.Background())
	s.In("panics", 0, func(context.Context, *Job) error { panic("boom") })
	s.In("after", time.Millisecond, func(context.Context, *Job) error { cancel(); return nil })
	s.Run(ctx)
}

func TestSchedulerRejectsAnInvalidCron(t *testing.T) {
	if err := New(1, quiet).Cron("bad", "not a cron", Options{}, nil); err == nil {
		t.Fatal("want an error")
	}
}

func TestSchedulerFiresCronJobsAtTheirNextTick(t *testing.T) {
	s := New(1, quiet)
	if err := s.Cron("fri", "*/30 14-16 * * 1-5", Options{}, nil); err != nil {
		t.Fatal(err)
	}
	// Friday 16:45 UTC: the next tick is Monday 14:00.
	next, ok := s.jobs[0].next(time.Date(2026, 4, 17, 16, 45, 0, 0, time.UTC))
	if !ok || !next.Equal(time.Date(2026, 4, 20, 14, 0, 0, 0, time.UTC)) {
		t.Fatalf("next %v", next)
	}
}
