// Package seeds embeds the config-as-data JSON of db/seeds: provider metadata, currency pegs, defunct and nascent
// currencies, and the Money gem patches.
//
// data/ is a copy of the repository's db/seeds (go:embed cannot reach outside the module). Refresh it with
// `go generate ./internal/seeds` after editing db/seeds; TestDataMatchesRepository fails while the copies differ.
package seeds

//go:generate sh -c "rm -rf data && cp -R ../../../db/seeds data"

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"time"
)

//go:embed data
var data embed.FS

// FS is the seed tree rooted at db/seeds.
var FS fs.FS

func init() {
	sub, err := fs.Sub(data, "data")
	if err != nil {
		panic(err)
	}
	FS = sub
}

// Provider is one db/seeds/providers/*.json file. Empty strings stand for keys the file leaves out (stored as NULL);
// Frequency defaults to "daily", as the providers table does.
type Provider struct {
	Key             string `json:"key"`
	Name            string `json:"name"`
	DataURL         string `json:"data_url"`
	TermsURL        string `json:"terms_url"`
	CoverageStart   string `json:"coverage_start"`
	PivotCurrency   string `json:"pivot_currency"`
	RateType        string `json:"rate_type"`
	CountryCode     string `json:"country_code"`
	PublishSchedule string `json:"publish_schedule"`
	PublishCadence  string `json:"publish_cadence"`
	Frequency       string `json:"frequency"`
}

// Providers returns every provider seed, sorted by file name.
func Providers() ([]Provider, error) {
	var out []Provider
	err := each("providers", func(name string, b []byte) error {
		var p Provider
		if err := json.Unmarshal(b, &p); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if p.Frequency == "" {
			p.Frequency = "daily"
		}
		out = append(out, p)
		return nil
	})
	return out, err
}

// Peg is one db/seeds/pegs/*.json file.
type Peg struct {
	Quote     string
	Base      string
	Rate      float64 // 1.0 when the file leaves it out
	Since     time.Time
	Authority string
	Source    string
}

// Pegs returns every peg, sorted by file name.
func Pegs() ([]Peg, error) {
	var out []Peg
	err := each("pegs", func(name string, b []byte) error {
		var raw struct {
			Quote, Base, Since, Authority, Source string
			Rate                                  *float64
		}
		if err := json.Unmarshal(b, &raw); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		since, err := time.Parse(time.DateOnly, raw.Since)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		rate := 1.0
		if raw.Rate != nil {
			rate = *raw.Rate
		}
		out = append(out, Peg{raw.Quote, raw.Base, rate, since, raw.Authority, raw.Source})
		return nil
	})
	return out, err
}

// Defunct is one entry of defunct_currencies.json.
type Defunct struct {
	ISOCode      string
	TerminalDate time.Time
	Successor    string  // empty when the seed has none
	Ratio        float64 // zero when the seed has none
	Source       string
	Note         string
}

// DefunctCurrencies returns defunct_currencies.json in file order.
func DefunctCurrencies() ([]Defunct, error) {
	var raw []struct {
		ISOCode      string  `json:"iso_code"`
		TerminalDate string  `json:"terminal_date"`
		Successor    string  `json:"successor"`
		Ratio        float64 `json:"ratio"`
		Source       string  `json:"source"`
		Note         string  `json:"note"`
	}
	if err := decode("defunct_currencies.json", &raw); err != nil {
		return nil, err
	}
	out := make([]Defunct, len(raw))
	for i, r := range raw {
		d, err := time.Parse(time.DateOnly, r.TerminalDate)
		if err != nil {
			return nil, fmt.Errorf("defunct %s: %w", r.ISOCode, err)
		}
		out[i] = Defunct{r.ISOCode, d, r.Successor, r.Ratio, r.Source, r.Note}
	}
	return out, nil
}

// Nascent is one entry of nascent_currencies.json.
type Nascent struct {
	ISOCode       string
	InceptionDate time.Time
	Predecessor   string // empty when the seed has none
	Source        string
	Note          string
}

// NascentCurrencies returns nascent_currencies.json in file order.
func NascentCurrencies() ([]Nascent, error) {
	var raw []struct {
		ISOCode       string `json:"iso_code"`
		InceptionDate string `json:"inception_date"`
		Predecessor   string `json:"predecessor"`
		Source        string `json:"source"`
		Note          string `json:"note"`
	}
	if err := decode("nascent_currencies.json", &raw); err != nil {
		return nil, err
	}
	out := make([]Nascent, len(raw))
	for i, r := range raw {
		d, err := time.Parse(time.DateOnly, r.InceptionDate)
		if err != nil {
			return nil, fmt.Errorf("nascent %s: %w", r.ISOCode, err)
		}
		out[i] = Nascent{r.ISOCode, d, r.Predecessor, r.Source, r.Note}
	}
	return out, nil
}

// CurrencyPatch is one entry of currency_patches.json. Nil fields are absent from the entry, so merging keeps the Money
// gem's value.
type CurrencyPatch struct {
	ISOCode       string  `json:"iso_code"`
	Name          *string `json:"name"`
	Symbol        *string `json:"symbol"`
	ISONumeric    *string `json:"iso_numeric"`
	SubunitToUnit *int    `json:"subunit_to_unit"`
}

// CurrencyPatches returns currency_patches.json in file order.
func CurrencyPatches() ([]CurrencyPatch, error) {
	var out []CurrencyPatch
	err := decode("currency_patches.json", &out)
	return out, err
}

func decode(name string, v any) error {
	b, err := fs.ReadFile(FS, name)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

// each calls fn with every *.json file in dir, sorted by name, as Ruby's Dir[] returns them.
func each(dir string, fn func(name string, b []byte) error) error {
	names, err := fs.Glob(FS, path.Join(dir, "*.json"))
	if err != nil {
		return err
	}
	sort.Strings(names)
	for _, name := range names {
		b, err := fs.ReadFile(FS, name)
		if err != nil {
			return err
		}
		if err := fn(name, b); err != nil {
			return err
		}
	}
	return nil
}
