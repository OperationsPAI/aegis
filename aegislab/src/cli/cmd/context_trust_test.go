package cmd

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aegis/cli/config"
)

func makeCert(t *testing.T, cn string, isCA, selfSigned bool, parent *x509.Certificate, parentKey *ecdsa.PrivateKey) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  isCA,
		BasicConstraintsValid: true,
	}
	if selfSigned || parent == nil {
		parent = tmpl
		parentKey = key
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, &key.PublicKey, parentKey)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	return cert, key
}

func TestPickCAFromChain_PrefersSelfSignedRoot(t *testing.T) {
	root, rootKey := makeCert(t, "root", true, true, nil, nil)
	intermediate, intKey := makeCert(t, "intermediate", true, false, root, rootKey)
	leaf, _ := makeCert(t, "leaf", false, false, intermediate, intKey)

	chain := []*x509.Certificate{leaf, intermediate, root}
	chosen, fallback := pickCAFromChain(chain)
	if fallback {
		t.Fatal("expected non-fallback when self-signed root is present")
	}
	if chosen.Subject.CommonName != "root" {
		t.Errorf("expected root, got %q", chosen.Subject.CommonName)
	}
}

func TestPickCAFromChain_FallsBackToLastIsCA(t *testing.T) {
	root, rootKey := makeCert(t, "interm-only", true, false, nil, nil) // not self-signed (parent nil ⇒ self-signed actually; force not via second pass)
	_ = root
	_ = rootKey
	// Build leaf + intermediate where no entry is self-signed.
	rootCA, rootCAKey := makeCert(t, "root-CA", true, true, nil, nil)
	intermediate, intKey := makeCert(t, "intermediate", true, false, rootCA, rootCAKey)
	leaf, _ := makeCert(t, "leaf", false, false, intermediate, intKey)

	chain := []*x509.Certificate{leaf, intermediate}
	chosen, fallback := pickCAFromChain(chain)
	if fallback {
		t.Fatal("expected non-fallback (intermediate IsCA)")
	}
	if chosen.Subject.CommonName != "intermediate" {
		t.Errorf("expected intermediate, got %q", chosen.Subject.CommonName)
	}
}

func TestPickCAFromChain_LeafFallback(t *testing.T) {
	leaf, _ := makeCert(t, "leaf-only", false, true, nil, nil)
	chain := []*x509.Certificate{leaf}
	chosen, fallback := pickCAFromChain(chain)
	if !fallback {
		t.Fatal("expected leaf-fallback")
	}
	if chosen != leaf {
		t.Errorf("expected leaf, got different cert")
	}
}

// trustCmdSetup wires the temp HOME + config so each test gets a clean
// ~/.aegisctl/certs writable target. Returns a cleanup function.
func trustCmdSetup(t *testing.T) (string, func()) {
	t.Helper()
	tmp := t.TempDir()
	prevHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", tmp); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	prevCfg := cfg
	cfg = &config.Config{Contexts: map[string]config.Context{}, CurrentContext: ""}
	return tmp, func() {
		_ = os.Setenv("HOME", prevHome)
		cfg = prevCfg
	}
}

func TestContextTrust_RejectsNonHTTPS(t *testing.T) {
	_, cleanup := trustCmdSetup(t)
	defer cleanup()
	cfg.Contexts["plain"] = config.Context{Server: "http://example.test:8080"}
	cfg.CurrentContext = "plain"

	err := runContextTrust(contextTrustCmd, nil)
	if err == nil {
		t.Fatal("expected usage error for non-HTTPS server")
	}
	if !strings.Contains(err.Error(), "not HTTPS") {
		t.Errorf("error message: %v", err)
	}
}

