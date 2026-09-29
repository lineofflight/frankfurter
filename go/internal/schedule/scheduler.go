// Package schedule is bin/schedule: the long-running process that backfills
// every provider at startup and again on its publish_schedule, flushes
// debounced cache purges, re-blends the trailing window at midnight and
// materialises the blend on installs that have never built it. Scheduler is the
// small part of rufus-scheduler it needs.
package schedule

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adhocore/gronx"
)

// Func is a job body. A job that has done its work for good calls
// job.Unschedule.
type Func func(ctx context.Context, job *Job) error

// Options tune Every and Cron jobs.
type Options struct {
	// FirstIn delays an Every job's first run; zero means one interval.
	FirstIn time.Duration
	// NoOverlap skips a run while the job's previous run is still going (rufus
	// overlap: false).
	NoOverlap bool
}

// Registrar is what Setup schedules jobs on. Scheduler is the real one; tests
// record.
type Registrar interface {
	In(name string, delay time.Duration, fn Func)
	Every(name string, interval time.Duration, opts Options, fn Func)
	Cron(name, expr string, opts Options, fn Func) error
}

// Job is one scheduled job.
type Job struct {
	Name        string
	opts        Options
	fn          Func
	next        func(now time.Time) (time.Time, bool)
	running     atomic.Bool
	unscheduled atomic.Bool
}

// Unschedule stops future runs. A run in progress finishes.
func (j *Job) Unschedule() { j.unscheduled.Store(true) }

// Scheduler runs jobs on timers, at most workers at a time (rufus
// max_work_threads). Jobs registered before Run start with it. Failures are
// logged and the job stays scheduled, as rufus does.
type Scheduler struct {
	log     *slog.Logger
	workers chan struct{}
	jobs    []*Job
	now     func() time.Time
}

// New returns a scheduler that runs up to workers jobs at once.
func New(workers int, log *slog.Logger) *Scheduler {
	if log == nil {
		log = slog.Default()
	}
	return &Scheduler{log: log, workers: make(chan struct{}, max(1, workers)), now: time.Now}
}

// In runs fn once after delay.
func (s *Scheduler) In(name string, delay time.Duration, fn Func) {
	fired := false
	s.add(&Job{Name: name, fn: fn, next: func(now time.Time) (time.Time, bool) {
		if fired {
			return time.Time{}, false
		}
		fired = true
		return now.Add(delay), true
	}})
}

// Every runs fn every interval, first after opts.FirstIn (or one interval).
func (s *Scheduler) Every(name string, interval time.Duration, opts Options, fn Func) {
	first := true
	s.add(&Job{Name: name, opts: opts, fn: fn, next: func(now time.Time) (time.Time, bool) {
		if first && opts.FirstIn > 0 {
			first = false
			return now.Add(opts.FirstIn), true
		}
		first = false
		return now.Add(interval), true
	}})
}

// Cron runs fn at each fire time of a five-field cron expression, in the local
// time zone as rufus does.
func (s *Scheduler) Cron(name, expr string, opts Options, fn Func) error {
	if !gronx.IsValid(expr) {
		return fmt.Errorf("%s: invalid cron %q", name, expr)
	}
	s.add(&Job{Name: name, opts: opts, fn: fn, next: func(now time.Time) (time.Time, bool) {
		t, err := gronx.NextTickAfter(expr, now, false)
		return t, err == nil
	}})
	return nil
}

func (s *Scheduler) add(j *Job) { s.jobs = append(s.jobs, j) }

// Run starts every registered job and blocks until ctx is done and running jobs
// have returned.
func (s *Scheduler) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, j := range s.jobs {
		wg.Go(func() { s.loop(ctx, j, &wg) })
	}
	<-ctx.Done()
	wg.Wait()
}

func (s *Scheduler) loop(ctx context.Context, j *Job, wg *sync.WaitGroup) {
	for !j.unscheduled.Load() {
		at, ok := j.next(s.now())
		if !ok {
			return
		}
		timer := time.NewTimer(time.Until(at))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if j.unscheduled.Load() {
			return
		}
		if j.opts.NoOverlap && !j.running.CompareAndSwap(false, true) {
			continue
		}
		wg.Go(func() {
			if j.opts.NoOverlap {
				defer j.running.Store(false)
			}
			s.run(ctx, j)
		})
	}
}

func (s *Scheduler) run(ctx context.Context, j *Job) {
	select {
	case s.workers <- struct{}{}:
	case <-ctx.Done():
		return
	}
	defer func() { <-s.workers }()
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("job panicked", "job", j.Name, "panic", r)
		}
	}()
	if err := j.fn(ctx, j); err != nil {
		s.log.Error("job failed", "job", j.Name, "error", err)
	}
}
