// Command smokeparity sends a request corpus to the Ruby and Go servers and compares the answers: status, media type
// and body, with JSON compared semantically (key order ignored, numbers within 1e-6 relative, error wording ignored),
// NDJSON line by line and CSV field by field. Every Go v2 answer the OpenAPI document describes is also validated
// against it. go/scripts/smoke_parity.sh starts the servers and runs it.
//
//	go run ./scripts/smokeparity -ruby http://localhost:9301 -go http://localhost:9302 \
//	  -corpus scripts/smoke_corpus.txt -openapi ../lib/public/v2/openapi.json
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
)

const tolerance = 1e-6

type request struct {
	line    string
	method  string
	path    string
	headers map[string]string
}

type response struct {
	status int
	header http.Header
	body   []byte
}

func main() {
	rubyURL := flag.String("ruby", "http://localhost:9301", "Ruby server")
	goURL := flag.String("go", "http://localhost:9302", "Go server")
	corpus := flag.String("corpus", "scripts/smoke_corpus.txt", "request corpus")
	spec := flag.String("openapi", "../lib/public/v2/openapi.json", "v2 OpenAPI document")
	timeout := flag.Duration("timeout", 5*time.Minute, "per-request timeout")
	flag.Parse()

	reqs, err := readCorpus(*corpus)
	if err != nil {
		fatal(err)
	}
	doc, err := loadSpec(*spec)
	if err != nil {
		fatal(err)
	}
	client := &http.Client{
		Timeout:       *timeout,
		Transport:     &http.Transport{DisableCompression: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	matched, validated := 0, 0
	var problems []string
	for _, r := range reqs {
		rb, err := send(client, *rubyURL, r)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: ruby: %v", r.line, err))
			continue
		}
		gr, err := send(client, *goURL, r)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: go: %v", r.line, err))
			continue
		}
		diffs := compare(r, rb, gr)
		if len(diffs) == 0 {
			matched++
		}
		for _, d := range diffs {
			problems = append(problems, fmt.Sprintf("%s: %s", r.line, d))
		}
		if ok, err := validate(doc, r, gr); ok {
			validated++
			if err != nil {
				problems = append(problems, fmt.Sprintf("%s: openapi: %v", r.line, err))
			}
		}
	}

	for _, p := range problems {
		fmt.Println("MISMATCH", p)
	}
	fmt.Printf("%d requests, %d matched, %d Go v2 responses validated against OpenAPI, %d problems\n",
		len(reqs), matched, validated, len(problems))
	if len(problems) > 0 {
		os.Exit(1)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "smokeparity:", err)
	os.Exit(2)
}

func readCorpus(path string) ([]request, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []request
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, " | ")
		method, path, ok := strings.Cut(parts[0], " ")
		if !ok {
			return nil, fmt.Errorf("bad corpus line %q", line)
		}
		r := request{line: line, method: method, path: path, headers: map[string]string{}}
		for _, h := range parts[1:] {
			k, v, ok := strings.Cut(h, ":")
			if !ok {
				return nil, fmt.Errorf("bad header in %q", line)
			}
			r.headers[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
		out = append(out, r)
	}
	return out, sc.Err()
}

func send(client *http.Client, base string, r request) (response, error) {
	req, err := http.NewRequest(r.method, base+r.path, nil)
	if err != nil {
		return response{}, err
	}
	for k, v := range r.headers {
		req.Header.Set(k, v)
	}
	res, err := client.Do(req)
	if err != nil {
		return response{}, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return response{}, err
	}
	return response{res.StatusCode, res.Header, body}, nil
}

func mediaType(h http.Header) string {
	mt, _, err := mime.ParseMediaType(h.Get("Content-Type"))
	if err != nil {
		return h.Get("Content-Type")
	}
	return mt
}

