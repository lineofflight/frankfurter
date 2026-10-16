// Package currency holds the currency reference data: the Money gem's currency
// table with Frankfurter's patches, defunct and nascent currencies, pegs and
// peg anchoring, and the currencies catalogue backed by the database.
//
// It ports lib/currency_patches.rb, lib/defunct_currency.rb,
// lib/nascent_currency.rb, lib/peg.rb, lib/peg_anchor.rb and lib/currency.rb.
package currency

import (
	_ "embed"
	"encoding/json"
	"sort"
	"strings"
	"sync"

	"github.com/lineofflight/frankfurter/go/internal/seeds"
)

// Info is a Money::Currency: what the Money gem knows about a code.
type Info struct {
	ISOCode       string // may differ from the code looked up: GHC is an alias of GHS
	Name          string
	Symbol        string // empty when the gem has none
	ISONumeric    string // empty when the gem has none (Ruby nil)
	SubunitToUnit int
}

// money.json is the Money gem's table before patches, dumped by
// go/scripts/dump_money.rb.
//
//go:embed money.json
var moneyJSON []byte

var table = sync.OnceValue(func() map[string]Info {
	var raw map[string]struct {
		ISOCode       string `json:"iso_code"`
		Name          string `json:"name"`
		Symbol        string `json:"symbol"`
		ISONumeric    string `json:"iso_numeric"`
		SubunitToUnit int    `json:"subunit_to_unit"`
	}
	if err := json.Unmarshal(moneyJSON, &raw); err != nil {
		panic("currency: money.json: " + err.Error())
	}
	t := make(map[string]Info, len(raw))
	for id, r := range raw {
		t[id] = Info(r)
	}

	patches, err := seeds.CurrencyPatches()
	if err != nil {
		panic("currency: " + err.Error())
	}
	for _, p := range patches {
		applyPatch(t, p)
	}
	return t
})

// applyPatch mirrors currency_patches.rb: merge the patch over an existing
// entry (keeping what it leaves out), or register it fresh.
func applyPatch(t map[string]Info, p seeds.CurrencyPatch) {
	id := strings.ToUpper(p.ISOCode)
	info := t[id]
	info.ISOCode = p.ISOCode
	if p.Name != nil {
		info.Name = *p.Name
	}
	if p.Symbol != nil {
		info.Symbol = *p.Symbol
	}
	if p.ISONumeric != nil {
		info.ISONumeric = *p.ISONumeric
	}
	if p.SubunitToUnit != nil {
		info.SubunitToUnit = *p.SubunitToUnit
	}
	t[id] = info
}

// Find is Money::Currency.find: the entry for code, case-insensitively,
// including aliases.
func Find(code string) (Info, bool) {
	info, ok := table()[strings.ToUpper(code)]
	return info, ok
}

// Named reports whether the Money gem can name code.
func Named(code string) bool {
	_, ok := Find(code)
	return ok
}

var codes = sync.OnceValue(func() []string {
	t := table()
	out := make([]string, 0, len(t))
	for id := range t {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
})

// Codes returns every code the Money gem can name, aliases included, sorted.
// Callers must not modify it.
func Codes() []string { return codes() }
