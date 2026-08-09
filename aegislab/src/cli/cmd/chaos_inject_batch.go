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
	chaosBatchChildrenFile string
	chaosBatchIdemKey      string
	chaosBatchCallerMeta   string
)

var chaosInjectBatchCmd = &cobra.Command{
	Use:   "batch",
	Short: "Submit / inspect / destroy aegis-chaos injection batches",
}

var chaosInjectBatchSubmitCmd = &cobra.Command{
	Use:   "submit",
	Short: "Submit a chaos injection batch (POST /v1beta/injection-batches)",
	Args:  requireNoArgs,
	RunE:  runChaosBatchSubmit,
}

var chaosInjectBatchGetCmd = &cobra.Command{
	Use:   "get <batch-id>",
	Short: "Fetch an injection batch (GET /v1beta/injection-batches/{id})",
	Args:  cobra.ExactArgs(1),
	RunE:  runChaosBatchGet,
}

var chaosInjectBatchDestroyCmd = &cobra.Command{
	Use:   "destroy <batch-id>",
	Short: "Destroy / cancel an injection batch (DELETE /v1beta/injection-batches/{id})",
	Args:  cobra.ExactArgs(1),
	RunE:  runChaosBatchDestroy,
}

// childrenFileShape mirrors the wire DTO so users can hand-author the file
// they pass via --children-file. Keys are JSON-tagged; arbitrary `params`
// and `caller_metadata` JSON objects are forwarded verbatim.
type childrenFileShape struct {
	Children []struct {
		PointID        string         `json:"point_id"`
		Namespace      string         `json:"namespace"`
		Params         map[string]any `json:"params,omitempty"`
		IdempotencyKey string         `json:"idempotency_key"`
		CallerMetadata map[string]any `json:"caller_metadata,omitempty"`
		ExecutorPin    string         `json:"executor_pin,omitempty"`
	} `json:"children"`
}

func runChaosBatchSubmit(_ *cobra.Command, _ []string) error {
	if chaosBatchChildrenFile == "" {
		return usageErrorf("--children-file is required")
	}
	if chaosBatchIdemKey == "" {
		return usageErrorf("--batch-idempotency-key is required")
	}
	raw, err := readChildrenFile(chaosBatchChildrenFile)
	if err != nil {
		return err
	}
	var spec childrenFileShape
	if err := json.Unmarshal(raw, &spec); err != nil {
		return fmt.Errorf("decode --children-file: %w", err)
	}
	if len(spec.Children) == 0 {
		return usageErrorf("--children-file must contain at least one child")
	}
	callerMeta, err := loadJSONFlag("--batch-caller-metadata", chaosBatchCallerMeta, false)
	if err != nil {
		return err
	}

	cli, ctx, err := newChaosAPIClient()
	if err != nil {
		return err
	}

	children := make([]apiclient.ChaosChaosCreateBatchChildReq, 0, len(spec.Children))
	for i, c := range spec.Children {
		if c.Namespace == "" {
			return usageErrorf("children[%d].namespace is required", i)
		}
		entry := apiclient.ChaosChaosCreateBatchChildReq{
			PointId:              ptr(c.PointID),
			IdempotencyKey:       ptr(c.IdempotencyKey),
			AdditionalProperties: map[string]any{"namespace": c.Namespace},
		}
		if c.Params != nil {
			entry.Params = c.Params
		}
		if c.CallerMetadata != nil {
			entry.CallerMetadata = c.CallerMetadata
		}
		if c.ExecutorPin != "" {
			entry.ExecutorPin = ptr(c.ExecutorPin)
		}
		children = append(children, entry)
	}

	body := *apiclient.NewChaosChaosCreateInjectionBatchReq(chaosBatchIdemKey, children)
	if callerMeta != nil {
		body.BatchCallerMetadata = callerMeta
	}

	resp, _, err := cli.ChaosAPI.ChaosCreateInjectionBatch(ctx).
		ChaosChaosCreateInjectionBatchReq(body).Execute()
	if err != nil {
		return err
	}
	return renderChaosBatch(resp.Data)
}

func runChaosBatchGet(_ *cobra.Command, args []string) error {
	cli, ctx, err := newChaosAPIClient()
	if err != nil {
		return err
	}
	resp, _, err := cli.ChaosAPI.ChaosGetInjectionBatch(ctx, args[0]).Execute()
	if err != nil {
		return err
	}
	return renderChaosBatch(resp.Data)
}

func runChaosBatchDestroy(_ *cobra.Command, args []string) error {
	cli, ctx, err := newChaosAPIClient()
	if err != nil {
		return err
	}
	resp, _, err := cli.ChaosAPI.ChaosDeleteInjectionBatch(ctx, args[0]).Execute()
	if err != nil {
		return err
	}
	return renderChaosBatch(resp.Data)
}

func renderChaosBatch(b *apiclient.ChaosChaosInjectionBatchResp) error {
	if b == nil {
		return fmt.Errorf("server returned an empty data envelope")
	}
	if output.OutputFormat(flagOutput) == output.FormatJSON {
		output.PrintJSON(b)
		return nil
	}
	headers := []string{"BATCH_ID", "AGG_STATUS", "IDEMPOTENCY_KEY", "TS", "FINISHED_AT", "CHILDREN"}
	rows := [][]string{{
		strDeref(b.Id), strDeref(b.AggregatedStatus), strDeref(b.IdempotencyKey),
		strDeref(b.Ts), strDeref(b.FinishedAt), fmt.Sprintf("%d", len(b.Children)),
	}}
	output.PrintTable(headers, rows)
	if len(b.Children) > 0 {
		ch := []string{"CHILD_ID", "POINT_ID", "STATUS", "HANDLE", "IDEMPOTENCY_KEY"}
		out := make([][]string, 0, len(b.Children))
		for _, c := range b.Children {
			out = append(out, []string{
				strDeref(c.Id), strDeref(c.PointId), strDeref(c.Status),
				strDeref(c.ExecutorHandle), strDeref(c.IdempotencyKey),
			})
		}
		fmt.Fprintln(os.Stderr)
		output.PrintTable(ch, out)
	}
	return nil
}

func readChildrenFile(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	path = strings.TrimPrefix(path, "@")
	return os.ReadFile(path)
}

func init() {
	chaosInjectBatchSubmitCmd.Flags().StringVar(&chaosBatchChildrenFile, "children-file", "", `Path to JSON file (use "-" for stdin) with {"children":[...]}`)
	chaosInjectBatchSubmitCmd.Flags().StringVar(&chaosBatchIdemKey, "batch-idempotency-key", "", "Batch-level idempotency key (required)")
	chaosInjectBatchSubmitCmd.Flags().StringVar(&chaosBatchCallerMeta, "batch-caller-metadata", "", "JSON object (or @file / @-) of batch-level caller metadata")

	chaosInjectBatchCmd.AddCommand(chaosInjectBatchSubmitCmd)
	chaosInjectBatchCmd.AddCommand(chaosInjectBatchGetCmd)
	chaosInjectBatchCmd.AddCommand(chaosInjectBatchDestroyCmd)
}
