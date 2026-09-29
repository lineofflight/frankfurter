package api

import (
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/lineofflight/frankfurter/go/internal/db"
)

// Versions::V1: the frozen, deprecated API. Every rate comes from ECB, quoted
// against the euro unless rebased.

func init() { registerVersion((*Server).routesV1) }

const contentTypeV1 = "application/json; charset=utf-8"

var v1RootPayload = struct {
	Version string `json:"version"`
	Status  string `json:"status"`
	OpenAPI string `json:"openapi"`
	Docs    string `json:"docs"`
}{"v1", "deprecated", "/v1/openapi.json", "https://frankfurter.dev/v1/"}

// Like Roda's r.is and r.root, the v1 routes answer any method, except /v1/
// (r.root), which answers GET only.
//
// Roda's params_capturing parses the query string before trying each matcher
// with arguments, and every v1 route but the index (r.is and r.root without
// arguments) has one. So any other v1 path, even one no route matches, fails
// with 422 on a query Rack cannot parse.
func (s *Server) routesV1(mux *http.ServeMux) {
	mux.HandleFunc("/v1", v1Raw(s.v1Root))
	mux.HandleFunc("/v1/{$}", v1Raw(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			v1Unmatched(w, r)
			return
		}
		s.v1Root(w, r)
	}))
	mux.HandleFunc("/v1/", v1Raw(v1Unmatched))
	mux.HandleFunc("/v1/currencies", v1Raw(s.v1Currencies))
	mux.HandleFunc("/v1/{spec}", v1Raw(s.v1Rates))
}

// v1Raw sends a path holding %-escapes where Roda would: it matches the raw
// PATH_INFO, so /v1/%6Catest is no v1 route and /v%31 is not under /v1 at all,
// although ServeMux decodes both to v1 paths.
func v1Raw(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw := r.URL.EscapedPath()
		switch {
		case !strings.Contains(raw, "%"):
			next(w, r)
		case strings.HasPrefix(raw, "/v1/"):
			v1Unmatched(w, r)
		default:
			notFound(w, contentTypeJSON)
		}
	}
}

func v1Unmatched(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", cacheOneDay)
	if _, _, _, ok := v1ParseQuery(w, r); ok {
		notFound(w, contentTypeJSON)
	}
}

// v1ParseQuery parses the query string, answering 422 when Rack could not.
func v1ParseQuery(w http.ResponseWriter, r *http.Request) (v1Params, v1Nested, rackHash, bool) {
	params, nested, query, err := parseV1Params(r.URL.RawQuery)
	if err != nil {
		v1Error(w, err)
		return nil, nil, nil, false
	}
	return params, nested, query, true
}

// v1Captures is what Roda's params_capturing leaves in params["captures"] once
// a route matches: the route's captures appended to whatever the query string
// put there. An array from the query keeps its elements first, so they stand in
// for the path's dates (?captures[]=2020-01-02 on /v1/2020-01-01 quotes
// 2020-01-02). A string or a hash there fails the append (String#concat takes
// no array, a Hash has no concat), so every matched route but the index answers
// 422.
func v1Captures(query rackHash, route ...any) ([]any, error) {
	switch c := query["captures"].(type) {
	case nil:
		return route, nil
	case *[]any:
		return append(slices.Clone(*c), route...), nil
	}
	return nil, errInvalidParam
}

// v1CapturedDates reads a date route's dates from its captures: the single
// date, or an interval's start and end (an open interval ends today). A date
// that is not a string fails, as Date.parse raises on nil, arrays and hashes.
func v1CapturedDates(query rackHash, today string, route ...any) (v1Params, error) {
	caps, err := v1Captures(query, route...)
	if err != nil {
		return nil, err
	}
	first, ok := caps[0].(string)
	if !ok {
		return nil, errInvalidDate
	}
	if len(route) == 1 {
		return v1Params{"date": first}, nil
	}
	out := v1Params{"start_date": first, "end_date": today}
	switch end := caps[1].(type) {
	case nil:
	case string:
		out["end_date"] = end
	default:
		return nil, errInvalidDate
	}
	return out, nil
}

func (s *Server) v1Root(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", cacheOneDay)
	writeJSON(w, http.StatusOK, contentTypeV1, v1RootPayload)
}

