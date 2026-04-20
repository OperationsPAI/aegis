package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"aegis/cmd/aegisctl/client"
	"aegis/cmd/aegisctl/cluster"
	"aegis/cmd/aegisctl/output"

	"github.com/OperationsPAI/chaos-experiment/pkg/guidedcli"
	"github.com/spf13/cobra"
)

const (
	regressionExitAuthFailure    = 2
	regressionExitMissingEnv     = 3
	regressionExitFailure        = 4
	defaultRegressionWaitSecs    = 600
	defaultRegressionPollSecs    = 5
	defaultRegressionProject     = "pair_diagnosis"
	regressionOutcomePass        = "pass"
	regressionOutcomeFail        = "fail"
	regressionOutcomeSubmitted   = "submitted"
	regressionErrCategoryAuth    = "auth_failure"
	regressionErrCategoryEnv     = "missing_environment"
	regressionErrCategoryFailure = "regression_failure"
)

var (
	regressionWaitEnabled  bool
	regressionEnsureEnv    bool
	regressionWaitTimeout  int
	regressionPollInterval int
)

type regressionTaskState struct {
	TaskID string `json:"task_id"`
	Type   string `json:"type"`
	State  string `json:"state"`
}

type regressionTraceTask struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	State string `json:"state"`
}

type regressionEnvCheck struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

type regressionSummary struct {
	CaseName      string                `json:"case_name"`
	TraceID       string                `json:"trace_id,omitempty"`
	TraceState    string                `json:"trace_state,omitempty"`
	FinalEvent    string                `json:"final_event,omitempty"`
	TaskStates    []regressionTaskState `json:"task_states,omitempty"`
	Outcome       string                `json:"outcome"`
	Waited        bool                  `json:"waited"`
	EnsuredEnv    bool                  `json:"ensured_env"`
	ErrorCategory string                `json:"error_category,omitempty"`
	Message       string                `json:"message,omitempty"`
	EnvChecks     []regressionEnvCheck  `json:"env_checks,omitempty"`
}

type regressionTraceDetail struct {
	ID        string                `json:"id"`
	State     string                `json:"state"`
	LastEvent string                `json:"last_event"`
	Tasks     []regressionTraceTask `json:"tasks"`
}

type regressionCaseDefinition struct {
	Name           string
	Description    string
	DefaultProject string
	GuidedConfig   guidedcli.GuidedConfig
	ApplyOptions   guidedApplyOptions
}

var regressionCases = map[string]regressionCaseDefinition{
	"otel-demo-guided": {
		Name:           "otel-demo-guided",
		Description:    "Canonical otel-demo guided network delay validation path",
		DefaultProject: defaultRegressionProject,
		GuidedConfig: guidedcli.GuidedConfig{
			System:        "otel-demo0",
			App:           "cart",
			ChaosType:     "NetworkDelay",
			TargetService: "valkey-cart",
			Direction:     "to",
			Duration:      intPtr(2),
			Latency:       intPtr(731),
			Correlation:   intPtr(100),
			Jitter:        intPtr(1),
		},
		ApplyOptions: guidedApplyOptions{
			PedestalName:  "otel-demo",
			PedestalTag:   "1.0.0",
			BenchmarkName: "otel-demo-bench",
			BenchmarkTag:  "1.0.0",
			Interval:      4,
			PreDuration:   1,
		},
	},
}

var regressionCmd = &cobra.Command{
	Use:   "regression",
	Short: "Run curated application-level regression validations",
	Long: `Run curated application-level regression validations through the same
` + "`aegisctl`" + ` client and backend protocol paths used by normal operators.

The first shipped case is ` + "`otel-demo-guided`" + `, which submits the
canonical guided otel-demo network-delay validation flow.

EXIT CODES:
  0 — submit succeeded (or waited and passed)
  1 — command misuse or unexpected CLI/runtime error
  2 — authentication/authorization failure
  3 — missing local environment or dependency (for example --ensure-env preflight failed)
  4 — regression execution failed after submission`,
}

var regressionRunCmd = &cobra.Command{
	Use:   "run <case-name>",
	Short: "Run a curated regression case",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		summary, exitCode, err := runRegressionCase(cmd.Context(), args[0], regressionRunOptions{
			Wait:         regressionWaitEnabled,
			EnsureEnv:    regressionEnsureEnv,
			WaitTimeout:  time.Duration(regressionWaitTimeout) * time.Second,
			PollInterval: time.Duration(regressionPollInterval) * time.Second,
		})
		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(summary)
		} else {
			printRegressionSummary(summary)
		}
		if err != nil {
			if exitCode == 0 || exitCode == 1 {
				return err
			}
			os.Exit(exitCode)
		}
		if exitCode != 0 {
			os.Exit(exitCode)
		}
		return nil
	},
}

