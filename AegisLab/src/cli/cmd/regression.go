package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"aegis/cli/client"
	"aegis/cli/output"
	"aegis/platform/consts"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
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
	regressionCasesDir          string
	regressionCaseFile          string
	regressionSkipPreflight     bool
	regressionAutoInstall       bool
	regressionSkipRestartPedestal bool
	regressionReadyTimeoutSeconds int
	regressionAppLabelKey       string
	regressionPodListerHook     PodLister // test injection seam; nil => real k8s client
	regressionInstallerHook     chartInstaller
	regressionSystemsHook       SystemsFetcher // test injection seam; nil => real HTTP client
)

// SystemsFetcher is the minimal /api/v2/systems surface
// resolveRegressionNamespaces needs. Tests can inject a fake; production uses
// an HTTP-backed implementation.
type SystemsFetcher interface {
	FetchSystem(ctx context.Context, name string) (nsPattern string, count int, err error)
}

// PodLister is the minimal k8s surface preflightRegressionCase needs. Tests
// can inject a fake; production uses a client-go backed implementation.
type PodLister interface {
	ListPods(ctx context.Context, namespace, labelSelector string) (count int, err error)
	// CountReadyPods returns (total, ready) where `ready` is the number of
	// matching pods whose containers all have Ready=true. Used by
	// --auto-install to wait for chart rollout before submitting.
	CountReadyPods(ctx context.Context, namespace, labelSelector string) (total int, ready int, err error)
}

type chartInstaller func(ctx context.Context, system, namespace string) error

