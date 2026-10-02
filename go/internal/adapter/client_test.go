package adapter

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

// tlsServer starts a TLS test server and returns a production client that
// reaches it under the hostname the test certificate names (example.com).
func tlsServer(t *testing.T) (*http.Client, string) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "ok")
	}))
	t.Cleanup(srv.Close)

	c := NewClient()
	tr := c.Transport.(*http.Transport)
	dial := tr.DialContext
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return dial(ctx, network, srv.Listener.Addr().String())
	}
	return c, srv.URL
}

// InsecureSkipVerify only hands verification to verifyConnection; a
// certificate that doesn't chain to a system root must still be rejected.
func TestNewClientRejectsUntrustedCertificates(t *testing.T) {
	c, _ := tlsServer(t)

	resp, err := c.Get("https://example.com/")
	if err == nil {
		resp.Body.Close()
		t.Fatal("Get succeeded against a self-signed certificate")
	}
	var unknown x509.UnknownAuthorityError
	if !errors.As(err, &unknown) {
		t.Fatalf("err = %v, want x509.UnknownAuthorityError", err)
	}
}

// TLS sends no server name for an IP address, so there is no hostname to check
// the certificate against. Such connections fail rather than verify the chain
// alone. No provider is reached by IP.
func TestNewClientRejectsIPHosts(t *testing.T) {
	c, u := tlsServer(t)

	resp, err := c.Get(u)
	if err == nil {
		resp.Body.Close()
		t.Fatal("Get succeeded against an IP host")
	}
	if !strings.Contains(err.Error(), "no server name") {
		t.Fatalf("err = %v, want the missing server name error", err)
	}
}

// rsaKexGet requests example.com with c from a TLS 1.2 test server that accepts
// only TLS_RSA_WITH_AES_128_GCM_SHA256, as api.bnm.gov.my does, and returns the
// error.
func rsaKexGet(t *testing.T, c *http.Client) error {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "ok")
	}))
	srv.TLS = &tls.Config{
		MaxVersion:   tls.VersionTLS12,
		CipherSuites: []uint16{tls.TLS_RSA_WITH_AES_128_GCM_SHA256},
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)

	c.Transport.(*http.Transport).DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
	}
	resp, err := c.Get("https://example.com/")
	if err == nil {
		resp.Body.Close()
	}
	return err
}

// The shared client can't agree a suite with an RSA key exchange only server.
// With the suite added, the handshake gets as far as verification, which still
// rejects a certificate that doesn't chain to a system root.
func TestWithCipherSuitesKeepsVerification(t *testing.T) {
	if err := rsaKexGet(t, NewClient()); err == nil || !strings.Contains(err.Error(), "handshake failure") {
		t.Fatalf("shared client: err = %v, want a handshake failure", err)
	}

	c := WithCipherSuites(NewClient(), tls.TLS_RSA_WITH_AES_128_GCM_SHA256)
	var unknown x509.UnknownAuthorityError
	if err := rsaKexGet(t, c); !errors.As(err, &unknown) {
		t.Fatalf("err = %v, want x509.UnknownAuthorityError", err)
	}
}

func TestWithCipherSuitesKeepsDefaults(t *testing.T) {
	base := NewClient()
	c := WithCipherSuites(base, tls.TLS_RSA_WITH_AES_128_GCM_SHA256)

	got := c.Transport.(*http.Transport).TLSClientConfig.CipherSuites
	for _, s := range tls.CipherSuites() {
		if !slices.Contains(got, s.ID) {
			t.Errorf("missing default %s", s.Name)
		}
	}
	if base.Transport.(*http.Transport).TLSClientConfig.CipherSuites != nil {
		t.Error("the original client was changed")
	}
}

// versionGet requests example.com with c from a test server limited to TLS
// versions lo through hi, and returns the error.
func versionGet(t *testing.T, c *http.Client, lo, hi uint16) error {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "ok")
	}))
	srv.TLS = &tls.Config{MinVersion: lo, MaxVersion: hi}
	srv.StartTLS()
	t.Cleanup(srv.Close)

	c.Transport.(*http.Transport).DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
	}
	resp, err := c.Get("https://example.com/")
	if err == nil {
		resp.Body.Close()
	}
	return err
}

// A client capped at TLS 1.2 can't reach a TLS 1.3 only server. Against a TLS
// 1.2 server the handshake gets as far as verification, which still rejects a
// certificate that doesn't chain to a system root.
func TestWithMaxVersionKeepsVerification(t *testing.T) {
	c := WithMaxVersion(NewClient(), tls.VersionTLS12)
	if err := versionGet(t, c, tls.VersionTLS13, tls.VersionTLS13); err == nil || !strings.Contains(err.Error(), "protocol version") {
		t.Fatalf("TLS 1.3 server: err = %v, want a protocol version error", err)
	}

	c = WithMaxVersion(NewClient(), tls.VersionTLS12)
	var unknown x509.UnknownAuthorityError
	if err := versionGet(t, c, tls.VersionTLS12, tls.VersionTLS12); !errors.As(err, &unknown) {
		t.Fatalf("TLS 1.2 server: err = %v, want x509.UnknownAuthorityError", err)
	}
}

func TestWithMaxVersionLeavesOriginal(t *testing.T) {
	base := NewClient()
	c := WithMaxVersion(base, tls.VersionTLS12)

	if got := c.Transport.(*http.Transport).TLSClientConfig.MaxVersion; got != tls.VersionTLS12 {
		t.Errorf("MaxVersion = %#x, want TLS 1.2", got)
	}
	if base.Transport.(*http.Transport).TLSClientConfig.MaxVersion != 0 {
		t.Error("the original client was changed")
	}
}
