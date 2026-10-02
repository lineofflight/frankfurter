package boa

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

// The adapter's client offers only TLS 1.2, so its ClientHello carries no
// ML-DSA schemes, and it still rejects a certificate that doesn't chain to a
// system root.
func TestClientOffersTLS12WithoutMLDSA(t *testing.T) {
	type hello struct {
		versions []uint16
		schemes  []tls.SignatureScheme
	}
	hellos := make(chan hello, 1)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.TLS = &tls.Config{GetConfigForClient: func(h *tls.ClientHelloInfo) (*tls.Config, error) {
		hellos <- hello{slices.Clone(h.SupportedVersions), slices.Clone(h.SignatureSchemes)}
		return nil, nil
	}}
	srv.StartTLS()
	t.Cleanup(srv.Close)

	c := newClient(adapter.NewClient())
	tr := c.Transport.(*http.Transport)
	if v := tr.TLSClientConfig.MaxVersion; v != tls.VersionTLS12 {
		t.Fatalf("MaxVersion = %#x, want TLS 1.2", v)
	}
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
	}
	resp, err := c.Get("https://example.com/")
	if err == nil {
		resp.Body.Close()
		t.Fatal("Get succeeded against a self-signed certificate")
	}
	var unknown x509.UnknownAuthorityError
	if !errors.As(err, &unknown) {
		t.Fatalf("err = %v, want x509.UnknownAuthorityError", err)
	}

	h := <-hellos
	if !slices.Equal(h.versions, []uint16{tls.VersionTLS12}) {
		t.Errorf("offered versions %#x, want TLS 1.2 only", h.versions)
	}
	for _, s := range []tls.SignatureScheme{tls.MLDSA44, tls.MLDSA65, tls.MLDSA87} {
		if slices.Contains(h.schemes, s) {
			t.Errorf("offered %v", s)
		}
	}
}