type regressionRunOptions struct {
	Wait         bool
	EnsureEnv    bool
	WaitTimeout  time.Duration
	PollInterval time.Duration
}

func init() {
	regressionRunCmd.Flags().BoolVar(&regressionEnsureEnv, "ensure-env", false, "Run dependency preflight checks before submitting the case")
	regressionRunCmd.Flags().BoolVar(&regressionWaitEnabled, "wait", false, "Block until the submitted trace reaches a terminal state")
	regressionRunCmd.Flags().IntVar(&regressionWaitTimeout, "timeout", defaultRegressionWaitSecs, "Maximum time to wait when --wait is set, in seconds")
	regressionRunCmd.Flags().IntVar(&regressionPollInterval, "interval", defaultRegressionPollSecs, "Trace poll interval in seconds when --wait is set")
	regressionCmd.AddCommand(regressionRunCmd)
}

func intPtr(v int) *int {
	return &v
}

func printRegressionSummary(summary regressionSummary) {
	rows := [][]string{
		{"case", summary.CaseName},
		{"outcome", summary.Outcome},
		{"trace_id", nonEmpty(summary.TraceID, "-")},
		{"trace_state", nonEmpty(summary.TraceState, "-")},
		{"final_event", nonEmpty(summary.FinalEvent, "-")},
	}
	if summary.ErrorCategory != "" {
		rows = append(rows, []string{"error_category", summary.ErrorCategory})
	}
	if summary.Message != "" {
		rows = append(rows, []string{"message", summary.Message})
	}
	output.PrintTable([]string{"FIELD", "VALUE"}, rows)
	if len(summary.TaskStates) == 0 {
		return
	}
	fmt.Fprintln(os.Stdout)
	taskRows := make([][]string, 0, len(summary.TaskStates))
	for _, task := range summary.TaskStates {
		taskRows = append(taskRows, []string{task.TaskID, task.Type, task.State})
	}
	output.PrintTable([]string{"TASK-ID", "TYPE", "STATE"}, taskRows)
}

func runRegressionCase(ctx context.Context, caseName string, opts regressionRunOptions) (regressionSummary, int, error) {
	summary := regressionSummary{
		CaseName:   caseName,
		Waited:     opts.Wait,
		EnsuredEnv: opts.EnsureEnv,
		Outcome:    regressionOutcomeSubmitted,
	}

	def, ok := regressionCases[caseName]
	if !ok {
		return summary, 1, fmt.Errorf("unsupported regression case %q (available: %s)", caseName, strings.Join(sortedRegressionCases(), ", "))
	}

	if opts.PollInterval <= 0 {
		opts.PollInterval = defaultRegressionPollSecs * time.Second
	}
	if opts.WaitTimeout <= 0 {
		opts.WaitTimeout = defaultRegressionWaitSecs * time.Second
	}

	if opts.EnsureEnv {
		allOK, checks, rendered, err := runRegressionPreflight(ctx)
		summary.EnvChecks = checks
		if err != nil {
			return summarizeRegressionError(summary, err)
		}
		if rendered != "" && output.OutputFormat(flagOutput) != output.FormatJSON && !output.Quiet {
			output.PrintInfo("Running regression preflight checks:")
			fmt.Fprint(os.Stderr, rendered)
		}
		if !allOK {
			summary.Outcome = regressionOutcomeFail
			summary.ErrorCategory = regressionErrCategoryEnv
			summary.Message = "environment preflight failed"
			return summary, regressionExitMissingEnv, nil
		}
	}

	projectName := flagProject
	if projectName == "" {
		projectName = def.DefaultProject
	}
	resp, err := submitGuidedApplyWithOptions(projectName, def.GuidedConfig, def.ApplyOptions)
	if err != nil {
		return summarizeRegressionError(summary, err)
	}
	if len(resp.Data.Items) == 0 || resp.Data.Items[0].TraceID == "" {
		return summary, 1, fmt.Errorf("server accepted regression submission but returned no trace_id")
	}
	summary.TraceID = resp.Data.Items[0].TraceID
	summary.Message = "regression case submitted"

	if !opts.Wait {
		if detail, err := fetchRegressionTraceDetail(newClient(), summary.TraceID); err == nil {
			populateRegressionSummaryFromTrace(&summary, detail)
		}
		return summary, 0, nil
	}

	detail, err := waitForRegressionTrace(ctx, summary.TraceID, opts.WaitTimeout, opts.PollInterval)
	if err != nil {
		return summarizeRegressionError(summary, err)
	}
	populateRegressionSummaryFromTrace(&summary, detail)
	if isPassingRegressionTrace(detail) {
		summary.Outcome = regressionOutcomePass
		summary.Message = "regression case passed"
		return summary, 0, nil
	}

	summary.Outcome = regressionOutcomeFail
	summary.ErrorCategory = regressionErrCategoryFailure
	summary.Message = "regression trace reached a failing terminal state"
	return summary, regressionExitFailure, nil
}

