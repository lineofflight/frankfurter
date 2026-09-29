package api

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/dbtest"
)

// The API golden check: go/scripts/api_golden.rb replays testdata/corpus.txt through the Ruby app and records the
// database it ran on and every response. TestGoldenAPI loads the same tables into a fresh database, replays each
// request through the Go handler with the same today, and compares status, the headers in goldenHeaders and the body
// (JSON semantically: key order ignored, numbers within 1e-9 relative). See docs/core-api_v1.md.

const goldenFile = "testdata/golden/api.json.gz"

type goldenTable struct {
	Columns []string          `json:"columns"`
	Raw     []json.RawMessage `json:"rows"`
}

type goldenResponse struct {
	Request  string            `json:"request"`
	Method   string            `json:"method"`
	Path     string            `json:"path"`
	Headers  map[string]string `json:"headers"`
	Status   int               `json:"status"`
	Response map[string]string `json:"response_headers"`
	Empty    bool              `json:"empty"`
	JSON     json.RawMessage   `json:"json"`
	Text     *string           `json:"text"`
	SHA256   string            `json:"sha256"`
}

type goldenAPI struct {
	Today     string                 `json:"today"`
	Tables    map[string]goldenTable `json:"tables"`
	Responses []goldenResponse       `json:"responses"`
}

// goldenHeaders are compared by value, and by absence when Ruby sent none. Content-Type is skipped on empty bodies.
var goldenHeaders = []string{
	"cache-control", "content-type", "deprecation", "link", "x-robots-tag", "vary", "etag", "retry-after", "allow",
	"access-control-allow-origin", "access-control-allow-methods", "access-control-allow-headers",
	"access-control-expose-headers", "access-control-max-age",
}

func loadGoldenAPI(t *testing.T) goldenAPI {
	t.Helper()
	f, err := os.Open(goldenFile)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var g goldenAPI
	if err := json.NewDecoder(zr).Decode(&g); err != nil {
		t.Fatal(err)
	}
	return g
}

// loadGoldenTables writes every recorded table into conn, replacing what the schema seeded.
func loadGoldenTables(t *testing.T, conn *sql.DB, tables map[string]goldenTable) {
	t.Helper()
	ctx := context.Background()
	err := db.Immediate(ctx, conn, func(q db.Querier) error {
		for name, table := range tables {
			if _, err := q.ExecContext(ctx, "DELETE FROM "+name); err != nil {
				return err
			}
			stmt := "INSERT INTO " + name + " (" + strings.Join(table.Columns, ", ") + ") VALUES (" +
				strings.TrimSuffix(strings.Repeat("?, ", len(table.Columns)), ", ") + ")"
			for _, raw := range table.Raw {
				dec := json.NewDecoder(bytes.NewReader(raw))
				dec.UseNumber()
				var row []any
				if err := dec.Decode(&row); err != nil {
					return err
				}
				for i, v := range row {
					if n, ok := v.(json.Number); ok {
						if n64, err := n.Int64(); err == nil && !strings.ContainsAny(n.String(), ".eE") {
							row[i] = n64
						} else if f, err := n.Float64(); err == nil {
							row[i] = f
						}
					}
				}
				if _, err := q.ExecContext(ctx, stmt, row...); err != nil {
					return fmt.Errorf("%s: %w", name, err)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestGoldenAPI(t *testing.T) {
	g := loadGoldenAPI(t)
	today, err := db.ParseDate(g.Today)
	if err != nil {
		t.Fatal(err)
	}
	conn := dbtest.New(t)
	loadGoldenTables(t, conn, g.Tables)
	h := (&Server{DB: conn, Today: func() time.Time { return today }}).Handler()

	for _, want := range g.Responses {
		req := httptest.NewRequest(want.Method, want.Path, nil)
		for k, v := range want.Headers {
			req.Header.Set(k, v)
		}
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		checkGolden(t, want, res)
	}
}

func checkGolden(t *testing.T, want goldenResponse, res *httptest.ResponseRecorder) {
	t.Helper()
	label := want.Request
	if res.Code != want.Status {
		t.Errorf("%s: status %d, Ruby %d (body %.200s)", label, res.Code, want.Status, res.Body.String())
		return
	}
	for _, name := range goldenHeaders {
		if name == "content-type" && (want.Empty || res.Code == http.StatusNotModified) {
			continue
		}
		rb, rok := want.Response[name]
		gv := res.Header().Values(name)
		if !rok && len(gv) == 0 {
			continue
		}
		if got := strings.Join(gv, ", "); !rok || got != rb {
			t.Errorf("%s: header %s = %q, Ruby %q (present %v)", label, name, got, rb, rok)
		}
	}

	// Go's server drops HEAD bodies itself; the recorder keeps them, and Roda leaves that to Puma.
	if want.Method == http.MethodHead {
		return
	}
	body := res.Body.Bytes()
	switch {
	case want.Empty:
		if len(body) != 0 {
			t.Errorf("%s: body %.200q, Ruby empty", label, body)
		}
	case want.JSON != nil:
		var got, exp any
		if err := json.Unmarshal(body, &got); err != nil {
			t.Errorf("%s: body is not JSON: %v (%.200q)", label, err, body)
			return
		}
		if err := json.Unmarshal(want.JSON, &exp); err != nil {
			t.Fatal(err)
		}
		for _, d := range diffJSON("", exp, got, want.Status >= 400) {
			t.Errorf("%s: %s", label, d)
		}
	case want.Text != nil:
		if string(body) != *want.Text {
			t.Errorf("%s: body %.200q, Ruby %.200q", label, body, *want.Text)
		}
	default:
		sum := sha256.Sum256(body)
		if got := hex.EncodeToString(sum[:]); got != want.SHA256 {
			t.Errorf("%s: body sha256 %s, Ruby %s", label, got, want.SHA256)
		}
	}
}

// diffJSON lists the differences between Ruby's value (want) and Go's (got): object keys in any order, arrays in
// order, numbers within 1e-9 relative. On error responses the message text may differ; it only has to be a string.
func diffJSON(path string, want, got any, errorBody bool) []string {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return []string{fmt.Sprintf("%s: got %T, Ruby object", orRoot(path), got)}
		}
		var out []string
		for _, k := range sortedMapKeys(w) {
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
		for _, k := range sortedMapKeys(g) {
			if _, ok := w[k]; !ok {
				out = append(out, fmt.Sprintf("%s.%s: extra (%v)", path, k, g[k]))
			}
		}
		return out
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return []string{fmt.Sprintf("%s: got %v, Ruby %v", orRoot(path), abbreviate(got), abbreviate(want))}
		}
		var out []string
		for i := range w {
			out = append(out, diffJSON(fmt.Sprintf("%s[%d]", path, i), w[i], g[i], errorBody)...)
		}
		return out
	case float64:
		g, ok := got.(float64)
		if !ok || !closeEnough(w, g) {
			return []string{fmt.Sprintf("%s: got %v, Ruby %v", orRoot(path), got, want)}
		}
		return nil
	default:
		if want != got {
			return []string{fmt.Sprintf("%s: got %v, Ruby %v", orRoot(path), abbreviate(got), abbreviate(want))}
		}
		return nil
	}
}

func closeEnough(want, got float64) bool {
	if want == got {
		return true
	}
	return math.Abs(want-got) <= 1e-9*math.Max(math.Abs(want), math.Abs(got))
}

func sortedMapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func orRoot(path string) string {
	if path == "" {
		return "body"
	}
	return path
}

func abbreviate(v any) string {
	s := fmt.Sprint(v)
	if len(s) > 120 {
		return s[:120] + "..."
	}
	return s
}
