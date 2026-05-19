package cmd

import (
	"fmt"
	"net/url"

	"aegis/cli/output"

	"github.com/spf13/cobra"
)

// blobLifecycleCmd hosts subcommands that operate on the cluster-wide
// bucket-lifecycle sweep worker (as opposed to `aegisctl bucket
// lifecycle`, which manages per-bucket policy JSON).
var blobLifecycleCmd = &cobra.Command{
	Use:   "lifecycle",
	Short: "Inspect the bucket-lifecycle sweep worker",
	Long: `Operate on the bucket-lifecycle sweep worker — the cluster-wide GC that
turns persisted lifecycle policies (managed via 'aegisctl bucket lifecycle')
into actual soft + hard deletes.

The worker is OFF by default. Use 'dry-run' to preview what would happen
on the next sweep before flipping blob.lifecycle.enabled=true.
`,
}

var blobLifecycleDryRunBucket string

// blobLifecycleDryRunCmd previews what the next sweep would soft-delete
// without acting. Useful for operator confidence on a new policy.
var blobLifecycleDryRunCmd = &cobra.Command{
	Use:   "dry-run",
	Short: "Preview the next bucket-lifecycle sweep (no changes)",
	Long: `Print the per-bucket, per-rule breakdown of objects the next sweep would
soft-delete. Adding --bucket=<name> limits the preview to a single bucket.

This calls /api/v2/blob/lifecycle/dry-run server-side; no state is mutated.

EXAMPLES:
  aegisctl blob lifecycle dry-run
  aegisctl blob lifecycle dry-run --bucket aegis-scratch
  aegisctl blob lifecycle dry-run --output json | jq '.buckets'
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAPIContext(true); err != nil {
			return err
		}
		c := newClient()
		path := "/api/v2/blob/lifecycle/dry-run"
		if blobLifecycleDryRunBucket != "" {
			path += "?bucket=" + url.QueryEscape(blobLifecycleDryRunBucket)
		}

		type rule struct {
			Name            string `json:"name"`
			MatchPrefix     string `json:"match_prefix"`
			ExpireAfterDays int    `json:"expire_after_days"`
			Matched         int    `json:"matched"`
		}
		type bucket struct {
			Bucket    string   `json:"bucket"`
			Rules     []rule   `json:"rules"`
			TotalKeys int      `json:"total_keys"`
			Examples  []string `json:"examples,omitempty"`
		}
		type result struct {
			Buckets     []bucket `json:"buckets"`
			LockHeld    bool     `json:"lock_held"`
			SweptAt     string   `json:"swept_at"`
			GracePeriod string   `json:"grace_period"`
		}
		type envelope struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Data    result `json:"data"`
		}
		var resp envelope
		if err := c.Get(path, &resp); err != nil {
			return err
		}

		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(resp.Data)
			return nil
		}
		if len(resp.Data.Buckets) == 0 {
			fmt.Println("(no buckets have lifecycle policies, or nothing would be swept)")
			return nil
		}
		rows := make([][]string, 0)
		for _, b := range resp.Data.Buckets {
			for _, r := range b.Rules {
				rows = append(rows, []string{
					b.Bucket,
					r.Name,
					r.MatchPrefix,
					fmt.Sprintf("%d", r.ExpireAfterDays),
					fmt.Sprintf("%d", r.Matched),
				})
			}
		}
		output.PrintTable([]string{"BUCKET", "RULE", "MATCH_PREFIX", "EXPIRE_DAYS", "WOULD_SWEEP"}, rows)
		fmt.Printf("\nGrace period: %s\n", resp.Data.GracePeriod)
		return nil
	},
}

func init() {
	blobLifecycleDryRunCmd.Flags().StringVar(&blobLifecycleDryRunBucket, "bucket", "", "Limit preview to a single bucket")
	blobLifecycleCmd.AddCommand(blobLifecycleDryRunCmd)
	blobCmd.AddCommand(blobLifecycleCmd)
}