func TestContextTrust_PrintDoesNotPersist(t *testing.T) {
	tmp, cleanup := trustCmdSetup(t)
	defer cleanup()
	cfg.Contexts["x"] = config.Context{Server: "https://example.test:8443"}
	cfg.CurrentContext = "x"

	root, rootKey := makeCert(t, "root", true, true, nil, nil)
	leaf, _ := makeCert(t, "leaf", false, false, root, rootKey)
	prevDial := trustDialer
	trustDialer = func(network, addr string, c *tls.Config, _ time.Duration) ([]*x509.Certificate, error) {
		return []*x509.Certificate{leaf, root}, nil
	}
	defer func() { trustDialer = prevDial }()

	contextTrustPrint = true
	contextTrustYes = false
	flagOutput = "table"
	flagQuiet = true
	t.Cleanup(func() {
		contextTrustPrint = false
		flagQuiet = false
		flagOutput = ""
	})

	if err := runContextTrust(contextTrustCmd, nil); err != nil {
		t.Fatalf("trust --print failed: %v", err)
	}

	certsDir := filepath.Join(tmp, ".aegisctl", "certs")
	if entries, err := os.ReadDir(certsDir); err == nil && len(entries) > 0 {
		t.Errorf("--print wrote files: %v", entries)
	}
	if cfg.Contexts["x"].CACert != "" {
		t.Errorf("--print modified context: %+v", cfg.Contexts["x"])
	}
}

func TestContextTrust_NonInteractiveWithoutYesRefuses(t *testing.T) {
	_, cleanup := trustCmdSetup(t)
	defer cleanup()
	cfg.Contexts["y"] = config.Context{Server: "https://example.test:8443"}
	cfg.CurrentContext = "y"

	root, rootKey := makeCert(t, "root", true, true, nil, nil)
	leaf, _ := makeCert(t, "leaf", false, false, root, rootKey)
	prevDial := trustDialer
	trustDialer = func(network, addr string, c *tls.Config, _ time.Duration) ([]*x509.Certificate, error) {
		return []*x509.Certificate{leaf, root}, nil
	}
	defer func() { trustDialer = prevDial }()

	prevNI := flagNonInteractive
	flagNonInteractive = true
	contextTrustYes = false
	contextTrustPrint = false
	flagQuiet = true
	t.Cleanup(func() {
		flagNonInteractive = prevNI
		flagQuiet = false
	})

	err := runContextTrust(contextTrustCmd, nil)
	if err == nil {
		t.Fatal("expected refusal in non-interactive mode without --yes")
	}
	if !strings.Contains(err.Error(), "non-interactive") {
		t.Errorf("error message: %v", err)
	}
}

func TestContextTrust_YesPersistsCAAndUpdatesContext(t *testing.T) {
	tmp, cleanup := trustCmdSetup(t)
	defer cleanup()
	cfg.Contexts["z"] = config.Context{Server: "https://example.test:8443"}
	cfg.CurrentContext = "z"

	root, rootKey := makeCert(t, "root", true, true, nil, nil)
	leaf, _ := makeCert(t, "leaf", false, false, root, rootKey)
	prevDial := trustDialer
	trustDialer = func(network, addr string, c *tls.Config, _ time.Duration) ([]*x509.Certificate, error) {
		return []*x509.Certificate{leaf, root}, nil
	}
	defer func() { trustDialer = prevDial }()

	contextTrustYes = true
	contextTrustPrint = false
	flagQuiet = true
	t.Cleanup(func() {
		contextTrustYes = false
		flagQuiet = false
	})

	if err := runContextTrust(contextTrustCmd, nil); err != nil {
		t.Fatalf("trust --yes failed: %v", err)
	}

	updated := cfg.Contexts["z"]
	if updated.CACert == "" {
		t.Fatal("CACert not updated on context")
	}
	if !strings.HasPrefix(updated.CACert, filepath.Join(tmp, ".aegisctl", "certs")) {
		t.Errorf("CACert path unexpected: %s", updated.CACert)
	}
	data, err := os.ReadFile(updated.CACert)
	if err != nil {
		t.Fatalf("read saved CA: %v", err)
	}
	if !bytes.Contains(data, []byte("BEGIN CERTIFICATE")) {
		t.Errorf("expected PEM-encoded CA, got %q", string(data[:min(60, len(data))]))
	}
}
