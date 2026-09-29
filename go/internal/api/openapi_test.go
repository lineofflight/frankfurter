package api

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"

	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/dbtest"
)

// loadSpec reads one of the embedded OpenAPI documents.
func loadSpec(t *testing.T, file string) *openapi3.T {
	t.Helper()
	data, err := public.ReadFile("public/" + file)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := openapi3.NewLoader().LoadFromData(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Validate(context.Background(), openapi3.DisableExamplesValidation()); err != nil {
		t.Fatal(err)
	}
	return doc
}

// validateResponse checks a response against the operation at specPath (a key of the document's paths), the way the
// Ruby specs use skooma. The route is given rather than matched: v1's path templates put two variables in one segment
// ("/{start_date}..{end_date}"), which kin-openapi's routers do not match.
func validateResponse(t *testing.T, doc *openapi3.T, specPath string, req *http.Request, res *httptest.ResponseRecorder) error {
	t.Helper()
	item := doc.Paths.Find(specPath)
	if item == nil {
		t.Fatalf("%s is not in the document", specPath)
	}
	op := item.GetOperation(http.MethodGet)
	route := &routers.Route{Spec: doc, Path: specPath, PathItem: item, Method: http.MethodGet, Operation: op}
	input := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: &openapi3filter.RequestValidationInput{Request: req, Route: route},
		Status:                 res.Code,
		Header:                 res.Header(),
		Body:                   io.NopCloser(bytes.NewReader(res.Body.Bytes())),
		Options:                &openapi3filter.Options{IncludeResponseStatus: true},
	}
	return openapi3filter.ValidateResponse(req.Context(), input)
}

// v1SpecPath is the v1 document's path for a request path, or "" for paths it does not describe.
func v1SpecPath(path string) string {
	spec, ok := strings.CutPrefix(path, "/v1/")
	if !ok {
		return ""
	}
	switch m := v1IntervalRe.FindStringSubmatch(spec); {
	case spec == "currencies":
		return "/currencies"
	case v1LatestRe.MatchString(spec):
		return "/latest"
	case v1DateRe.MatchString(spec):
		return "/{date}"
	case m != nil && m[2] == "":
		return "/{start_date}.."
	case m != nil:
		return "/{start_date}..{end_date}"
	}
	return ""
}

// Every GET in the golden corpus that v1's document describes, answered by the Go handler, conforms to it.
func TestV1ResponsesMatchOpenAPI(t *testing.T) {
	doc := loadSpec(t, "v1/openapi.json")
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
		specPath := v1SpecPath(strings.SplitN(c.Path, "?", 2)[0])
		if c.Method != http.MethodGet || specPath == "" || len(c.Headers) > 0 {
			continue
		}
		req := httptest.NewRequest(c.Method, c.Path, nil)
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		// v1's document lists 200 and 404 only; its 422s (as Ruby's) are undocumented.
		if doc.Paths.Find(specPath).Get.Responses.Status(res.Code) == nil {
			continue
		}
		if err := validateResponse(t, doc, specPath, req, res); err != nil {
			t.Errorf("%s (%d): %v", c.Request, res.Code, err)
		}
		checked++
	}
	if checked < 30 {
		t.Fatalf("checked only %d responses", checked)
	}
}
