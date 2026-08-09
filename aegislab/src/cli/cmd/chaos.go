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
	"time"

	"aegis/cli/client"
	"aegis/cli/output"

	"github.com/spf13/cobra"
)

var chaosCmd = &cobra.Command{
	Use:   "chaos",
	Short: "Operate on chaos-mesh CRs (prune orphans, etc.)",
}

var (
	chaosPruneNamespace string
	chaosPruneOlderThan string
	chaosPruneApply     bool
	chaosPruneKinds     string
)

var chaosPruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Find and delete orphaned chaos-mesh CRs",
	Long: `Scan chaos-mesh.org CRs across the cluster (or a single namespace) and
report those whose backing injection task is in a terminal state (Completed /
Error / Cancelled) past the age cutoff, or whose task_id label does not
resolve to any task row in the DB.

Defaults to a dry-run. Pass --apply (and clear --dry-run) to actually delete.

Supported chaos kinds (all chaos-mesh.org namespaced resources are discovered
at runtime; defaults below match the cluster install): PodChaos, NetworkChaos,
HTTPChaos, IOChaos, StressChaos, TimeChaos, JVMChaos, DNSChaos.`,
	Example: `  aegisctl chaos prune                              # dry-run, all namespaces
  aegisctl chaos prune --namespace hs0              # dry-run scoped to hs0
  aegisctl chaos prune --older-than 30m             # tighter age cutoff
  aegisctl chaos prune --kinds podchaos,networkchaos
  aegisctl chaos prune --apply                      # actually delete`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ageSec, err := parsePruneAge(chaosPruneOlderThan)
		if err != nil {
			return err
		}
		dryRun := !chaosPruneApply || flagDryRun
		req := map[string]any{
			"namespace":   chaosPruneNamespace,
			"age_seconds": ageSec,
			"dry_run":     dryRun,
		}
		if chaosPruneKinds != "" {
			kinds := splitCSV(chaosPruneKinds)
			normalized := make([]string, 0, len(kinds))
			for _, k := range kinds {
				if k != "" {
					normalized = append(normalized, strings.ToLower(k))
				}
			}
			req["include_kinds"] = normalized
		}
		body, err := json.Marshal(req)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		raw, status, err := chaosPruneDoJSON(http.MethodPost, "/api/v2/admin/chaos/prune", body)
		if err != nil {
			return err
		}
		if status < 200 || status >= 300 {
			return fmt.Errorf("server returned %d: %s", status, string(raw))
		}
		var env struct {
			Data chaosPruneResp `json:"data"`
		}
		if err := json.Unmarshal(raw, &env); err != nil {
			return fmt.Errorf("decode response: %w (body: %s)", err, string(raw))
		}
		return renderChaosPrune(&env.Data)
	},
}

type chaosPruneCandidate struct {
	Kind        string  `json:"kind"`
	Resource    string  `json:"resource"`
	Namespace   string  `json:"namespace"`
	Name        string  `json:"name"`
	TaskID      string  `json:"task_id"`
	TaskState   string  `json:"task_state"`
	Reason      string  `json:"reason"`
	AgeSeconds  float64 `json:"age_seconds"`
	Deleted     bool    `json:"deleted"`
	DeleteError string  `json:"delete_error,omitempty"`
}

type chaosPruneResp struct {
	DryRun     bool                  `json:"dry_run"`
	Namespace  string                `json:"namespace,omitempty"`
	AgeSeconds int                   `json:"age_seconds"`
	Candidates []chaosPruneCandidate `json:"candidates"`
	Warnings   []string              `json:"warnings,omitempty"`
}

func renderChaosPrune(r *chaosPruneResp) error {
	if output.OutputFormat(flagOutput) == output.FormatJSON {
		output.PrintJSON(r)
		return nil
	}

	headers := []string{"KIND", "NAMESPACE", "NAME", "TASK_ID", "TASK_STATE", "REASON", "AGE", "STATUS"}
	rows := make([][]string, 0, len(r.Candidates))
	for _, c := range r.Candidates {
		status := "DRY-RUN"
		if !r.DryRun {
			switch {
			case c.Deleted:
				status = "DELETED"
			case c.DeleteError != "":
				status = "ERROR: " + c.DeleteError
			default:
				status = "SKIPPED"
			}
		}
		taskState := c.TaskState
		if taskState == "" {
			taskState = "-"
		}
		taskID := c.TaskID
		if taskID == "" {
			taskID = "-"
		}
		rows = append(rows, []string{
			c.Kind, c.Namespace, c.Name, taskID, taskState, c.Reason,
			formatAgeSeconds(c.AgeSeconds), status,
		})
	}
	output.PrintTable(headers, rows)

	// Human-oriented summaries go to stderr so `aegisctl chaos prune | wc -l`
	// reflects table rows only.
	fmt.Fprintf(os.Stderr, "\n%d orphan candidate(s) (age cutoff %ds)\n",
		len(r.Candidates), r.AgeSeconds)
	if r.DryRun {
		fmt.Fprintln(os.Stderr, "dry-run only. Re-run with --apply to delete.")
	}
	for _, w := range r.Warnings {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}
	return nil
}

func formatAgeSeconds(s float64) string {
	d := time.Duration(s * float64(time.Second)).Round(time.Second)
	return d.String()
}

func parsePruneAge(s string) (int, error) {
	if strings.TrimSpace(s) == "" {
		return 0, nil // server-side default
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid --older-than %q: %w", s, err)
	}
	if d < 0 {
		return 0, fmt.Errorf("--older-than must be non-negative, got %s", d)
	}
	return int(d.Seconds()), nil
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimSpace(p))
	}
	return out
}

// chaosPruneDoJSON mirrors shareDoJSON — same transport / auth wiring, kept
// local to keep the share package surface small.
func chaosPruneDoJSON(method, path string, body []byte) ([]byte, int, error) {
	if flagServer == "" {
		return nil, 0, missingEnvErrorf("--server or AEGIS_SERVER is required")
	}
	req, err := http.NewRequestWithContext(context.Background(),
		method,
		strings.TrimRight(flagServer, "/")+path,
		bytes.NewReader(body))
	if err != nil {
		return nil, 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
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
	chaosCmd.PersistentFlags().StringVar(&flagChaosServer, "chaos-server", "",
		"aegis-chaos service URL (env: AEGIS_CHAOS_SERVER; required for system / inject / capability subcommands)")

	chaosPruneCmd.Flags().StringVar(&chaosPruneNamespace, "namespace", "", "Limit to a single namespace (default: all)")
	chaosPruneCmd.Flags().StringVar(&chaosPruneOlderThan, "older-than", "", "Minimum age of terminal-task CRs before reaping, e.g. 1h, 30m, 300s. Default: 5m server-side.")
	chaosPruneCmd.Flags().BoolVar(&chaosPruneApply, "apply", false, "Actually delete (default: dry-run)")
	chaosPruneCmd.Flags().StringVar(&chaosPruneKinds, "kinds", "", "Comma-separated chaos resource plurals to scope to (e.g. podchaos,networkchaos). Empty = all.")
	chaosCmd.AddCommand(chaosPruneCmd)
	cobra.OnInitialize(func() {
		markDryRunSupported(chaosPruneCmd)
	})
	rootCmd.AddCommand(chaosCmd)
}
