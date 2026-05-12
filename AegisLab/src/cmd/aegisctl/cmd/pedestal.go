package cmd

import (
	"fmt"
	"sort"
	"strings"

	"aegis/cmd/aegisctl/client"
	"aegis/cmd/aegisctl/output"
	"aegis/platform/consts"

	"github.com/spf13/cobra"
)

// Local types mirroring the backend response shapes in src/dto/container.go.
// We duplicate these instead of importing aegis/dto to keep the aegisctl
// binary free of the full server's gorm+entity graph.

type pedestalHelmConfig struct {
	ID                 int    `json:"id"`
	ContainerVersionID int    `json:"container_version_id"`
	ChartName          string `json:"chart_name"`
	Version            string `json:"version"`
	RepoURL            string `json:"repo_url"`
	RepoName           string `json:"repo_name"`
	ValueFile          string `json:"value_file"`
	LocalPath          string `json:"local_path"`
	Checksum           string `json:"checksum,omitempty"`
}

type pedestalHelmVerifyCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

type pedestalHelmVerifyResp struct {
	OK     bool                      `json:"ok"`
	Checks []pedestalHelmVerifyCheck `json:"checks"`
}

type pedestalHelmSetReq struct {
	ChartName string `json:"chart_name"`
	Version   string `json:"version"`
	RepoURL   string `json:"repo_url"`
	RepoName  string `json:"repo_name"`
	ValueFile string `json:"value_file,omitempty"`
	LocalPath string `json:"local_path,omitempty"`
}

var pedestalCmd = &cobra.Command{
	Use:   "pedestal",
	Short: "Manage pedestal (SUT) container infrastructure",
}

var pedestalHelmCmd = &cobra.Command{
	Use:   "helm",
	Short: "Inspect and edit helm_configs rows bound to pedestal container versions",
	Long: `Manage the helm chart configuration bound to a pedestal container version.

Typical workflow for fixing a bad repo URL without touching MySQL directly:

  aegisctl pedestal helm get    --container-version-id 42
  aegisctl pedestal helm set    --container-version-id 42 --repo-url https://...
  aegisctl pedestal helm verify --container-version-id 42

To propagate a data.yaml chart bump (with new override values) to a running
cluster without raw SQL — the issue #201 use case — use:

  aegisctl pedestal helm reseed --container-version-id 42            # dry-run
  aegisctl pedestal helm reseed --container-version-id 42 --apply    # commit`,
}

// --- shared flags ---
var pedestalHelmVersionID int

// --- get ---
var pedestalHelmGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Get the helm_configs row for a container version",
	RunE: func(cmd *cobra.Command, args []string) error {
		if pedestalHelmVersionID <= 0 {
			return fmt.Errorf("--container-version-id is required and must be > 0")
		}
		c := newClient()
		var resp client.APIResponse[pedestalHelmConfig]
		if err := c.Get(consts.APIPathPedestalHelmByID(pedestalHelmVersionID), &resp); err != nil {
			return err
		}
		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(resp.Data)
			return nil
		}
		d := resp.Data
		fmt.Printf("ID:                   %d\n", d.ID)
		fmt.Printf("ContainerVersionID:   %d\n", d.ContainerVersionID)
		fmt.Printf("ChartName:            %s\n", d.ChartName)
		fmt.Printf("Version:              %s\n", d.Version)
		fmt.Printf("RepoURL:              %s\n", d.RepoURL)
		fmt.Printf("RepoName:             %s\n", d.RepoName)
		fmt.Printf("ValueFile:            %s\n", d.ValueFile)
		fmt.Printf("LocalPath:            %s\n", d.LocalPath)
		if d.Checksum != "" {
			fmt.Printf("Checksum:             %s\n", d.Checksum)
		}
		return nil
	},
}

// --- set ---
var (
	pedestalHelmSetChart    string
	pedestalHelmSetVersion  string
	pedestalHelmSetRepoURL  string
	pedestalHelmSetRepoName string
	pedestalHelmSetValues   string
	pedestalHelmSetLocal    string
)

var pedestalHelmSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Upsert the helm_configs row for a container version (admin only)",
	RunE: func(cmd *cobra.Command, args []string) error {
		if pedestalHelmVersionID <= 0 {
			return fmt.Errorf("--container-version-id is required and must be > 0")
		}
		if pedestalHelmSetChart == "" || pedestalHelmSetVersion == "" ||
			pedestalHelmSetRepoURL == "" || pedestalHelmSetRepoName == "" {
			return fmt.Errorf("--chart-name, --version, --repo-url, --repo-name are all required")
		}
		body := pedestalHelmSetReq{
			ChartName: pedestalHelmSetChart,
			Version:   pedestalHelmSetVersion,
			RepoURL:   pedestalHelmSetRepoURL,
			RepoName:  pedestalHelmSetRepoName,
			ValueFile: pedestalHelmSetValues,
			LocalPath: pedestalHelmSetLocal,
		}
		c := newClient()
		var resp client.APIResponse[pedestalHelmConfig]
		if err := c.Put(consts.APIPathPedestalHelmByID(pedestalHelmVersionID), body, &resp); err != nil {
			return err
		}
		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(resp.Data)
			return nil
		}
		output.PrintInfo(fmt.Sprintf("helm_configs row for container_version_id=%d upserted (id=%d)",
			resp.Data.ContainerVersionID, resp.Data.ID))
		return nil
	},
}

// --- verify ---
var pedestalHelmVerifyCmd = &cobra.Command{
	Use:   "verify",
	Short: "Dry-run helm repo add + pull + value-file parse without starting a restart task",
	RunE: func(cmd *cobra.Command, args []string) error {
		if pedestalHelmVersionID <= 0 {
			return fmt.Errorf("--container-version-id is required and must be > 0")
		}
		path := consts.APIPathPedestalHelmVerify(pedestalHelmVersionID)
		if flagDryRun {
			plan := map[string]any{
				"dry_run":              true,
				"operation":            "pedestal_helm_verify",
				"container_version_id": pedestalHelmVersionID,
				"method":               "POST",
				"path":                 path,
			}
			if output.OutputFormat(flagOutput) == output.FormatJSON {
				output.PrintJSON(plan)
			} else {
				output.PrintInfo(fmt.Sprintf("Dry run: POST %s", path))
			}
			return nil
		}

		c := newClient()
		var resp client.APIResponse[pedestalHelmVerifyResp]
		if err := c.Post(path, nil, &resp); err != nil {
			return err
		}
		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(resp.Data)
			if !resp.Data.OK {
				return missingEnvErrorf("pedestal helm verify failed for container_version_id=%d", pedestalHelmVersionID)
			}
			return nil
		}
		for _, chk := range resp.Data.Checks {
			mark := "OK"
			if !chk.OK {
				mark = "FAIL"
			}
			fmt.Printf("[%s] %s\n", mark, chk.Name)
			if chk.Detail != "" {
				fmt.Printf("       %s\n", chk.Detail)
			}
		}
		if !resp.Data.OK {
			return missingEnvErrorf("pedestal helm verify failed for container_version_id=%d", pedestalHelmVersionID)
		}
		return nil
	},
}

// --- reseed ---
//
// `aegisctl pedestal helm reseed` is the hot-reseed counterpart to `set`.
// `set` only mutates chart-level fields on `helm_configs`; reseed also
// reconciles the linked `parameter_configs` + `helm_config_values` rows from
// the seed YAML so a chart bump that adds new override keys (issue #201,
// teastore 0.1.1 -> 0.1.2 + jmeter.waitForRegistryImage.* values) takes
// effect on a running cluster without raw SQL.
//
// Defaults to dry-run (server side, mirrors `aegisctl system reseed`). Pass
// --apply to actually write. --prune drops links whose key disappeared
// from the seed.
var (
	pedestalHelmReseedEnv      string
	pedestalHelmReseedDataPath string
	pedestalHelmReseedApply    bool
	pedestalHelmReseedPrune    bool
)

