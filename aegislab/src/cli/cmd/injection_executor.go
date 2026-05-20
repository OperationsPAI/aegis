package cmd

// `aegisctl injection executor` is the supported surface for the per-system
// executor-authoritative flag. Writes/reads/deletes go through
// aegis-configcenter (same as `aegisctl etcd`), so audit/validators/pub-sub
// all apply — there is no direct etcd client here.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"

	"aegis/cli/output"
	"aegis/platform/consts"

	"github.com/spf13/cobra"
)

const injectionExecutorNamespace = "aegis"

var injectionCmd = &cobra.Command{
	Use:   "injection",
	Short: "Manage fault-injection configuration",
	Long:  `Subcommands for inspecting and configuring fault-injection behavior.`,
}

var injectionExecutorCmd = &cobra.Command{
	Use:   "executor",
	Short: "Get/set the per-system executor-authoritative flag",
	Long: `Manage the per-system fault-injection executor path stored at
aegis.injection.system.<system>.executor_authoritative in configcenter.
Unset / unknown values dispatch via chaos-mesh-direct (legacy SDK).`,
	Example: `  # Route train-ticket dispatches through chaos-service
  aegisctl injection executor set --system=ts --path=chaos-service --reason="cutover §11"

  # Show the effective path for a system (printed bare for shell capture)
  aegisctl injection executor get --system=ts

  # List every system with an override
  aegisctl injection executor list

  # Remove the override (system reverts to chaos-mesh-direct)
  aegisctl injection executor unset --system=ts --yes`,
}

var (
	injectionExecutorSystem    string
	injectionExecutorPath      string
	injectionExecutorReason    string
	injectionExecutorUnsetYes  bool
)

func injectionExecutorKey(system string) (ns, key string, err error) {
	s := strings.TrimSpace(system)
	if s == "" {
		return "", "", usageErrorf("--system is required")
	}
	full := consts.ExecutorFlagKey(s)
	// Configcenter splits at the first ".": namespace="aegis", key=the rest.
	idx := strings.Index(full, ".")
	return full[:idx], full[idx+1:], nil
}

func injectionExecutorFetch(ns, key string) (string, bool, error) {
	path := fmt.Sprintf("/api/v2/config/%s/%s", ns, key)
	raw, status, err := etcdDoJSON(http.MethodGet, path, nil)
	if err != nil {
		return "", false, err
	}
	if status == http.StatusNotFound {
		return "", false, nil
	}
	if status < 200 || status >= 300 {
		return "", false, fmt.Errorf("server returned %d: %s", status, string(raw))
	}
	var entry configEntry
	if err := json.Unmarshal(raw, &entry); err != nil {
		return "", false, fmt.Errorf("decode response: %w (body: %s)", err, string(raw))
	}
	var s string
	if err := json.Unmarshal(entry.Value, &s); err != nil {
		return "", false, fmt.Errorf("decode value: %w (raw: %s)", err, string(entry.Value))
	}
	return s, true, nil
}

var injectionExecutorSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Set executor-authoritative path for a system",
	RunE: func(cmd *cobra.Command, args []string) error {
		switch injectionExecutorPath {
		case consts.ExecutorPathChaosService, consts.ExecutorPathChaosMeshDirect:
		default:
			return usageErrorf("--path must be %q or %q (got %q)",
				consts.ExecutorPathChaosService, consts.ExecutorPathChaosMeshDirect, injectionExecutorPath)
		}
		ns, key, err := injectionExecutorKey(injectionExecutorSystem)
		if err != nil {
			return err
		}
		oldVal, found, err := injectionExecutorFetch(ns, key)
		if err != nil {
			return err
		}
		oldDisplay := oldVal
		if !found {
			oldDisplay = "<unset, defaults to " + consts.ExecutorPathChaosMeshDirect + ">"
		}
		encoded, err := json.Marshal(injectionExecutorPath)
		if err != nil {
			return fmt.Errorf("encode value: %w", err)
		}
		body, err := json.Marshal(map[string]any{
			"value":  json.RawMessage(encoded),
			"reason": injectionExecutorReason,
		})
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		path := fmt.Sprintf("/api/v2/config/%s/%s", ns, key)
		if flagDryRun {
			fmt.Fprintf(os.Stderr, "[dry-run] PUT %s\n  system: %s\n  old: %s\n  new: %s\n",
				path, injectionExecutorSystem, oldDisplay, injectionExecutorPath)
			return nil
		}
		raw, status, err := etcdDoJSON(http.MethodPut, path, body)
		if err != nil {
			return err
		}
		if status < 200 || status >= 300 {
			return fmt.Errorf("server returned %d: %s", status, string(raw))
		}
		fmt.Fprintf(os.Stderr, "ok: system=%s %s -> %s\n", injectionExecutorSystem, oldDisplay, injectionExecutorPath)
		return nil
	},
}

var injectionExecutorGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Get executor-authoritative path for a system",
	RunE: func(cmd *cobra.Command, args []string) error {
		ns, key, err := injectionExecutorKey(injectionExecutorSystem)
		if err != nil {
			return err
		}
		val, found, err := injectionExecutorFetch(ns, key)
		if err != nil {
			return err
		}
		if !found {
			val = consts.ExecutorPathChaosMeshDirect
		}
		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(map[string]any{
				"system": injectionExecutorSystem,
				"path":   val,
				"set":    found,
			})
			return nil
		}
		fmt.Println(val)
		return nil
	},
}