// compare lists how Go's answer differs from Ruby's.
func compare(r request, rb, gr response) []string {
	if rb.status != gr.status {
		return []string{fmt.Sprintf("status %d, Ruby %d (Go body %.200q)", gr.status, rb.status, gr.body)}
	}
	var out []string
	mt := mediaType(rb.header)
	if rb.status != http.StatusNotModified && len(rb.body) > 0 && mediaType(gr.header) != mt {
		out = append(out, fmt.Sprintf("content-type %q, Ruby %q", gr.header.Get("Content-Type"), rb.header.Get("Content-Type")))
	}
	if cd := rb.header.Get("Content-Disposition"); cd != gr.header.Get("Content-Disposition") {
		out = append(out, fmt.Sprintf("content-disposition %q, Ruby %q", gr.header.Get("Content-Disposition"), cd))
	}
	if r.method == http.MethodHead {
		return out
	}
	switch {
	case strings.HasSuffix(mt, "json") && mt != "application/x-ndjson":
		out = append(out, compareJSON(rb.body, gr.body, rb.status >= 400)...)
	case mt == "application/x-ndjson":
		out = append(out, compareLines(rb.body, gr.body)...)
	case mt == "text/csv":
		out = append(out, compareCSV(rb.body, gr.body)...)
	default:
		if !bytes.Equal(rb.body, gr.body) {
			out = append(out, fmt.Sprintf("body differs (%d bytes, Ruby %d)", len(gr.body), len(rb.body)))
		}
	}
	return out
}

func compareJSON(want, got []byte, errorBody bool) []string {
	var w, g any
	if err := json.Unmarshal(want, &w); err != nil {
		return []string{fmt.Sprintf("Ruby body is not JSON: %v", err)}
	}
	if err := json.Unmarshal(got, &g); err != nil {
		return []string{fmt.Sprintf("Go body is not JSON: %v (%.200q)", err, got)}
	}
	return limit(diffJSON("", w, g, errorBody))
}

func compareLines(want, got []byte) []string {
	wl := strings.Split(strings.TrimSuffix(string(want), "\n"), "\n")
	gl := strings.Split(strings.TrimSuffix(string(got), "\n"), "\n")
	if len(wl) != len(gl) {
		return []string{fmt.Sprintf("%d NDJSON lines, Ruby %d", len(gl), len(wl))}
	}
	var out []string
	for i := range wl {
		for _, d := range compareJSON([]byte(wl[i]), []byte(gl[i]), false) {
			out = append(out, fmt.Sprintf("line %d: %s", i, d))
		}
	}
	return limit(out)
}

func compareCSV(want, got []byte) []string {
	w, err := csv.NewReader(bytes.NewReader(want)).ReadAll()
	if err != nil {
		return []string{fmt.Sprintf("Ruby CSV: %v", err)}
	}
	g, err := csv.NewReader(bytes.NewReader(got)).ReadAll()
	if err != nil {
		return []string{fmt.Sprintf("Go CSV: %v", err)}
	}
	if len(w) != len(g) {
		return []string{fmt.Sprintf("%d CSV rows, Ruby %d", len(g), len(w))}
	}
	var out []string
	for i := range w {
		if len(w[i]) != len(g[i]) {
			out = append(out, fmt.Sprintf("row %d: %v, Ruby %v", i, g[i], w[i]))
			continue
		}
		for j := range w[i] {
			if !sameField(w[i][j], g[i][j]) {
				out = append(out, fmt.Sprintf("row %d field %d: %q, Ruby %q", i, j, g[i][j], w[i][j]))
			}
		}
	}
	return limit(out)
}

func sameField(w, g string) bool {
	if w == g {
		return true
	}
	wf, err1 := strconv.ParseFloat(w, 64)
	gf, err2 := strconv.ParseFloat(g, 64)
	return err1 == nil && err2 == nil && closeEnough(wf, gf)
}