type pedestalHelmReseedReq struct {
	Env      string `json:"env,omitempty"`
	DataPath string `json:"data_path,omitempty"`
	Apply    bool   `json:"apply"`
	Prune    bool   `json:"prune,omitempty"`
}

type pedestalHelmReseedAction struct {
	Layer    string `json:"layer"`
	System   string `json:"system"`
	Key      string `json:"key"`
	OldValue string `json:"old_value"`
	NewValue string `json:"new_value"`
	Note     string `json:"note"`
	Applied  bool   `json:"applied"`
}

type pedestalHelmReseedResp struct {
	DryRun       bool                       `json:"dry_run"`
	SystemFilter string                     `json:"system_filter"`
	SeedPath     string                     `json:"seed_path"`
	Actions      []pedestalHelmReseedAction `json:"actions"`
}

var pedestalHelmReseedCmd = &cobra.Command{
	Use:   "reseed",
	Short: "Hot-reseed helm_configs + values for one container_version from data.yaml",
	Long: `Reconcile the helm_configs row and its linked parameter_configs +
helm_config_values for a pedestal container_version against the seed YAML.
Use this to propagate a data.yaml chart-version bump (and any new overridable
values) to a running cluster without raw SQL — e.g. issue #201's teastore
0.1.1 -> 0.1.2 + jmeter.waitForRegistryImage.* values.

Defaults to DRY-RUN for safety. Pass --apply to actually write.

Conflict semantics:
  - Existing parameter_configs.default_value is NEVER overwritten (warning
    logged + reported as a skipped action). Operators who want to clobber
    drift must edit the row directly.
  - New parameter_configs / helm_config_values rows ARE inserted.
  - --prune removes helm_config_values links whose key disappeared from the
    seed; off by default to avoid surprising deletions.

Idempotent: a re-run with no upstream change yields zero applied actions.`,
	Example: `  aegisctl pedestal helm reseed --container-version-id 62
  aegisctl pedestal helm reseed --container-version-id 62 --apply
  aegisctl pedestal helm reseed --container-version-id 62 --apply --prune
  aegisctl pedestal helm reseed --container-version-id 62 --env staging`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if pedestalHelmVersionID <= 0 {
			return fmt.Errorf("--container-version-id is required and must be > 0")
		}
		body := pedestalHelmReseedReq{
			Env:      strings.TrimSpace(pedestalHelmReseedEnv),
			DataPath: strings.TrimSpace(pedestalHelmReseedDataPath),
			Apply:    pedestalHelmReseedApply,
			Prune:    pedestalHelmReseedPrune,
		}
		path := consts.APIPathPedestalHelmReseed(pedestalHelmVersionID)
		if flagDryRun {
			plan := map[string]any{
				"dry_run":              true,
				"operation":            "pedestal_helm_reseed",
				"container_version_id": pedestalHelmVersionID,
				"method":               "POST",
				"path":                 path,
				"body":                 body,
			}
			if output.OutputFormat(flagOutput) == output.FormatJSON {
				output.PrintJSON(plan)
			} else {
				output.PrintInfo(fmt.Sprintf("Dry run: POST %s apply=%v prune=%v", path, body.Apply, body.Prune))
			}
			return nil
		}
		c := newClient()
		var resp client.APIResponse[pedestalHelmReseedResp]
		if err := c.Post(path, body, &resp); err != nil {
			return fmt.Errorf("reseed: %w (hint: retry without --apply to diff without writing; check backend logs for which subsystem failed)", err)
		}
		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(resp.Data)
			return nil
		}
		printPedestalHelmReseedReport(&resp.Data)
		return nil
	},
}

