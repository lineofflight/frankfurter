package api

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
)

// headerWriter runs before on the response headers once the status is known, just before they are sent. It is how a
// middleware edits the headers of a response it has passed downstream, as a Rack middleware does after @app.call.
type headerWriter struct {
	http.ResponseWriter
	before func(status int, h http.Header)
	sent   bool
}

func (w *headerWriter) WriteHeader(status int) {
	if !w.sent {
		w.sent = true
		w.before(status, w.Header())
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *headerWriter) Write(b []byte) (int, error) {
	if !w.sent {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

func (w *headerWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Flush lets streamed responses reach the client through the wrapper.
func (w *headerWriter) Flush() {
	if !w.sent {
		w.WriteHeader(http.StatusOK)
	}
	http.NewResponseController(w.ResponseWriter).Flush()
}

func withHeaders(next http.Handler, before func(r *http.Request, status int, h http.Header)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hw := &headerWriter{ResponseWriter: w, before: func(status int, h http.Header) { before(r, status, h) }}
		next.ServeHTTP(hw, r)
		if !hw.sent {
			hw.WriteHeader(http.StatusOK) // a handler that wrote nothing still answers 200
		}
	})
}

// noStoreOnError keeps CDNs and caches from holding onto error responses (lib/no_store_on_error.rb). Without it, an
// error inheriting the success path's public caching could be pinned at the edge for a day.
func noStoreOnError(next http.Handler) http.Handler {
	return withHeaders(next, func(_ *http.Request, status int, h http.Header) {
		if status >= 400 {
			h.Set("Cache-Control", "no-store")
		}
	})
}

// noindexExempt are the entry points that stay indexable: the index documents and the OpenAPI specs.
var noindexExempt = []string{"/", "/v1", "/v2", "/v1/openapi.json", "/v2/openapi.json"}

// noindex tells search engines not to index API responses (lib/noindex.rb). Google had crawled hundreds of rate URLs
// it then declined to index; the header makes that explicit. It says nothing about crawling, so user-driven fetchers
// (Claude-User, ChatGPT-User, Perplexity-User) and plain HTTP clients are unaffected, unlike a robots.txt Disallow,
// which some of them honour.
func noindex(next http.Handler) http.Handler {
	return withHeaders(next, func(r *http.Request, _ int, h http.Header) {
		if !slices.Contains(noindexExempt, r.URL.Path) {
			h.Set("X-Robots-Tag", "noindex")
		}
	})
}

// CORS policy, as Rack::Cors configures it in lib/app.rb: any origin, any request headers, GET and OPTIONS.
const (
	corsMethods = "GET, OPTIONS"
	corsMaxAge  = "7200"
)

// cors is Rack::Cors with that single public resource. A preflight (OPTIONS with an Origin and
// Access-Control-Request-Method) is answered here with an empty 200; any other request with an Origin gets the CORS
// headers under whatever the app sets. Every other response carries Vary: Origin.
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			origin = r.Header.Get("X-Origin")
		}
		if origin != "" && r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			method := strings.ToLower(r.Header.Get("Access-Control-Request-Method"))
			if method == "get" || method == "options" {
				h := w.Header()
				setCORS(h)
				if req := r.Header.Get("Access-Control-Request-Headers"); req != "" {
					h.Set("Access-Control-Allow-Headers", req)
				}
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		withHeaders(next, func(_ *http.Request, _ int, h http.Header) {
			if origin != "" {
				saved := h.Clone() // the app's own headers win
				setCORS(h)
				for k, v := range saved {
					h[k] = v
				}
			}
			h.Set("Vary", mergeVary(h.Values("Vary"), "Origin"))
		}).ServeHTTP(w, r)
	})
}

func setCORS(h http.Header) {
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Allow-Methods", corsMethods)
	h.Set("Access-Control-Expose-Headers", "")
	h.Set("Access-Control-Max-Age", corsMaxAge)
}

func mergeVary(existing []string, add string) string {
	var out []string
	for _, v := range existing {
		for _, part := range strings.Split(v, ",") {
			if part = strings.TrimSpace(part); part != "" && !slices.Contains(out, part) {
				out = append(out, part)
			}
		}
	}
	if !slices.Contains(out, add) {
		out = append(out, add)
	}
	return strings.Join(out, ", ")
}

type deadlineKey struct{}

// RequestDeadline is when the request's time budget runs out (zero outside the API handler). Handlers that compute
// while they stream check it themselves and answer 503, as the v2 rate query does.
func RequestDeadline(r *http.Request) time.Time {
	d, _ := r.Context().Value(deadlineKey{}).(time.Time)
	return d
}

// TimeoutError is RequestTimeout::Error: the request outlived its deadline.
type TimeoutError struct{ Timeout time.Duration }

func (e TimeoutError) Error() string {
	return fmt.Sprintf("request exceeded %ds timeout", int(e.Timeout/time.Second))
}

// requestTimeout bounds how long a successful response may take to write (lib/request_timeout.rb). The deadline
// starts when the request arrives and is put on the request context for handlers. Error responses pass through
// untouched: they are small and final, and a 503 the handler generated after the deadline must survive. A body write
// after the deadline fails: before anything is sent the response becomes a 500, as Puma answers a body that raises;
// mid-stream the connection is aborted.
func requestTimeout(next http.Handler, timeout time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deadline := time.Now().Add(timeout)
		r = r.WithContext(context.WithValue(r.Context(), deadlineKey{}, deadline))
		tw := &timedWriter{ResponseWriter: w, deadline: deadline, timeout: timeout, now: time.Now}
		next.ServeHTTP(tw, r)
		tw.finish()
	})
}

// timedWriter holds back the status until the first body write so an expired deadline can still turn a success into
// an error.
type timedWriter struct {
	http.ResponseWriter
	deadline time.Time
	timeout  time.Duration
	now      func() time.Time

	status    int  // held status, 0 until WriteHeader
	committed bool // status sent downstream
	failed    bool
}

func (w *timedWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *timedWriter) Write(b []byte) (int, error) {
	if w.failed {
		return 0, TimeoutError{w.timeout}
	}
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.status < 400 && w.now().After(w.deadline) {
		w.failed = true
		if w.committed {
			panic(http.ErrAbortHandler)
		}
		w.committed = true
		h := w.Header()
		h.Del("Etag")
		h.Del("Content-Length")
		h.Set("Cache-Control", "no-store")
		h.Set("Content-Type", contentTypeJSON)
		w.ResponseWriter.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w.ResponseWriter, `{"status":500,"message":%q}`, TimeoutError{w.timeout}.Error())
		return 0, TimeoutError{w.timeout}
	}
	w.commit()
	return w.ResponseWriter.Write(b)
}

func (w *timedWriter) commit() {
	if !w.committed {
		w.committed = true
		w.ResponseWriter.WriteHeader(w.status)
	}
}

// finish sends a status the handler set without writing a body (a 304, a HEAD answer, an empty 200).
func (w *timedWriter) finish() {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	w.commit()
}

func (w *timedWriter) Flush() {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	w.commit()
	http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *timedWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
