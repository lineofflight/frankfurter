// Package vcrtest replays the converted Ruby VCR cassettes in go/testdata/cassettes.
//
// Client mirrors a Ruby spec's VCR.insert_cassette call:
//
//	# Ruby
//	VCR.insert_cassette("banrep", match_requests_on: [:method, :host], allow_playback_repeats: true)
//
//	// Go
//	client := vcrtest.Client(t, "banrep", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host), vcrtest.AllowPlaybackRepeats)
//
// As in VCR, each recorded interaction plays once unless repeats are allowed, the first unused match wins, and a
// request that matches nothing fails.
package vcrtest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/dnaeon/go-vcr.v4/pkg/cassette"
	"gopkg.in/dnaeon/go-vcr.v4/pkg/recorder"
)

// Matcher compares one aspect of a live request with a recorded one.
type Matcher func(r *http.Request, body []byte, rec cassette.Request) bool

// The matchers used by match_requests_on in spec/.
var (
	// Method compares HTTP methods.
	Method Matcher = func(r *http.Request, _ []byte, rec cassette.Request) bool {
		return strings.EqualFold(r.Method, rec.Method)
	}

	// Host compares host names.
	Host Matcher = func(r *http.Request, _ []byte, rec cassette.Request) bool {
		u, err := url.Parse(rec.URL)
		return err == nil && strings.EqualFold(r.URL.Hostname(), u.Hostname())
	}

	// Path compares URL paths.
	Path Matcher = func(r *http.Request, _ []byte, rec cassette.Request) bool {
		u, err := url.Parse(rec.URL)
		return err == nil && r.URL.Path == u.Path
	}

	// URI compares scheme, host, port, path and query. Query parameters compare as decoded name/value sets, so order
	// and escaping differences between http.rb (recorded through WebMock, which sorts them) and net/url don't matter.
	URI Matcher = func(r *http.Request, _ []byte, rec cassette.Request) bool {
		u, err := url.Parse(unsecret(rec.URL))
		if err != nil {
			return false
		}
		live, err := url.Parse(unsecret(r.URL.String()))
		if err != nil {
			return false
		}
		return strings.EqualFold(live.Scheme, u.Scheme) &&
			strings.EqualFold(live.Hostname(), u.Hostname()) &&
			port(live) == port(u) &&
			live.Path == u.Path &&
			reflect.DeepEqual(live.Query(), u.Query())
	}

	// Body compares request bodies exactly, or as equal form fields or equal JSON when both sides parse as such.
	Body Matcher = func(_ *http.Request, body []byte, rec cassette.Request) bool {
		live, recorded := unsecret(string(body)), unsecret(rec.Body)
		if live == recorded {
			return true
		}
		if lf, err := url.ParseQuery(live); err == nil && strings.Contains(live, "=") {
			if rf, err := url.ParseQuery(recorded); err == nil && reflect.DeepEqual(lf, rf) {
				return true
			}
		}
		var lj, rj any
		return json.Unmarshal([]byte(live), &lj) == nil && json.Unmarshal([]byte(recorded), &rj) == nil &&
			reflect.DeepEqual(lj, rj)
	}
)

// Option configures Client.
type Option func(*config)

type config struct {
	matchers []Matcher
	repeats  bool
}

// MatchOn sets the matchers, like match_requests_on. The default is Method and URI, as in VCR.
func MatchOn(matchers ...Matcher) Option {
	return func(c *config) { c.matchers = matchers }
}

// AllowPlaybackRepeats lets an interaction answer more than one request, like allow_playback_repeats: true.
var AllowPlaybackRepeats Option = func(c *config) { c.repeats = true }

// Client returns an HTTP client that answers from the named cassette (no extension) and never touches the network.
func Client(t testing.TB, name string, opts ...Option) *http.Client {
	t.Helper()
	cfg := config{matchers: []Matcher{Method, URI}}
	for _, opt := range opts {
		opt(&cfg)
	}

	path := filepath.Join(CassetteDir(), name)
	rec, err := recorder.New(path,
		recorder.WithMode(recorder.ModeReplayOnly),
		recorder.WithReplayableInteractions(cfg.repeats),
		recorder.WithSkipRequestLatency(true),
		recorder.WithRealTransport(offline{}),
		recorder.WithMatcher(func(r *http.Request, i cassette.Request) bool {
			body, err := readBody(r)
			if err != nil {
				return false
			}
			for _, m := range cfg.matchers {
				if !m(r, body, i) {
					return false
				}
			}
			return true
		}),
	)
	if err != nil {
		t.Fatalf("vcrtest: load cassette %s: %v", name, err)
	}
	return &http.Client{Transport: annotate{name, rec}}
}

// CassetteDir is the absolute path of go/testdata/cassettes.
func CassetteDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "testdata", "cassettes")
}

// Secrets are the placeholders spec/helper.rb substitutes for credentials when recording (filter_sensitive_data).
var Secrets = []string{
	"TCMB_API_KEY", "FRED_API_KEY", "BAM_API_KEY", "BANXICO_API_KEY", "BCCH_USER", "BCCH_PASS", "BOT_API_KEY",
}

// SetSecrets sets every unset credential variable to its placeholder (FRED_API_KEY=<FRED_API_KEY>), so an adapter
// that reads its key from the environment builds the recorded request. Ruby skips these specs without the variable;
// Go runs them. Like t.Setenv, it can't be used in parallel tests.
func SetSecrets(t testing.TB) {
	t.Helper()
	for _, name := range Secrets {
		if os.Getenv(name) == "" {
			t.Setenv(name, "<"+name+">")
		}
	}
}

// unsecret swaps real credential values for their placeholders, as VCR does when it records.
func unsecret(s string) string {
	for _, name := range Secrets {
		value := os.Getenv(name)
		if value == "" || value == "<"+name+">" {
			continue
		}
		s = strings.ReplaceAll(s, value, "<"+name+">")
		s = strings.ReplaceAll(s, url.QueryEscape(value), "<"+name+">")
	}
	return s
}

func port(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	if strings.EqualFold(u.Scheme, "http") {
		return "80"
	}
	return "443"
}

// readBody returns the request body and leaves it readable again.
func readBody(r *http.Request) ([]byte, error) {
	if r.Body == nil || r.Body == http.NoBody {
		return nil, nil
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return body, nil
}

type annotate struct {
	name string
	next http.RoundTripper
}

func (a annotate) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := a.next.RoundTrip(r)
	if err != nil {
		return nil, fmt.Errorf("vcrtest: cassette %s has no unused interaction for %s %s: %w", a.name, r.Method, r.URL, err)
	}
	return resp, nil
}

type offline struct{}

func (offline) RoundTrip(r *http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("vcrtest: real request attempted: %s %s", r.Method, r.URL)
}
