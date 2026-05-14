package cmd

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"aegis/cli/cluster"
	"aegis/cli/output"

	"github.com/spf13/cobra"
)

var (
	clusterPreflightCheck   string
	clusterPreflightFix     bool
	clusterPreflightConfig  string
	clusterPreflightTimeout int

	clusterPrepareApply   bool
	clusterPrepareConfig  string
	clusterPrepareTimeout int
)

var clusterCmd = &cobra.Command{
	Use:   "cluster",
	Short: "Inspect and repair aegislab cluster dependencies",
	Long: `The "cluster" subcommand groups operations that target the aegislab
cluster and its backing services (Kubernetes, MySQL, ClickHouse, Redis, etcd).`,
}

var clusterPrepareCmd = &cobra.Command{
	Use:   "prepare",
	Short: "Preview or apply Aegis-specific cluster preparation flows",
	Long: `Prepare runs Aegis-specific readiness actions that sit above generic cluster
lifecycle steps. Use it to preview or apply local/e2e repair and seed actions
such as namespaces, service accounts, PVCs, and etcd config keys.

This command intentionally does not wrap generic kind, helm, or kubectl
bootstrap workflows.`,
}

var clusterPrepareLocalE2ECmd = &cobra.Command{
	Use:   "local-e2e",
	Short: "Preview or apply the Aegis-specific local/e2e preparation contract",
	Long: `local-e2e encodes the Aegis-specific repair/seed/config steps that a fresh
development cluster needs before guided local end-to-end validation.

By default the command previews intended changes without writing. Pass
--apply (or the alias --fix) to perform the actual writes. --dry-run can be
used explicitly to force a no-write preview, --non-interactive is treated
as an explicit apply request, and --output json returns a
stable machine-readable summary with create/update/skip outcomes.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if clusterPrepareTimeout <= 0 {
			return fmt.Errorf("--timeout must be greater than 0")
		}

		cfg, err := cluster.LoadConfig(clusterPrepareConfig)
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}

		env := cluster.NewLiveEnv(cfg)
		defer func() {
			_ = env.Etcd().Close()
		}()
		runner := cluster.LocalE2EPrepareRunner()
		ctx, cancel := context.WithTimeout(cmd.Context(), time.Duration(clusterPrepareTimeout)*time.Second)
		defer cancel()

		apply := shouldApplyClusterPrepareLocalE2E()
		results, err := runner.Run(ctx, env, apply)
		if err != nil {
			return err
		}

		summary := cluster.PrepareSummary{
			Target:  "local-e2e",
			DryRun:  !apply,
			Results: results,
		}

		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(summary)
			return nil
		}

		cluster.RenderPrepareTable(os.Stdout, results)
		return nil
	},
}

func shouldApplyClusterPrepareLocalE2E() bool {
	return (clusterPrepareApply || flagNonInteractive) && !flagDryRun
}

var clusterPreflightCmd = &cobra.Command{
	Use:   "preflight",
	Short: "Verify that every dependency required by aegislab is reachable and configured",
	Args:  requireNoArgs,
	Long: `Runs a catalog of checks against the cluster and the services referenced
by config.dev.toml. The command prints one row per check with status
[OK] / [FAIL] / [WARN] and a suggested fix on failure.

Use --check <id> to run a single check, or --fix to apply idempotent
remediation for the subset of checks that support it (currently:
k8s.rcabench-sa and redis.token-bucket-leaks).

Use "aegisctl cluster prepare local-e2e" when you want the apply/seed side of
the contract for Aegis-specific local/e2e prerequisites.

Exit code is 0 when every executed check is OK and 4 when a dependency
or environment prerequisite is missing or failing.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := cluster.LoadConfig(clusterPreflightConfig)
		if err != nil {
			return missingEnvErrorf("load config: %v", err)
		}
		env := cluster.NewLiveEnv(cfg)
		reg := cluster.NewRegistry(cluster.DefaultChecks())
		if clusterPreflightCheck != "" {
			if _, ok := reg.Get(clusterPreflightCheck); !ok {
				ids := reg.IDs()
				sort.Strings(ids)
				return usageErrorf("unknown --check %q (available: %s)", clusterPreflightCheck, strings.Join(ids, ", "))
			}
		}
		runner := &cluster.Runner{Registry: reg}
		opts := cluster.RunOptions{
			OnlyID:          clusterPreflightCheck,
			Fix:             clusterPreflightFix,
			PerCheckTimeout: time.Duration(clusterPreflightTimeout) * time.Second,
		}
		ctx, cancel := context.WithCancel(cmd.Context())
		defer cancel()
		allOK, _ := runner.Run(ctx, env, opts, os.Stdout)
		if !allOK {
			return silentExit(ExitCodeMissingEnv)
		}
		return nil
	},
}

func init() {
	clusterPreflightCmd.Flags().StringVar(&clusterPreflightCheck, "check", "", "Run only the named check (see --help for catalog)")
	clusterPreflightCmd.Flags().BoolVar(&clusterPreflightFix, "fix", false, "Attempt idempotent remediation for failing checks that support it")
	clusterPreflightCmd.Flags().StringVar(&clusterPreflightConfig, "config", "", "Path to a specific config TOML (defaults to config.$ENV_MODE.toml in cwd)")
	clusterPreflightCmd.Flags().IntVar(&clusterPreflightTimeout, "check-timeout", 10, "Per-check timeout in seconds")

	clusterPrepareLocalE2ECmd.Flags().BoolVar(&clusterPrepareApply, "apply", false, "Perform the local/e2e preparation writes instead of previewing them")
	clusterPrepareLocalE2ECmd.Flags().BoolVar(&clusterPrepareApply, "fix", false, "Alias for --apply")
	clusterPrepareLocalE2ECmd.Flags().StringVar(&clusterPrepareConfig, "config", "", "Path to a specific config TOML (defaults to config.$ENV_MODE.toml in cwd)")
	clusterPrepareLocalE2ECmd.Flags().IntVar(&clusterPrepareTimeout, "timeout", 30, "Overall timeout for the local/e2e preparation run in seconds")
	cobra.OnInitialize(func() {
		markDryRunSupported(clusterPrepareLocalE2ECmd)
	})

	clusterCmd.AddCommand(clusterPreflightCmd)
	clusterPrepareCmd.AddCommand(clusterPrepareLocalE2ECmd)
	clusterCmd.AddCommand(clusterPrepareCmd)
	rootCmd.AddCommand(clusterCmd)
}
