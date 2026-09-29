package api

import (
	"bytes"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/fixtures"
)

// spec/app_spec.rb, except the cases that need the v2 routes (see docs/core-api_v1.md).

func TestServesRoot(t *testing.T) {
	a := newTestApp(t)
	a.get("/")
	a.status(http.StatusOK)
	if got := a.header("Cache-Control"); got != "public, max-age=86400" {
		t.Errorf("Cache-Control = %q", got)
	}
	j := a.json()
	if j["name"] != "Frankfurter" {
		t.Errorf("name = %v", j["name"])
	}
	versions := j["versions"].(map[string]any)
	v1, v2 := versions["v1"].(map[string]any), versions["v2"].(map[string]any)
	if v1["openapi"] != "/v1/openapi.json" || v1["status"] != "frozen" {
		t.Errorf("v1 = %v", v1)
	}
	if v2["openapi"] != "/v2/openapi.json" || v2["status"] != "current" {
		t.Errorf("v2 = %v", v2)
	}
}

func TestServesV1Root(t *testing.T) {
	a := newTestApp(t)
	a.get("/v1")
	a.status(http.StatusOK)
	j := a.json()
	want := map[string]string{"version": "v1", "status": "deprecated", "openapi": "/v1/openapi.json",
		"docs": "https://frankfurter.dev/v1/"}
	for k, v := range want {
		if j[k] != v {
			t.Errorf("%s = %v, want %s", k, j[k], v)
		}
	}
}

func checkDeprecated(t *testing.T, a *testApp, path, successor string) {
	t.Helper()
	if got := a.header("Deprecation"); got != "@1779103800" {
		t.Errorf("%s: Deprecation = %q", path, got)
	}
	want := `<https://api.frankfurter.dev` + successor + `>; rel="successor-version"`
	if got := a.header("Link"); got != want {
		t.Errorf("%s: Link = %q, want %q", path, got, want)
	}
}

func TestV1DeprecationLinksSuccessor(t *testing.T) {
	a := newTestApp(t)
	for _, c := range []struct {
		path, successor string
		status          int
	}{
		{"/v1", "/v2", 200},
		{"/v1/", "/v2", 200},
		{"/v1/currencies", "/v2/currencies", 200},
		{"/v1/latest", "/v2/rates", 200},
		{"/v1/current", "/v2/rates", 200},
		{"/v1/latest?from=USD&to=GBP&amount=2", "/v2/rates", 200},
		{"/v1/" + bday(30), "/v2/rates", 200},
		{"/v1/" + bday(30) + ".." + latest(), "/v2/rates", 200},
		{"/v1/" + bday(30) + "..", "/v2/rates", 200},
		{"/v1/openapi.json", "/v2/openapi.json", 200},
		{"/v1/nonexistent", "/v2/rates", 404},
		{"/v1/1000-01-01", "/v2/rates", 404},
		{"/v1/latest?amount=invalid", "/v2/rates", 422},
	} {
		a.get(c.path)
		if a.res.Code != c.status {
			t.Errorf("%s: status = %d, want %d", c.path, a.res.Code, c.status)
		}
		checkDeprecated(t, a, c.path, c.successor)
	}
}

func TestV1DeprecationOnHead(t *testing.T) {
	a := newTestApp(t)
	for _, c := range []struct {
		path, successor string
		status          int
	}{
		{"/v1", "/v2", 200},
		{"/v1/", "/v2", 404},
		{"/v1/currencies", "/v2/currencies", 200},
		{"/v1/latest", "/v2/rates", 200},
		{"/v1/openapi.json", "/v2/openapi.json", 200},
	} {
		a.do(http.MethodHead, c.path)
		if a.res.Code != c.status {
			t.Errorf("HEAD %s: status = %d, want %d", c.path, a.res.Code, c.status)
		}
		checkDeprecated(t, a, c.path, c.successor)
	}
}

func TestV1DeprecationOnConditionalResponses(t *testing.T) {
	a := newTestApp(t)
	for _, c := range []struct{ path, successor string }{
		{"/v1/currencies", "/v2/currencies"},
		{"/v1/latest", "/v2/rates"},
		{"/v1/current", "/v2/rates"},
		{"/v1/" + bday(30), "/v2/rates"},
		{"/v1/" + bday(30) + "..", "/v2/rates"},
	} {
		a.get(c.path)
		a.get(c.path, "If-None-Match", a.header("ETag"))
		if a.res.Code != http.StatusNotModified {
			t.Errorf("%s: status = %d", c.path, a.res.Code)
		}
		if a.res.Body.Len() != 0 {
			t.Errorf("%s: body = %q", c.path, a.res.Body.String())
		}
		checkDeprecated(t, a, c.path, c.successor)
	}
}

func TestV1DeprecationOnPreflight(t *testing.T) {
	a := newTestApp(t)
	for _, c := range []struct{ path, successor string }{
		{"/v1", "/v2"},
		{"/v1/currencies", "/v2/currencies"},
		{"/v1/latest", "/v2/rates"},
		{"/v1/openapi.json", "/v2/openapi.json"},
	} {
		a.do(http.MethodOptions, c.path, "Origin", "https://example.com", "Access-Control-Request-Method", "GET")
		if a.res.Code != http.StatusOK {
			t.Errorf("%s: status = %d", c.path, a.res.Code)
		}
		if got := a.header("Access-Control-Allow-Origin"); got != "*" {
			t.Errorf("%s: Access-Control-Allow-Origin = %q", c.path, got)
		}
		checkDeprecated(t, a, c.path, c.successor)
	}
}

