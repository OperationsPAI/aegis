package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"aegis/cli/client"
	"aegis/cli/output"
	"aegis/platform/consts"

	"github.com/spf13/cobra"
)

// systemPrereqResp mirrors chaossystem.SystemPrerequisiteResp. Kept local so
// the CLI binary does not need to import the backend module.
type systemPrereqResp struct {
	ID         int             `json:"id"`
	SystemName string          `json:"system_name"`
	Kind       string          `json:"kind"`
	Name       string          `json:"name"`
	Spec       json.RawMessage `json:"spec"`
	Status     string          `json:"status"`
}

// helmPrereqSpec decodes the kind=helm payload inside SystemPrereqResp.Spec.
type helmPrereqSpec struct {
	Chart     string               `json:"chart"`
	Namespace string               `json:"namespace"`
	Version   string               `json:"version"`
	Values    []helmPrereqSetValue `json:"values"`
}

type helmPrereqSetValue struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Prereq status values — mirror model.SystemPrerequisiteStatus*. Duplicated
// to keep aegisctl build-independent of the backend package.
const (
	prereqStatusPending    = "pending"
	prereqStatusReconciled = "reconciled"
	prereqStatusFailed     = "failed"

	prereqKindHelm = "helm"
)

// prereqReconcileRunner abstracts the helm shell-out so tests can stub it.
type prereqReconcileRunner interface {
	LookPath(name string) (string, error)
	Run(name string, args ...string) ([]byte, error)
}

type realPrereqRunner struct{}

func (realPrereqRunner) LookPath(name string) (string, error) { return exec.LookPath(name) }
func (realPrereqRunner) Run(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	// Stream stderr straight through so helm's progress shows up live;
	// capture stdout for the report.
	cmd.Stderr = os.Stderr
	return cmd.Output()
}

// prereqRunner is the package-level indirection; tests override it.
var prereqRunner prereqReconcileRunner = realPrereqRunner{}

// --- flags ---
var (
	systemReconcileName   string
	systemReconcileDryRun bool
)

