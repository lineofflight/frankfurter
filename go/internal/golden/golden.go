// Package golden checks a Go adapter against rows the Ruby adapter produced for the same cassette, as recorded by
// go/scripts/golden.rb. See PORTING.md for the command that writes a golden file; never edit one by hand.
package golden

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

// Tolerance is the relative difference allowed between a Ruby and a Go rate.
const Tolerance = 1e-9

// numeric are the fields compared within Tolerance; every other field must be equal.
var numeric = map[string]bool{"rate": true, "bid": true, "ask": true, "mid": true}

// File is one golden recording.
type File struct {
	Adapter              string           `json:"adapter"`
	Cassette             string           `json:"cassette"`
	MatchRequestsOn      []string         `json:"match_requests_on"`
	AllowPlaybackRepeats bool             `json:"allow_playback_repeats"`
	Today                string           `json:"today"`
	Call                 string           `json:"call"`
	Rows                 []map[string]any `json:"rows"`
}

// Load reads a golden file.
func Load(t testing.TB, path string) File {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden: %v", err)
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("golden: %s: %v", path, err)
	}
	return f
}

// Client replays the file's cassette with the same matching the Ruby run used.
func (f File) Client(t testing.TB) *http.Client {
	t.Helper()
	matchers := map[string]vcrtest.Matcher{
		"method": vcrtest.Method, "host": vcrtest.Host, "path": vcrtest.Path, "uri": vcrtest.URI, "body": vcrtest.Body,
	}
	var on []vcrtest.Matcher
	for _, name := range f.MatchRequestsOn {
		m, ok := matchers[name]
		if !ok {
			t.Fatalf("golden: unknown matcher %q", name)
		}
		on = append(on, m)
	}
	opts := []vcrtest.Option{vcrtest.MatchOn(on...)}
	if f.AllowPlaybackRepeats {
		opts = append(opts, vcrtest.AllowPlaybackRepeats)
	}
	return vcrtest.Client(t, f.Cassette, opts...)
}

// Now returns a clock pinned to the file's --today, or nil when the Ruby run used the real date. Assign it to the
// adapter's Base.Now.
func (f File) Now(t testing.TB) func() time.Time {
	t.Helper()
	if f.Today == "" {
		return nil
	}
	today, err := time.Parse(time.DateOnly, f.Today)
	if err != nil {
		t.Fatalf("golden: today: %v", err)
	}
	return func() time.Time { return today.Add(12 * time.Hour) }
}

// Check fails the test unless got holds the same rows as the file: equal on every non-rate field, rates within
// Tolerance, in any order.
func (f File) Check(t testing.TB, got []adapter.Rate) {
	t.Helper()
	gotRows := make([]map[string]any, len(got))
	for i, r := range got {
		gotRows[i] = Row(r)
	}
	if diff := Diff(f.Rows, gotRows); diff != "" {
		t.Errorf("rows differ from Ruby (%s: %s)\n%s", f.Cassette, f.Call, diff)
	}
}

// Row renders a rate the way golden.rb renders a Ruby record.
func Row(r adapter.Rate) map[string]any {
	row := map[string]any{
		"date":  r.Date.Format(time.DateOnly),
		"base":  r.Base,
		"quote": r.Quote,
		"rate":  r.Rate,
	}
	for name, v := range map[string]*float64{"bid": r.Bid, "ask": r.Ask, "mid": r.Mid} {
		if v != nil {
			row[name] = *v
		}
	}
	return row
}

// Diff describes how got differs from want, or returns "" when they match. Rows pair up by their non-rate fields.
func Diff(want, got []map[string]any) string {
	wantBy, gotBy := group(want), group(got)
	var keys []string
	for k := range wantBy {
		keys = append(keys, k)
	}
	for k := range gotBy {
		if _, ok := wantBy[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	var b strings.Builder
	problems := 0
	report := func(format string, args ...any) {
		problems++
		if problems <= 30 {
			fmt.Fprintf(&b, format+"\n", args...)
		}
	}
	for _, k := range keys {
		w, g := wantBy[k], gotBy[k]
		for i := 0; i < max(len(w), len(g)); i++ {
			switch {
			case i >= len(g):
				report("  missing: %s", format(w[i]))
			case i >= len(w):
				report("  extra:   %s", format(g[i]))
			default:
				if field, ok := numbersMatch(w[i], g[i]); !ok {
					report("  %s: %s want %v, got %v", k, field, w[i][field], g[i][field])
				}
			}
		}
	}
	if problems == 0 {
		return ""
	}
	if problems > 30 {
		fmt.Fprintf(&b, "  ... and %d more\n", problems-30)
	}
	return fmt.Sprintf("want %d rows, got %d; %d problems:\n%s", len(want), len(got), problems, b.String())
}

// group buckets rows by their non-numeric fields, each bucket sorted by rate so duplicates pair up stably.
func group(rows []map[string]any) map[string][]map[string]any {
	by := map[string][]map[string]any{}
	for _, row := range rows {
		k := identity(row)
		by[k] = append(by[k], row)
	}
	for _, rs := range by {
		sort.SliceStable(rs, func(i, j int) bool { return number(rs[i]["rate"]) < number(rs[j]["rate"]) })
	}
	return by
}

func identity(row map[string]any) string {
	var names []string
	for name := range row {
		if !numeric[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	parts := make([]string, len(names))
	for i, name := range names {
		parts[i] = fmt.Sprintf("%s=%v", name, row[name])
	}
	var present []string
	for name := range numeric {
		if _, ok := row[name]; ok {
			present = append(present, name)
		}
	}
	sort.Strings(present)
	return strings.Join(parts, " ") + " [" + strings.Join(present, ",") + "]"
}

func numbersMatch(want, got map[string]any) (string, bool) {
	for name := range numeric {
		w, g := number(want[name]), number(got[name])
		if w == g {
			continue
		}
		if math.Abs(w-g) > Tolerance*math.Max(math.Abs(w), math.Abs(g)) {
			return name, false
		}
	}
	return "", true
}

func number(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case json.Number:
		f, _ := n.Float64()
		return f
	case int:
		return float64(n)
	}
	return 0
}

func format(row map[string]any) string {
	data, _ := json.Marshal(row)
	return string(data)
}
