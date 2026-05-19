package cmd

import (
	"fmt"
	"os"

	"aegis/cli/apiclient"
	"aegis/cli/output"

	"github.com/spf13/cobra"
)

var (
	chaosSysName        string
	chaosSysNsPattern   string
	chaosSysAppLabelKey string
	chaosSysEnabled     bool
	chaosSysMaxConc     int
)

var chaosSystemCmd = &cobra.Command{
	Use:   "system",
	Short: "Manage aegis-chaos System bindings",
}

var chaosSystemRegisterCmd = &cobra.Command{
	Use:   "register",
	Short: "Register or update a chaos System binding (PUT /v1beta/systems/{sys})",
	Args:  requireNoArgs,
	RunE:  runChaosSystemRegister,
}

var chaosSystemGetCmd = &cobra.Command{
	Use:   "get <name>",
	Short: "Fetch a chaos System by name (GET /v1beta/systems/{sys})",
	Args:  cobra.ExactArgs(1),
	RunE:  runChaosSystemGet,
}

func runChaosSystemRegister(_ *cobra.Command, _ []string) error {
	if chaosSysName == "" {
		return usageErrorf("--name is required")
	}
	if chaosSysNsPattern == "" {
		return usageErrorf("--namespace (ns_pattern) is required")
	}
	if chaosSysAppLabelKey == "" {
		return usageErrorf("--app-label-key is required")
	}
	cli, ctx, err := newChaosAPIClient()
	if err != nil {
		return err
	}
	enabled := chaosSysEnabled
	body := *apiclient.NewChaosChaosSystemUpsertReq()
	body.NsPattern = &chaosSysNsPattern
	body.AppLabelKey = &chaosSysAppLabelKey
	body.Enabled = &enabled
	if chaosSysMaxConc > 0 {
		mc := int32(chaosSysMaxConc)
		body.MaxConcurrentInjections = &mc
	}
	resp, _, err := cli.ChaosAPI.ChaosPutSystem(ctx, chaosSysName).
		ChaosChaosSystemUpsertReq(body).Execute()
	if err != nil {
		return err
	}
	return renderChaosSystem(resp.Data)
}

func runChaosSystemGet(_ *cobra.Command, args []string) error {
	cli, ctx, err := newChaosAPIClient()
	if err != nil {
		return err
	}
	resp, _, err := cli.ChaosAPI.ChaosGetSystem(ctx, args[0]).Execute()
	if err != nil {
		return err
	}
	return renderChaosSystem(resp.Data)
}

func renderChaosSystem(s *apiclient.ChaosChaosSystemResp) error {
	if s == nil {
		return fmt.Errorf("server returned an empty data envelope")
	}
	if output.OutputFormat(flagOutput) == output.FormatJSON {
		output.PrintJSON(s)
		return nil
	}
	headers := []string{"NAME", "NS_PATTERN", "APP_LABEL_KEY", "ENABLED", "MAX_CONCURRENT"}
	row := []string{
		strDeref(s.Name), strDeref(s.NsPattern), strDeref(s.AppLabelKey),
		boolDerefStr(s.Enabled), intDerefStr(s.MaxConcurrentInjections),
	}
	output.PrintTable(headers, [][]string{row})
	if !flagQuiet {
		fmt.Fprintf(os.Stderr, "created_at=%s updated_at=%s\n",
			strDeref(s.CreatedAt), strDeref(s.UpdatedAt))
	}
	return nil
}

func strDeref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func boolDerefStr(p *bool) string {
	if p == nil {
		return ""
	}
	if *p {
		return "true"
	}
	return "false"
}

func intDerefStr(p *int32) string {
	if p == nil {
		return ""
	}
	return fmt.Sprintf("%d", *p)
}

func init() {
	chaosSystemRegisterCmd.Flags().StringVar(&chaosSysName, "name", "", "System name (required)")
	chaosSystemRegisterCmd.Flags().StringVar(&chaosSysNsPattern, "namespace", "", "Kubernetes namespace pattern (required)")
	chaosSystemRegisterCmd.Flags().StringVar(&chaosSysAppLabelKey, "app-label-key", "", "Label key used to select target pods, e.g. app.kubernetes.io/name (required)")
	chaosSystemRegisterCmd.Flags().BoolVar(&chaosSysEnabled, "enabled", true, "Whether the system accepts injections")
	chaosSystemRegisterCmd.Flags().IntVar(&chaosSysMaxConc, "max-concurrent", 0, "Override max concurrent injections (default: server-side 5)")

	chaosSystemCmd.AddCommand(chaosSystemRegisterCmd)
	chaosSystemCmd.AddCommand(chaosSystemGetCmd)
	chaosCmd.AddCommand(chaosSystemCmd)
}
