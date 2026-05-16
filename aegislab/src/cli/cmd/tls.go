package cmd

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"aegis/cli/client"
	"aegis/cli/config"
	"aegis/cli/internal/cli/clierr"
)

// tlsWarnOnce makes sure the "TLS verification disabled" stderr nudge
// fires at most once per process even when multiple HTTP clients are
// constructed in the same command.
var tlsWarnOnce sync.Once

// resolveTLSOptions implements the flag > env > context > auto-discover
// resolution chain for --ca-cert / --insecure-skip-tls-verify. Callers
// pass the result into client.NewClientWithTLS / TransportFor.
//
// Edge case: if Insecure is set and CACert is also resolved, Insecure
// wins (it's strictly more permissive) and we emit a one-shot warning
// to stderr so the operator notices the inconsistency.
func resolveTLSOptions() client.TLSOptions {
	opts := client.TLSOptions{}

	// CA cert — flag > env > active context. Auto-discovery is handled
	// inside client.TransportFor regardless of what we return here.
	switch {
	case flagCACert != "":
		opts.CACert = expandPath(flagCACert)
	case os.Getenv("AEGIS_CA_CERT") != "":
		opts.CACert = expandPath(os.Getenv("AEGIS_CA_CERT"))
	default:
		if ctx, _, err := config.GetCurrentContext(cfg); err == nil {
			opts.CACert = ctx.CACert
		}
	}

	// Insecure — flag > env > active context.
	switch {
	case flagInsecureSet:
		opts.Insecure = flagInsecure
	case os.Getenv("AEGIS_INSECURE_SKIP_VERIFY") != "":
		opts.Insecure = envTruthyString(os.Getenv("AEGIS_INSECURE_SKIP_VERIFY"))
	default:
		if ctx, _, err := config.GetCurrentContext(cfg); err == nil {
			opts.Insecure = ctx.Insecure
		}
	}

	if opts.Insecure && opts.CACert != "" {
		warnTLSOnce("--insecure-skip-tls-verify overrides --ca-cert; certificate verification is disabled")
		opts.CACert = ""
	}
	if opts.Insecure {
		warnTLSOnce("TLS verification disabled (--insecure-skip-tls-verify)")
	}

	return opts
}

func warnTLSOnce(msg string) {
	if flagQuiet {
		return
	}
	tlsWarnOnce.Do(func() {
		fmt.Fprintln(os.Stderr, "warning: "+msg)
	})
}

// expandPath resolves "~" prefixes and makes the path absolute so we
// don't persist relative paths into config.yaml.
func expandPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if strings.HasPrefix(p, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			p = filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

func envTruthyString(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// translateTLSError detects x509/tls verification failures buried under
// net/http or apiclient wrappers and returns a CLIError that lists the
// four remediation paths. Returns nil if err is not a TLS failure.
func translateTLSError(err error) *clierr.CLIError {
	if err == nil {
		return nil
	}

	reason := ""
	var verifyErr *tls.CertificateVerificationError
	if errors.As(err, &verifyErr) {
		if verifyErr.Err != nil {
			reason = verifyErr.Err.Error()
		} else {
			reason = "certificate verification failed"
		}
	}
	var unknown x509.UnknownAuthorityError
	if reason == "" && errors.As(err, &unknown) {
		reason = "certificate signed by unknown authority"
	}
	var hostErr x509.HostnameError
	if reason == "" && errors.As(err, &hostErr) {
		reason = hostErr.Error()
	}
	if reason == "" {
		msg := err.Error()
		if strings.Contains(msg, "x509:") || strings.Contains(msg, "tls: ") {
			reason = extractTLSReason(msg)
		}
	}
	if reason == "" {
		return nil
	}

	body := tlsErrorBody(flagServer, reason, err.Error())
	return &clierr.CLIError{
		Type:     "tls_verification_failed",
		Message:  body,
		Cause:    reason,
		ExitCode: ExitCodeAuthFailure,
	}
}

func extractTLSReason(msg string) string {
	// Pull out the most useful slice of the underlying message: typical
	// shapes are
	//   "Get ...: tls: failed to verify certificate: x509: certificate signed by unknown authority"
	//   "tls: failed to verify certificate: x509: certificate has expired or is not yet valid"
	for _, marker := range []string{"x509:", "tls: failed to verify certificate:", "tls:"} {
		if i := strings.Index(msg, marker); i >= 0 {
			return strings.TrimSpace(msg[i:])
		}
	}
	return strings.TrimSpace(msg)
}

func tlsErrorBody(server, reason, underlying string) string {
	if server == "" {
		server = "the configured server"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "TLS verification failed talking to %s.\n", server)
	fmt.Fprintf(&b, "  %s\n\n", reason)
	b.WriteString("Fix by one of:\n")
	b.WriteString("  (a) Auto-trust the server (TOFU):\n")
	b.WriteString("        aegisctl context trust\n")
	b.WriteString("  (b) Persist a CA in the current context:\n")
	b.WriteString("        aegisctl context set --name <ctx> --ca-cert /path/to/ca.crt\n")
	b.WriteString("  (c) One-shot:\n")
	b.WriteString("        --ca-cert /path/to/ca.crt          (or AEGIS_CA_CERT=/path)\n")
	b.WriteString("  (d) Bypass (DEV ONLY):\n")
	b.WriteString("        --insecure-skip-tls-verify          (or AEGIS_INSECURE_SKIP_VERIFY=1)\n")
	if underlying != "" {
		fmt.Fprintf(&b, "\nUnderlying: %s", underlying)
	}
	return b.String()
}
