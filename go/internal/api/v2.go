package api

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/lineofflight/frankfurter/go/internal/heavyslots"
	"github.com/lineofflight/frankfurter/go/internal/provider"
	"github.com/lineofflight/frankfurter/go/internal/ratequery"
)

// Versions::V2: the current API. Rates are blended across providers unless
// providers= names the sources.

func init() { registerVersion((*Server).routesV2) }

const (
	contentTypeV2 = "application/json; charset=utf-8"

	// v2CacheControl is every v2 response's default; date-relative rate queries
	// and the provider and coverage endpoints override it.
	v2CacheControl    = "public, max-age=86400, stale-while-revalidate=86400, stale-if-error=86400"
	v2CacheOneHour    = "public, max-age=3600"
	contentTypeCSV    = "text/csv"
	contentTypeNDJSON = "application/x-ndjson"
)

var v2RootPayload = struct {
	Version string `json:"version"`
	Status  string `json:"status"`
	OpenAPI string `json:"openapi"`
	Docs    string `json:"docs"`
}{"v2", "current", "/v2/openapi.json", "https://frankfurter.dev"}

func (s *Server) routesV2(mux *http.ServeMux) {
	mux.HandleFunc("/v2", s.v2)
	mux.HandleFunc("/v2/", s.v2)
}

// v2Request is one request's routing state. Paths match raw, as Roda matches
// PATH_INFO: /v2/r%61tes is no route, and captured segments are not decoded.
type v2Request struct {
	s *Server
	w http.ResponseWriter
	r *http.Request

	// Roda's type_routing: an extension (.json, .xml, .html, .csv) picks the
	// response type and is dropped from the path; without one the Accept header
	// picks it, when a route asks.
	ext      string
	accepted string
	asked    bool

	params    ratequery.Params
	paramsErr error
	parsed    bool
}

var (
	v2ExtRe = regexp.MustCompile(`^(.*?)\.(json|xml|html|csv)$`)

	acceptSplit  = regexp.MustCompile(`\s*,\s*`)
	acceptParams = regexp.MustCompile(`\s*;\s*`)
	acceptTypes  = map[string]string{
		"text/json": "json", "application/json": "json", "text/xml": "xml", "application/xml": "xml",
		"text/html": "html", contentTypeCSV: "csv",
	}
)

// acceptHeader is Rack's HTTP_ACCEPT: repeated headers joined with commas.
func acceptHeader(r *http.Request) string { return strings.Join(r.Header.Values("Accept"), ", ") }

func (s *Server) v2(w http.ResponseWriter, r *http.Request) {
	rest, ok := strings.CutPrefix(r.URL.EscapedPath(), "/v2")
	if !ok || rest != "" && rest[0] != '/' { // an escaped /v2 is not the v2 mount
		notFound(w, contentTypeJSON)
		return
	}
	c := &v2Request{s: s, w: w, r: r}
	if m := v2ExtRe.FindStringSubmatch(rest); m != nil {
		rest, c.ext = m[1], m[2]
	}
	w.Header().Set("Cache-Control", v2CacheControl)
	c.route(rest)
}

func (c *v2Request) route(path string) {
	get := c.r.Method == http.MethodGet
	switch {
	case path == "", path == "/" && get:
		writeJSON(c.w, http.StatusOK, contentTypeV2, v2RootPayload)
		return
	case path == "/coverage":
		c.only(get, c.coverage)
		return
	case path == "/rates":
		c.only(get, func() { c.rates(c.query()) })
		return
	case path == "/providers" || strings.HasPrefix(path, "/providers/"):
		c.provider(strings.TrimPrefix(path, "/providers"))
		return
	}
	if base, quote, ok := twoSegments(path, "/rate/"); ok {
		c.only(get, func() { c.rate(c.query(), base, quote) })
		return
	}
	if c.requestedType() == "csv" {
		c.w.Header().Set("Content-Type", contentTypeCSV)
		c.w.WriteHeader(http.StatusNotAcceptable)
		return
	}
	if code, ok := strings.CutPrefix(path, "/currency/"); ok && code != "" && !strings.Contains(code, "/") {
		c.only(get, func() { c.currency(code) })
		return
	}
	if path == "/currencies" {
		c.only(get, c.currencies)
		return
	}
	c.notFound()
}

// only runs serve for GET; a route matched by any other method answers 404, as
// Roda's r.is with r.get inside does.
func (c *v2Request) only(get bool, serve func()) {
	if !get {
		c.notFound()
		return
	}
	serve()
}

// twoSegments matches prefix followed by two segments and nothing else, as
// r.is(prefix, String, String) does. Roda's String matcher captures an empty
// segment when a slash follows it (/rate//USD has base ""), but not at the end.
func twoSegments(path, prefix string) (string, string, bool) {
	rest, ok := strings.CutPrefix(path, prefix)
	if !ok {
		return "", "", false
	}
	a, b, ok := strings.Cut(rest, "/")
	if !ok || b == "" || strings.Contains(b, "/") {
		return "", "", false
	}
	return a, b, true
}

