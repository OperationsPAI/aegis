package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"aegis/cmd/aegisctl/client"
	"aegis/cmd/aegisctl/output"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

type regressionCase struct {
	Name        string               `yaml:"name"`
	Description string               `yaml:"description,omitempty"`
	ProjectName string               `yaml:"project_name"`
	Submit      map[string]any       `yaml:"submit"`
	Validation  regressionValidation `yaml:"validation"`
}

type regressionValidation struct {
	TimeoutSeconds     int      `yaml:"timeout_seconds,omitempty"`
	MinEvents          int      `yaml:"min_events,omitempty"`
	ExpectedFinalEvent string   `yaml:"expected_final_event"`
	RequiredEvents     []string `yaml:"required_events,omitempty"`
	RequiredTaskChain  []string `yaml:"required_task_chain"`
}

type regressionSummary struct {
	CaseName           string   `json:"case_name"`
	CaseFile           string   `json:"case_file"`
	ProjectName        string   `json:"project_name"`
	TraceID            string   `json:"trace_id"`
	FinalEvent         string   `json:"final_event"`
	EventCount         int      `json:"event_count"`
	ObservedEvents     []string `json:"observed_events"`
	ObservedTaskChain  []string `json:"observed_task_chain"`
	ExpectedFinalEvent string   `json:"expected_final_event"`
	RequiredEvents     []string `json:"required_events"`
	RequiredTaskChain  []string `json:"required_task_chain"`
	Status             string   `json:"status"`
}

var (
	regressionCasesDir string
	regressionCaseFile string
)

var regressionCmd = &cobra.Command{
	Use:   "regression",
	Short: "Run repo-tracked regression cases",
	Long: `Run repo-tracked regression cases for aegisctl.

Regression cases live as YAML files under the repo's regression directory.
Each case carries both the submit payload and the validation contract so the
canonical smoke path is additive, reviewable, and versioned in git.`,
}

var regressionRunCmd = &cobra.Command{
	Use:   "run <case-name>",
	Short: "Load and execute a named repo-tracked regression case",
	Args: func(cmd *cobra.Command, args []string) error {
		if regressionCaseFile != "" {
			if len(args) > 0 {
				return fmt.Errorf("do not pass <case-name> when --file is set")
			}
			return nil
		}
		if len(args) != 1 {
			return fmt.Errorf("requires exactly one <case-name> argument unless --file is set")
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		var (
			rc       regressionCase
			casePath string
			err      error
		)
		if regressionCaseFile != "" {
			rc, casePath, err = loadRegressionCaseFile(regressionCaseFile)
		} else {
			rc, casePath, err = loadRegressionCaseByName(regressionCasesDir, args[0])
		}
		if err != nil {
			return err
		}

		summary, err := runRegressionCase(cmd.Context(), casePath, rc)
		if err != nil {
			return err
		}

		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(summary)
			return nil
		}

		output.PrintTable(
			[]string{"CASE", "TRACE", "FINAL EVENT", "EVENTS", "STATUS"},
			[][]string{{summary.CaseName, summary.TraceID, summary.FinalEvent, fmt.Sprintf("%d", summary.EventCount), summary.Status}},
		)
		return nil
	},
}

func loadRegressionCaseByName(casesDir, name string) (regressionCase, string, error) {
	if name == "" {
		return regressionCase{}, "", fmt.Errorf("regression case name is required")
	}
	// If the argument resolves to an existing file on disk (absolute or
	// relative path, or a bare filename with an extension), honor it as-is.
	// This keeps `aegisctl regression run ./foo.yaml` working.
	if strings.ContainsAny(name, string(filepath.Separator)) || strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml") {
		if _, err := os.Stat(name); err == nil {
			return loadRegressionCaseFile(name)
		}
	}
	if name == "." || name == ".." || filepath.Base(name) != name {
		return regressionCase{}, "", fmt.Errorf("invalid regression case name %q", name)
	}

	tried := make([]string, 0, 8)
	exts := []string{".yaml", ".yml"}

	// (1) Honor explicit --cases-dir (defaults to "regression" relative to cwd).
	for _, ext := range exts {
		path := filepath.Join(casesDir, name+ext)
		tried = append(tried, path)
		if _, err := os.Stat(path); err == nil {
			return loadRegressionCaseFile(path)
		}
	}

	// (2) Walk up from cwd looking for a sibling `regression/<name>.{yaml,yml}`.
	// Bounded by filesystem root or by a directory containing `.git`.
	if cwd, err := os.Getwd(); err == nil {
		dir := cwd
		for {
			for _, ext := range exts {
				path := filepath.Join(dir, "regression", name+ext)
				// Skip duplicates already tried via casesDir.
				if !containsPath(tried, path) {
					tried = append(tried, path)
					if _, err := os.Stat(path); err == nil {
						return loadRegressionCaseFile(path)
					}
				}
			}
			if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}

	// (3) AEGIS_REPO fallback: $AEGIS_REPO/AegisLab/regression/<name>.{yaml,yml}.
	if repo := strings.TrimSpace(os.Getenv("AEGIS_REPO")); repo != "" {
		for _, ext := range exts {
			path := filepath.Join(repo, "AegisLab", "regression", name+ext)
			if !containsPath(tried, path) {
				tried = append(tried, path)
				if _, err := os.Stat(path); err == nil {
					return loadRegressionCaseFile(path)
				}
			}
		}
	}

	return regressionCase{}, "", fmt.Errorf("regression case %q not found; tried:\n  %s", name, strings.Join(tried, "\n  "))
}

