package cmd

import (
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"aegis/cli/config"
)

func TestResolveTLSOptions_FlagBeatsEnvAndContext(t *testing.T) {
	prevCfg := cfg
	prevCACert := flagCACert
	prevInsecure := flagInsecure
	prevSet := flagInsecureSet
	t.Cleanup(func() {
		cfg = prevCfg
		flagCACert = prevCACert
		flagInsecure = prevInsecure
		flagInsecureSet = prevSet
		_ = os.Unsetenv("AEGIS_CA_CERT")
		_ = os.Unsetenv("AEGIS_INSECURE_SKIP_VERIFY")
		tlsWarnOnce = oncePtr()
	})

	tmp := t.TempDir()
	ctxCert := tmp + "/ctx.pem"
	envCert := tmp + "/env.pem"
	flagCert := tmp + "/flag.pem"
	for _, p := range []string{ctxCert, envCert, flagCert} {
		if err := os.WriteFile(p, []byte("dummy"), 0o600); err != nil {
			t.Fatalf("seed %s: %v", p, err)
		}
	}

	cfg = &config.Config{
		CurrentContext: "active",
		Contexts: map[string]config.Context{
			"active": {Server: "https://example", CACert: ctxCert, Insecure: false},
		},
	}

	t.Setenv("AEGIS_CA_CERT", envCert)
	t.Setenv("AEGIS_INSECURE_SKIP_VERIFY", "0")

	flagCACert = flagCert
	flagInsecure = false
	flagInsecureSet = false

	tlsWarnOnce = oncePtr()
	got := resolveTLSOptions()
	if got.CACert != flagCert {
		t.Errorf("flag should win for CACert: got %q, want %q", got.CACert, flagCert)
	}
	if got.Insecure {
		t.Errorf("expected Insecure=false from env override")
	}

	// Now drop flag; env should win over context.
	flagCACert = ""
	tlsWarnOnce = oncePtr()
	got = resolveTLSOptions()
	if got.CACert != envCert {
		t.Errorf("env should win over context: got %q, want %q", got.CACert, envCert)
	}

	// Drop env; context wins.
	t.Setenv("AEGIS_CA_CERT", "")
	tlsWarnOnce = oncePtr()
	got = resolveTLSOptions()
	if got.CACert != ctxCert {
		t.Errorf("context fallback: got %q, want %q", got.CACert, ctxCert)
	}
}

func TestResolveTLSOptions_InsecureOverridesCACertWithWarning(t *testing.T) {
	prevCfg := cfg
	prevCACert := flagCACert
	prevInsecure := flagInsecure
	prevSet := flagInsecureSet
	prevQuiet := flagQuiet
	t.Cleanup(func() {
		cfg = prevCfg
		flagCACert = prevCACert
		flagInsecure = prevInsecure
		flagInsecureSet = prevSet
		flagQuiet = prevQuiet
		tlsWarnOnce = oncePtr()
	})

	cfg = &config.Config{Contexts: map[string]config.Context{}}
	flagCACert = "/tmp/whatever.pem"
	flagInsecure = true
	flagInsecureSet = true
	flagQuiet = true // silence stderr; the override behaviour is what we test

	tlsWarnOnce = oncePtr()
	got := resolveTLSOptions()
	if !got.Insecure {
		t.Fatal("expected Insecure=true")
	}
	if got.CACert != "" {
		t.Errorf("expected CACert cleared when Insecure set, got %q", got.CACert)
	}
}

func TestTranslateTLSError_UnknownAuthority(t *testing.T) {
	prevSrv := flagServer
	flagServer = "https://example.test"
	t.Cleanup(func() { flagServer = prevSrv })

	err := errors.New(`Get "https://example.test": tls: failed to verify certificate: x509: certificate signed by unknown authority`)
	out := translateTLSError(err)
	if out == nil {
		t.Fatal("expected translated error, got nil")
	}
	if out.Type != "tls_verification_failed" {
		t.Errorf("type = %q", out.Type)
	}
	if out.ExitCode != ExitCodeAuthFailure {
		t.Errorf("exit code = %d, want %d", out.ExitCode, ExitCodeAuthFailure)
	}
	for _, want := range []string{"context trust", "--ca-cert", "--insecure-skip-tls-verify"} {
		if !strings.Contains(out.Message, want) {
			t.Errorf("message missing %q:\n%s", want, out.Message)
		}
	}
}

func TestTranslateTLSError_TypedX509UnknownAuthority(t *testing.T) {
	cert := &x509.Certificate{Subject: pkixName("leaf")}
	wrapped := fmt.Errorf("dial: %w", x509.UnknownAuthorityError{Cert: cert})
	if out := translateTLSError(wrapped); out == nil {
		t.Fatal("expected typed x509 error to be translated")
	}
}

func TestTranslateTLSError_NonTLSReturnsNil(t *testing.T) {
	if out := translateTLSError(errors.New("connection refused")); out != nil {
		t.Errorf("expected nil for non-tls error, got %+v", out)
	}
}

func TestTranslateTLSError_VerifyErrorWrapper(t *testing.T) {
	verifyErr := &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}
	if out := translateTLSError(verifyErr); out == nil {
		t.Fatal("expected tls.CertificateVerificationError to be translated")
	}
}

// pkixName returns a dummy CN-only PKIX name for synthetic certs.
func pkixName(cn string) (n pkix.Name) {
	n.CommonName = cn
	return
}

// oncePtr returns a fresh sync.Once. Importing sync for this is only
// needed in the test reset path; the real package keeps the package-
// level `tlsWarnOnce` as the singleton.
func oncePtr() sync.Once { return sync.Once{} }