type injectionExecutorRow struct {
	System string `json:"system"`
	Path   string `json:"path"`
}

var injectionExecutorListCmd = &cobra.Command{
	Use:   "list",
	Short: "List per-system executor-authoritative settings",
	RunE: func(cmd *cobra.Command, args []string) error {
		path := fmt.Sprintf("/api/v2/config/%s", injectionExecutorNamespace)
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
		// Configcenter keys are missing the "aegis." prefix; match against the
		// full-key prefix/suffix minus that.
		keyPrefix := strings.TrimPrefix(consts.ExecutorFlagKeyPrefix, injectionExecutorNamespace+".")
		keySuffix := consts.ExecutorFlagKeySuffix
		rows := make([]injectionExecutorRow, 0)
		for _, e := range resp.Items {
			if !strings.HasPrefix(e.Key, keyPrefix) || !strings.HasSuffix(e.Key, keySuffix) {
				continue
			}
			system := strings.TrimSuffix(strings.TrimPrefix(e.Key, keyPrefix), keySuffix)
			if system == "" {
				continue
			}
			var val string
			if err := json.Unmarshal(e.Value, &val); err != nil {
				val = string(e.Value)
			}
			rows = append(rows, injectionExecutorRow{System: system, Path: val})
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].System < rows[j].System })
		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(rows)
			return nil
		}
		headers := []string{"SYSTEM", "PATH"}
		tableRows := make([][]string, 0, len(rows))
		for _, r := range rows {
			tableRows = append(tableRows, []string{r.System, r.Path})
		}
		output.PrintTable(headers, tableRows)
		return nil
	},
}

var injectionExecutorUnsetCmd = &cobra.Command{
	Use:   "unset",
	Short: "Remove executor-authoritative override (falls back to chaos-mesh-direct)",
	RunE: func(cmd *cobra.Command, args []string) error {
		ns, key, err := injectionExecutorKey(injectionExecutorSystem)
		if err != nil {
			return err
		}
		if err := confirmEtcdDelete(ns, key, injectionExecutorUnsetYes); err != nil {
			return err
		}
		path := fmt.Sprintf("/api/v2/config/%s/%s", ns, key)
		if injectionExecutorReason != "" {
			path += "?reason=" + injectionExecutorReason
		}
		if flagDryRun {
			fmt.Fprintf(os.Stderr, "[dry-run] DELETE %s (system=%s)\n", path, injectionExecutorSystem)
			return nil
		}
		raw, status, err := etcdDoJSON(http.MethodDelete, path, nil)
		if err != nil {
			return err
		}
		if status == http.StatusNotFound {
			fmt.Fprintf(os.Stderr, "no-op: system=%s already unset\n", injectionExecutorSystem)
			return nil
		}
		if status < 200 || status >= 300 {
			return fmt.Errorf("server returned %d: %s", status, string(raw))
		}
		fmt.Fprintf(os.Stderr, "unset: system=%s (now defaults to %s)\n",
			injectionExecutorSystem, consts.ExecutorPathChaosMeshDirect)
		return nil
	},
}

func init() {
	injectionExecutorSetCmd.Flags().StringVar(&injectionExecutorSystem, "system", "", "Logical system name (e.g. ts, sn, hr)")
	injectionExecutorSetCmd.Flags().StringVar(&injectionExecutorPath, "path", "",
		fmt.Sprintf("Executor path: %q or %q", consts.ExecutorPathChaosService, consts.ExecutorPathChaosMeshDirect))
	injectionExecutorSetCmd.Flags().StringVar(&injectionExecutorReason, "reason", "", "Human-facing reason recorded in the audit log")
	_ = injectionExecutorSetCmd.MarkFlagRequired("system")
	_ = injectionExecutorSetCmd.MarkFlagRequired("path")

	injectionExecutorGetCmd.Flags().StringVar(&injectionExecutorSystem, "system", "", "Logical system name")
	_ = injectionExecutorGetCmd.MarkFlagRequired("system")

	injectionExecutorUnsetCmd.Flags().StringVar(&injectionExecutorSystem, "system", "", "Logical system name")
	injectionExecutorUnsetCmd.Flags().StringVar(&injectionExecutorReason, "reason", "", "Human-facing reason recorded in the audit log")
	injectionExecutorUnsetCmd.Flags().BoolVar(&injectionExecutorUnsetYes, "yes", false, "Skip interactive confirmation")
	_ = injectionExecutorUnsetCmd.MarkFlagRequired("system")

	injectionExecutorCmd.AddCommand(injectionExecutorSetCmd)
	injectionExecutorCmd.AddCommand(injectionExecutorGetCmd)
	injectionExecutorCmd.AddCommand(injectionExecutorListCmd)
	injectionExecutorCmd.AddCommand(injectionExecutorUnsetCmd)

	injectionCmd.AddCommand(injectionExecutorCmd)
	cobra.OnInitialize(func() {
		markDryRunSupported(injectionExecutorSetCmd)
		markDryRunSupported(injectionExecutorUnsetCmd)
	})
	rootCmd.AddCommand(injectionCmd)
}