// requestedType is type_routing's requested_type: the extension, else the first
// Accept entry naming a known type (which adds Vary: Accept), else html.
func (c *v2Request) requestedType() string {
	if c.ext != "" {
		return c.ext
	}
	if !c.asked {
		c.asked = true
		c.accepted = "html"
		for _, part := range acceptSplit.Split(acceptHeader(c.r), -1) {
			mime := acceptParams.Split(part, 2)[0]
			if t, ok := acceptTypes[mime]; ok {
				h := c.w.Header()
				if vary := h.Get("Vary"); vary != "" {
					h.Set("Vary", vary+", Accept")
				} else {
					h.Set("Vary", "Accept")
				}
				c.accepted = t
				break
			}
		}
	}
	return c.accepted
}

// query is Roda's r.params, parsed once. A query string Rack cannot parse fails
// the request.
func (c *v2Request) query() ratequery.Params {
	if !c.parsed {
		c.parsed = true
		h, err := parseRackQuery(c.r.URL.RawQuery)
		c.params, c.paramsErr = ratequery.Params(h), err
	}
	return c.params
}

// notFound is the status handler's 404: the response's headers are cleared
// first.
func (c *v2Request) notFound() {
	clear(c.w.Header())
	writeJSON(c.w, http.StatusNotFound, contentTypeV2, message{Status: http.StatusNotFound, Message: "not found"})
}

// fail is V2's error handler: validation fails with 422, an expired deadline or
// a full compute cap with 503 (the latter with Retry-After), anything else with
// 500. The response starts over, as Roda reinitializes it.
func (c *v2Request) fail(err error) {
	status := http.StatusInternalServerError
	var (
		deadline *ratequery.DeadlineError
		timeout  TimeoutError
		busy     ratequery.BusyError
	)
	h := c.w.Header()
	clear(h)
	switch {
	case ratequery.IsValidation(err):
		status = http.StatusUnprocessableEntity
	case errors.As(err, &deadline), errors.As(err, &timeout):
		status = http.StatusServiceUnavailable
	case errors.As(err, &busy):
		status = http.StatusServiceUnavailable
		h.Set("Retry-After", strconv.Itoa(heavyslots.RetryAfterSeconds))
	}
	writeJSON(c.w, status, contentTypeV2, message{Status: status, Message: err.Error()})
}

func (c *v2Request) options() ratequery.Options {
	slots := c.s.HeavySlots
	if slots == nil {
		slots = ratequery.DefaultSlots
	}
	return ratequery.Options{Today: c.s.today(), Deadline: RequestDeadline(c.r), Timeout: c.s.timeout(), Slots: slots}
}

func (c *v2Request) ctx() context.Context { return c.r.Context() }

func (c *v2Request) coverage() {
	params := c.query()
	if c.paramsErr != nil {
		c.fail(c.paramsErr)
		return
	}
	var unknown []string
	for k := range params {
		if !slices.Contains(ratequery.CoverageParams, k) {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		c.fail(&ratequery.ValidationError{Message: "unknown parameter: " + strings.Join(unknown, ", ")})
		return
	}
	c.w.Header().Set("Cache-Control", v2CacheOneHour)
	q, err := ratequery.New(c.ctx(), c.s.DB, params, c.options())
	if err != nil {
		c.fail(err)
		return
	}
	result, err := q.History(c.ctx())
	if err != nil {
		c.fail(err)
		return
	}
	writeJSON(c.w, http.StatusOK, contentTypeV2, result)
}

// provider serves /providers and everything under it. /providers/<key>/rates
// and /providers/<key>/rate/<base>/<quote> alias /rates?providers=<key> byte
// for byte (#643): one code path, so single-provider behaviour cannot drift
// between the two URLs.
func (c *v2Request) provider(rest string) {
	get := c.r.Method == http.MethodGet
	if rest == "" {
		c.only(get, func() {
			c.w.Header().Set("Cache-Control", v2CacheOneHour)
			c.providers()
		})
		return
	}
	key, tail := rest[1:], ""
	if i := strings.IndexByte(key, '/'); i >= 0 {
		key, tail = key[:i], key[i:]
	}
	if key == "" {
		c.notFound()
		return
	}
	p, err := provider.Find(c.ctx(), c.s.DB, strings.ToUpper(key))
	if err != nil {
		c.fail(err)
		return
	}
	if p == nil {
		c.notFound()
		return
	}
	params := c.query()
	if c.paramsErr != nil {
		c.fail(c.paramsErr)
		return
	}
	if _, ok := params["providers"]; ok {
		c.fail(&ratequery.ValidationError{Message: "providers is implied by the route; drop the parameter"})
		return
	}
	merged := ratequery.Params{"providers": p.Key}
	for k, v := range params {
		if k != "providers" {
			merged[k] = v
		}
	}

	switch {
	case tail == "":
		c.only(get, func() {
			c.w.Header().Set("Cache-Control", v2CacheOneHour)
			entry, err := providerEntry(c.ctx(), c.s.DB, *p, c.s.today())
			if err != nil {
				c.fail(err)
				return
			}
			if entry == nil {
				c.notFound()
				return
			}
			writeJSON(c.w, http.StatusOK, contentTypeV2, entry)
		})
		return
	case tail == "/rates":
		c.only(get, func() { c.rates(merged) })
		return
	}
	if base, quote, ok := twoSegments(tail, "/rate/"); ok {
		c.only(get, func() { c.rate(merged, base, quote) })
		return
	}
	if c.requestedType() == "csv" {
		c.w.Header().Set("Content-Type", contentTypeCSV)
		c.w.WriteHeader(http.StatusNotAcceptable)
		return
	}
	c.notFound()
}
