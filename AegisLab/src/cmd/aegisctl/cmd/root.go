package cmd

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"aegis/cmd/aegisctl/config"
	"aegis/cmd/aegisctl/output"

	"github.com/spf13/cobra"
)

var (
	// Global flag values.
	flagServer         string
	flagToken          string
	flagProject        string
	flagOutput         string
	flagRequestTimeout int
	flagQuiet          bool
	flagNoColor        bool
	flagNonInteractive bool
	flagDryRun         bool
	flagVersion        bool

	// Resolved at PersistentPreRun time.
	cfg *config.Config
)

// rootCmd is the top-level aegisctl command.
var rootCmd = &cobra.Command{
	Use:           "aegisctl",
	Short:         "CLI client for the AegisLab platform",
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if flagVersion {
			printVersionInfo()
			return nil
		}
		return cmd.Help()
	},
	Long: `aegisctl is a command-line interface for managing the AegisLab (RCABench)
fault-injection and root-cause-analysis benchmarking platform.

QUICK START:
  # 1. Exchange Key ID / Key Secret for a token (saves token to ~/.aegisctl/config.yaml)
  aegisctl auth login --server http://HOST:8082 --key-id pk_xxx --key-secret ks_xxx

  # 2. Set default project so you don't need --project every time
  aegisctl context set --name default --default-project pair_diagnosis

  # 3. Browse available resources
  aegisctl project list
  aegisctl container list
  aegisctl container list --type algorithm
  aegisctl dataset list

  # 4. Submit a fault injection via the guided flow
  aegisctl inject guided --reset-config --no-save-config
  aegisctl inject guided --apply --project pair_diagnosis \
    --pedestal-name ts --pedestal-tag 1.0.0 \
    --benchmark-name otel-demo-bench --benchmark-tag 1.0.0

  # 5. Monitor progress
  aegisctl trace list --project pair_diagnosis
  aegisctl trace watch <trace-id>
  aegisctl task list --state Running
  aegisctl task logs <task-id> --follow

  # 6. Wait for completion (blocks until terminal state, exit code 0=ok, 5=fail, 6=timeout)
  aegisctl wait <trace-id> --timeout 600

  # 7. View results
  aegisctl inject list --project pair_diagnosis
  aegisctl inject get <injection-name>
  aegisctl execute list --project pair_diagnosis

OUTPUT:
  All commands support --output table|json|ndjson (or -o) for machine output.
  --output table remains human-friendly and writes to stdout.
  Table output goes to stdout; informational messages go to stderr.
  Use --quiet (-q) to suppress informational messages.
  Use --non-interactive to lock automation-facing commands into fail-fast,
  prompt-free behavior suitable for CI and agent execution.

ENVIRONMENT VARIABLES:
  AEGIS_SERVER      - Server URL (overridden by --server flag)
  AEGIS_TOKEN       - Auth token (overridden by --token flag)
  AEGIS_KEY_ID      - API key ID for 'aegisctl auth login'
  AEGIS_KEY_SECRET  - API key secret for 'aegisctl auth login'
  AEGIS_USERNAME    - Username for password login
  AEGIS_PASSWORD    - Password for 'aegisctl auth login'
  AEGIS_PASSWORD_FILE - File containing the password for 'aegisctl auth login'
  AEGIS_PROJECT     - Default project name (overridden by --project flag)
  AEGIS_OUTPUT      - Output format: table|json|ndjson (overridden by --output flag)
  AEGIS_TIMEOUT     - Request timeout in seconds (overridden by --request-timeout flag)
  AEGIS_NON_INTERACTIVE - Set true/1 to disable prompts and require explicit input

NAMING CONVENTION:
  Most commands accept human-readable names instead of numeric IDs.
  For example: "aegisctl container get detector" resolves "detector" to its ID.
  The --project flag also accepts project names (e.g. "pair_diagnosis").`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if flagVersion {
			printVersionInfo()
			return silentExit(ExitCodeSuccess)
		}

		// Load configuration file.
		var err error
		cfg, err = config.LoadConfig()
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}

		// Resolve --server: flag > env > config context.
		if flagServer == "" {
			flagServer = os.Getenv("AEGIS_SERVER")
		}
		if flagServer == "" {
			if ctx, _, err := config.GetCurrentContext(cfg); err == nil {
				flagServer = ctx.Server
			}
		}

		// Resolve --token: flag > env > config context.
		if flagToken == "" {
			flagToken = os.Getenv("AEGIS_TOKEN")
		}
		if flagToken == "" {
			if ctx, _, err := config.GetCurrentContext(cfg); err == nil {
				flagToken = ctx.Token
			}
		}

		// Resolve --project: flag > env > config context.
		if flagProject == "" {
			flagProject = os.Getenv("AEGIS_PROJECT")
		}
		if flagProject == "" {
			if ctx, _, err := config.GetCurrentContext(cfg); err == nil {
				flagProject = ctx.DefaultProject
			}
		}

		// Resolve --output: flag > env > config preferences.
		if flagOutput == "" {
			flagOutput = os.Getenv("AEGIS_OUTPUT")
		}
		if flagOutput == "" && cfg.Preferences.Output != "" {
			flagOutput = cfg.Preferences.Output
		}
		if flagOutput == "" {
			flagOutput = "table"
		}

		// Resolve --request-timeout: flag > env > config preferences.
		if flagRequestTimeout == 0 {
			if v := os.Getenv("AEGIS_TIMEOUT"); v != "" {
				if n, err := strconv.Atoi(v); err == nil {
					flagRequestTimeout = n
				}
			}
		}
		if flagRequestTimeout == 0 && cfg.Preferences.RequestTimeout > 0 {
			flagRequestTimeout = cfg.Preferences.RequestTimeout
		}
		if flagRequestTimeout == 0 {
			flagRequestTimeout = 30
		}

		if !cmd.Flags().Lookup("non-interactive").Changed {
			if v := os.Getenv("AEGIS_NON_INTERACTIVE"); v != "" {
				if b, err := strconv.ParseBool(v); err == nil {
					flagNonInteractive = b
				}
			}
		}

		// Respect --no-color and NO_COLOR for all colorized output.
		output.SetNoColor(flagNoColor || os.Getenv("NO_COLOR") != "")

		// Forward quiet flag into the output package.
		output.Quiet = flagQuiet

		if err := validateOutputFormat(cmd); err != nil {
			return err
		}

		// Reject --dry-run on commands that don't implement it. We do this
		// after the resolution logic so env-expansion still runs, but before
		// any RunE executes — otherwise users get a silently-ignored flag.
		// --help and --dump-schema / non-runnable groups are exempt because
		// PersistentPreRunE is not invoked for pure --help paths, and group
		// commands have no Run/RunE so they'd just print usage.
		if flagDryRun && cmd.Runnable() && !isDryRunSupported(cmd) {
			return usageErrorf("--dry-run is not supported for '%s'", cmd.CommandPath())
		}

		return nil
	},
}