// diffJSON compares Ruby's value (want) with Go's (got): object keys in any order, arrays in order, numbers within
// tolerance. An error body's message only has to be a non-empty string.
func diffJSON(path string, want, got any, errorBody bool) []string {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return []string{fmt.Sprintf("%s: got %T, Ruby object", root(path), got)}
		}
		var out []string
		for _, k := range sortedKeys(w) {
			gv, ok := g[k]
			if !ok {
				out = append(out, fmt.Sprintf("%s.%s: missing", path, k))
				continue
			}
			if errorBody && path == "" && k == "message" {
				if s, ok := gv.(string); !ok || s == "" {
					out = append(out, fmt.Sprintf(".message: %v", gv))
				}
				continue
			}
			out = append(out, diffJSON(path+"."+k, w[k], gv, errorBody)...)
		}
		for _, k := range sortedKeys(g) {
			if _, ok := w[k]; !ok {
				out = append(out, fmt.Sprintf("%s.%s: extra", path, k))
			}
		}
		return out
	case []any:
		g, ok := got.([]any)
		if !ok {
			return []string{fmt.Sprintf("%s: got %T, Ruby array", root(path), got)}
		}
		if len(g) != len(w) {
			return []string{fmt.Sprintf("%s: %d elements, Ruby %d", root(path), len(g), len(w))}
		}
		var out []string
		for i := range w {
			out = append(out, diffJSON(fmt.Sprintf("%s[%d]", path, i), w[i], g[i], errorBody)...)
		}
		return out
	case float64:
		if g, ok := got.(float64); !ok || !closeEnough(w, g) {
			return []string{fmt.Sprintf("%s: got %v, Ruby %v", root(path), got, want)}
		}
		return nil
	default:
		if want != got {
			return []string{fmt.Sprintf("%s: got %.120v, Ruby %.120v", root(path), got, want)}
		}
		return nil
	}
}

func closeEnough(w, g float64) bool {
	return w == g || math.Abs(w-g) <= tolerance*math.Max(math.Abs(w), math.Abs(g))
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func root(path string) string {
	if path == "" {
		return "body"
	}
	return path
}

// limit keeps a long list of differences readable.
func limit(diffs []string) []string {
	if len(diffs) > 10 {
		return append(diffs[:10], fmt.Sprintf("... and %d more", len(diffs)-10))
	}
	return diffs
}

func loadSpec(path string) (*openapi3.T, error) {
	doc, err := openapi3.NewLoader().LoadFromFile(path)
	if err != nil {
		return nil, err
	}
	return doc, doc.Validate(context.Background(), openapi3.DisableExamplesValidation())
}

// validate checks a Go answer to a v2 GET against the document. It reports whether the response was checked:
// undocumented paths, statuses and CSV are skipped.
func validate(doc *openapi3.T, r request, res response) (bool, error) {
	path, query, _ := strings.Cut(r.path, "?")
	specPath := v2SpecPath(path)
	if r.method != http.MethodGet || specPath == "" {
		return false, nil
	}
	item := doc.Paths.Find(specPath)
	op := item.GetOperation(http.MethodGet)
	if op.Responses.Status(res.status) == nil {
		return false, nil
	}
	switch mediaType(res.header) {
	case "text/csv":
		return false, nil
	case "application/x-ndjson":
		// The document types the stream as a string of Rate objects, one per line.
		rate := doc.Components.Schemas["Rate"].Value
		for i, line := range strings.Split(strings.TrimSuffix(string(res.body), "\n"), "\n") {
			if line == "" {
				continue
			}
			var v any
			if err := json.Unmarshal([]byte(line), &v); err != nil {
				return true, fmt.Errorf("line %d: %w", i, err)
			}
			if err := rate.VisitJSON(v); err != nil {
				return true, fmt.Errorf("line %d: %w", i, err)
			}
		}
		return true, nil
	}
	req, err := http.NewRequest(http.MethodGet, "http://localhost"+path+"?"+query, nil)
	if err != nil {
		return true, err
	}
	for k, v := range r.headers {
		req.Header.Set(k, v)
	}
	route := &routers.Route{Spec: doc, Path: specPath, PathItem: item, Method: http.MethodGet, Operation: op}
	return true, openapi3filter.ValidateResponse(context.Background(), &openapi3filter.ResponseValidationInput{
		RequestValidationInput: &openapi3filter.RequestValidationInput{Request: req, Route: route},
		Status:                 res.status,
		Header:                 res.header,
		Body:                   io.NopCloser(bytes.NewReader(res.body)),
		Options:                &openapi3filter.Options{IncludeResponseStatus: true},
	})
}

// v2SpecPath is the document's path for a request path, or "" for paths it does not describe (an extension picks
// another representation).
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
