package currency

import (
	"sync"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/seeds"
)

// Defunct is a currency code that has been retired or redenominated, paired
// with the statutory date its rates stop being valid.
//
// The registry is a reactive safety net, not an exhaustive list of every
// defunct currency. A code only needs an entry when the Money gem still names
// it AND a provider keeps publishing it past the changeover. Scopes use these
// dates to keep those observations out of blends and currency catalogues while
// retaining the provider's history.
type Defunct = seeds.Defunct

// Nascent is a currency code whose published history reaches back before the
// currency existed, paired with the date it came into being. Providers
// sometimes backfill a successor's series with its predecessor's values under
// the successor code (the Riksbank labels pre-1999 ECU values as EUR).
// Validation relabels those rows with the known predecessor and drops them only
// when there is none.
type Nascent = seeds.Nascent

var defunct = sync.OnceValues(func() ([]Defunct, map[string]Defunct) {
	all, err := seeds.DefunctCurrencies()
	if err != nil {
		panic("currency: " + err.Error())
	}
	by := make(map[string]Defunct, len(all))
	for _, e := range all {
		by[e.ISOCode] = e
	}
	return all, by
})

// DefunctCurrencies returns every defunct entry in seed order. Callers must not
// modify it.
func DefunctCurrencies() []Defunct {
	all, _ := defunct()
	return all
}

// FindDefunct returns the defunct entry for code.
func FindDefunct(code string) (Defunct, bool) {
	_, by := defunct()
	e, ok := by[code]
	return e, ok
}

// Expired reports whether date is on or after code's terminal date.
func Expired(code string, date time.Time) bool {
	e, ok := FindDefunct(code)
	return ok && !date.Before(e.TerminalDate)
}

var nascent = sync.OnceValues(func() ([]Nascent, map[string]Nascent) {
	all, err := seeds.NascentCurrencies()
	if err != nil {
		panic("currency: " + err.Error())
	}
	by := make(map[string]Nascent, len(all))
	for _, e := range all {
		by[e.ISOCode] = e
	}
	return all, by
})

// NascentCurrencies returns every nascent entry in seed order. Callers must not
// modify it.
func NascentCurrencies() []Nascent {
	all, _ := nascent()
	return all
}

// FindNascent returns the nascent entry for code.
func FindNascent(code string) (Nascent, bool) {
	_, by := nascent()
	e, ok := by[code]
	return e, ok
}

// Premature reports whether date is before code came into being.
func Premature(code string, date time.Time) bool {
	e, ok := FindNascent(code)
	return ok && date.Before(e.InceptionDate)
}
