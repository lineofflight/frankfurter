// Package heavyslots caps concurrent heavy range computes per process (#650). Live-path daily ranges recompute the
// blend per date, so a few full-history single-provider requests can hold every worker for the better part of a minute
// while cheap shapes queue behind them; the request deadline bounds each of them, not their sum. Acquire never blocks:
// past the cap the request fails fast with ErrBusy (a 503 with Retry-After) instead of waiting.
package heavyslots

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
)

// RetryAfterSeconds is the Retry-After a Busy response carries.
const RetryAfterSeconds = 30

// ErrBusy reports that every slot is held.
var ErrBusy = errors.New("heavy compute slots busy")

// DefaultMax is MAX_HEAVY_COMPUTES, or 2. An unparseable value panics at startup, as Ruby's Integer() raises on load.
var DefaultMax = defaultMax()

func defaultMax() int {
	s, ok := os.LookupEnv("MAX_HEAVY_COMPUTES")
	if !ok {
		return 2
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		panic(fmt.Sprintf("MAX_HEAVY_COMPUTES: %v", err))
	}
	return n
}

// Slots is a non-blocking counting semaphore.
type Slots struct {
	max  int
	mu   sync.Mutex
	held int
}

// New returns slots capped at limit.
func New(limit int) *Slots { return &Slots{max: limit} }

// Max is the cap.
func (s *Slots) Max() int { return s.max }

// Held is how many slots are taken.
func (s *Slots) Held() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.held
}

// TryAcquire takes a slot and reports whether one was free.
func (s *Slots) TryAcquire() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.held >= s.max {
		return false
	}
	s.held++
	return true
}

// Release returns a slot. It never counts below zero.
func (s *Slots) Release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.held > 0 {
		s.held--
	}
}