// regressionTarget captures one (namespace, app, system) preflight subject
// derived from a regression case's submit.specs entries.
type regressionTarget struct {
	System    string
	Namespace string
	App       string
}

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

		if err := resolveRegressionNamespaces(cmd.Context(), &rc, regressionSystemsHook); err != nil {
			return err
		}

		if !regressionSkipPreflight {
			if err := preflightRegressionCase(cmd.Context(), rc, regressionPodListerHook, regressionInstallerHook); err != nil {
				return err
			}
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
	path := consts.APIPathProjectInjectionsInject(pid)
	// When --skip-restart-pedestal is set, hint the backend to no-op the
	// helm install inside RestartPedestal (preflight already installed +
	// waited for readiness). Backend falls back to a real install if the
	// release is missing or unhealthy, so this is safe either way.
	if regressionSkipRestartPedestal {
		if rc.Submit == nil {
			rc.Submit = map[string]any{}
		}
		rc.Submit["skip_restart_pedestal"] = true
	}
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

	reader := client.NewSSEReader(flagServer, consts.APIPathTraceStream(traceID), flagToken)
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
	if err := c.Get(consts.APIPathTrace(traceID), &resp); err != nil {
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

// resolveRegressionNamespaces rewrites every `spec.namespace` in rc.Submit.specs
// in-place (on the in-memory copy only; the YAML on disk is untouched) so that
// both preflight and the backend submit see a namespace that actually exists on
// the cluster.
//
// For each spec that carries a `system` field:
//   - fetch the system's ns_pattern once (per-run cache), via fetcher.
//   - if spec.namespace is empty, fill with nsPatternToNamespace(pattern, 0).
//   - if spec.namespace matches the pattern regex, leave it alone.
//   - if spec.namespace equals the bare system name (no digit suffix), rewrite
//     it to nsPatternToNamespace(pattern, 0) with a WARN on stderr.
//   - otherwise fail with a clear error pointing at the expected form.
//
// Specs without a `system` field are left untouched (back-compat). When the
// backend is unreachable we emit a warning and fall back to the current
// verbatim behavior so existing --skip-preflight flows aren't regressed.
func resolveRegressionNamespaces(ctx context.Context, rc *regressionCase, fetcher SystemsFetcher) error {
	if rc == nil {
		return nil
	}
	specsRaw, ok := rc.Submit["specs"]
	if !ok {
		return nil
	}
	groups, ok := specsRaw.([]any)
	if !ok {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	type sysInfo struct {
		pattern string
		re      *regexp.Regexp
		derived string
		err     error
	}
	cache := map[string]*sysInfo{}
	backendDown := false

	lookup := func(sys string) *sysInfo {
		if info, ok := cache[sys]; ok {
			return info
		}
		info := &sysInfo{}
		cache[sys] = info
		if backendDown {
			info.err = fmt.Errorf("backend unreachable")
			return info
		}
		if fetcher == nil {
			f, err := newLiveSystemsFetcher()
			if err != nil {
				backendDown = true
				output.PrintInfo(fmt.Sprintf("WARN: cannot build systems fetcher (%v); falling back to verbatim spec.namespace", err))
				info.err = err
				return info
			}
			fetcher = f
		}
		pat, _, err := fetcher.FetchSystem(ctx, sys)
		if err != nil {
			backendDown = true
			output.PrintInfo(fmt.Sprintf("WARN: cannot resolve system %q from /api/v2/systems (%v); falling back to verbatim spec.namespace", sys, err))
			info.err = err
			return info
		}
		info.pattern = pat
		if pat != "" {
			if re, reErr := regexp.Compile(pat); reErr == nil {
				info.re = re
			}
			info.derived = nsPatternToNamespace(pat, 0)
		}
		return info
	}

	for _, g := range groups {
		inner, ok := g.([]any)
		if !ok {
			continue
		}
		for _, s := range inner {
			spec, ok := s.(map[string]any)
			if !ok {
				continue
			}
			sys := strings.TrimSpace(stringField(spec, "system"))
			if sys == "" {
				continue // back-compat: no system => leave namespace alone
			}
			info := lookup(sys)
			if info.err != nil || info.pattern == "" {
				continue // fallback: keep whatever was in YAML
			}
			ns := strings.TrimSpace(stringField(spec, "namespace"))
			switch {
			case ns == "":
				if info.derived == "" {
					return fmt.Errorf("regression: cannot derive namespace for system %q from ns_pattern %q", sys, info.pattern)
				}
				spec["namespace"] = info.derived
			case info.re != nil && info.re.MatchString(ns):
				// User already wrote a valid namespace — trust it.
			case ns == sys:
				if info.derived == "" {
					return fmt.Errorf("regression: cannot derive namespace for system %q from ns_pattern %q", sys, info.pattern)
				}
				output.PrintInfo(fmt.Sprintf("WARN: namespace %q auto-resolved to %q from ns_pattern %q", ns, info.derived, info.pattern))
				spec["namespace"] = info.derived
			default:
				expected := info.derived
				if expected == "" {
					expected = sys + "0"
				}
				return fmt.Errorf("regression: namespace %q does not match system %q ns_pattern %q; expected e.g. %q",
					ns, sys, info.pattern, expected)
			}
		}
	}
	return nil
}

// liveSystemsFetcher is the real /api/v2/systems-backed SystemsFetcher. It
// mirrors deriveNamespaceFromSystem's API call but returns the raw pattern +
// count so callers can cache and run their own pattern logic.
type liveSystemsFetcher struct{ c *client.Client }

func newLiveSystemsFetcher() (*liveSystemsFetcher, error) {
	return &liveSystemsFetcher{c: newClient()}, nil
}

func (l *liveSystemsFetcher) FetchSystem(_ context.Context, name string) (string, int, error) {
	type systemItem struct {
		Name      string `json:"name"`
		NsPattern string `json:"ns_pattern"`
		Count     int    `json:"count"`
	}
	var resp client.APIResponse[client.PaginatedData[systemItem]]
	if err := l.c.Get(consts.APIPathSystems+"?page=1&size=100", &resp); err != nil {
		return "", 0, err
	}
	for _, s := range resp.Data.Items {
		if s.Name == name {
			return s.NsPattern, s.Count, nil
		}
	}
	return "", 0, fmt.Errorf("system %q not found via /api/v2/systems", name)
}

// extractRegressionTargets walks rc.Submit.specs (shape: [[{system, namespace,
// app, ...}, ...], ...]) and returns the unique (namespace, app) triples the
// backend will validate against live pods. Entries missing namespace or app
// are skipped — the backend will surface its own error for those.
func extractRegressionTargets(rc regressionCase) []regressionTarget {
	specsRaw, ok := rc.Submit["specs"]
	if !ok {
		return nil
	}
	groups, ok := specsRaw.([]any)
	if !ok {
		return nil
	}
	pedestalName := ""
	if ped, ok := rc.Submit["pedestal"].(map[string]any); ok {
		pedestalName, _ = ped["name"].(string)
	}
	seen := make(map[string]struct{})
	var out []regressionTarget
	for _, g := range groups {
		inner, ok := g.([]any)
		if !ok {
			continue
		}
		for _, s := range inner {
			spec, ok := s.(map[string]any)
			if !ok {
				continue
			}
			ns := strings.TrimSpace(stringField(spec, "namespace"))
			app := strings.TrimSpace(stringField(spec, "app"))
			if ns == "" || app == "" {
				continue
			}
			sys := strings.TrimSpace(stringField(spec, "system"))
			if sys == "" {
				sys = pedestalName
			}
			key := ns + "\x00" + app
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, regressionTarget{System: sys, Namespace: ns, App: app})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].App < out[j].App
	})
	return out
}

// preflightRegressionCase verifies, for each unique (namespace, app) pair in
// the regression submit payload, that at least one pod matches
// `<appLabelKey>=<app>`. When a target has zero pods the check fails fast with
// an actionable "fix:" hint pointing at `aegisctl pedestal chart install`.
// When --auto-install is set, it attempts the install in-process.
//
// Error format (grep-friendly):
//
//	preflight: namespace <ns> has no pods matching <labelKey>=<app>
//	  fix: aegisctl pedestal chart install <system> --namespace <ns>
func preflightRegressionCase(ctx context.Context, rc regressionCase, lister PodLister, installer chartInstaller) error {
	targets := extractRegressionTargets(rc)
	if len(targets) == 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if lister == nil {
		l, err := newLivePodLister()
		if err != nil {
			return fmt.Errorf("preflight: build k8s client: %w (use --skip-preflight to bypass)", err)
		}
		lister = l
	}
	labelKey := strings.TrimSpace(regressionAppLabelKey)
	if labelKey == "" {
		labelKey = "app"
	}

	var misses []regressionTarget
	for _, t := range targets {
		selector := labelKey + "=" + t.App
		n, err := lister.ListPods(ctx, t.Namespace, selector)
		if err != nil {
			return fmt.Errorf("preflight: list pods in ns=%s selector=%s: %w (use --skip-preflight to bypass)", t.Namespace, selector, err)
		}
		if n == 0 {
			misses = append(misses, t)
		}
	}
	if len(misses) == 0 {
		return nil
	}

	var b strings.Builder
	for _, m := range misses {
		sys := m.System
		if sys == "" {
			sys = "<system>"
		}
		fmt.Fprintf(&b, "preflight: namespace %s has no pods matching %s=%s\n", m.Namespace, labelKey, m.App)
		fmt.Fprintf(&b, "  fix: aegisctl pedestal chart install %s --namespace %s\n", sys, m.Namespace)
	}

	if regressionAutoInstall {
		if installer == nil {
			installer = defaultChartInstaller
		}
		fmt.Fprint(os.Stderr, b.String())
		fmt.Fprintln(os.Stderr, "preflight: --auto-install set; attempting chart install for each miss")
		installed := make(map[string]struct{})
		for _, m := range misses {
			sys := m.System
			if sys == "" {
				return fmt.Errorf("preflight: cannot auto-install for namespace %s (no system resolvable from spec or pedestal.name)", m.Namespace)
			}
			key := sys + "\x00" + m.Namespace
			if _, dup := installed[key]; dup {
				continue
			}
			installed[key] = struct{}{}
			if err := installer(ctx, sys, m.Namespace); err != nil {
				return fmt.Errorf("preflight: auto-install failed for system=%s namespace=%s: %w", sys, m.Namespace, err)
			}
		}
		// Wait for the newly-installed charts to reach Ready before returning.
		// Without this the caller submits immediately and the backend's
		// RestartPedestal step re-uninstalls/reinstalls before pods stabilize,
		// erasing the install work done here. Poll each missed target for
		// --ready-timeout seconds (default 600) and fail fast with context.
		timeoutSec := regressionReadyTimeoutSeconds
		if timeoutSec <= 0 {
			timeoutSec = 600
		}
		deadline := time.Now().Add(time.Duration(timeoutSec) * time.Second)
		for _, m := range misses {
			selector := labelKey + "=" + m.App
			for {
				total, ready, err := lister.CountReadyPods(ctx, m.Namespace, selector)
				if err != nil {
					return fmt.Errorf("preflight: wait-for-ready ns=%s selector=%s: %w", m.Namespace, selector, err)
				}
				if total > 0 && ready == total {
					fmt.Fprintf(os.Stderr, "preflight: ready ns=%s %s (%d/%d)\n", m.Namespace, selector, ready, total)
					break
				}
				if time.Now().After(deadline) {
					return fmt.Errorf("preflight: timed out after %ds waiting for pods ns=%s selector=%s (ready %d/%d); bump --ready-timeout or inspect with `kubectl -n %s get pods -l %s`",
						timeoutSec, m.Namespace, selector, ready, total, m.Namespace, selector)
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(5 * time.Second):
				}
			}
		}
		return nil
	}

	return fmt.Errorf("%spreflight failed: %d missing chart(s); rerun with --auto-install or run the printed fix command, or pass --skip-preflight to bypass", b.String(), len(misses))
}

// defaultChartInstaller shells out to the current aegisctl binary via
// os.Args[0] so this module does not depend on the pedestal chart install
// implementation (landed in parallel).
func defaultChartInstaller(ctx context.Context, system, namespace string) error {
	bin := os.Args[0]
	if bin == "" {
		bin = "aegisctl"
	}
	cmd := exec.CommandContext(ctx, bin, "pedestal", "chart", "install", system, "--namespace", namespace)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// livePodLister is the real k8s-backed PodLister built from the usual
// in-cluster-or-kubeconfig resolution path.
type livePodLister struct{ cs *kubernetes.Clientset }

func newLivePodLister() (*livePodLister, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		home, _ := os.UserHomeDir()
		path := filepath.Join(home, ".kube", "config")
		cfg, err = clientcmd.BuildConfigFromFlags("", path)
		if err != nil {
			return nil, err
		}
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &livePodLister{cs: cs}, nil
}

func (l *livePodLister) ListPods(ctx context.Context, namespace, labelSelector string) (int, error) {
	pods, err := l.cs.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
	if err != nil {
		return 0, err
	}
	return len(pods.Items), nil
}

func (l *livePodLister) CountReadyPods(ctx context.Context, namespace, labelSelector string) (int, int, error) {
	pods, err := l.cs.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
	if err != nil {
		return 0, 0, err
	}
	ready := 0
	for i := range pods.Items {
		p := &pods.Items[i]
		allReady := len(p.Status.ContainerStatuses) > 0
		for _, cs := range p.Status.ContainerStatuses {
			if !cs.Ready {
				allReady = false
				break
			}
		}
		if allReady {
			ready++
		}
	}
	return len(pods.Items), ready, nil
}

func init() {
	regressionRunCmd.Flags().StringVar(&regressionCasesDir, "cases-dir", "regression", "Directory containing repo-tracked regression case YAML files")
	regressionRunCmd.Flags().StringVar(&regressionCaseFile, "file", "", "Path to a regression case YAML file")
	regressionRunCmd.Flags().BoolVar(&regressionSkipPreflight, "skip-preflight", false, "Skip the pod/chart-installed preflight check (use when the CLI host cannot reach the target k8s cluster)")
	regressionRunCmd.Flags().BoolVar(&regressionAutoInstall, "auto-install", false, "If preflight finds missing charts, shell out to `aegisctl pedestal chart install <system> --namespace <ns>` to fix them before submit")
	regressionRunCmd.Flags().BoolVar(&regressionSkipRestartPedestal, "skip-restart-pedestal", false, "Hint the backend to skip the RestartPedestal helm install when the chart is already deployed (useful after --auto-install + wait-for-ready)")
	regressionRunCmd.Flags().IntVar(&regressionReadyTimeoutSeconds, "ready-timeout", 600, "Seconds --auto-install waits for all preflight targets to become Ready before submit")
	regressionRunCmd.Flags().StringVar(&regressionAppLabelKey, "app-label-key", "app", "Label key used to match pods against each spec's `app` value during preflight")

	regressionCmd.AddCommand(regressionRunCmd)
}