func containsPath(list []string, target string) bool {
	for _, p := range list {
		if p == target {
			return true
		}
	}
	return false
}

func loadRegressionCaseFile(path string) (regressionCase, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return regressionCase{}, "", fmt.Errorf("read regression case %q: %w", path, err)
	}

	var rc regressionCase
	if err := yaml.Unmarshal(data, &rc); err != nil {
		return regressionCase{}, "", fmt.Errorf("parse regression case %q: %w", path, err)
	}
	if err := validateRegressionCase(rc, path); err != nil {
		return regressionCase{}, "", err
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return rc, path, nil
	}
	return rc, absPath, nil
}

func validateRegressionCase(rc regressionCase, path string) error {
	if strings.TrimSpace(rc.Name) == "" {
		return fmt.Errorf("validate regression case %q: name is required", path)
	}
	if strings.TrimSpace(rc.ProjectName) == "" {
		return fmt.Errorf("validate regression case %q: project_name is required", path)
	}
	if len(rc.Submit) == 0 {
		return fmt.Errorf("validate regression case %q: submit payload is required", path)
	}
	if strings.TrimSpace(rc.Validation.ExpectedFinalEvent) == "" {
		return fmt.Errorf("validate regression case %q: validation.expected_final_event is required", path)
	}
	if len(rc.Validation.RequiredTaskChain) == 0 {
		return fmt.Errorf("validate regression case %q: validation.required_task_chain must include at least one task type", path)
	}
	if rc.Validation.TimeoutSeconds < 0 {
		return fmt.Errorf("validate regression case %q: validation.timeout_seconds must be >= 0", path)
	}
	if rc.Validation.MinEvents < 0 {
		return fmt.Errorf("validate regression case %q: validation.min_events must be >= 0", path)
	}
	return nil
}

func runRegressionCase(parentCtx context.Context, casePath string, rc regressionCase) (regressionSummary, error) {
	projectName := rc.ProjectName
	if flagProject != "" {
		projectName = flagProject
	}

	pid, err := newResolver().ProjectID(projectName)
	if err != nil {
		return regressionSummary{}, err
	}

	c := newClient()
	path := fmt.Sprintf("/api/v2/projects/%d/injections/inject", pid)
	var submitResp client.APIResponse[injectSubmitResponse]
	if err := c.Post(path, rc.Submit, &submitResp); err != nil {
		return regressionSummary{}, fmt.Errorf("submit regression case %q: %w", rc.Name, err)
	}
	if submitResp.Data.IsDedupedAll() {
		summary := submitResp.Data.DedupeSummary()
		output.PrintInfo(fmt.Sprintf("regression case %q: %s", rc.Name, summary))
		return regressionSummary{}, newDedupeSuppressedError(fmt.Sprintf("submit regression case %q: %s", rc.Name, summary))
	}
	if len(submitResp.Data.Items) == 0 || strings.TrimSpace(submitResp.Data.Items[0].TraceID) == "" {
		return regressionSummary{}, fmt.Errorf("submit regression case %q: server response missing trace_id", rc.Name)
	}
	traceID := submitResp.Data.Items[0].TraceID

	observedEvents, err := collectRegressionEvents(parentCtx, traceID, rc.Validation.TimeoutSeconds)
	if err != nil {
		return regressionSummary{}, fmt.Errorf("run regression case %q: %w", rc.Name, err)
	}

	traceData, err := fetchTraceData(c, traceID)
	if err != nil {
		return regressionSummary{}, fmt.Errorf("fetch trace %s for regression case %q: %w", traceID, rc.Name, err)
	}
	observedTaskChain := extractTaskChain(traceData)
	finalEvent := ""
	if len(observedEvents) > 0 {
		finalEvent = observedEvents[len(observedEvents)-1]
	}
	if err := validateRegressionOutcome(rc.Validation, observedEvents, observedTaskChain); err != nil {
		return regressionSummary{}, fmt.Errorf("validate regression case %q: %w", rc.Name, err)
	}

	return regressionSummary{
		CaseName:           rc.Name,
		CaseFile:           casePath,
		ProjectName:        projectName,
		TraceID:            traceID,
		FinalEvent:         finalEvent,
		EventCount:         len(observedEvents),
		ObservedEvents:     observedEvents,
		ObservedTaskChain:  observedTaskChain,
		ExpectedFinalEvent: rc.Validation.ExpectedFinalEvent,
		RequiredEvents:     rc.Validation.RequiredEvents,
		RequiredTaskChain:  rc.Validation.RequiredTaskChain,
		Status:             "passed",
	}, nil
}

