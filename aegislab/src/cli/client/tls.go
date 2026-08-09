package client

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// TLSOptions controls how the HTTP transport is built. Both fields take
// precedence over env vars and the auto-discovered files in
// ~/.aegisctl/certs/ and ~/.aegisctl/ca.crt.
type TLSOptions struct {
	// CACert is an absolute path to a PEM file containing one or more
	// CA certificates to trust. Empty means "no explicit override".
	CACert string
	// Insecure disables certificate verification entirely. When true,
	// CACert is ignored.
	Insecure bool
}

// DefaultTransport returns an http.RoundTripper that trusts the system
// root pool plus everything auto-discovered under ~/.aegisctl/ and the
// env vars AEGIS_CA_CERT / AEGIS_INSECURE_SKIP_VERIFY. Callers that
// have already resolved TLS options through the flag/env/context chain
// should use TransportFor instead.
func DefaultTransport() http.RoundTripper {
	return TransportFor(TLSOptions{})
}

// TransportFor returns an http.RoundTripper built from the given
// options merged with env vars and auto-discovery. The merge order is:
//
//   - opts.Insecure (explicit caller request) OR env truthy
//   - opts.CACert (explicit caller request) layered on top of the
//     system pool plus all auto-discovered files
//
// bytecluster's edge-proxy runs Caddy `tls internal`, which issues a
// self-signed root that engineers must trust out-of-band. The
// auto-discovery + TOFU dance is here so users don't have to juggle
// SSL_CERT_FILE on every invocation.
func TransportFor(opts TLSOptions) http.RoundTripper {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return http.DefaultTransport
	}
	t := base.Clone()
	cfg := tlsConfigFor(opts)
	if cfg != nil {
		t.TLSClientConfig = cfg
	}
	return t
}

func tlsConfigFor(opts TLSOptions) *tls.Config {
	insecure := opts.Insecure || envTruthy(os.Getenv("AEGIS_INSECURE_SKIP_VERIFY"))

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
	if opts.CACert != "" {
		if appendCAFile(pool, opts.CACert) {
			added = true
		}
	}

	if !insecure && !added {
		return nil
	}
	cfg := &tls.Config{InsecureSkipVerify: insecure} //nolint:gosec // insecure is an explicit opt-in for dev clusters
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
