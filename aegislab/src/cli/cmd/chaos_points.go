package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"aegis/cli/apiclient"
	"aegis/cli/output"

	"github.com/spf13/cobra"
)

var (
	chaosPointsSystem     string
	chaosPointsService    string
	chaosPointsCapability string
	chaosPointsStatus     string
	chaosPointsLimit      int32
	chaosPointsOffset     int32
)

var chaosPointsCmd = &cobra.Command{
	Use:   "points",
	Short: "Inspect aegis-chaos Points",
}

var chaosPointsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List Points under a system (GET /v1beta/systems/{sys}/points)",
	Args:  requireNoArgs,
	RunE:  runChaosPointsList,
}

func runChaosPointsList(_ *cobra.Command, _ []string) error {
	if chaosPointsSystem == "" {
		return usageErrorf("--system is required")
	}
	cli, ctx, err := newChaosAPIClient()
	if err != nil {
		return err
	}
	req := cli.ChaosAPI.ChaosListSystemPoints(ctx, chaosPointsSystem)
	if chaosPointsService != "" {
		req = req.Service(chaosPointsService)
	}
	if chaosPointsCapability != "" {
		req = req.Capability(chaosPointsCapability)
	}
	if chaosPointsStatus != "" {
		req = req.Status(chaosPointsStatus)
	}
	if chaosPointsLimit > 0 {
		req = req.Limit(chaosPointsLimit)
	}
	if chaosPointsOffset > 0 {
		req = req.Offset(chaosPointsOffset)
	}
	resp, _, err := req.Execute()
	if err != nil {
		return err
	}
	return renderChaosPoints(resp.Data)
}

func renderChaosPoints(p *apiclient.ChaosChaosListPointsResp) error {
	if p == nil {
		return fmt.Errorf("server returned an empty data envelope")
	}
	if output.OutputFormat(flagOutput) == output.FormatJSON {
		output.PrintJSON(p)
		return nil
	}
	headers := []string{"ID", "SYSTEM", "SERVICE", "CAPABILITY", "STATUS", "SOURCE", "TARGET"}
	rows := make([][]string, 0, len(p.Points))
	for _, pt := range p.Points {
		targetStr := ""
		if pt.Target != nil {
			if b, err := json.Marshal(pt.Target); err == nil {
				targetStr = string(b)
			}
		}
		rows = append(rows, []string{
			strDeref(pt.Id), strDeref(pt.SystemName), strDeref(pt.ServiceName),
			strDeref(pt.CapabilityName), strDeref(pt.Status), strDeref(pt.Source),
			targetStr,
		})
	}
	output.PrintTable(headers, rows)
	if !flagQuiet {
		total := int32Deref(p.Total)
		fmt.Fprintf(os.Stderr, "total=%d shown=%d limit=%d offset=%d\n",
			total, len(p.Points), int32Deref(p.Limit), int32Deref(p.Offset))
	}
	return nil
}

func int32Deref(p *int32) int32 {
	if p == nil {
		return 0
	}
	return *p
}

func init() {
	chaosPointsListCmd.Flags().StringVar(&chaosPointsSystem, "system", "", "System name (required)")
	chaosPointsListCmd.Flags().StringVar(&chaosPointsService, "service", "", "Filter by service name")
	chaosPointsListCmd.Flags().StringVar(&chaosPointsCapability, "capability", "", "Filter by capability name")
	chaosPointsListCmd.Flags().StringVar(&chaosPointsStatus, "status", "", "Filter by point status (active / superseded / deprecated)")
	chaosPointsListCmd.Flags().Int32Var(&chaosPointsLimit, "limit", 0, "Page size (default 100 server-side, max 500)")
	chaosPointsListCmd.Flags().Int32Var(&chaosPointsOffset, "offset", 0, "Page offset")

	chaosPointsCmd.AddCommand(chaosPointsListCmd)
	chaosCmd.AddCommand(chaosPointsCmd)
}
