package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"aegis/cli/apiclient"
	"aegis/cli/output"

	"github.com/spf13/cobra"
)

var chaosCapabilityCmd = &cobra.Command{
	Use:   "capability",
	Short: "List / inspect the aegis-chaos Capability catalog",
}

var chaosCapabilityListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all chaos Capabilities (GET /v1beta/capabilities)",
	Args:  requireNoArgs,
	RunE:  runChaosCapabilityList,
}

var chaosCapabilityGetCmd = &cobra.Command{
	Use:   "get <name>",
	Short: "Fetch one Capability by name (GET /v1beta/capabilities/{name})",
	Args:  cobra.ExactArgs(1),
	RunE:  runChaosCapabilityGet,
}

func runChaosCapabilityList(_ *cobra.Command, _ []string) error {
	cli, ctx, err := newChaosAPIClient()
	if err != nil {
		return err
	}
	resp, _, err := cli.ChaosAPI.ChaosListCapabilities(ctx).Execute()
	if err != nil {
		return err
	}
	caps := resp.Data
	if output.OutputFormat(flagOutput) == output.FormatJSON {
		output.PrintJSON(caps)
		return nil
	}
	headers := []string{"NAME", "STATUS"}
	rows := make([][]string, 0, len(caps))
	for _, c := range caps {
		rows = append(rows, []string{strDeref(c.Name), strDeref(c.Status)})
	}
	output.PrintTable(headers, rows)
	if !flagQuiet {
		fmt.Fprintf(os.Stderr, "\n%d capabilities\n", len(caps))
	}
	return nil
}

func runChaosCapabilityGet(_ *cobra.Command, args []string) error {
	cli, ctx, err := newChaosAPIClient()
	if err != nil {
		return err
	}
	resp, _, err := cli.ChaosAPI.ChaosGetCapability(ctx, args[0]).Execute()
	if err != nil {
		return err
	}
	return renderChaosCapability(resp.Data)
}

func renderChaosCapability(c *apiclient.ChaosChaosCapabilityResp) error {
	if c == nil {
		return fmt.Errorf("server returned an empty data envelope")
	}
	if output.OutputFormat(flagOutput) == output.FormatJSON {
		output.PrintJSON(c)
		return nil
	}
	headers := []string{"FIELD", "VALUE"}
	rows := [][]string{
		{"name", strDeref(c.Name)},
		{"status", strDeref(c.Status)},
		{"target_schema", compactJSON(c.TargetSchema)},
		{"param_schema", compactJSON(c.ParamSchema)},
		{"observable_contract", compactJSON(c.ObservableContract)},
		{"created_at", strDeref(c.CreatedAt)},
	}
	output.PrintTable(headers, rows)
	return nil
}

func compactJSON(m map[string]any) string {
	if len(m) == 0 {
		return "{}"
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func init() {
	chaosCapabilityCmd.AddCommand(chaosCapabilityListCmd)
	chaosCapabilityCmd.AddCommand(chaosCapabilityGetCmd)
	chaosCmd.AddCommand(chaosCapabilityCmd)
}
