package adapter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// UserAgent identifies Frankfurter to providers.
const UserAgent = "Mozilla/5.0 (compatible; Frankfurter; +https://frankfurter.dev)"

// maxTries matches http.rb's retriable default: the first attempt plus four retries.
const maxTries = 5

// Base carries the HTTP client and clock shared by every adapter. Embed it by value and build it with NewBase:
//
//	type Adapter struct{ adapter.Base }
//
//	func New(client *http.Client) adapter.Adapter { return &Adapter{adapter.NewBase(client)} }
type Base struct {
	client *http.Client

	// Now returns the current time. Tests pin it; nil means time.Now.
	Now func() time.Time
}

// NewBase wraps client so that redirects are returned instead of followed: a moved or retired page must fail loudly
// rather than parse as an empty day. A nil client gets NewClient().
func NewBase(client *http.Client) Base {
	if client == nil {
		client = NewClient()
	}
	c := *client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return Base{client: &c}
}

// BackfillRange implements Adapter: fetch everything in one call.
func (b *Base) BackfillRange() int { return 0 }

// LeadDays implements Adapter: no publication ahead of the grace window.
func (b *Base) LeadDays() int { return 0 }

// Revises implements Adapter: published values are final.
func (b *Base) Revises() bool { return false }

// Today returns the current date as UTC midnight, in the server's local calendar as Ruby's Date.today does.
func (b *Base) Today() time.Time {
	now := time.Now
	if b.Now != nil {
		now = b.Now
	}
	y, m, d := now().Date()
	return Date(y, m, d)
}

// Sleep pauses between requests to go easy on a source. It returns early when ctx ends and does nothing under go
// test, as the Ruby adapters' sleep is stubbed out in the test environment.
func (b *Base) Sleep(ctx context.Context, d time.Duration) error {
	if testing.Testing() {
		return nil
	}
	return sleep(ctx, d)
}

// Response is a successful HTTP response with its body read.
type Response struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

// StatusError reports a response outside 2xx that the caller did not allow.
type StatusError struct {
	Method     string
	URL        string
	StatusCode int
	Body       []byte
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("%s %s: HTTP %d", e.Method, e.URL, e.StatusCode)
}

// Get fetches rawURL with query merged into its query string and returns the body.
func (b *Base) Get(ctx context.Context, rawURL string, query url.Values) ([]byte, error) {
	if len(query) > 0 {
		u, err := url.Parse(rawURL)
		if err != nil {
			return nil, err
		}
		q := u.Query()
		for k, vs := range query {
			q[k] = append(q[k], vs...)
		}
		u.RawQuery = q.Encode()
		rawURL = u.String()
	}
	req, err := b.NewRequest(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := b.Do(req)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// PostForm posts form URL-encoded and returns the body.
func (b *Base) PostForm(ctx context.Context, rawURL string, form url.Values) ([]byte, error) {
	req, err := b.NewRequest(ctx, http.MethodPost, rawURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := b.Do(req)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// NewRequest builds a request carrying Frankfurter's User-Agent and an Accept of */*: a request without Accept is a
// bot fingerprint some WAFs reject, sometimes silently with a 200 "Request Rejected" page. Override either header on
// the returned request when a source needs something specific.
func (b *Base) NewRequest(ctx context.Context, method, rawURL string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "*/*")
	return req, nil
}

// Do sends req and reads the response. Any status outside 2xx is a *StatusError unless listed in allow. A 429, or a
// connection error or timeout, is retried up to five attempts in all, waiting as Retry-After says or backing off
// exponentially. A request with a body must be replayable (http.NewRequest sets GetBody for the usual readers).
func (b *Base) Do(req *http.Request, allow ...int) (*Response, error) {
	for attempt := 1; ; attempt++ {
		resp, err := b.try(req)
		if err == nil && resp.StatusCode != http.StatusTooManyRequests {
			if resp.StatusCode/100 == 2 || slices.Contains(allow, resp.StatusCode) {
				return resp, nil
			}
			return nil, &StatusError{req.Method, req.URL.String(), resp.StatusCode, resp.Body}
		}
		if err != nil && !retriable(err) {
			return nil, err
		}
		if attempt == maxTries {
			if err != nil {
				return nil, fmt.Errorf("%s %s: out of retries: %w", req.Method, req.URL, err)
			}
			return nil, fmt.Errorf("%s %s: out of retries with HTTP %d", req.Method, req.URL, resp.StatusCode)
		}

		var header http.Header
		if resp != nil {
			header = resp.Header
		}
		if err := sleep(req.Context(), retryDelay(attempt, header)); err != nil {
			return nil, err
		}
		if req.GetBody != nil {
			body, err := req.GetBody()
			if err != nil {
				return nil, err
			}
			req = req.Clone(req.Context())
			req.Body = body
		}
	}
}

func (b *Base) try(req *http.Request) (*Response, error) {
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return &Response{StatusCode: resp.StatusCode, Header: resp.Header, Body: body}, nil
}

// retriable mirrors http.rb's list: timeouts, refused or reset connections, TLS failures and early EOFs.
func retriable(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	// *url.Error itself satisfies net.Error, so judge what it wraps.
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		err = urlErr.Err
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}

// retryDelay follows Retry-After (seconds or an HTTP date) when present, else backs off 2^(n-1)-1 seconds plus up to
// a second of jitter.
func retryDelay(attempt int, header http.Header) time.Duration {
	if v := strings.TrimSpace(header.Get("Retry-After")); v != "" {
		if secs, err := strconv.Atoi(v); err == nil {
			return time.Duration(secs) * time.Second
		}
		if at, err := http.ParseTime(v); err == nil {
			return max(time.Until(at), 0)
		}
		return 0
	}
	secs := math.Pow(2, float64(attempt-1)) - 1 + rand.Float64()
	return time.Duration(secs * float64(time.Second))
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// CookieHeader turns a response's Set-Cookie headers into a Cookie request header value, keeping each cookie's
// name=value and dropping its attributes.
func CookieHeader(h http.Header) string {
	var pairs []string
	for _, c := range h.Values("Set-Cookie") {
		pair, _, _ := strings.Cut(c, ";")
		pairs = append(pairs, strings.TrimSpace(pair))
	}
	return strings.Join(pairs, "; ")
}
