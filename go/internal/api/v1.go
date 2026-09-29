package api

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/lineofflight/frankfurter/go/internal/db"
)

// Versions::V1: the frozen, deprecated API. Every rate comes from ECB, quoted against the euro unless rebased.

func init() { registerVersion((*Server).routesV1) }

const contentTypeV1 = "application/json; charset=utf-8"

var v1RootPayload = struct {
	Version string `json:"version"`
	Status  string `json:"status"`
	OpenAPI string `json:"openapi"`
	Docs    string `json:"docs"`
}{"v1", "deprecated", "/v1/openapi.json", "https://frankfurter.dev/v1/"}

// Like Roda's r.is and r.root, the v1 routes answer any method, except /v1/ (r.root), which answers GET only.
//
// Roda's params_capturing parses the query string before trying each matcher with arguments, and every v1 route but
// the index (r.is and r.root without arguments) has one. So any other v1 path, even one no route matches, fails with
// 422 on a query Rack cannot parse.
func (s *Server) routesV1(mux *http.ServeMux) {
	mux.HandleFunc("/v1", s.v1Root)
	mux.HandleFunc("/v1/{$}", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			v1Unmatched(w, r)
			return
		}
		s.v1Root(w, r)
	})
	mux.HandleFunc("/v1/", v1Unmatched)
	mux.HandleFunc("/v1/currencies", s.v1Currencies)
	mux.HandleFunc("/v1/{spec}", s.v1Rates)
}

func v1Unmatched(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", cacheOneDay)
	if _, _, ok := v1ParseQuery(w, r); ok {
		notFound(w, contentTypeJSON)
	}
}

// v1ParseQuery parses the query string, answering 422 when Rack could not.
func v1ParseQuery(w http.ResponseWriter, r *http.Request) (v1Params, v1Nested, bool) {
	params, nested, err := parseV1Params(r.URL.RawQuery)
	if err != nil {
		v1Error(w, err)
		return nil, nil, false
	}
	return params, nested, true
}

func (s *Server) v1Root(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", cacheOneDay)
	writeJSON(w, http.StatusOK, contentTypeV1, v1RootPayload)
}

// v1NotFound is a quote that found nothing. A path no v1 route matches gets the app's own not found, as Roda's
// not_found plugin answers the empty 404 that r.run returns.
func v1NotFound(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", cacheOneDay)
	writeJSON(w, http.StatusNotFound, contentTypeV1, message{Message: "not found"})
}

// v1Error is V1's error handler: any failure to read the request is a 422 carrying its message.
func v1Error(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusUnprocessableEntity, contentTypeV1, message{Message: err.Error()})
}

var (
	v1LatestRe   = regexp.MustCompile(`^(?:latest|current)$`)
	v1DateRe     = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})$`)
	v1IntervalRe = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})\.\.(\d{4}-\d{2}-\d{2})?$`)

	errNotInterval = errors.New("invalid date range")
)

// v1Rates serves /latest (or /current), /YYYY-MM-DD and /YYYY-MM-DD..[YYYY-MM-DD]. The route's dates overwrite any
// date parameters in the query string; an open interval ends today.
func (s *Server) v1Rates(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", cacheOneDay)
	spec := r.PathValue("spec")
	today := db.FormatDate(s.today())

	params, nested, ok := v1ParseQuery(w, r)
	if !ok {
		return
	}
	var captures v1Params
	interval := false
	switch m := v1IntervalRe.FindStringSubmatch(spec); {
	case v1LatestRe.MatchString(spec):
		captures = v1Params{"date": today}
	case v1DateRe.MatchString(spec):
		captures = v1Params{"date": spec}
	case m != nil:
		interval = true
		captures = v1Params{"start_date": m[1], "end_date": m[2]}
		if m[2] == "" {
			captures["end_date"] = today
		}
	default:
		notFound(w, contentTypeJSON)
		return
	}
	for k, v := range captures {
		params[k] = v
		delete(nested, k)
	}
	if err := nested.check(params); err != nil {
		v1Error(w, err)
		return
	}
	query, err := buildV1Query(params)
	if err != nil {
		v1Error(w, err)
		return
	}

	var (
		quote     *v1Quote
		formatted func() any
		cacheKey  func() string
	)
	if interval {
		if !query.IsInterval {
			v1Error(w, errNotInterval)
			return
		}
		iv := newV1Interval(s.DB, query, s.today())
		quote, formatted, cacheKey = &iv.v1Quote, func() any { return iv.Formatted() }, iv.CacheKey
	} else {
		e := newV1EndOfDay(s.DB, query)
		quote, formatted, cacheKey = &e.v1Quote, func() any { return e.Formatted() }, e.CacheKey
	}
	if _, err := quote.Perform(r.Context()); errors.Is(err, errNotFinite) {
		v1Error(w, err)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if quote.NotFound() {
		v1NotFound(w)
		return
	}
	if etag(w, r, cacheKey()) {
		return
	}
	writeJSON(w, http.StatusOK, contentTypeV1, formatted())
}

func (s *Server) v1Currencies(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", cacheOneDay)
	if _, _, ok := v1ParseQuery(w, r); !ok {
		return
	}
	names, err := loadV1CurrencyNames(r.Context(), s.DB, s.today())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if etag(w, r, names.CacheKey()) {
		return
	}
	writeJSON(w, http.StatusOK, contentTypeV1, names.Formatted())
}

// etag sets a strong ETag and answers a request that already holds it, as Roda's r.etag does: 304 to GET and HEAD,
// 412 to other methods. It reports whether it answered.
func etag(w http.ResponseWriter, r *http.Request, value string) bool {
	tag := `"` + value + `"`
	w.Header().Set("ETag", tag)
	list := r.Header.Get("If-None-Match")
	if list == "" {
		return false
	}
	match := list == "*" && r.Method != http.MethodPost
	if list != "*" {
		for _, t := range strings.Split(list, ",") {
			if strings.TrimSpace(t) == tag {
				match = true
				break
			}
		}
	}
	if !match {
		return false
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		w.WriteHeader(http.StatusNotModified)
	} else {
		w.WriteHeader(http.StatusPreconditionFailed)
	}
	return true
}

// v1Successor is the v2 resource that replaces a v1 path.
func v1Successor(path string) (string, bool) {
	if path != "/v1" && !strings.HasPrefix(path, "/v1/") {
		return "", false
	}
	switch path {
	case "/v1", "/v1/":
		return "/v2", true
	case "/v1/currencies":
		return "/v2/currencies", true
	case "/v1/openapi.json":
		return "/v2/openapi.json", true
	}
	return "/v2/rates", true
}

// v1Deprecation marks every v1 response deprecated and links its successor (RFC 9745 and RFC 8288). It wraps the whole
// app, so static files, preflight requests and errors carry the headers too.
func v1Deprecation(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		successor, ok := v1Successor(r.URL.Path)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		link := `<https://api.frankfurter.dev` + successor + `>; rel="successor-version"`
		withHeaders(next, func(_ *http.Request, _ int, h http.Header) {
			h.Set("Deprecation", "@1779103800") // V2 release: 2026-05-18 11:30 UTC (RFC 9745 structured date).
			if existing := h.Get("Link"); existing != "" {
				link = existing + ", " + link
			}
			h.Set("Link", link)
		}).ServeHTTP(w, r)
	})
}