const outputFormatAllowlistAnnotation = "allowed-output-formats"

func registerOutputFormats(cmd *cobra.Command, extras ...output.OutputFormat) {
	if cmd == nil || len(extras) == 0 {
		return
	}
	seen := make(map[string]struct{}, len(extras))
	for _, extra := range extras {
		seen[strings.ToLower(string(extra))] = struct{}{}
	}
	values := make([]string, 0, len(seen))
	for v := range seen {
		values = append(values, v)
	}
	sort.Strings(values)
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[outputFormatAllowlistAnnotation] = strings.Join(values, ",")
}

func validateOutputFormat(cmd *cobra.Command) error {
	allowed := map[string]struct{}{
		string(output.FormatTable):  {},
		string(output.FormatJSON):   {},
		string(output.FormatNDJSON): {},
	}
	for _, v := range strings.Split(getAnnotatedOutputFormats(cmd), ",") {
		if strings.TrimSpace(v) == "" {
			continue
		}
		allowed[strings.ToLower(v)] = struct{}{}
	}
	if _, ok := allowed[strings.ToLower(flagOutput)]; ok {
		return nil
	}
	return usageErrorf("invalid --output %q; expected %s", flagOutput, formatCSV(allowed))
}

func getAnnotatedOutputFormats(cmd *cobra.Command) string {
	if cmd == nil || cmd.Annotations == nil {
		return ""
	}
	return cmd.Annotations[outputFormatAllowlistAnnotation]
}

