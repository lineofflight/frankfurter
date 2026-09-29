package adapter

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
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