// v1NotFound is a quote that found nothing. A path no v1 route matches gets the
// app's own not found, as Roda's not_found plugin answers the empty 404 that
// r.run returns.
func v1NotFound(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", cacheOneDay)
	writeJSON(w, http.StatusNotFound, contentTypeV1, message{Message: "not found"})
}

// v1Error is V1's error handler: any failure to read the request is a 422
// carrying its message.
func v1Error(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusUnprocessableEntity, contentTypeV1, message{Message: err.Error()})
}

var (
	v1LatestRe   = regexp.MustCompile(`^(?:latest|current)$`)
	v1DateRe     = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})$`)
	v1IntervalRe = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})\.\.(\d{4}-\d{2}-\d{2})?$`)

	errNotInterval = errors.New("invalid date range")
)

// v1Rates serves /latest (or /current), /YYYY-MM-DD and
// /YYYY-MM-DD..[YYYY-MM-DD]. The route's dates overwrite any date parameters in
// the query string; an open interval ends today.
func (s *Server) v1Rates(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", cacheOneDay)
	spec := r.PathValue("spec")
	today := db.FormatDate(s.today())

	params, nested, raw, ok := v1ParseQuery(w, r)
	if !ok {
		return
	}
	var (
		captures v1Params
		err      error
	)
	interval := false
	switch m := v1IntervalRe.FindStringSubmatch(spec); {
	case v1LatestRe.MatchString(spec):
		_, err = v1Captures(raw)
		captures = v1Params{"date": today}
	case v1DateRe.MatchString(spec):
		captures, err = v1CapturedDates(raw, today, spec)
	case m != nil:
		interval = true
		var end any
		if m[2] != "" {
			end = m[2]
		}
		captures, err = v1CapturedDates(raw, today, m[1], end)
	default:
		notFound(w, contentTypeJSON)
		return
	}
	if err != nil {
		v1Error(w, err)
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
	if _, err := quote.Perform(r.Context()); errors.Is(err, errNotFinite) || errors.Is(err, errNoRate) {
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
	_, _, query, ok := v1ParseQuery(w, r)
	if !ok {
		return
	}
	if _, err := v1Captures(query); err != nil {
		v1Error(w, err)
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

// etag sets a strong ETag and answers a conditional request as Roda's r.etag
// does: an If-None-Match that holds the tag gets 304 (to GET, HEAD, OPTIONS and
// TRACE) or 412 (to other methods); an If-Match that does not hold it gets 412.
// A POST never matches "*". It reports whether it answered.
func etag(w http.ResponseWriter, r *http.Request, value string) bool {
	tag := `"` + value + `"`
	w.Header().Set("ETag", tag)
	newResource := r.Method == http.MethodPost
	if list, ok := r.Header["If-None-Match"]; ok && etagMatches(strings.Join(list, ", "), tag, newResource) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
			w.WriteHeader(http.StatusNotModified)
		default:
			w.WriteHeader(http.StatusPreconditionFailed)
		}
		return true
	}
	if list, ok := r.Header["If-Match"]; ok && !etagMatches(strings.Join(list, ", "), tag, newResource) {
		w.WriteHeader(http.StatusPreconditionFailed)
		return true
	}
	return false
}

// etagMatches is Roda's etag_matches?: "*" matches unless the request creates a
// resource; otherwise the list, split on commas and the spaces around them,
// must hold the tag exactly.
func etagMatches(list, tag string, newResource bool) bool {
	if list == "*" {
		return !newResource
	}
	parts := strings.Split(list, ",")
	for i, p := range parts {
		if i > 0 {
			p = strings.TrimLeft(p, " \t\n\v\f\r")
		}
		if i < len(parts)-1 {
			p = strings.TrimRight(p, " \t\n\v\f\r")
		}
		if p == tag {
			return true
		}
	}
	return false
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

// v1Deprecation marks every v1 response deprecated and links its successor (RFC
// 9745 and RFC 8288). It wraps the whole app, so static files, preflight
// requests and errors carry the headers too.
func v1Deprecation(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		successor, ok := v1Successor(r.URL.EscapedPath()) // Rack sees the raw path
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
