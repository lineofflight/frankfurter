package adapter

import (
	"crypto/tls"
	"crypto/x509"
	"embed"
	"errors"
	"net"
	"net/http"
	"path"
	"time"
)

// Some provider sites serve their leaf certificate without the intermediate that issued it. Browsers fetch the missing
// link on the fly; Go does not. These intermediates (copied from config/ca_bundles) are offered during verification.
// Chains still have to end at a system root, so nothing new is trusted.
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

// NewClient returns the production HTTP client, with the Ruby app's timeouts (10s connect, 120s read) and the
// bundled intermediates. Adapters never call it directly; the registry's caller passes a client in.
func NewClient() *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DialContext = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	t.TLSHandshakeTimeout = 10 * time.Second
	t.ResponseHeaderTimeout = 120 * time.Second
	t.TLSClientConfig = &tls.Config{
		// Verification is not skipped: VerifyConnection below redoes it with the extra intermediates.
		InsecureSkipVerify: true,
		VerifyConnection:   verifyConnection,
	}
	return &http.Client{Transport: t, Timeout: 10 * time.Minute}
}

func verifyConnection(cs tls.ConnectionState) error {
	if len(cs.PeerCertificates) == 0 {
		return errors.New("tls: server sent no certificate")
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
