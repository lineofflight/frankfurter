package adapter

import (
	"crypto/tls"
	"crypto/x509"
	"embed"
	"errors"
	"net"
	"net/http"
	"path"
	"slices"
	"time"
)

// Some provider sites serve their leaf certificate without the intermediate
// that issued it. Browsers fetch the missing link on the fly; Go does not.
// These intermediates (copied from config/ca_bundles) are offered during
// verification. Chains still have to end at a system root, so nothing new is
// trusted.
//
//go:embed cabundles/*.pem
var caBundles embed.FS

var intermediates = func() *x509.CertPool {
	pool := x509.NewCertPool()
	entries, err := caBundles.ReadDir("cabundles")
	if err != nil {
		panic(err)
	}
	for _, e := range entries {
		pem, err := caBundles.ReadFile(path.Join("cabundles", e.Name()))
		if err != nil {
			panic(err)
		}
		if !pool.AppendCertsFromPEM(pem) {
			panic("adapter: no certificate in cabundles/" + e.Name())
		}
	}
	return pool
}()

// NewClient returns the production HTTP client, with the Ruby app's timeouts
// (10s connect, 120s read) and the bundled intermediates. Adapters never call
// it directly; the registry's caller passes a client in.
func NewClient() *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DialContext = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	t.TLSHandshakeTimeout = 10 * time.Second
	t.ResponseHeaderTimeout = 120 * time.Second
	t.TLSClientConfig = &tls.Config{
		// Verification is not skipped: VerifyConnection below redoes it with
		// the extra intermediates.
		InsecureSkipVerify: true,
		VerifyConnection:   verifyConnection,
	}
	return &http.Client{Transport: t, Timeout: 10 * time.Minute}
}

// WithCipherSuites returns a copy of client that also offers suites in TLS 1.2
// handshakes, on top of the ones it already offers (Go's secure defaults unless
// set). It is for the odd server that accepts only a suite Go no longer offers
// by default. Certificate verification is unchanged. A nil client means
// NewClient(); one whose transport is not an *http.Transport, such as the test
// recorder, is returned as is.
func WithCipherSuites(client *http.Client, suites ...uint16) *http.Client {
	if client == nil {
		client = NewClient()
	}
	t, ok := client.Transport.(*http.Transport)
	if !ok {
		return client
	}
	t = t.Clone()
	cfg := t.TLSClientConfig.Clone()
	if cfg == nil {
		cfg = &tls.Config{}
	}
	offered := cfg.CipherSuites
	if offered == nil {
		for _, s := range tls.CipherSuites() {
			offered = append(offered, s.ID)
		}
	}
	cfg.CipherSuites = append(slices.Clone(offered), suites...)
	t.TLSClientConfig = cfg
	c := *client
	c.Transport = t
	return &c
}

// WithMaxVersion returns a copy of client that offers TLS versions up to max
// (tls.VersionTLS12, say) and no higher. It is for the odd server that speaks
// an older version and whose edge rejects something Go adds to the hello only
// when it offers a newer one. Certificate verification is unchanged. A nil
// client means NewClient(); one whose transport is not an *http.Transport, such
// as the test recorder, is returned as is.
func WithMaxVersion(client *http.Client, max uint16) *http.Client {
	if client == nil {
		client = NewClient()
	}
	t, ok := client.Transport.(*http.Transport)
	if !ok {
		return client
	}
	t = t.Clone()
	cfg := t.TLSClientConfig.Clone()
	if cfg == nil {
		cfg = &tls.Config{}
	}
	cfg.MaxVersion = max
	t.TLSClientConfig = cfg
	c := *client
	c.Transport = t
	return &c
}

func verifyConnection(cs tls.ConnectionState) error {
	if len(cs.PeerCertificates) == 0 {
		return errors.New("tls: server sent no certificate")
	}
	// An empty DNSName would skip the hostname check. ServerName is empty when
	// the host is an IP address (TLS sends no name for one), so fail closed.
	if cs.ServerName == "" {
		return errors.New("tls: no server name to verify")
	}
	pool := intermediates.Clone()
	for _, cert := range cs.PeerCertificates[1:] {
		pool.AddCert(cert)
	}
	_, err := cs.PeerCertificates[0].Verify(x509.VerifyOptions{
		DNSName:       cs.ServerName,
		Intermediates: pool,
	})
	return err
}
