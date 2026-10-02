package api

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// spec/request_timeout_spec.rb. TimedBody's chunks are the handler's writes. A
// deadline already in the past stands in for Ruby's seconds: 0, which relies on
// the clock having moved by the time the body is read.

func serveTimed(t *testing.T, timeout time.Duration, h http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	res := httptest.NewRecorder()
	requestTimeout(h, timeout).ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/", nil))
	return res
}

func TestTimedBodyYieldsEveryChunkBeforeDeadline(t *testing.T) {
	res := serveTimed(t, 1000*time.Second, func(w http.ResponseWriter, _ *http.Request) {
		for _, chunk := range []string{"a", "b", "c"} {
			if _, err := io.WriteString(w, chunk); err != nil {
				t.Fatal(err)
			}
		}
	})
	if res.Body.String() != "abc" {
		t.Fatalf("body = %q", res.Body.String())
	}
}

func TestTimedBodyRaisesWhenDeadlinePassed(t *testing.T) {
	var err error
	res := serveTimed(t, -time.Second, func(w http.ResponseWriter, _ *http.Request) {
		_, err = io.WriteString(w, "a")
	})
	var timeout TimeoutError
	if !errors.As(err, &timeout) {
		t.Fatalf("err = %v", err)
	}
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", res.Code)
	}
	if got := res.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
}

// Ruby's TimedBody#close delegates to the wrapped body so a raise mid-stream
// releases it. In Go the handler owns its resources; what the wrapper must do
// mid-stream is abort the response, which net/http turns into a closed
// connection.
func TestTimedBodyAbortsMidStream(t *testing.T) {
	now := time.Now()
	tw := &timedWriter{ResponseWriter: httptest.NewRecorder(), deadline: now.Add(time.Second), timeout: time.Second,
		now: func() time.Time { return now }}
	if _, err := io.WriteString(tw, "x"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Second)
	defer func() {
		if r := recover(); r != http.ErrAbortHandler {
			t.Fatalf("recovered %v", r)
		}
	}()
	io.WriteString(tw, "y")
	t.Fatal("wrote past the deadline")
}

func TestTimeoutPassesStatusAndHeadersThrough(t *testing.T) {
	res := serveTimed(t, 1000*time.Second, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, "ok")
	})
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d", res.Code)
	}
	if got := res.Header(); len(got) != 1 || got.Get("Content-Type") != "text/plain" {
		t.Fatalf("headers = %v", got)
	}
}

func TestTimeoutBoundsSlowStream(t *testing.T) {
	var err error
	serveTimed(t, -time.Second, func(w http.ResponseWriter, _ *http.Request) {
		_, err = io.WriteString(w, "chunk")
	})
	if !errors.As(err, new(TimeoutError)) {
		t.Fatalf("err = %v", err)
	}
}

func TestTimeoutPassesErrorResponsesThrough(t *testing.T) {
	res := serveTimed(t, -time.Second, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		io.WriteString(w, "too slow")
	})
	if res.Code != http.StatusServiceUnavailable || res.Body.String() != "too slow" {
		t.Fatalf("got %d %q", res.Code, res.Body.String())
	}
}

func TestRequestDeadlineOnContext(t *testing.T) {
	before := time.Now()
	var got time.Time
	serveTimed(t, 90*time.Second, func(_ http.ResponseWriter, r *http.Request) { got = RequestDeadline(r) })
	if got.Before(before.Add(90*time.Second)) || got.After(time.Now().Add(90*time.Second)) {
		t.Fatalf("deadline = %v", got)
	}
}
