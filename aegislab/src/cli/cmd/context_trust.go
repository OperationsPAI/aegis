package cmd

import (
	"bufio"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"aegis/cli/config"
	"aegis/cli/output"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var (
	contextTrustYes    bool
	contextTrustPrint  bool
	contextTrustCAOnly bool
)

var contextTrustCmd = &cobra.Command{
	Use:   "trust [name]",
	Short: "Trust the TLS CA of the server configured in a context (TOFU)",
	Long: `Performs a TLS handshake against the context's server with verification
disabled, extracts the issuing CA from the presented chain, prints a
fingerprint summary, prompts for confirmation, and persists the CA to
~/.aegisctl/certs/<host>-<sha8>.crt. The context's ca-cert field is then
updated to point at the saved file so subsequent calls validate normally.

This is a Trust-On-First-Use (TOFU) workflow: there is no chain of trust
on the very first connection. For cluster admins, prefer out-of-band CA
distribution where the operational risk warrants it.

Examples:
  aegisctl context trust                       # active context
  aegisctl context trust byte                  # named context
  aegisctl context trust --print               # show what would be saved
  aegisctl context trust --yes                 # skip the confirmation prompt`,
	Args: cobra.MaximumNArgs(1),
	RunE: runContextTrust,
}

func init() {
	contextTrustCmd.Flags().BoolVar(&contextTrustYes, "yes", false, "Skip confirmation prompt")
	contextTrustCmd.Flags().BoolVar(&contextTrustYes, "force", false, "Alias for --yes")
	contextTrustCmd.Flags().BoolVar(&contextTrustPrint, "print", false, "Show what would be saved without writing")
	contextTrustCmd.Flags().BoolVar(&contextTrustCAOnly, "ca-only", true, "Save only the issuing CA, not the leaf")
}

// trustDialer is overridden in tests so we can hand back a synthetic
// peer chain without spinning up a real TLS listener.
var trustDialer = func(network, addr string, cfg *tls.Config, timeout time.Duration) ([]*x509.Certificate, error) {
	dialer := &net.Dialer{Timeout: timeout}
	conn, err := tls.DialWithDialer(dialer, network, addr, cfg)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	return conn.ConnectionState().PeerCertificates, nil
}

func runContextTrust(cmd *cobra.Command, args []string) error {
	ctxName, ctx, err := resolveTrustContext(args)
	if err != nil {
		return err
	}

	u, err := url.Parse(strings.TrimSpace(ctx.Server))
	if err != nil || u.Host == "" {
		return usageErrorf("context %q has no valid server URL", ctxName)
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return usageErrorf("context %q server %q is not HTTPS; nothing to trust", ctxName, ctx.Server)
	}

	host := u.Hostname()
	port := u.Port()
	if port == "" {
		port = "443"
	}
	addr := net.JoinHostPort(host, port)

	timeout := time.Duration(flagRequestTimeout) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	cfg := &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // TOFU: we need the chain regardless of trust
		ServerName:         host,
	}
	peers, err := trustDialer("tcp", addr, cfg, timeout)
	if err != nil {
		return fmt.Errorf("tls handshake with %s: %w", addr, err)
	}
	if len(peers) == 0 {
		return &exitError{Code: ExitCodeServerError, Message: fmt.Sprintf("server %s returned no certificates", addr)}
	}

	chosen, isLeafFallback := pickCAFromChain(peers)
	if isLeafFallback {
		fmt.Fprintln(os.Stderr, "warning: no CA cert in chain; falling back to the leaf certificate (expect short rotation cycles)")
	}

	derSum := sha256.Sum256(chosen.Raw)
	fp := formatFingerprint(derSum[:])

	summary := trustSummary{
		Server:     ctx.Server,
		Address:    addr,
		LeafSubj:   peers[0].Subject.String(),
		LeafIssuer: peers[0].Issuer.String(),
		LeafExpiry: peers[0].NotAfter.UTC().Format(time.RFC3339),
		CASubject:  chosen.Subject.String(),
		CAIssuer:   chosen.Issuer.String(),
		CAExpiry:   chosen.NotAfter.UTC().Format(time.RFC3339),
		CAFP:       fp,
		LeafFB:     isLeafFallback,
	}

	target, err := trustCertPath(host, derSum[:])
	if err != nil {
		return err
	}
	summary.SavePath = target

	if output.OutputFormat(flagOutput) == output.FormatJSON {
		output.PrintJSON(summary.json())
	} else {
		printTrustSummary(summary)
	}

	if contextTrustPrint {
		return nil
	}

	if !contextTrustYes {
		if flagNonInteractive {
			return &exitError{Code: ExitCodeTimeout, Message: "refusing to trust without --yes in non-interactive mode"}
		}
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			return &exitError{Code: ExitCodeTimeout, Message: "refusing to trust without --yes when stdin is not a TTY"}
		}
		fmt.Fprintf(os.Stderr, "Trust this CA and save it to %s? [y/N] ", target)
		reader := bufio.NewReader(os.Stdin)
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(strings.ToLower(line))
		if line != "y" && line != "yes" {
			return usageErrorf("aborted by user")
		}
	}

	if err := writeCAFile(target, chosen); err != nil {
		return err
	}
	ctx.CACert = target
	cfg2 := configForTrust()
	cfg2.Contexts[ctxName] = *ctx
	if err := config.SaveConfig(cfg2); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	output.PrintInfo(fmt.Sprintf("Trusted. Context %q now uses --ca-cert %s.", ctxName, target))
	return nil
}