func printPedestalHelmReseedReport(r *pedestalHelmReseedResp) {
	mode := "APPLY"
	if r.DryRun {
		mode = "DRY-RUN"
	}
	suffix := ""
	if r.SystemFilter != "" {
		suffix = " filter=" + r.SystemFilter
	}
	output.PrintInfo(fmt.Sprintf("pedestal helm reseed %s (seed=%s%s)", mode, r.SeedPath, suffix))

	if len(r.Actions) == 0 {
		output.PrintInfo("No drift — DB already in sync with data.yaml for this container_version.")
		return
	}

	actions := append([]pedestalHelmReseedAction(nil), r.Actions...)
	sort.SliceStable(actions, func(i, j int) bool {
		if actions[i].Layer != actions[j].Layer {
			return actions[i].Layer < actions[j].Layer
		}
		return actions[i].Key < actions[j].Key
	})

	rows := make([][]string, 0, len(actions))
	for _, a := range actions {
		status := "plan"
		if a.Applied {
			status = "applied"
		} else if r.DryRun {
			status = "would-apply"
		} else if strings.Contains(a.Note, "preserved") || strings.Contains(a.Note, "drift on existing parameter_config") {
			status = "preserved"
		}
		rows = append(rows, []string{
			a.Layer,
			truncCell(a.Key, 48),
			truncCell(a.OldValue, 28),
			truncCell(a.NewValue, 28),
			status,
			truncCell(a.Note, 48),
		})
	}
	output.PrintTable([]string{"Layer", "Key", "Old", "New", "Status", "Note"}, rows)

	if r.DryRun {
		output.PrintInfo("Re-run with --apply to write the planned changes.")
	}
	preserved := 0
	for _, a := range actions {
		if !a.Applied && (strings.Contains(a.Note, "preserved") || strings.Contains(a.Note, "drift on existing parameter_config")) {
			preserved++
		}
	}
	if preserved > 0 {
		output.PrintInfo(fmt.Sprintf("%d parameter_config default_value drift(s) preserved; edit the row directly if you want to clobber.", preserved))
	}
}

func init() {
	// Shared flag across all four subcommands.
	for _, c := range []*cobra.Command{pedestalHelmGetCmd, pedestalHelmSetCmd, pedestalHelmVerifyCmd, pedestalHelmReseedCmd} {
		c.Flags().IntVar(&pedestalHelmVersionID, "container-version-id", 0, "Container version ID (required)")
		_ = c.MarkFlagRequired("container-version-id")
	}

	pedestalHelmSetCmd.Flags().StringVar(&pedestalHelmSetChart, "chart-name", "", "Helm chart name (required)")
	pedestalHelmSetCmd.Flags().StringVar(&pedestalHelmSetVersion, "version", "", "Helm chart version, semver (required)")
	pedestalHelmSetCmd.Flags().StringVar(&pedestalHelmSetRepoURL, "repo-url", "", "Helm repository URL (required)")
	pedestalHelmSetCmd.Flags().StringVar(&pedestalHelmSetRepoName, "repo-name", "", "Helm repository name / alias (required)")
	pedestalHelmSetCmd.Flags().StringVar(&pedestalHelmSetValues, "values-file", "", "Path to the values YAML file (optional)")
	pedestalHelmSetCmd.Flags().StringVar(&pedestalHelmSetLocal, "local-path", "", "Local chart fallback path (optional)")

	pedestalHelmReseedCmd.Flags().StringVar(&pedestalHelmReseedEnv, "env", "", "Environment selector ('prod' / 'staging') for the server-side seed root")
	pedestalHelmReseedCmd.Flags().StringVar(&pedestalHelmReseedDataPath, "data-path", "", "Override server-side initialization.data_path (advanced)")
	pedestalHelmReseedCmd.Flags().BoolVar(&pedestalHelmReseedApply, "apply", false, "Actually write changes (default is dry-run)")
	pedestalHelmReseedCmd.Flags().BoolVar(&pedestalHelmReseedPrune, "prune", false, "Delete helm_config_values links whose key disappeared from the seed")

	pedestalHelmCmd.AddCommand(pedestalHelmGetCmd)
	pedestalHelmCmd.AddCommand(pedestalHelmSetCmd)
	pedestalHelmCmd.AddCommand(pedestalHelmVerifyCmd)
	pedestalHelmCmd.AddCommand(pedestalHelmReseedCmd)

	pedestalCmd.AddCommand(pedestalHelmCmd)
}
