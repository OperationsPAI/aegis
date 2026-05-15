package client

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// DefaultTransport returns an http.RoundTripper derived from
// http.DefaultTransport with a TLS config that trusts:
//
//   - the system root pool,
//   - every PEM file under ~/.aegisctl/certs/*.{crt,pem},
//   - the file at ~/.aegisctl/ca.crt (if present),
//   - the file pointed to by $AEGIS_CA_CERT (if set).
//
// $AEGIS_INSECURE_SKIP_VERIFY=1 disables verification entirely (for
// scratch clusters that haven't trusted a real CA yet).
//
// The auto-discovery is here because bytecluster runs `tls internal` —
// edge-proxy Caddy issues a self-signed root and clients need to trust it
// without juggling SSL_CERT_FILE on every invocation.
func DefaultTransport() http.RoundTripper {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return http.DefaultTransport
	}
	t := base.Clone()
	cfg := tlsConfig()
	if cfg != nil {
		t.TLSClientConfig = cfg
	}
	return t
}

func tlsConfig() *tls.Config {
	insecure := envTruthy(os.Getenv("AEGIS_INSECURE_SKIP_VERIFY"))

	pool, _ := x509.SystemCertPool()
	if pool == nil {
		pool = x509.NewCertPool()
	}
	added := false

	home, _ := os.UserHomeDir()
	if home != "" {
		certDir := filepath.Join(home, ".aegisctl", "certs")
		if entries, err := os.ReadDir(certDir); err == nil {
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				name := strings.ToLower(e.Name())
				if !strings.HasSuffix(name, ".crt") && !strings.HasSuffix(name, ".pem") {
					continue
				}
				if appendCAFile(pool, filepath.Join(certDir, e.Name())) {
					added = true
				}
			}
		}
		if appendCAFile(pool, filepath.Join(home, ".aegisctl", "ca.crt")) {
			added = true
		}
	}
	if p := os.Getenv("AEGIS_CA_CERT"); p != "" {
		if appendCAFile(pool, p) {
			added = true
		}
	}

	if !insecure && !added {
		return nil
	}
	cfg := &tls.Config{InsecureSkipVerify: insecure}
	if added {
		cfg.RootCAs = pool
	}
	return cfg
}

func appendCAFile(pool *x509.CertPool, path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return pool.AppendCertsFromPEM(data)
}

func envTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