// configForTrust just returns the package-level loaded config so tests
// can override it if needed.
func configForTrust() *config.Config {
	return cfg
}

type trustSummary struct {
	Server     string
	Address    string
	LeafSubj   string
	LeafIssuer string
	LeafExpiry string
	CASubject  string
	CAIssuer   string
	CAExpiry   string
	CAFP       string
	SavePath   string
	LeafFB     bool
}

func (s trustSummary) json() map[string]any {
	return map[string]any{
		"server":          s.Server,
		"resolves_to":     s.Address,
		"leaf_subject":    s.LeafSubj,
		"leaf_issuer":     s.LeafIssuer,
		"leaf_expires":    s.LeafExpiry,
		"ca_subject":      s.CASubject,
		"ca_issuer":       s.CAIssuer,
		"ca_expires":      s.CAExpiry,
		"ca_sha256":       s.CAFP,
		"save_path":       s.SavePath,
		"leaf_fallback":   s.LeafFB,
		"would_write":     !contextTrustPrint,
	}
}

func printTrustSummary(s trustSummary) {
	if flagQuiet {
		return
	}
	w := &strings.Builder{}
	fmt.Fprintf(w, "Server:        %s\n", s.Server)
	fmt.Fprintf(w, "Resolves to:   %s\n", s.Address)
	fmt.Fprintf(w, "Leaf cert:\n")
	fmt.Fprintf(w, "  Subject:     %s\n", s.LeafSubj)
	fmt.Fprintf(w, "  Issuer:      %s\n", s.LeafIssuer)
	fmt.Fprintf(w, "  Valid until: %s\n", s.LeafExpiry)
	fmt.Fprintf(w, "CA to trust:\n")
	fmt.Fprintf(w, "  Subject:     %s\n", s.CASubject)
	fmt.Fprintf(w, "  SHA-256:     %s\n", s.CAFP)
	fmt.Fprintf(w, "  Valid until: %s\n", s.CAExpiry)
	fmt.Fprintf(w, "Save path:     %s\n", s.SavePath)
	_, _ = os.Stderr.WriteString(w.String())
}

func resolveTrustContext(args []string) (string, *config.Context, error) {
	var name string
	if len(args) == 1 {
		name = strings.TrimSpace(args[0])
	}
	if name == "" {
		_, n, err := config.GetCurrentContext(cfg)
		if err != nil {
			return "", nil, err
		}
		name = n
	}
	ctx, ok := cfg.Contexts[name]
	if !ok {
		return "", nil, notFoundErrorf("context %q not found", name)
	}
	return name, &ctx, nil
}

// pickCAFromChain walks the peer chain and returns the cert most likely
// to be the trust anchor: the last self-signed IsCA cert, falling back
// to the last IsCA cert, finally the leaf itself (with a stderr warning
// from the caller). The bool return is true when the leaf is used.
func pickCAFromChain(chain []*x509.Certificate) (*x509.Certificate, bool) {
	var lastIsCA *x509.Certificate
	for i := len(chain) - 1; i >= 0; i-- {
		c := chain[i]
		if c.IsCA && c.Subject.String() == c.Issuer.String() {
			return c, false
		}
		if c.IsCA && lastIsCA == nil {
			lastIsCA = c
		}
	}
	if lastIsCA != nil {
		return lastIsCA, false
	}
	return chain[0], true
}

func formatFingerprint(b []byte) string {
	parts := make([]string, len(b))
	for i, x := range b {
		parts[i] = fmt.Sprintf("%02X", x)
	}
	return strings.Join(parts, ":")
}

func trustCertPath(host string, der []byte) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	dir := filepath.Join(home, ".aegisctl", "certs")
	safe := safeHostComponent(host)
	short := fmt.Sprintf("%x", der[:4])[:8]
	return filepath.Join(dir, safe+"-"+short+".crt"), nil
}

func safeHostComponent(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	if h == "" {
		return "host"
	}
	mapped := make([]rune, 0, len(h))
	for _, r := range h {
		switch {
		case r >= 'a' && r <= 'z':
			mapped = append(mapped, r)
		case r >= '0' && r <= '9':
			mapped = append(mapped, r)
		case r == '-' || r == '.':
			mapped = append(mapped, r)
		default:
			mapped = append(mapped, '_')
		}
	}
	return string(mapped)
}

func writeCAFile(path string, cert *x509.Certificate) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create certs directory: %w", err)
	}
	block := &pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}
	data := pem.EncodeToMemory(block)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write CA file: %w", err)
	}
	return nil
}
