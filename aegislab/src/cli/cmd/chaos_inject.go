package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"aegis/cli/apiclient"
	"aegis/cli/output"

	"github.com/spf13/cobra"
)

var (
	chaosInjectPointID    string
	chaosInjectParams     string
	chaosInjectIdemKey    string
	chaosInjectCallerMeta string
	chaosInjectExecutor   string
)

var chaosInjectCmd = &cobra.Command{
	Use:   "inject",
	Short: "Submit / inspect / destroy aegis-chaos injections",
}

var chaosInjectSubmitCmd = &cobra.Command{
	Use:   "submit",
	Short: "Submit a chaos injection (POST /v1beta/injections)",
	Args:  requireNoArgs,
	RunE:  runChaosInjectSubmit,
}

var chaosInjectGetCmd = &cobra.Command{
	Use:   "get <id>",
	Short: "Fetch a chaos injection by id (GET /v1beta/injections/{id})",
	Args:  cobra.ExactArgs(1),
	RunE:  runChaosInjectGet,
}

var chaosInjectDestroyCmd = &cobra.Command{
	Use:   "destroy <id>",
	Short: "Destroy / cancel a chaos injection (DELETE /v1beta/injections/{id})",
	Args:  cobra.ExactArgs(1),
	RunE:  runChaosInjectDestroy,
}

func runChaosInjectSubmit(_ *cobra.Command, _ []string) error {
	if chaosInjectPointID == "" {
		return usageErrorf("--point-id is required")
	}
	if chaosInjectIdemKey == "" {
		return usageErrorf("--idempotency-key is required")
	}
	params, err := loadJSONFlag("--params", chaosInjectParams, true)
	if err != nil {
		return err
	}
	callerMeta, err := loadJSONFlag("--caller-metadata", chaosInjectCallerMeta, false)
	if err != nil {
		return err
	}

	cli, ctx, err := newChaosAPIClient()
	if err != nil {
		return err
	}
	body := *apiclient.NewChaosChaosCreateInjectionReq()
	body.PointId = &chaosInjectPointID
	body.IdempotencyKey = &chaosInjectIdemKey
	if params != nil {
		body.Params = params
	}
	if callerMeta != nil {
		body.CallerMetadata = callerMeta
	}
	if chaosInjectExecutor != "" {
		body.ExecutorPin = &chaosInjectExecutor
	}
	resp, _, err := cli.ChaosAPI.ChaosCreateInjection(ctx).
		ChaosChaosCreateInjectionReq(body).Execute()
	if err != nil {
		return err
	}
	return renderChaosInjection(resp.Data)
}

func runChaosInjectGet(_ *cobra.Command, args []string) error {
	cli, ctx, err := newChaosAPIClient()
	if err != nil {
		return err
	}
	resp, _, err := cli.ChaosAPI.ChaosGetInjection(ctx, args[0]).Execute()
	if err != nil {
		return err
	}
	return renderChaosInjection(resp.Data)
}

func runChaosInjectDestroy(_ *cobra.Command, args []string) error {
	cli, ctx, err := newChaosAPIClient()
	if err != nil {
		return err
	}
	resp, _, err := cli.ChaosAPI.ChaosDeleteInjection(ctx, args[0]).Execute()
	if err != nil {
		return err
	}
	return renderChaosInjection(resp.Data)
}

func renderChaosInjection(inj *apiclient.ChaosChaosInjectionResp) error {
	if inj == nil {
		return fmt.Errorf("server returned an empty data envelope")
	}
	if output.OutputFormat(flagOutput) == output.FormatJSON {
		output.PrintJSON(inj)
		return nil
	}
	headers := []string{"ID", "POINT_ID", "STATUS", "EXECUTOR", "HANDLE", "IDEMPOTENCY_KEY"}
	rows := [][]string{{
		strDeref(inj.Id), strDeref(inj.PointId), strDeref(inj.Status),
		strDeref(inj.ExecutorName), strDeref(inj.ExecutorHandle), strDeref(inj.IdempotencyKey),
	}}
	output.PrintTable(headers, rows)
	if !flagQuiet {
		fmt.Fprintf(os.Stderr, "ts=%s started_at=%s finished_at=%s destroyed_at=%s\n",
			strDeref(inj.Ts), strDeref(inj.StartedAt),
			strDeref(inj.FinishedAt), strDeref(inj.DestroyedAt))
		if e := strDeref(inj.DestroyError); e != "" {
			fmt.Fprintf(os.Stderr, "destroy_error: %s\n", e)
		}
	}
	return nil
}

// loadJSONFlag decodes value as JSON. `@<path>` reads from a file (or "-" for
// stdin). Returns nil if value is empty and required is false.
func loadJSONFlag(name, value string, required bool) (map[string]any, error) {
	if value == "" {
		if required {
			return nil, usageErrorf("%s is required", name)
		}
		return nil, nil
	}
	raw := []byte(value)
	if strings.HasPrefix(value, "@") {
		path := strings.TrimPrefix(value, "@")
		var (
			b   []byte
			err error
		)
		if path == "-" {
			b, err = io.ReadAll(os.Stdin)
		} else {
			b, err = os.ReadFile(path)
		}
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		raw = b
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode %s as JSON object: %w", name, err)
	}
	return out, nil
}

func init() {
	chaosInjectSubmitCmd.Flags().StringVar(&chaosInjectPointID, "point-id", "", "Point id to inject against (required)")
	chaosInjectSubmitCmd.Flags().StringVar(&chaosInjectParams, "params", "", "JSON object (or @file / @-) of capability params (required)")
	chaosInjectSubmitCmd.Flags().StringVar(&chaosInjectIdemKey, "idempotency-key", "", "Unique key — duplicate POSTs return the existing row (required)")
	chaosInjectSubmitCmd.Flags().StringVar(&chaosInjectCallerMeta, "caller-metadata", "", "JSON object (or @file / @-) of opaque caller metadata")
	chaosInjectSubmitCmd.Flags().StringVar(&chaosInjectExecutor, "executor-pin", "", "Optional executor pin for advanced routing")

	chaosInjectCmd.AddCommand(chaosInjectSubmitCmd)
	chaosInjectCmd.AddCommand(chaosInjectGetCmd)
	chaosInjectCmd.AddCommand(chaosInjectDestroyCmd)
	chaosCmd.AddCommand(chaosInjectCmd)
}
