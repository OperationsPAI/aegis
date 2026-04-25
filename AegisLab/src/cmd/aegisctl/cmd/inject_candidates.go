package cmd

import (
	"fmt"
	"net/url"

	"aegis/cmd/aegisctl/client"
	"aegis/cmd/aegisctl/output"

	"github.com/spf13/cobra"
)

// injectCandidate mirrors chaossystem.InjectCandidateResp. Kept local so the
// CLI binary doesn't need to import the backend module — same pattern as
// systemPrereqResp in system_prereqs.go.
type injectCandidate struct {
	System        string `json:"system"`
	SystemType    string `json:"system_type,omitempty"`
	Namespace     string `json:"namespace"`
	App           string `json:"app"`
	ChaosType     string `json:"chaos_type"`
	Container     string `json:"container,omitempty"`
	TargetService string `json:"target_service,omitempty"`
	Domain        string `json:"domain,omitempty"`
	Class         string `json:"class,omitempty"`
	Method        string `json:"method,omitempty"`
	MutatorConfig string `json:"mutator_config,omitempty"`
	Route         string `json:"route,omitempty"`
	HTTPMethod    string `json:"http_method,omitempty"`
	Database      string `json:"database,omitempty"`
	Table         string `json:"table,omitempty"`
	Operation     string `json:"operation,omitempty"`
}

type injectCandidatesResp struct {
	Count      int               `json:"count"`
	Candidates []injectCandidate `json:"candidates"`
}

// --- flags ---
var (
	candidatesSystem    string
	candidatesNamespace string
)

// injectCandidatesCmd is the parent for `aegisctl inject candidates ...`.
// Currently exposes only `ls`; future tooling (e.g. `score`, `pick`) plugs
// in as siblings.
var injectCandidatesCmd = &cobra.Command{
	Use:   "candidates",
	Short: "Bulk operations on the inject-candidate pool for a system",
	Long: `Bulk operations on the inject-candidate pool for a system.

Replaces the previous N-round-trip walk through 'aegisctl inject guided' for
adversarial / coverage-driven loops that need the full candidate pool up front.

  aegisctl inject candidates ls --system sockshop --namespace sockshop1
  aegisctl inject candidates ls --system sockshop --namespace sockshop1 --output json | jq .
`,
}

// injectCandidatesLsCmd implements `aegisctl inject candidates ls`.
//
// Default output is a table summary (one row per candidate, aggregated by
// chaos_type + leaf identity). --output json emits the verbatim backend
// payload for piping into Python / shell scripts.
var injectCandidatesLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List every (app, chaos_type, target) candidate for a system+namespace",
	RunE: func(cmd *cobra.Command, args []string) error {
		if candidatesSystem == "" {
			return usageErrorf("--system is required")
		}
		if candidatesNamespace == "" {
			return usageErrorf("--namespace is required")
		}

		c := newClient()
		path := fmt.Sprintf("/api/v2/systems/by-name/%s/inject-candidates?namespace=%s",
			url.PathEscape(candidatesSystem),
			url.QueryEscape(candidatesNamespace),
		)
		var resp client.APIResponse[injectCandidatesResp]
		if err := c.Get(path, &resp); err != nil {
			return err
		}

		// JSON output is the contract our tools/enumerate.sh replacement
		// consumes — we emit the candidates slice directly (not the
		// envelope) so the shape stays flat: [{system, namespace, app,
		// chaos_type, params}, ...].
		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(resp.Data.Candidates)
			return nil
		}

		headers := []string{"APP", "CHAOS-TYPE", "TARGET"}
		rows := make([][]string, 0, len(resp.Data.Candidates))
		for _, c := range resp.Data.Candidates {
			rows = append(rows, []string{c.App, c.ChaosType, formatCandidateTarget(c)})
		}
		output.PrintTable(headers, rows)
		output.PrintInfo(fmt.Sprintf("Total: %d candidates", resp.Data.Count))
		return nil
	},
}

// formatCandidateTarget collapses the per-leaf identity fields into a single
// human-readable cell. Order matches the natural target dimension for each
// chaos family (container > http endpoint > network target > dns > jvm
// method > db op > mutator config). At most one of these is non-empty for
// any single candidate.
func formatCandidateTarget(c injectCandidate) string {
	switch {
	case c.Container != "":
		return "container=" + c.Container
	case c.Route != "" || c.HTTPMethod != "":
		return c.HTTPMethod + " " + c.Route
	case c.TargetService != "":
		return "->" + c.TargetService
	case c.Domain != "":
		return "domain=" + c.Domain
	case c.MutatorConfig != "":
		return c.Class + "#" + c.Method + " [" + c.MutatorConfig + "]"
	case c.Class != "" || c.Method != "":
		return c.Class + "#" + c.Method
	case c.Database != "":
		return c.Database + "/" + c.Table + "/" + c.Operation
	default:
		return ""
	}
}

func init() {
	injectCandidatesLsCmd.Flags().StringVar(&candidatesSystem, "system", "", "System short code (e.g. sockshop, ts)")
	injectCandidatesLsCmd.Flags().StringVar(&candidatesNamespace, "namespace", "", "Target namespace (e.g. sockshop1)")
	_ = injectCandidatesLsCmd.MarkFlagRequired("system")
	_ = injectCandidatesLsCmd.MarkFlagRequired("namespace")

	injectCandidatesCmd.AddCommand(injectCandidatesLsCmd)
	injectCmd.AddCommand(injectCandidatesCmd)
}