func collectRegressionEvents(parentCtx context.Context, traceID string, timeoutSeconds int) ([]string, error) {
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	if timeoutSeconds <= 0 {
		timeoutSeconds = 600
	}
	ctx, cancel := context.WithTimeout(parentCtx, time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	reader := client.NewSSEReader(flagServer, fmt.Sprintf("/api/v2/traces/%s/stream", traceID), flagToken)
	events, errs := reader.Stream(ctx)

	var observed []string
	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("timed out waiting for trace %s terminal event after %ds", traceID, timeoutSeconds)
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("trace %s stream error: %w", traceID, err)
			}
		case evt, ok := <-events:
			if !ok {
				return nil, fmt.Errorf("trace %s stream closed before terminal event", traceID)
			}
			parsed := parseTraceSSEEvent(evt)
			if parsed.SSEEvent == "end" {
				return observed, nil
			}
			if parsed.EventName != "" {
				observed = append(observed, parsed.EventName)
			}
		}
	}
}

func fetchTraceData(c *client.Client, traceID string) (map[string]any, error) {
	var resp client.APIResponse[map[string]any]
	if err := c.Get(fmt.Sprintf("/api/v2/traces/%s", traceID), &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

func extractTaskChain(traceData map[string]any) []string {
	tasksRaw, ok := traceData["tasks"]
	if !ok || tasksRaw == nil {
		return nil
	}
	payload, err := json.Marshal(tasksRaw)
	if err != nil {
		return nil
	}
	var tasks []map[string]any
	if err := json.Unmarshal(payload, &tasks); err != nil {
		return nil
	}
	chain := make([]string, 0, len(tasks))
	for _, task := range tasks {
		typ := strings.TrimSpace(stringField(task, "type"))
		if typ == "" {
			continue
		}
		chain = append(chain, typ)
	}
	return chain
}

func validateRegressionOutcome(v regressionValidation, observedEvents, observedTaskChain []string) error {
	if v.MinEvents > 0 && len(observedEvents) < v.MinEvents {
		return fmt.Errorf("expected at least %d events, got %d", v.MinEvents, len(observedEvents))
	}
	if len(observedEvents) == 0 {
		return fmt.Errorf("observed no trace events")
	}
	finalEvent := observedEvents[len(observedEvents)-1]
	if finalEvent != v.ExpectedFinalEvent {
		return fmt.Errorf("expected final event %q, got %q", v.ExpectedFinalEvent, finalEvent)
	}
	if err := requireOrderedSubsequence("required events", observedEvents, v.RequiredEvents); err != nil {
		return err
	}
	if err := requireOrderedSubsequence("required task chain", observedTaskChain, v.RequiredTaskChain); err != nil {
		return err
	}
	return nil
}

func requireOrderedSubsequence(label string, observed, required []string) error {
	if len(required) == 0 {
		return nil
	}
	if len(observed) == 0 {
		return fmt.Errorf("%s: observed sequence is empty", label)
	}
	idx := 0
	for _, item := range observed {
		if item == required[idx] {
			idx++
			if idx == len(required) {
				return nil
			}
		}
	}
	missing := required[idx:]
	return fmt.Errorf("%s: missing ordered subsequence %q in observed sequence %q", label, strings.Join(missing, " -> "), strings.Join(observed, " -> "))
}

func init() {
	regressionRunCmd.Flags().StringVar(&regressionCasesDir, "cases-dir", "regression", "Directory containing repo-tracked regression case YAML files")
	regressionRunCmd.Flags().StringVar(&regressionCaseFile, "file", "", "Path to a regression case YAML file")

	regressionCmd.AddCommand(regressionRunCmd)
}