func runRegressionPreflight(ctx context.Context) (bool, []regressionEnvCheck, string, error) {
	cfg, err := cluster.LoadConfig("")
	if err != nil {
		return false, nil, "", fmt.Errorf("load cluster preflight config: %w", err)
	}
	env := cluster.NewLiveEnv(cfg)
	reg := cluster.NewRegistry(cluster.DefaultChecks())
	runner := &cluster.Runner{Registry: reg}
	var buf bytes.Buffer
	allOK, results := runner.Run(ctx, env, cluster.RunOptions{PerCheckTimeout: 10 * time.Second}, &buf)
	checks := make([]regressionEnvCheck, 0, len(results))
	for _, res := range results {
		checks = append(checks, regressionEnvCheck{
			ID:     res.ID,
			Status: string(res.Status),
			Detail: res.Detail,
			Fix:    res.Fix,
		})
	}
	return allOK, checks, buf.String(), nil
}

func waitForRegressionTrace(ctx context.Context, traceID string, timeout, interval time.Duration) (*regressionTraceDetail, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	c := newClient()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		state, _, err := pollState(c, "trace", traceID)
		if err != nil {
			return nil, err
		}
		if isTerminal("trace", state) {
			return fetchRegressionTraceDetail(c, traceID)
		}

		select {
		case <-waitCtx.Done():
			if errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
				return nil, fmt.Errorf("timed out waiting for trace %s after %s", traceID, timeout)
			}
			return nil, waitCtx.Err()
		case <-ticker.C:
		}
	}
}

func fetchRegressionTraceDetail(c *client.Client, traceID string) (*regressionTraceDetail, error) {
	var resp client.APIResponse[regressionTraceDetail]
	if err := c.Get(fmt.Sprintf("/api/v2/traces/%s", traceID), &resp); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

func populateRegressionSummaryFromTrace(summary *regressionSummary, detail *regressionTraceDetail) {
	if summary == nil || detail == nil {
		return
	}
	summary.TraceID = nonEmpty(summary.TraceID, detail.ID)
	summary.TraceState = detail.State
	summary.FinalEvent = detail.LastEvent
	summary.TaskStates = make([]regressionTaskState, 0, len(detail.Tasks))
	for _, task := range detail.Tasks {
		summary.TaskStates = append(summary.TaskStates, regressionTaskState{
			TaskID: task.ID,
			Type:   task.Type,
			State:  task.State,
		})
	}
}

func isPassingRegressionTrace(detail *regressionTraceDetail) bool {
	if detail == nil || detail.State != "Completed" || len(detail.Tasks) == 0 {
		return false
	}
	for _, task := range detail.Tasks {
		if task.State != "Completed" {
			return false
		}
	}
	return true
}

func summarizeRegressionError(summary regressionSummary, err error) (regressionSummary, int, error) {
	var apiErr *client.APIError
	switch {
	case errors.As(err, &apiErr) && (apiErr.StatusCode == 401 || apiErr.StatusCode == 403):
		summary.Outcome = regressionOutcomeFail
		summary.ErrorCategory = regressionErrCategoryAuth
		summary.Message = apiErr.Error()
		return summary, regressionExitAuthFailure, nil
	case errors.As(err, &apiErr):
		summary.Outcome = regressionOutcomeFail
		summary.ErrorCategory = regressionErrCategoryFailure
		summary.Message = apiErr.Error()
		return summary, regressionExitFailure, nil
	case strings.Contains(strings.ToLower(err.Error()), "timed out waiting for trace"):
		summary.Outcome = regressionOutcomeFail
		summary.ErrorCategory = regressionErrCategoryFailure
		summary.Message = err.Error()
		return summary, regressionExitFailure, nil
	default:
		return summary, 1, err
	}
}

func sortedRegressionCases() []string {
	out := make([]string, 0, len(regressionCases))
	for name := range regressionCases {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
