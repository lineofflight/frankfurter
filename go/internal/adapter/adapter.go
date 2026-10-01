// Package adapter defines what a provider adapter is, the helpers every adapter
// shares, and the registry that maps provider keys to adapters. It mirrors
// lib/provider/adapters/adapter.rb.
//
// An adapter is pure fetch and parse: it turns a date window into rate rows and
// never touches the database. Validation, precision normalisation and storage
// belong to the caller.
package adapter

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"
)

// GramsPerTroyOunce converts per-gram precious-metal prices to the
// per-troy-ounce convention ISO 4217 uses for XAU, XAG, XPT and XPD.
const GramsPerTroyOunce = 31.1034768

// Rate is one observation as a source publishes it: Quote units per one Base
// unit on Date.
type Rate struct {
	Date  time.Time // UTC midnight
	Base  string
	Quote string
	Rate  float64

	// Published components, per one Base unit, when the source quotes a spread.
	// Nil means not published. A zero is a real published value (BOJA publishes
	// a zero bid on some days).
	Bid *float64
	Ask *float64
	Mid *float64
}

// Adapter fetches rates from one provider.
//
// Fetch returns rows dated after `after` (exclusive) through `upto`
// (inclusive). A zero time leaves that side open: zero `after` means from the
// start of what the source serves, zero `upto` means through the latest
// publication. Individual adapters may clip further, as their Ruby counterparts
// do.
//
// Embed Base to get the defaults for the other methods and override the ones a
// provider needs.
type Adapter interface {
	Fetch(ctx context.Context, after, upto time.Time) ([]Rate, error)

	// BackfillRange is the window, in days, that FetchEach asks for per call.
	// Zero fetches everything in one call.
	BackfillRange() int

	// LeadDays is how many days ahead of today a row may legitimately be dated,
	// beyond the universal grace window.
	LeadDays() int

	// Revises reports whether the source may replace an already-published value
	// in place, so backfill compares fetched rows against stored ones and warns
	// on drift.
	Revises() bool
}

// FetchEach walks from after to today in BackfillRange windows, calling yield
// with each non-empty batch. The last window is open-ended. It returns
// immediately when after is today or later.
//
// Each window's after is the previous window's upto, so an adapter that takes
// after as exclusive still gets the day following upto. One that takes it as
// inclusive refetches upto, which the insert skips. A one-day range steps past
// upto instead: only an inclusive adapter can use it, and it would otherwise
// never advance.
func FetchEach(ctx context.Context, a Adapter, after, today time.Time, yield func([]Rate) error) error {
	if !after.IsZero() && !after.Before(today) {
		return nil
	}
	for {
		var upto time.Time
		days := a.BackfillRange()
		if !after.IsZero() && days > 0 {
			upto = after.AddDate(0, 0, days-1)
			if !upto.Before(today) {
				upto = time.Time{}
			}
		}
		rates, err := a.Fetch(ctx, after, upto)
		if err != nil {
			return err
		}
		if len(rates) > 0 {
			if err := yield(rates); err != nil {
				return err
			}
		}
		if upto.IsZero() {
			return nil
		}
		if days > 1 {
			after = upto
		} else {
			after = upto.AddDate(0, 0, 1)
		}
	}
}

// Constructor builds an adapter that makes its requests with client.
type Constructor func(client *http.Client) Adapter

var (
	mu       sync.RWMutex
	registry = map[string]Constructor{}
)

// Register makes an adapter available under its provider key (upper case, as in
// db/seeds/providers). Adapter packages call it from init. It panics on a
// duplicate key.
func Register(key string, c Constructor) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := registry[key]; dup {
		panic(fmt.Sprintf("adapter: %s registered twice", key))
	}
	registry[key] = c
}

// Lookup returns the constructor registered under key.
func Lookup(key string) (Constructor, bool) {
	mu.RLock()
	defer mu.RUnlock()
	c, ok := registry[key]
	return c, ok
}

// All returns the registered provider keys, sorted.
func All() []string {
	mu.RLock()
	defer mu.RUnlock()
	keys := make([]string, 0, len(registry))
	for k := range registry {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
