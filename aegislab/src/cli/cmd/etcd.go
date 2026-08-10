package cmd

// The `aegisctl etcd` subcommands do NOT talk to etcd directly. They
// route through aegis-configcenter's HTTP API (PUT/GET/DELETE/LIST under
// /api/v2/config/:namespace[/:key]); the CLI is named after the user's
// mental model, not the implementation.

import (
	"bufio"
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
	"golang.org/x/term"
)

var etcdCmd = &cobra.Command{
	Use:   "etcd",
	Short: "Read/write dynamic configuration via aegis-configcenter",
	Long: `Manage dynamic configuration entries served by the aegis-configcenter
microservice. The CLI name "etcd" reflects that these entries are persisted
in etcd, but all reads and writes go through the configcenter HTTP API
(/api/v2/config/...), not a direct etcd client — so audit logs, validators,
and pub/sub fanout all apply.

Keys are addressed by (namespace, key). When --namespace is omitted, the
argument is split on the first ".": "aegis.injection.catalog_source" becomes
namespace="aegis", key="injection.catalog_source".`,
}

var (
	etcdNamespace      string
	etcdPutReason      string
	etcdPutValueFile   string
	etcdGetMetadata    bool
	etcdListPrefix     string
	etcdListPageSize   int
	etcdDeleteYes      bool
	etcdDeleteReason   string
)

func splitEtcdKey(arg string) (namespace, key string, err error) {
	if etcdNamespace != "" {
		if arg == "" {
			return "", "", usageErrorf("key argument required")
		}
		return etcdNamespace, arg, nil
	}
	idx := strings.Index(arg, ".")
	if idx <= 0 || idx == len(arg)-1 {
		return "", "", usageErrorf("key %q has no namespace prefix; pass --namespace or use dotted form e.g. aegis.injection.catalog_source", arg)
	}
	return arg[:idx], arg[idx+1:], nil
}

// etcdDoJSON mirrors chaosPruneDoJSON: same auth + TLS wiring.
func etcdDoJSON(method, path string, body []byte) ([]byte, int, error) {
	if flagServer == "" {
		return nil, 0, missingEnvErrorf("--server or AEGIS_SERVER is required")
	}
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(context.Background(),
		method,
		strings.TrimRight(flagServer, "/")+path,
		rdr)
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

// configEntry mirrors crud/admin/configcenter.EntryResp.
type configEntry struct {
	Namespace string          `json:"namespace"`
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value"`
	Layer     string          `json:"layer"`
}

var etcdPutCmd = &cobra.Command{
	Use:   "put <key> [value]",
	Short: "Set or overwrite a dynamic config entry",
	Long: `Write a value for the given key. The positional value is taken as a
plain string and JSON-encoded; for multi-line or structured payloads use
"-f <file>" or "-f -" (stdin) — those bodies are sent verbatim as the JSON
value, so they must already be valid JSON.`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		ns, key, err := splitEtcdKey(args[0])
		if err != nil {
			return err
		}
		var rawValue json.RawMessage
		switch {
		case etcdPutValueFile != "":
			var data []byte
			if etcdPutValueFile == "-" {
				data, err = io.ReadAll(os.Stdin)
			} else {
				data, err = os.ReadFile(etcdPutValueFile)
			}
			if err != nil {
				return fmt.Errorf("read value: %w", err)
			}
			if !json.Valid(data) {
				return usageErrorf("value loaded from %q is not valid JSON", etcdPutValueFile)
			}
			rawValue = json.RawMessage(bytes.TrimRight(data, "\n"))
		case len(args) == 2:
			encoded, err := json.Marshal(args[1])
			if err != nil {
				return fmt.Errorf("encode value: %w", err)
			}
			rawValue = encoded
		default:
			return usageErrorf("provide a positional <value> or -f <file>")
		}

		body, err := json.Marshal(map[string]any{
			"value":  rawValue,
			"reason": etcdPutReason,
		})
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		path := fmt.Sprintf("/api/v2/config/%s/%s", ns, key)
		if flagDryRun {
			fmt.Fprintf(os.Stderr, "[dry-run] PUT %s body=%s\n", path, string(body))
			return nil
		}
		raw, status, err := etcdDoJSON(http.MethodPut, path, body)
		if err != nil {
			return err
		}
		if status < 200 || status >= 300 {
			return fmt.Errorf("server returned %d: %s", status, string(raw))
		}
		fmt.Fprintf(os.Stderr, "ok: %s/%s\n", ns, key)
		return nil
	},
}

var etcdGetCmd = &cobra.Command{
	Use:   "get <key>",
	Short: "Read a dynamic config entry",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ns, key, err := splitEtcdKey(args[0])
		if err != nil {
			return err
		}
		path := fmt.Sprintf("/api/v2/config/%s/%s", ns, key)
		raw, status, err := etcdDoJSON(http.MethodGet, path, nil)
		if err != nil {
			return err
		}
		if status == http.StatusNotFound {
			return fmt.Errorf("not found: %s/%s", ns, key)
		}
		if status < 200 || status >= 300 {
			return fmt.Errorf("server returned %d: %s", status, string(raw))
		}
		var entry configEntry
		if err := json.Unmarshal(raw, &entry); err != nil {
			return fmt.Errorf("decode response: %w (body: %s)", err, string(raw))
		}
		if output.OutputFormat(flagOutput) == output.FormatJSON || etcdGetMetadata {
			output.PrintJSON(entry)
			return nil
		}
		// Bare value for shell ergonomics: dotted keys feed straight into
		// `export FOO=$(aegisctl etcd get ...)`.
		var strVal string
		if err := json.Unmarshal(entry.Value, &strVal); err == nil {
			fmt.Println(strVal)
		} else {
			fmt.Println(string(entry.Value))
		}
		return nil
	},
}

