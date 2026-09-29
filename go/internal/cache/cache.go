// Package cache purges the CDN cache after data imports. Only Cloudflare is supported, and every call is a no-op when
// its credentials are not configured.
package cache

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Endpoint is Cloudflare's API root.
const Endpoint = "https://api.cloudflare.com/client/v4"

// DefaultWindow is CACHE_PURGE_DEBOUNCE_SECONDS, or 300 seconds. It parses like Ruby's Integer() and panics at startup
// on an invalid value, as Integer() raises on load.
var DefaultWindow = defaultWindow()

func defaultWindow() time.Duration {
	s, ok := os.LookupEnv("CACHE_PURGE_DEBOUNCE_SECONDS")
	if !ok {
		return 300 * time.Second
	}
	n, err := strconv.ParseInt(strings.TrimSpace(s), 0, 0)
	if err != nil {
		panic(fmt.Sprintf("CACHE_PURGE_DEBOUNCE_SECONDS: %v", err))
	}
	return time.Duration(n) * time.Second
}

// Cache purges one Cloudflare zone and debounces purges across the process. Share one Cache per process: the debounce
// state lives on it.
type Cache struct {
	ZoneID   string
	APIToken string
	Endpoint string        // defaults to Endpoint
	Client   *http.Client  // defaults to a client with a 60s timeout
	Window   time.Duration // the debounce window; defaults to DefaultWindow

	// Test seams: purge defaults to Purge, now to time.Now.
	purge func(ctx context.Context) error
	now   func() time.Time

	mu        sync.Mutex
	pending   bool
	lastPurge time.Time // when the last purge attempt started; zero before the first
}

// New returns a cache for the given zone and token. Empty values leave it unconfigured.
func New(zoneID, apiToken string) *Cache {
	return &Cache{ZoneID: zoneID, APIToken: apiToken}
}

// FromEnv returns a cache configured from CLOUDFLARE_ZONE_ID and CLOUDFLARE_API_TOKEN.
func FromEnv() *Cache {
	return New(os.Getenv("CLOUDFLARE_ZONE_ID"), os.Getenv("CLOUDFLARE_API_TOKEN"))
}

// Configured reports whether both credentials are set.
func (c *Cache) Configured() bool { return c.ZoneID != "" && c.APIToken != "" }

// Purge empties the whole zone. It does nothing when unconfigured and returns an error on any non-2xx response, so a
// rejected purge counts as a failure and stays pending for retry.
func (c *Cache) Purge(ctx context.Context) error {
	if !c.Configured() {
		return nil
	}
	endpoint := c.Endpoint
	if endpoint == "" {
		endpoint = Endpoint
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/zones/"+c.ZoneID+"/purge_cache",
		strings.NewReader(`{"purge_everything":true}`))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIToken)
	req.Header.Set("Content-Type", "application/json")
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("purge cache: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("purge cache: %s", resp.Status)
	}
	return nil
}

// PurgeDebounced is the leading edge of the purge debounce (#568): with ~50 providers publishing daily, a purge per
// provider insert dumps the whole CDN cache many times a day, and a deploy's startup backfill fires a burst of purges
// within minutes. The first insert of a quiet window purges immediately; later inserts coalesce into a pending purge
// for PurgePending to flush.
func (c *Cache) PurgeDebounced(ctx context.Context) error {
	c.mu.Lock()
	fire := !c.windowOpen()
	if fire {
		c.openWindow()
	} else {
		c.pending = true
	}
	c.mu.Unlock()
	if !fire {
		return nil
	}
	return c.attempt(ctx)
}

// PurgePending is the trailing edge: it flushes the coalesced purge once the window has expired, so the last insert
// of a backfill wave always becomes visible. The scheduler calls it every minute; it is safe at any cadence. A failed
// purge is marked pending again, so a later call retries it.
func (c *Cache) PurgePending(ctx context.Context) error { return c.flush(ctx, false) }

// FlushPending flushes a pending purge regardless of the window (Ruby's purge_pending(ignore_window: true)): for the
// end of a backfill run, when the wave is over and the process is about to exit.
func (c *Cache) FlushPending(ctx context.Context) error { return c.flush(ctx, true) }

func (c *Cache) flush(ctx context.Context, ignoreWindow bool) error {
	c.mu.Lock()
	fire := c.pending && (ignoreWindow || !c.windowOpen())
	if fire {
		c.openWindow()
	}
	c.mu.Unlock()
	if !fire {
		return nil
	}
	return c.attempt(ctx)
}

// openWindow starts the window when a purge attempt starts, not when it completes, so callers arriving while the HTTP
// call is in flight coalesce into pending instead of firing concurrently. Callers hold mu.
func (c *Cache) openWindow() {
	c.pending = false
	c.lastPurge = c.clock()
}

func (c *Cache) windowOpen() bool {
	return !c.lastPurge.IsZero() && c.clock().Sub(c.lastPurge) < c.window()
}

func (c *Cache) attempt(ctx context.Context) error {
	purge := c.purge
	if purge == nil {
		purge = c.Purge
	}
	if err := purge(ctx); err != nil {
		c.mu.Lock()
		c.pending = true
		c.mu.Unlock()
		return err
	}
	return nil
}

func (c *Cache) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (c *Cache) window() time.Duration {
	if c.Window > 0 {
		return c.Window
	}
	return DefaultWindow
}
