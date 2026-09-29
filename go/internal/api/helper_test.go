package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/fixtures"
)

// testApp is the full app over a freshly seeded fixture database, as the Ruby
// specs' App.freeze with Fixtures.seed!.
type testApp struct {
	t   *testing.T
	db  *sql.DB
	h   http.Handler
	res *httptest.ResponseRecorder
}

func newTestApp(t *testing.T) *testApp {
	t.Helper()
	conn := fixtures.New(t)
	return &testApp{t: t, db: conn, h: (&Server{DB: conn, Today: fixtures.Today}).Handler()}
}

// do sends a request; headers are name, value pairs.
func (a *testApp) do(method, target string, headers ...string) *httptest.ResponseRecorder {
	a.t.Helper()
	req := httptest.NewRequest(method, target, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	a.res = httptest.NewRecorder()
	a.h.ServeHTTP(a.res, req)
	return a.res
}

func (a *testApp) get(target string, headers ...string) *httptest.ResponseRecorder {
	a.t.Helper()
	return a.do(http.MethodGet, target, headers...)
}

// json decodes the last response body.
func (a *testApp) json() map[string]any {
	a.t.Helper()
	var v map[string]any
	if err := json.Unmarshal(a.res.Body.Bytes(), &v); err != nil {
		a.t.Fatalf("decode %q: %v", a.res.Body.String(), err)
	}
	return v
}

func (a *testApp) header(name string) string { return a.res.Header().Get(name) }

func (a *testApp) status(want int) {
	a.t.Helper()
	if a.res.Code != want {
		a.t.Fatalf("status = %d, want %d (body %s)", a.res.Code, want, a.res.Body.String())
	}
}

func bday(n int) string { return db.FormatDate(fixtures.BusinessDay(n)) }

func latest() string { return db.FormatDate(fixtures.LatestDate()) }

func rateMap(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("rates = %#v", v)
	}
	return m
}

func today(t *testing.T) string {
	t.Helper()
	return db.FormatDate(fixtures.Today())
}
