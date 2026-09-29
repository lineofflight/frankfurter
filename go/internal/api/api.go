// Package api serves Frankfurter's HTTP API (lib/app.rb): the index, static files and OpenAPI documents, and each API
// version, behind the same middleware as the Ruby app (v1 deprecation headers, request timeout, no-store on errors,
// noindex, CORS).
//
// Each version lives in its own files and adds its routes from init through registerVersion, so a new version never
// edits this file.
package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/heavyslots"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

// Server holds what the handlers share. Build it with a DB and call Handler; zero fields take the defaults below.
type Server struct {
	DB *sql.DB

	// Today is Ruby's Date.today; defaults to rates.Today.
	Today func() time.Time

	// Timeout is the request deadline (REQUEST_TIMEOUT_SECONDS, default 90s); defaults to DefaultTimeout.
	Timeout time.Duration

	// HeavySlots caps concurrent live range computes (v2's RateQuery.heavy_slots). The v2 routes own its default;
	// tests set it to exhaust the cap.
	HeavySlots *heavyslots.Slots
}

// versionRoutes register each API version's routes on the mux.
var versionRoutes []func(s *Server, mux *http.ServeMux)

func registerVersion(f func(s *Server, mux *http.ServeMux)) { versionRoutes = append(versionRoutes, f) }

// Handler returns the app wrapped in its middleware, outermost first as lib/app.rb lists it.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/{$}", s.root)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { notFound(w, contentTypeJSON) })
	for _, register := range versionRoutes {
		register(s, mux)
	}

	h := staticRoute(mux)
	h = cors(h)
	h = noindex(h)
	h = noStoreOnError(h)
	h = requestTimeout(h, s.timeout())
	h = v1Deprecation(h)
	return h
}

func (s *Server) today() time.Time {
	if s.Today != nil {
		return s.Today()
	}
	return rates.Today()
}

func (s *Server) timeout() time.Duration {
	if s.Timeout > 0 {
		return s.Timeout
	}
	return DefaultTimeout
}

// DefaultTimeout is REQUEST_TIMEOUT_SECONDS, or 90 seconds. It parses like Ruby's Integer() and panics at startup on
// an invalid value, as Integer() raises on load.
var DefaultTimeout = defaultTimeout()

func defaultTimeout() time.Duration {
	s, ok := os.LookupEnv("REQUEST_TIMEOUT_SECONDS")
	if !ok {
		return 90 * time.Second
	}
	n, err := strconv.ParseInt(strings.TrimSpace(s), 0, 0)
	if err != nil {
		panic(fmt.Sprintf("REQUEST_TIMEOUT_SECONDS: %v", err))
	}
	return time.Duration(n) * time.Second
}

const (
	contentTypeJSON = "application/json"
	cacheOneDay     = "public, max-age=86400"
)

type versionLink struct {
	Base    string `json:"base"`
	OpenAPI string `json:"openapi"`
	Status  string `json:"status"`
}

var rootPayload = struct {
	Name     string `json:"name"`
	Versions struct {
		V1 versionLink `json:"v1"`
		V2 versionLink `json:"v2"`
	} `json:"versions"`
	Docs   string `json:"docs"`
	Source string `json:"source"`
}{
	Name: "Frankfurter",
	Versions: struct {
		V1 versionLink `json:"v1"`
		V2 versionLink `json:"v2"`
	}{
		V1: versionLink{"/v1", "/v1/openapi.json", "frozen"},
		V2: versionLink{"/v2", "/v2/openapi.json", "current"},
	},
	Docs:   "https://frankfurter.dev",
	Source: "https://github.com/lineofflight/frankfurter",
}

// root answers GET / only, as Roda's r.root does; other methods fall through to not found.
func (s *Server) root(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		notFound(w, contentTypeJSON)
		return
	}
	w.Header().Set("Cache-Control", cacheOneDay)
	writeJSON(w, http.StatusOK, contentTypeJSON, rootPayload)
}

type message struct {
	Status  int    `json:"status,omitempty"`
	Message string `json:"message"`
}

func notFound(w http.ResponseWriter, contentType string) {
	writeJSON(w, http.StatusNotFound, contentType, message{Status: http.StatusNotFound, Message: "not found"})
}

// writeJSON writes v as JSON without HTML escaping, as Oj does.
func writeJSON(w http.ResponseWriter, status int, contentType string, v any) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	w.Write(bytes.TrimSuffix(buf.Bytes(), []byte("\n")))
}
