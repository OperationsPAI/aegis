package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"aegis/cli/apiclient"
	"aegis/cli/client"
	"aegis/cli/output"
	"aegis/platform/consts"

	"github.com/spf13/cobra"
)

// systemPrereqResp mirrors chaossystem.SystemPrerequisiteResp. The generated
// apiclient.ChaossystemSystemPrerequisiteResp types Spec as []int32 (swag
// annotation bug — TODO: missing swag annotation for json.RawMessage), so we
// keep the manual decode for the listing endpoint.
type systemPrereqResp struct {
	ID         int             `json:"id"`
	SystemName string          `json:"system_name"`
	Kind       string          `json:"kind"`
	Name       string          `json:"name"`
	Spec       json.RawMessage `json:"spec"`
	Status     string          `json:"status"`
}

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
	cmd.Stderr = os.Stderr
	return cmd.Output()
}

var prereqRunner prereqReconcileRunner = realPrereqRunner{}

var (
	systemReconcileName   string
	systemReconcileDryRun bool
)

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

		cli, ctx := newAPIClient()

		for _, item := range plan {
			r := prereqReconcileResult{
				System: item.System,
				Kind:   item.Prereq.Kind,
				Name:   item.Prereq.Name,
			}
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
				_ = markPrereqTyped(cli, ctx, item.System, item.Prereq.ID, prereqStatusFailed)
				fmt.Fprintf(os.Stderr, "[fail] %s/%s: %v\n", item.System, item.Prereq.Name, err)
				continue
			}
			if err := markPrereqTyped(cli, ctx, item.System, item.Prereq.ID, prereqStatusReconciled); err != nil {
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
// unknown). Still on *client.Client because findSystemByName is shared with
// system.go's test surface.
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
	cli, ctx := newAPIClient()
	resp, _, err := cli.SystemsAPI.ListChaosSystems(ctx).Page(1).Size(200).Execute()
	if err != nil {
		return nil, err
	}
	data := resp.GetData()
	names := make([]string, 0, len(data.GetItems()))
	for _, s := range data.GetItems() {
		names = append(names, s.GetName())
	}
	sort.Strings(names)
	return names, nil
}

// fetchPrereqs wraps GET /api/v2/systems/by-name/:name/prerequisites. We bypass
// the typed client because its ChaossystemSystemPrerequisiteResp.Spec is typed
// as []int32 instead of json.RawMessage (TODO: missing swag annotation for
// json.RawMessage on SystemPrerequisiteResp.Spec).
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

// markPrereqTyped POSTs a status update via the generated client.
func markPrereqTyped(cli *apiclient.APIClient, ctx context.Context, systemName string, id int, status string) error {
	body := apiclient.ChaossystemMarkPrerequisiteReq{Status: status}
	_, _, err := cli.SystemsAPI.MarkSystemPrerequisite(ctx, systemName, int32(id)).
		ChaossystemMarkPrerequisiteReq(body).
		Execute()
	return err
}

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
