package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/lineofflight/frankfurter/go/internal/blend"
	"github.com/lineofflight/frankfurter/go/internal/fixtures"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

// v2App is Versions::V2.freeze over a freshly seeded fixture: the v2 routes alone, without the app's middleware, as
// the Ruby specs mount them.
type v2App struct {
	t   *testing.T
	s   *Server
	db  *sql.DB
	h   http.Handler
	req *http.Request
	res *httptest.ResponseRecorder
}

func newV2App(t *testing.T) *v2App {
	t.Helper()
	conn := fixtures.New(t)
	s := &Server{DB: conn, Today: fixtures.Today}
	mux := http.NewServeMux()
	s.routesV2(mux)
	return &v2App{t: t, s: s, db: conn, h: mux}
}

// get requests a v2 path (relative to /v2, as the Ruby specs write them); headers are name, value pairs.
func (a *v2App) get(path string, headers ...string) *httptest.ResponseRecorder {
	a.t.Helper()
	a.req = httptest.NewRequest(http.MethodGet, "/v2"+path, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		a.req.Header.Set(headers[i], headers[i+1])
	}
	a.res = httptest.NewRecorder()
	a.h.ServeHTTP(a.res, a.req)
	return a.res
}

// getParams is Rack::Test's get(path, params): the parameters encoded into the query string.
func (a *v2App) getParams(path string, kv ...string) *httptest.ResponseRecorder {
	a.t.Helper()
	v := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		v.Add(kv[i], kv[i+1])
	}
	return a.get(path + "?" + v.Encode())
}

func (a *v2App) status(want int) {
	a.t.Helper()
	if a.res.Code != want {
		a.t.Fatalf("GET %s: status %d, want %d (body %.300s)", a.req.URL, a.res.Code, want, a.res.Body.String())
	}
}

func (a *v2App) ok() { a.t.Helper(); a.status(http.StatusOK) }

func (a *v2App) header(name string) string { return a.res.Header().Get(name) }

func (a *v2App) body() string { return a.res.Body.String() }

func (a *v2App) object() map[string]any {
	a.t.Helper()
	var v map[string]any
	if err := json.Unmarshal(a.res.Body.Bytes(), &v); err != nil {
		a.t.Fatalf("decode %.300q: %v", a.res.Body.String(), err)
	}
	return v
}

func (a *v2App) array() []map[string]any {
	a.t.Helper()
	var v []map[string]any
	if err := json.Unmarshal(a.res.Body.Bytes(), &v); err != nil {
		a.t.Fatalf("decode %.300q: %v", a.res.Body.String(), err)
	}
	return v
}

var v2Doc = sync.OnceValues(func() (*openapi3.T, error) {
	data, err := public.ReadFile("public/v2/openapi.json")
	if err != nil {
		return nil, err
	}
	doc, err := openapi3.NewLoader().LoadFromData(data)
	if err != nil {
		return nil, err
	}
	return doc, doc.Validate(context.Background())
})

// conform is skooma's assert_conform_schema: the last response matches the document for its route and status.
func (a *v2App) conform(status int) {
	a.t.Helper()
	a.status(status)
	doc, err := v2Doc()
	if err != nil {
		a.t.Fatal(err)
	}
	specPath := v2SpecPath(a.req.URL.EscapedPath())
	if specPath == "" {
		a.t.Fatalf("%s is not in the document", a.req.URL.Path)
	}
	if err := validateResponse(a.t, doc, specPath, a.req, a.res); err != nil {
		a.t.Fatalf("GET %s does not conform: %v", a.req.URL, err)
	}
}

func (a *v2App) exec(query string, args ...any) {
	a.t.Helper()
	if _, err := a.db.ExecContext(context.Background(), query, args...); err != nil {
		a.t.Fatal(err)
	}
}

func (a *v2App) insert(provider, date, base, quote string, mid float64) {
	a.t.Helper()
	a.exec("INSERT INTO rates (provider, date, base, quote, mid) VALUES (?, ?, ?, ?, ?)", provider, date, base, quote, mid)
}

func (a *v2App) rebuildDaily() {
	a.t.Helper()
	if err := blend.RebuildDaily(context.Background(), a.db, fixtures.Today()); err != nil {
		a.t.Fatal(err)
	}
}

// refreshSummaries is Provider#refresh_currency_summaries.
func (a *v2App) refreshSummaries(provider string, codes ...string) {
	a.t.Helper()
	if err := rates.RefreshSummaries(context.Background(), a.db, codes, provider); err != nil {
		a.t.Fatal(err)
	}
}

func field(rows []map[string]any, key string) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i], _ = r[key].(string)
	}
	return out
}

func findRow(rows []map[string]any, key, value string) map[string]any {
	for _, r := range rows {
		if r[key] == value {
			return r
		}
	}
	return nil
}

func uniqStrings(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func has(values []string, v string) bool {
	for _, x := range values {
		if x == v {
			return true
		}
	}
	return false
}

func ndjsonLines(t *testing.T, body string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(body, "\n") {
		if line == "" {
			continue
		}
		var v map[string]any
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			t.Fatalf("line %q: %v", line, err)
		}
		out = append(out, v)
	}
	return out
}