func formatCSV(values map[string]struct{}) string {
	parts := make([]string, 0, len(values))
	for v := range values {
		parts = append(parts, v)
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func init() {
	rootCmd.PersistentFlags().StringVar(&flagServer, "server", "", "AegisLab server URL (env: AEGIS_SERVER)")
	rootCmd.PersistentFlags().StringVar(&flagToken, "token", "", "Authentication token (env: AEGIS_TOKEN)")
	rootCmd.PersistentFlags().StringVar(&flagProject, "project", "", "Default project name (resolved to ID; env: AEGIS_PROJECT)")
	rootCmd.PersistentFlags().StringVarP(&flagOutput, "output", "o", "", "Output format: table|json|ndjson (env: AEGIS_OUTPUT)")
	rootCmd.PersistentFlags().IntVar(&flagRequestTimeout, "request-timeout", 0, "Request timeout in seconds (env: AEGIS_TIMEOUT)")
	rootCmd.PersistentFlags().BoolVarP(&flagQuiet, "quiet", "q", false, "Suppress informational output")
	rootCmd.PersistentFlags().BoolVar(&flagNoColor, "no-color", false, "Disable ANSI color output (env: NO_COLOR)")
	rootCmd.PersistentFlags().BoolVar(&flagNonInteractive, "non-interactive", false, "Disable prompts and require explicit input (env: AEGIS_NON_INTERACTIVE)")
	rootCmd.PersistentFlags().BoolVar(&flagDryRun, "dry-run", false, "Show what would be done without executing")
	rootCmd.PersistentFlags().BoolVar(&flagVersion, "version", false, "Print version information and exit")

	// Register subcommands.
	rootCmd.AddCommand(authCmd)
	rootCmd.AddCommand(contextCmd)

	// Stubs for subcommands implemented by other agents:
	rootCmd.AddCommand(projectCmd)
	rootCmd.AddCommand(containerCmd)
	rootCmd.AddCommand(injectCmd)
	rootCmd.AddCommand(executeCmd)
	rootCmd.AddCommand(taskCmd)
	rootCmd.AddCommand(traceCmd)
	rootCmd.AddCommand(datasetCmd)
	rootCmd.AddCommand(evalCmd)
	rootCmd.AddCommand(waitCmd)
	rootCmd.AddCommand(regressionCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(completionCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(pedestalCmd)
	rootCmd.AddCommand(schemaCmd)
	rootCmd.AddCommand(systemCmd)
}

// setupDryRunRegistry records which commands opt in to --dry-run. It must be
// invoked after ALL package-level init() functions have wired subcommands into
// their parents (so CommandPath() resolves to the full dotted path). We call
// it from Execute(), which is the single entry point.
func setupDryRunRegistry() {
	if injectGuidedCmd != nil {
		markDryRunSupported(injectGuidedCmd)
	}
	if executeCreateCmd != nil {
		markDryRunSupported(executeCreateCmd)
	}
	if clusterPrepareCmd != nil {
		markDryRunSupported(clusterPrepareCmd)
	}
	if schemaDumpCmd != nil {
		// schema dump is read-only; --dry-run is a no-op but allowed.
		markDryRunSupported(schemaDumpCmd)
	}
	markPedestalDryRunSupported()
}

// Execute runs the root command and returns the process exit code.
func Execute() int {
	return executeArgs(os.Args[1:])
}
