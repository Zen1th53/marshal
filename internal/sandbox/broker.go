package sandbox

import (
	"crypto/x509"
	"fmt"
	"os"
)

// SystemRootBundle provides PEM roots for SSL_CERT_FILE, which replaces rather
// than extends platform trust. Platform-specific discovery stays in the sandbox
// backend; the broker only receives public PEM bytes and a target HOME path.
func SystemRootBundle() ([]byte, error) {
	for _, path := range []string{"/etc/ssl/certs/ca-certificates.crt", "/etc/pki/tls/certs/ca-bundle.crt", "/etc/ssl/cert.pem"} {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		pool := x509.NewCertPool()
		if pool.AppendCertsFromPEM(raw) {
			return raw, nil
		}
	}
	return nil, fmt.Errorf("credential broker: system TLS root bundle unavailable")
}
