package cache

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDoesNothingWhenNotConfigured(t *testing.T) {
	c := New("", "")
	c.Endpoint = "http://127.0.0.1:1" // would fail if it were called
	if err := c.Purge(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func stub(t *testing.T, status int, body string, seen *int) *Cache {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen++
		if r.Method != http.MethodPost || r.URL.Path != "/zones/z/purge_cache" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer t" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q", got)
		}
		if b, _ := io.ReadAll(r.Body); string(b) != `{"purge_everything":true}` {
			t.Errorf("body = %s", b)
		}
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	c := New("z", "t")
	c.Endpoint = srv.URL
	c.Client = srv.Client()
	return c
}

func TestRaisesWhenPurgeEndpointRejects(t *testing.T) {
	var seen int
	c := stub(t, http.StatusServiceUnavailable, "", &seen)
	if err := c.Purge(context.Background()); err == nil {
		t.Fatal("want an error on 503")
	}
	if seen != 1 {
		t.Fatalf("requests = %d", seen)
	}
}

func TestSucceedsQuietlyOn2xx(t *testing.T) {
	var seen int
	c := stub(t, http.StatusOK, `{"success":true}`, &seen)
	if err := c.Purge(context.Background()); err != nil {
		t.Fatal(err)
	}
	if seen != 1 {
		t.Fatalf("requests = %d", seen)
	}
}

// debounced returns a cache whose purges run fn and whose clock the test advances by hand.
func debounced(fn func() error) (*Cache, func()) {
	now := time.Unix(1_000_000, 0)
	c := &Cache{Window: 300 * time.Second}
	c.now = func() time.Time { return now }
	c.purge = func(context.Context) error { return fn() }
	// Moves the clock past the window, so it reads as expired, as the Ruby spec rewinds @last_purge_at.
	expire := func() { now = now.Add(c.Window) }
	return c, expire
}

func counter(n *int) func() error { return func() error { *n++; return nil } }

func TestPurgesImmediatelyOnFirstCallOfQuietWindow(t *testing.T) {
	var purges int
	c, _ := debounced(counter(&purges))
	c.PurgeDebounced(context.Background())
	if purges != 1 {
		t.Fatalf("purges = %d", purges)
	}
}

func TestCoalescesCallsInsideWindowAndFlushesOnceItExpires(t *testing.T) {
	ctx := context.Background()
	var purges int
	c, expire := debounced(counter(&purges))
	c.PurgeDebounced(ctx)
	c.PurgeDebounced(ctx)
	c.PurgeDebounced(ctx)
	if purges != 1 {
		t.Fatalf("after debounced calls: purges = %d", purges)
	}
	c.PurgePending(ctx)
	if purges != 1 {
		t.Fatalf("inside window: purges = %d", purges)
	}
	expire()
	c.PurgePending(ctx)
	if purges != 2 {
		t.Fatalf("after expiry: purges = %d", purges)
	}
	c.PurgePending(ctx)
	if purges != 2 {
		t.Fatalf("nothing pending: purges = %d", purges)
	}
}

func TestPurgesImmediatelyAgainOnceWindowExpired(t *testing.T) {
	ctx := context.Background()
	var purges int
	c, expire := debounced(counter(&purges))
	c.PurgeDebounced(ctx)
	expire()
	c.PurgeDebounced(ctx)
	if purges != 2 {
		t.Fatalf("purges = %d", purges)
	}
}

func TestFlushesPendingRegardlessOfWindowWhenAsked(t *testing.T) {
	ctx := context.Background()
	var purges int
	c, _ := debounced(counter(&purges))
	c.PurgeDebounced(ctx)
	c.PurgeDebounced(ctx)
	c.FlushPending(ctx)
	if purges != 2 {
		t.Fatalf("purges = %d", purges)
	}
}

func TestDoesNotFlushWhenNothingPending(t *testing.T) {
	ctx := context.Background()
	var purges int
	c, _ := debounced(counter(&purges))
	c.PurgeDebounced(ctx)
	c.FlushPending(ctx)
	if purges != 1 {
		t.Fatalf("purges = %d", purges)
	}
}

func TestCoalescesCallersArrivingWhilePurgeInFlight(t *testing.T) {
	ctx := context.Background()
	var purges int
	var c *Cache
	c, _ = debounced(func() error {
		purges++
		if purges == 1 {
			c.PurgeDebounced(ctx)
		}
		return nil
	})
	c.PurgeDebounced(ctx)
	if purges != 1 {
		t.Fatalf("purges = %d", purges)
	}
	if !c.pending {
		t.Fatal("want a pending purge")
	}
}

var errTimeout = errors.New("open timeout")

func TestReMarksFailedFlushPending(t *testing.T) {
	ctx := context.Background()
	var calls int
	c, expire := debounced(func() error {
		calls++
		if calls == 2 {
			return errTimeout
		}
		return nil
	})
	c.PurgeDebounced(ctx)
	c.PurgeDebounced(ctx)
	expire()
	if err := c.PurgePending(ctx); !errors.Is(err, errTimeout) {
		t.Fatalf("err = %v", err)
	}
	// The failed attempt opened a new window, so the retry waits for it to expire.
	c.PurgePending(ctx)
	if calls != 2 {
		t.Fatalf("calls = %d", calls)
	}
	expire()
	c.PurgePending(ctx)
	if calls != 3 {
		t.Fatalf("calls = %d", calls)
	}
}

func TestReMarksFailedLeadingPurgePending(t *testing.T) {
	ctx := context.Background()
	var calls int
	c, expire := debounced(func() error {
		calls++
		if calls == 1 {
			return errTimeout
		}
		return nil
	})
	if err := c.PurgeDebounced(ctx); !errors.Is(err, errTimeout) {
		t.Fatalf("err = %v", err)
	}
	expire()
	c.PurgePending(ctx)
	if calls != 2 {
		t.Fatalf("calls = %d", calls)
	}
}