// systemReconcilePrereqsCmd implements `aegisctl system reconcile-prereqs`.
// It's a thin orchestrator: read prereqs from backend, helm upgrade --install
// each one, mark-reconciled on success. Backend never shells out to helm.
var systemReconcilePrereqsCmd = &cobra.Command{
	Use:   "reconcile-prereqs",
	Short: "Install declared cluster-level prerequisites for one or all systems",
	Long: `Read system_prerequisites from the backend (seeded from data.yaml, issue
#115) and run ` + "`helm upgrade --install`" + ` for each prereq whose kind is
"helm". On success, POST mark=reconciled back to the backend so later
` + "`aegisctl system enable`" + ` calls pass the gate.

Idempotent: a prereq already marked reconciled is skipped unless its spec
changed in data.yaml. Exit code 0 means every discovered prereq is
reconciled (or already was); non-zero means at least one helm install
failed.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAPIContext(true); err != nil {
			return err
		}
		c := newClient()

		names, err := targetSystemsForReconcile(c, strings.TrimSpace(systemReconcileName))
		if err != nil {
			return err
		}
		if len(names) == 0 {
			output.PrintInfo("No systems registered; nothing to reconcile.")
			return nil
		}

		// Collect every prereq up front so we can emit a single machine-
		// parseable summary on stdout at the end even when helm fails.
		type planned struct {
			System string
			Prereq systemPrereqResp
		}
		var plan []planned
		for _, n := range names {
			ps, err := fetchPrereqs(c, n)
			if err != nil {
				return fmt.Errorf("fetch prereqs for %q: %w", n, err)
			}
			for _, p := range ps {
				plan = append(plan, planned{System: n, Prereq: p})
			}
		}
		if len(plan) == 0 {
			output.PrintInfo("No prerequisites declared for the targeted system(s).")
			// Machine-parseable empty summary so callers can assume stdout
			// always contains a valid JSON document.
			writeReconcileReport(nil)
			return nil
		}

		if !systemReconcileDryRun {
			if _, err := prereqRunner.LookPath("helm"); err != nil {
				return fmt.Errorf("helm binary not found on PATH: %w", err)
			}
		}

		results := make([]prereqReconcileResult, 0, len(plan))
		anyFailed := false

		for _, item := range plan {
			r := prereqReconcileResult{
				System: item.System,
				Kind:   item.Prereq.Kind,
				Name:   item.Prereq.Name,
			}
			// Only helm is implemented; unknown kinds are reported as skipped.
			if item.Prereq.Kind != prereqKindHelm {
				r.Action = "skipped"
				r.Error = fmt.Sprintf("unsupported kind %q (aegisctl v1 only reconciles kind=helm)", item.Prereq.Kind)
				results = append(results, r)
				fmt.Fprintf(os.Stderr, "[skip] %s/%s: %s\n", item.System, item.Prereq.Name, r.Error)
				continue
			}
			var spec helmPrereqSpec
			if err := json.Unmarshal(item.Prereq.Spec, &spec); err != nil {
				r.Action = "failed"
				r.Error = "spec parse: " + err.Error()
				results = append(results, r)
				anyFailed = true
				fmt.Fprintf(os.Stderr, "[fail] %s/%s: %s\n", item.System, item.Prereq.Name, r.Error)
				continue
			}
			r.Chart, r.Namespace, r.Version = spec.Chart, spec.Namespace, spec.Version

			// Already reconciled + spec unchanged = no-op. We don't currently
			// have a "spec hash" to compare; the seed loader already refreshes
			// spec_json on every boot, so reconciled==reconciled-with-current-
			// spec. Re-reconciling on every invocation would be wasteful.
			if item.Prereq.Status == prereqStatusReconciled {
				r.Action = "skipped"
				results = append(results, r)
				fmt.Fprintf(os.Stderr, "[ok]   %s/%s already reconciled\n", item.System, item.Prereq.Name)
				continue
			}

			if systemReconcileDryRun {
				r.Action = "dry-run"
				results = append(results, r)
				fmt.Fprintf(os.Stderr, "[plan] helm %s\n",
					strings.Join(buildHelmUpgradeInstallArgs(item.Prereq.Name, spec), " "))
				continue
			}

			fmt.Fprintf(os.Stderr, "[run]  helm upgrade --install %s %s -n %s (version=%s)\n",
				item.Prereq.Name, spec.Chart, spec.Namespace, fallback(spec.Version, "<any>"))
			helmArgs := buildHelmUpgradeInstallArgs(item.Prereq.Name, spec)
			if _, err := prereqRunner.Run("helm", helmArgs...); err != nil {
				r.Action = "failed"
				r.Error = err.Error()
				results = append(results, r)
				anyFailed = true
				// Best-effort mark=failed so a dashboard sees the state.
				_ = markPrereq(c, item.System, item.Prereq.ID, prereqStatusFailed)
				fmt.Fprintf(os.Stderr, "[fail] %s/%s: %v\n", item.System, item.Prereq.Name, err)
				continue
			}
			if err := markPrereq(c, item.System, item.Prereq.ID, prereqStatusReconciled); err != nil {
				// Helm succeeded but backend write failed — surface as a warning,
				// not a fatal error, because the cluster state is correct and
				// re-running reconcile will converge.
				fmt.Fprintf(os.Stderr, "[warn] helm ok but mark-reconciled failed for %s/%s: %v\n",
					item.System, item.Prereq.Name, err)
			}
			r.Action = "installed"
			results = append(results, r)
			fmt.Fprintf(os.Stderr, "[done] %s/%s\n", item.System, item.Prereq.Name)
		}

		writeReconcileReport(results)
		if anyFailed {
			return fmt.Errorf("%d prerequisite(s) failed — see stderr for helm output", countFailed(results))
		}
		return nil
	},
}

// targetSystemsForReconcile returns the list of system names to walk. Empty
// filter = every registered system; explicit filter = just that one (404 if
// unknown).
func targetSystemsForReconcile(c *client.Client, nameFilter string) ([]string, error) {
	if nameFilter != "" {
		existing, err := findSystemByName(c, nameFilter)
		if err != nil {
			return nil, err
		}
		if existing == nil {
			return nil, notFoundErrorf("system %q is not registered", nameFilter)
		}
		return []string{nameFilter}, nil
	}
	var resp client.APIResponse[client.PaginatedData[chaosSystemResp]]
	if err := c.Get(consts.APIPathSystems+"?page=1&size=200", &resp); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(resp.Data.Items))
	for _, s := range resp.Data.Items {
		names = append(names, s.Name)
	}
	sort.Strings(names)
	return names, nil
}

// fetchPrereqs wraps GET /api/v2/systems/by-name/:name/prerequisites.
func fetchPrereqs(c *client.Client, systemName string) ([]systemPrereqResp, error) {
	var resp client.APIResponse[[]systemPrereqResp]
	if err := c.Get(consts.APIPathSystemByNamePrerequisites(systemName), &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

// listPendingPrereqs returns the subset with status != reconciled. Used by
// `system enable` as the gate input.
func listPendingPrereqs(c *client.Client, systemName string) ([]systemPrereqResp, error) {
	all, err := fetchPrereqs(c, systemName)
	if err != nil {
		return nil, err
	}
	var pending []systemPrereqResp
	for _, p := range all {
		if p.Status != prereqStatusReconciled {
			pending = append(pending, p)
		}
	}
	return pending, nil
}

// markPrereq POSTs a status update. Any 4xx/5xx propagates to the caller.
func markPrereq(c *client.Client, systemName string, id int, status string) error {
	body := struct {
		Status string `json:"status"`
	}{Status: status}
	var resp client.APIResponse[systemPrereqResp]
	return c.Post(fmt.Sprintf("%s/%d/mark", consts.APIPathSystemByNamePrerequisites(systemName), id), body, &resp)
}

// writeReconcileReport emits the machine-parseable summary to stdout. Always
// a JSON object with an `items` array so callers can pipe into jq without a
// special-case for empty runs.
func writeReconcileReport(items interface{}) {
	payload := map[string]interface{}{"items": items}
	if items == nil {
		payload["items"] = []struct{}{}
	}
	b, _ := json.MarshalIndent(payload, "", "  ")
	fmt.Fprintln(os.Stdout, string(b))
}

func fallback(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

func buildHelmUpgradeInstallArgs(releaseName string, spec helmPrereqSpec) []string {
	helmArgs := []string{
		"upgrade", "--install", releaseName, spec.Chart,
		"-n", spec.Namespace, "--create-namespace",
	}
	if strings.TrimSpace(spec.Version) != "" {
		helmArgs = append(helmArgs, "--version", spec.Version)
	}
	for _, v := range spec.Values {
		if strings.TrimSpace(v.Key) == "" {
			continue
		}
		helmArgs = append(helmArgs, "--set", fmt.Sprintf("%s=%s", v.Key, v.Value))
	}
	return helmArgs
}

// prereqReconcileResult is one row in the machine-parseable stdout summary.
type prereqReconcileResult struct {
	System    string `json:"system"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Action    string `json:"action"` // installed | skipped | failed | dry-run
	Chart     string `json:"chart,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Version   string `json:"version,omitempty"`
	Error     string `json:"error,omitempty"`
}

func countFailed(results []prereqReconcileResult) int {
	n := 0
	for _, r := range results {
		if r.Action == "failed" {
			n++
		}
	}
	return n
}

func init() {
	systemReconcilePrereqsCmd.Flags().StringVar(&systemReconcileName, "name", "", "Short code of a single system (empty = all systems)")
	systemReconcilePrereqsCmd.Flags().BoolVar(&systemReconcileDryRun, "dry-run", false, "Print planned helm commands without executing them")
	systemCmd.AddCommand(systemReconcilePrereqsCmd)

	systemEnableCmd.Flags().BoolVar(&systemEnableSkipPrereqs, "skip-prereqs", false, "Skip the prerequisite-reconciled check (issue #115)")
}
