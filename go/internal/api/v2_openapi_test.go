package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/dbtest"
)

// v2SpecPath is the v2 document's path for a request path, or "" for paths it does not describe (extensions pick
// other representations).
func v2SpecPath(path string) string {
	rest, ok := strings.CutPrefix(path, "/v2/")
	if !ok || strings.ContainsAny(rest, ".%") {
		return ""
	}
	parts := strings.Split(rest, "/")
	for _, p := range parts {
		if p == "" {
			return ""
		}
	}
	switch {
	case len(parts) == 1 && (parts[0] == "coverage" || parts[0] == "rates" || parts[0] == "providers" ||
		parts[0] == "currencies"):
		return "/" + parts[0]
	case len(parts) == 3 && parts[0] == "rate":
		return "/rate/{base}/{quote}"
	case len(parts) == 2 && parts[0] == "currency":
		return "/currency/{code}"
	case len(parts) == 2 && parts[0] == "providers":
		return "/providers/{provider}"
	case len(parts) == 3 && parts[0] == "providers" && parts[2] == "rates":
		return "/providers/{provider}/rates"
	case len(parts) == 5 && parts[0] == "providers" && parts[2] == "rate":
		return "/providers/{provider}/rate/{base}/{quote}"
	}
	return ""
}

// Every plain GET in the golden corpus that v2's document describes, answered by the Go handler, conforms to it,
// as the Ruby specs check with skooma.
func TestV2ResponsesMatchOpenAPI(t *testing.T) {
	doc := loadSpec(t, "v2/openapi.json")
	g := loadGoldenAPI(t)
	today, err := db.ParseDate(g.Today)
	if err != nil {
		t.Fatal(err)
	}
	conn := dbtest.New(t)
	loadGoldenTables(t, conn, g.Tables)
	h := (&Server{DB: conn, Today: func() time.Time { return today }}).Handler()

	checked := 0
	for _, c := range g.Responses {
		specPath := v2SpecPath(strings.SplitN(c.Path, "?", 2)[0])
		if c.Method != http.MethodGet || specPath == "" || len(c.Headers) > 0 {
			continue
		}
		req := httptest.NewRequest(c.Method, c.Path, nil)
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		if doc.Paths.Find(specPath).Get.Responses.Status(res.Code) == nil {
			continue // 500s and the like are undocumented
		}
		if err := validateResponse(t, doc, specPath, req, res); err != nil {
			t.Errorf("%s (%d): %v", c.Request, res.Code, err)
		}
		checked++
	}
	if checked < 150 {
		t.Fatalf("checked only %d responses", checked)
	}
}

// The validation has teeth: a rate row without its rate breaks the document.
func TestV2OpenAPIRejectsMalformedRates(t *testing.T) {
	doc := loadSpec(t, "v2/openapi.json")
	req := httptest.NewRequest(http.MethodGet, "/v2/rates", nil)
	res := httptest.NewRecorder()
	res.Header().Set("Content-Type", contentTypeV2)
	res.WriteHeader(http.StatusOK)
	res.WriteString(`[{"date":"2026-09-28","base":"EUR","quote":"USD"}]`)
	if validateResponse(t, doc, "/rates", req, res) == nil {
		t.Fatal("a row without a rate conforms")
	}
}