func TestDoesNotDeprecateOtherRoutes(t *testing.T) {
	a := newTestApp(t)
	for _, path := range []string{"/", "/v2", "/v2/currencies", "/v2/rates", "/v2/openapi.json", "/v10/latest",
		"/nonexistent"} {
		a.get(path)
		if _, ok := a.res.Header()["Deprecation"]; ok {
			t.Errorf("%s: Deprecation set", path)
		}
		if _, ok := a.res.Header()["Link"]; ok {
			t.Errorf("%s: Link set", path)
		}
	}
}

func TestServesStaticFiles(t *testing.T) {
	a := newTestApp(t)
	for _, path := range []string{"/favicon.ico", "/robots.txt", "/v1/openapi.json"} {
		a.get(path)
		if a.res.Code != http.StatusOK {
			t.Errorf("%s: status = %d", path, a.res.Code)
		}
		if got := a.header("Cache-Control"); got != "public, max-age=86400" {
			t.Errorf("%s: Cache-Control = %q", path, got)
		}
	}
}

func TestSetsNoindex(t *testing.T) {
	a := newTestApp(t)
	for _, path := range []string{"/robots.txt", "/v1/latest", "/v2/rates", "/v2/currencies", "/nonexistent"} {
		a.get(path)
		if got := a.header("X-Robots-Tag"); got != "noindex" {
			t.Errorf("%s: X-Robots-Tag = %q", path, got)
		}
	}
}

func TestLeavesEntryPointsIndexable(t *testing.T) {
	a := newTestApp(t)
	for _, path := range []string{"/", "/v1", "/v2", "/v1/openapi.json", "/v2/openapi.json"} {
		a.get(path)
		if a.res.Code != http.StatusOK {
			t.Errorf("%s: status = %d", path, a.res.Code)
		}
		if _, ok := a.res.Header()["X-Robots-Tag"]; ok {
			t.Errorf("%s: X-Robots-Tag set", path)
		}
	}
}

func TestReturnsJSONFor404(t *testing.T) {
	a := newTestApp(t)
	a.get("/nonexistent")
	a.status(http.StatusNotFound)
	if got := a.header("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := a.json()["message"]; got != "not found" {
		t.Errorf("message = %v", got)
	}
}

// The v2 rows of app_spec's table are TestV2ErrorResponsesAreNotCached.
func TestErrorResponsesAreNotCached(t *testing.T) {
	a := newTestApp(t)
	for _, c := range []struct {
		path   string
		status int
	}{
		{"/nonexistent", 404},
		{"/v1/1000-01-01", 404},
	} {
		a.get(c.path)
		if a.res.Code != c.status {
			t.Errorf("%s: status = %d", c.path, a.res.Code)
		}
		if got := a.header("Cache-Control"); got != "no-store" {
			t.Errorf("%s: Cache-Control = %q", c.path, got)
		}
	}
}

func TestRoutesV1ToV1Handler(t *testing.T) {
	a := newTestApp(t)
	a.get("/v1/latest")
	a.status(http.StatusOK)
}

func TestAllowsCrossOriginRequests(t *testing.T) {
	a := newTestApp(t)
	for _, path := range []string{"/v1/", "/v1/latest", "/v1/" + db.FormatDate(fixtures.LatestDate().AddDate(0, 0, -30))} {
		a.get(path, "Origin", "*")
		if _, ok := a.res.Header()["Access-Control-Allow-Methods"]; !ok {
			t.Errorf("%s: no Access-Control-Allow-Methods", path)
		}
	}
}

func TestRespondsToPreflightRequests(t *testing.T) {
	a := newTestApp(t)
	for _, path := range []string{"/v1/", "/v1/latest", "/v1/" + db.FormatDate(fixtures.LatestDate().AddDate(0, 0, -30))} {
		a.do(http.MethodOptions, path, "Origin", "*", "Access-Control-Request-Method", "GET",
			"Access-Control-Request-Headers", "Content-Type")
		if _, ok := a.res.Header()["Access-Control-Allow-Methods"]; !ok {
			t.Errorf("%s: no Access-Control-Allow-Methods", path)
		}
		if got := a.header("Access-Control-Allow-Headers"); got != "Content-Type" {
			t.Errorf("%s: Access-Control-Allow-Headers = %q", path, got)
		}
	}
}

func TestPublicMatchesRepository(t *testing.T) {
	repo := os.DirFS(filepath.Join("..", "..", "..", "lib", "public"))
	seen := map[string]bool{}
	err := fs.WalkDir(repo, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		seen[name] = true
		want, err := fs.ReadFile(repo, name)
		if err != nil {
			return err
		}
		got, err := fs.ReadFile(public, "public/"+name)
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s differs from lib/public; run go generate ./internal/api", name)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	fs.WalkDir(public, "public", func(name string, d fs.DirEntry, err error) error {
		if rel, _ := filepath.Rel("public", name); err == nil && !d.IsDir() && !seen[rel] {
			t.Errorf("%s is gone from lib/public; run go generate ./internal/api", rel)
		}
		return err
	})
}
