package cmd

import (
	"fmt"

	"aegis/cli/apiclient"
	"aegis/cli/output"

	"github.com/spf13/cobra"
)

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

		cli, ctx := newAPIClient()
		resp, _, err := cli.SystemsAPI.ListSystemInjectCandidates(ctx, candidatesSystem).
			Namespace(candidatesNamespace).
			Execute()
		if err != nil {
			return err
		}
		var candidates []apiclient.ChaossystemInjectCandidateResp
		var count int32
		if resp.Data != nil {
			candidates = resp.Data.GetCandidates()
			count = resp.Data.GetCount()
		}

		// JSON output is the contract our tools/enumerate.sh replacement
		// consumes — we emit the candidates slice directly (not the
		// envelope) so the shape stays flat: [{system, namespace, app,
		// chaos_type, params}, ...].
		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(candidates)
			return nil
		}

		headers := []string{"APP", "CHAOS-TYPE", "TARGET"}
		rows := make([][]string, 0, len(candidates))
		for _, c := range candidates {
			rows = append(rows, []string{c.GetApp(), c.GetChaosType(), formatCandidateTarget(c)})
		}
		output.PrintTable(headers, rows)
		output.PrintInfo(fmt.Sprintf("Total: %d candidates", count))
		return nil
	},
}

// formatCandidateTarget collapses the per-leaf identity fields into a single
// human-readable cell. Order matches the natural target dimension for each
// chaos family (container > http endpoint > network target > dns > jvm
// method > db op > mutator config). At most one of these is non-empty for
// any single candidate.
func formatCandidateTarget(c apiclient.ChaossystemInjectCandidateResp) string {
	container := c.GetContainer()
	route := c.GetRoute()
	httpMethod := c.GetHttpMethod()
	target := c.GetTargetService()
	domain := c.GetDomain()
	class := c.GetClass()
	method := c.GetMethod()
	mutator := c.GetMutatorConfig()
	database := c.GetDatabase()
	switch {
	case container != "":
		return "container=" + container
	case route != "" || httpMethod != "":
		return httpMethod + " " + route
	case target != "":
		return "->" + target
	case domain != "":
		return "domain=" + domain
	case mutator != "":
		return class + "#" + method + " [" + mutator + "]"
	case class != "" || method != "":
		return class + "#" + method
	case database != "":
		return database + "/" + c.GetTable() + "/" + c.GetOperation()
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
