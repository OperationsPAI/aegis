package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"aegis/cli/client"
	"aegis/cli/output"

	"github.com/spf13/cobra"
)

const ssoServerEnv = "AEGIS_SSO_SERVER"

var (
	flagSSOServer       string
	saIssueLifetimeDays int
)

var serviceAccountCmd = &cobra.Command{
	Use:   "service-account",
	Short: "Mint or revoke service account tokens (SSO admin endpoints)",
	Long: `Manage service account tokens via the aegis-sso admin endpoints.

Service accounts themselves are seeded via initial-data; this CLI only mints
(issue) and revokes tokens for accounts that already exist server-side. There
are no create / get / list commands because aegis-sso does not expose those
endpoints — service account rows are managed exclusively through seed data.`,
}

var serviceAccountIssueCmd = &cobra.Command{
	Use:   "issue <name>",
	Short: "Mint a JWT for an existing service account (POST /v1/service-accounts/{name}/issue)",
	Args:  cobra.ExactArgs(1),
	RunE:  runServiceAccountIssue,
}

var serviceAccountRevokeCmd = &cobra.Command{
	Use:   "revoke <name>",
	Short: "Revoke a service account (POST /v1/service-accounts/{name}/revoke)",
	Args:  cobra.ExactArgs(1),
	RunE:  runServiceAccountRevoke,
}

type serviceAccountIssueReq struct {
	LifetimeDays int `json:"lifetime_days,omitempty"`
}

type serviceAccountIssueData struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
}

type serviceAccountIssueEnvelope struct {
	Code    int                     `json:"code"`
	Message string                  `json:"message"`
	Data    serviceAccountIssueData `json:"data"`
}

func runServiceAccountIssue(_ *cobra.Command, args []string) error {
	name := strings.TrimSpace(args[0])
	if name == "" {
		return usageErrorf("service account name is required")
	}
	var reqBody []byte
	if saIssueLifetimeDays > 0 {
		b, err := json.Marshal(serviceAccountIssueReq{LifetimeDays: saIssueLifetimeDays})
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		reqBody = b
	}
	body, status, err := ssoDoJSON(http.MethodPost,
		"/v1/service-accounts/"+name+"/issue", reqBody)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("issue service account %s: aegis-sso returned %d: %s",
			name, status, string(body))
	}
	var env serviceAccountIssueEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if output.OutputFormat(flagOutput) == output.FormatJSON {
		output.PrintJSON(env.Data)
		return nil
	}
	headers := []string{"NAME", "TOKEN", "EXPIRES_AT"}
	row := []string{name, env.Data.Token, env.Data.ExpiresAt}
	output.PrintTable(headers, [][]string{row})
	return nil
}

func runServiceAccountRevoke(_ *cobra.Command, args []string) error {
	name := strings.TrimSpace(args[0])
	if name == "" {
		return usageErrorf("service account name is required")
	}
	body, status, err := ssoDoJSON(http.MethodPost,
		"/v1/service-accounts/"+name+"/revoke", nil)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("revoke service account %s: aegis-sso returned %d: %s",
			name, status, string(body))
	}
	if output.OutputFormat(flagOutput) == output.FormatJSON {
		output.PrintJSON(map[string]string{"name": name, "status": "revoked"})
		return nil
	}
	if !flagQuiet {
		fmt.Fprintf(os.Stderr, "service account %s revoked\n", name)
	}
	return nil
}

func resolveSSOServer() (string, error) {
	if flagSSOServer != "" {
		return flagSSOServer, nil
	}
	if v := os.Getenv(ssoServerEnv); v != "" {
		return v, nil
	}
	return "", usageErrorf("aegis-sso URL required: pass --sso-server or set %s "+
		"(typically aegis-sso on port 8083)", ssoServerEnv)
}

func ssoDoJSON(method, path string, body []byte) ([]byte, int, error) {
	server, err := resolveSSOServer()
	if err != nil {
		return nil, 0, err
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(context.Background(), method,
		strings.TrimRight(server, "/")+path, reader)
	if err != nil {
		return nil, 0, fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if flagToken != "" {
		req.Header.Set("Authorization", "Bearer "+flagToken)
	}
	httpClient := &http.Client{Transport: client.TransportFor(resolveTLSOptions())}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read response: %w", err)
	}
	return b, resp.StatusCode, nil
}

func init() {
	serviceAccountCmd.PersistentFlags().StringVar(&flagSSOServer, "sso-server", "",
		"aegis-sso service URL (env: AEGIS_SSO_SERVER; typically port 8083)")

	serviceAccountIssueCmd.Flags().IntVar(&saIssueLifetimeDays, "lifetime-days", 0,
		"Token lifetime in days (default: server-side 365; max 1825)")

	serviceAccountCmd.AddCommand(serviceAccountIssueCmd)
	serviceAccountCmd.AddCommand(serviceAccountRevokeCmd)
	rootCmd.AddCommand(serviceAccountCmd)
}
