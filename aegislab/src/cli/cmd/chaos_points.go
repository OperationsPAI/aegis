package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"aegis/cli/apiclient"
	"aegis/cli/output"

	"github.com/spf13/cobra"
	sigsyaml "sigs.k8s.io/yaml"
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
		shown := int32(len(p.Points))
		fmt.Fprintf(os.Stderr, "total=%d shown=%d limit=%d offset=%d\n",
			total, shown, int32Deref(p.Limit), int32Deref(p.Offset))
		if total > shown {
			fmt.Fprintf(os.Stderr, "⚠ %d points not shown (total %d > page %d). Use --capability/--service to filter, or --limit 500 to see more.\n",
				total-shown, total, shown)
		}
	}
	return nil
}

func int32Deref(p *int32) int32 {
	if p == nil {
		return 0
	}
	return *p
}

var (
	chaosPointsExportSystem            string
	chaosPointsExportIncludeSuperseded bool
)

var chaosPointsExportCmd = &cobra.Command{
	Use:   "export <out-dir>",
	Short: "Dump chaos_points back into PointManifest YAMLs (GET /v1beta/systems/{sys}/points/export)",
	Long: `Write one YAML per (system, service) under <out-dir>/<system>/<service>-export.yaml.

The result is round-trip-safe: re-feeding each file through
` + "`aegisctl manifest import`" + ` reproduces the same PointID set in MySQL.
Only active rows by default; pass --include-superseded to also dump rows
that an earlier import marked obsolete.`,
	Args: cobra.ExactArgs(1),
	RunE: runChaosPointsExport,
}

func runChaosPointsExport(_ *cobra.Command, args []string) error {
	outDir := args[0]
	if chaosPointsExportSystem == "" {
		return usageErrorf("--system is required")
	}

	cli, ctx, err := newChaosAPIClient()
	if err != nil {
		return err
	}
	req := cli.ChaosAPI.ChaosExportSystemPoints(ctx, chaosPointsExportSystem)
	if chaosPointsExportIncludeSuperseded {
		req = req.IncludeSuperseded(true)
	}
	resp, _, err := req.Execute()
	if err != nil {
		return err
	}
	if resp == nil || resp.Data == nil {
		return fmt.Errorf("export: aegis-chaos returned an empty envelope")
	}

	if err := os.MkdirAll(filepath.Join(outDir, chaosPointsExportSystem), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", outDir, err)
	}

	written := 0
	for _, m := range resp.Data.Manifests {
		var svc string
		if m.Metadata != nil {
			svc = m.Metadata.GetService()
		}
		if svc == "" {
			return fmt.Errorf("export response carries a manifest with empty metadata.service")
		}
		raw, err := json.Marshal(m)
		if err != nil {
			return fmt.Errorf("marshal %s: %w", svc, err)
		}
		yamlBytes, err := sigsyaml.JSONToYAML(raw)
		if err != nil {
			return fmt.Errorf("yaml %s: %w", svc, err)
		}
		filePath := filepath.Join(outDir, chaosPointsExportSystem, svc+"-export.yaml")
		if err := os.WriteFile(filePath, yamlBytes, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", filePath, err)
		}
		written++
	}
	if !flagQuiet {
		fmt.Fprintf(os.Stderr, "exported %d manifest(s) to %s/%s\n",
			written, outDir, chaosPointsExportSystem)
	}
	return nil
}

func init() {
	chaosPointsListCmd.Flags().StringVar(&chaosPointsSystem, "system", "", "System name (required)")
	chaosPointsListCmd.Flags().StringVar(&chaosPointsService, "service", "", "Filter by service name")
	chaosPointsListCmd.Flags().StringVar(&chaosPointsCapability, "capability", "", "Filter by capability name")
	chaosPointsListCmd.Flags().StringVar(&chaosPointsStatus, "status", "", "Filter by point status (active / superseded / deprecated)")
	chaosPointsListCmd.Flags().Int32Var(&chaosPointsLimit, "limit", 0, "Page size (default 100 server-side, max 500)")
	chaosPointsListCmd.Flags().Int32Var(&chaosPointsOffset, "offset", 0, "Page offset")

	chaosPointsExportCmd.Flags().StringVar(&chaosPointsExportSystem, "system", "", "System name (required)")
	chaosPointsExportCmd.Flags().BoolVar(&chaosPointsExportIncludeSuperseded, "include-superseded", false,
		"Also dump rows with status='superseded' (default: only 'active')")

	chaosPointsCmd.AddCommand(chaosPointsListCmd)
	chaosPointsCmd.AddCommand(chaosPointsExportCmd)
	chaosCmd.AddCommand(chaosPointsCmd)
}
