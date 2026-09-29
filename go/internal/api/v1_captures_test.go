package api

import (
	"context"
	"net/http"
	"testing"
)

// Behaviour the specs do not cover, checked against the Ruby app through go/scripts/api_golden.rb.

// Roda's params_capturing appends a route's captures to params["captures"], which the query string can set first.
func TestV1QueryCaptures(t *testing.T) {
	a := newTestApp(t)
	for _, c := range []struct {
		target string
		status int
		field  string // a date field to check on success
		want   string
	}{
		{"/v1/latest?captures=x", 422, "", ""},
		{"/v1/latest?captures[x]=1", 422, "", ""},
		{"/v1/latest?captures", 200, "date", latest()},
		{"/v1/currencies?captures=x", 422, "", ""},
		{"/v1/currencies?captures[x]=1", 422, "", ""},
		{"/v1/" + bday(30) + "?captures=x", 422, "", ""},
		{"/v1/" + bday(30) + "?captures[]=" + bday(40), 200, "date", bday(40)},
		{"/v1/" + bday(30) + "?captures[]", 422, "", ""},
		{"/v1/" + bday(30) + "?captures[][x]=1", 422, "", ""},
		{"/v1/" + bday(30) + "?captures[]=garbage", 422, "", ""},
		{"/v1/" + bday(30) + ".." + bday(20) + "?captures[]=" + bday(60), 200, "end_date", bday(30)},
		{"/v1/" + bday(30) + ".." + bday(20) + "?captures[]=" + bday(60) + "&captures[]=" + bday(50), 200, "end_date",
			bday(50)},
		{"/v1/" + bday(30) + "..?captures[]=" + bday(60), 200, "start_date", bday(60)},
		{"/v1/" + bday(30) + "..?captures=x", 422, "", ""},
		{"/v1/" + bday(30) + "..?captures[]=" + bday(60) + "&captures[]", 200, "end_date", latest()},
		{"/v1/" + bday(30) + "..?captures[]=" + bday(60) + "&captures[][]=1", 422, "", ""},
		{"/v1/nonexistent?captures[x]=1", 404, "", ""},
		{"/v1?captures[x]=1", 200, "", ""},
	} {
		a.get(c.target)
		if a.res.Code != c.status {
			t.Errorf("%s: status %d, want %d (%s)", c.target, a.res.Code, c.status, a.res.Body.String())
			continue
		}
		if c.field != "" {
			if got := a.json()[c.field]; got != c.want {
				t.Errorf("%s: %s = %v, want %s", c.target, c.field, got, c.want)
			}
		}
	}
}

// Rack's type conflicts and depth limit apply below the top level too.
func TestV1NestedQueryConflicts(t *testing.T) {
	a := newTestApp(t)
	for target, status := range map[string]int{
		"/v1/latest?a[b]=1&a[b][c]=2":             422,
		"/v1/latest?a[b][]=1&a[b][c]=2":           422,
		"/v1/latest?a[][b]=1&a[][b]=2&to=USD":     200,
		"/v1/latest?a" + deepKey(31) + "=1":       200,
		"/v1/latest?a" + deepKey(32) + "=1":       422,
		"/v1/nonexistent?a" + deepKey(32) + "=1":  422,
		"/v1/currencies?a[b]=1&a[b][c]=2":         422,
		"/v1/" + bday(30) + "..?a[b]=1&a[b][c]=2": 422,
	} {
		a.get(target)
		if a.res.Code != status {
			t.Errorf("%.80s: status %d, want %d", target, a.res.Code, status)
		}
	}
}

func deepKey(n int) string {
	s := ""
	for range n {
		s += "[x]"
	}
	return s
}

// A stored row with no resolvable rate (one side of a quote) fails the request, as Ruby's amount * nil raises; a
// query that leaves the row out is unaffected.
func TestV1RowWithoutRate(t *testing.T) {
	a := newTestApp(t)
	_, err := a.db.ExecContext(context.Background(),
		`INSERT INTO rates (provider, date, base, quote, bid) VALUES ('ECB', ?, 'EUR', 'ISK', 150)`, latest())
	if err != nil {
		t.Fatal(err)
	}
	a.get("/v1/latest")
	a.status(http.StatusUnprocessableEntity)
	a.get("/v1/latest?to=USD")
	a.status(http.StatusOK)
}

// Roda and the middleware see the raw path, so a %-escape keeps a path off the v1 routes and the header exemptions
// even when it decodes to one of them; static files match the decoded path.
func TestV1EscapedPaths(t *testing.T) {
	a := newTestApp(t)
	for _, c := range []struct {
		target, successor string
		status            int
	}{
		{"/v1/openapi%2Ejson", "/v2/rates", 200},
		{"/v%31", "", 404},
		{"/%76%31/openapi.json", "", 200},
		{"/v1%2F", "", 404},
		{"/v1/%6Catest", "/v2/rates", 404},
		{"/v1/%6Catest?foo=1&foo[]=2", "/v2/rates", 422},
		{"/%76%31/latest?foo=1&foo[]=2", "", 404},
		{"/v1/a%20b", "/v2/rates", 404},
	} {
		a.get(c.target)
		if a.res.Code != c.status {
			t.Errorf("%s: status %d, want %d", c.target, a.res.Code, c.status)
		}
		if got := a.header("X-Robots-Tag"); got != "noindex" {
			t.Errorf("%s: X-Robots-Tag = %q", c.target, got)
		}
		want := ""
		if c.successor != "" {
			want = `<https://api.frankfurter.dev` + c.successor + `>; rel="successor-version"`
		}
		if got := a.header("Link"); got != want {
			t.Errorf("%s: Link = %q, want %q", c.target, got, want)
		}
	}
}
