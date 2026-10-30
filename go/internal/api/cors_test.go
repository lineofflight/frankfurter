package api

import (
	"net/http"
	"testing"
)

// Rack::Cors edge cases, checked against the Ruby app: it tests Origin and
// Access-Control-Request-Method for presence, not content, and refuses a
// preflight whose path holds a NUL byte.
func TestCORSHeaderPresence(t *testing.T) {
	a := newTestApp(t)

	a.get("/", "Origin", "")
	a.status(http.StatusOK)
	if got := a.header("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("empty Origin: Access-Control-Allow-Origin = %q", got)
	}

	a.do(http.MethodOptions, "/", "Origin", "", "Access-Control-Request-Method", "GET")
	a.status(http.StatusOK)
	if got := a.header("Access-Control-Allow-Methods"); got != corsMethods {
		t.Errorf("empty Origin preflight: Access-Control-Allow-Methods = %q", got)
	}
	if a.res.Body.Len() != 0 {
		t.Errorf("empty Origin preflight: body %q", a.res.Body.String())
	}

	a.do(http.MethodOptions, "/", "Origin", "https://x", "Access-Control-Request-Method", "")
	a.status(http.StatusOK)
	if _, ok := a.res.Header()["Access-Control-Allow-Origin"]; ok || a.res.Body.Len() != 0 {
		t.Errorf("empty method preflight: headers %v, body %q", a.res.Header(), a.res.Body.String())
	}

	a.do(http.MethodOptions, "/robots.txt", "X-Origin", "https://x", "Access-Control-Request-Method", "GET")
	if got := a.header("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("X-Origin preflight: Access-Control-Allow-Origin = %q", got)
	}

	a.do(http.MethodOptions, "/v1/%00", "Origin", "https://x", "Access-Control-Request-Method", "GET")
	a.status(http.StatusBadRequest)
	if got := a.header("Cache-Control"); got != "no-store" {
		t.Errorf("NUL preflight: Cache-Control = %q", got)
	}
}