var etcdListCmd = &cobra.Command{
	Use:   "list",
	Short: "List dynamic config entries under a namespace",
	Long: `List entries under --namespace (or under the first dot segment of
--prefix). The configcenter currently returns all entries for a namespace in
a single response; --page-size is reserved for forward-compatibility.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ns := etcdNamespace
		keyPrefix := ""
		if ns == "" && etcdListPrefix != "" {
			if idx := strings.Index(etcdListPrefix, "."); idx > 0 {
				ns = etcdListPrefix[:idx]
				keyPrefix = etcdListPrefix[idx+1:]
			} else {
				ns = etcdListPrefix
			}
		}
		if ns == "" {
			return usageErrorf("--namespace or --prefix <ns>[.<key>] is required")
		}
		path := fmt.Sprintf("/api/v2/config/%s", ns)
		raw, status, err := etcdDoJSON(http.MethodGet, path, nil)
		if err != nil {
			return err
		}
		if status < 200 || status >= 300 {
			return fmt.Errorf("server returned %d: %s", status, string(raw))
		}
		var resp struct {
			Items []configEntry `json:"items"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			return fmt.Errorf("decode response: %w (body: %s)", err, string(raw))
		}
		filtered := resp.Items[:0]
		for _, e := range resp.Items {
			if keyPrefix != "" && !strings.HasPrefix(e.Key, keyPrefix) {
				continue
			}
			filtered = append(filtered, e)
		}
		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(filtered)
			return nil
		}
		headers := []string{"NAMESPACE", "KEY", "LAYER", "VALUE"}
		rows := make([][]string, 0, len(filtered))
		for _, e := range filtered {
			rows = append(rows, []string{e.Namespace, e.Key, e.Layer, string(e.Value)})
		}
		output.PrintTable(headers, rows)
		return nil
	},
}

var etcdDeleteCmd = &cobra.Command{
	Use:   "delete <key>",
	Short: "Remove a dynamic config entry",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ns, key, err := splitEtcdKey(args[0])
		if err != nil {
			return err
		}
		if err := confirmEtcdDelete(ns, key, etcdDeleteYes); err != nil {
			return err
		}
		path := fmt.Sprintf("/api/v2/config/%s/%s", ns, key)
		if etcdDeleteReason != "" {
			path += "?reason=" + etcdDeleteReason
		}
		if flagDryRun {
			fmt.Fprintf(os.Stderr, "[dry-run] DELETE %s\n", path)
			return nil
		}
		raw, status, err := etcdDoJSON(http.MethodDelete, path, nil)
		if err != nil {
			return err
		}
		if status < 200 || status >= 300 {
			return fmt.Errorf("server returned %d: %s", status, string(raw))
		}
		fmt.Fprintf(os.Stderr, "deleted: %s/%s\n", ns, key)
		return nil
	},
}

func confirmEtcdDelete(ns, key string, yes bool) error {
	if yes {
		return nil
	}
	if flagNonInteractive {
		return usageErrorf("refusing to delete without --yes in non-interactive mode")
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return usageErrorf("refusing to delete without --yes when stdin is not a TTY")
	}
	fmt.Fprintf(os.Stderr, "Delete config entry %s/%s? [y/N] ", ns, key)
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	if line != "y" && line != "yes" {
		return usageErrorf("aborted by user")
	}
	return nil
}

func init() {
	etcdCmd.PersistentFlags().StringVarP(&etcdNamespace, "namespace", "n", "", "Config namespace (overrides first-dot split of the key)")

	etcdPutCmd.Flags().StringVar(&etcdPutReason, "reason", "", "Human-facing reason recorded in the audit log")
	etcdPutCmd.Flags().StringVarP(&etcdPutValueFile, "file", "f", "", "Read value from file ('-' for stdin); contents must be valid JSON")

	etcdGetCmd.Flags().BoolVar(&etcdGetMetadata, "metadata", false, "Print full entry object (namespace, key, value, layer) instead of bare value")

	etcdListCmd.Flags().StringVar(&etcdListPrefix, "prefix", "", "Restrict to entries whose dotted name starts with this prefix")
	etcdListCmd.Flags().IntVar(&etcdListPageSize, "page-size", 100, "Page size (reserved; configcenter currently returns full namespace per call)")

	etcdDeleteCmd.Flags().BoolVar(&etcdDeleteYes, "yes", false, "Skip interactive confirmation")
	etcdDeleteCmd.Flags().StringVar(&etcdDeleteReason, "reason", "", "Human-facing reason recorded in the audit log")

	etcdCmd.AddCommand(etcdPutCmd)
	etcdCmd.AddCommand(etcdGetCmd)
	etcdCmd.AddCommand(etcdListCmd)
	etcdCmd.AddCommand(etcdDeleteCmd)
	cobra.OnInitialize(func() {
		markDryRunSupported(etcdPutCmd)
		markDryRunSupported(etcdDeleteCmd)
	})
	rootCmd.AddCommand(etcdCmd)
}
