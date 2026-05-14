package cmd

import (
	"fmt"
	"os"
	"strings"

	"aegis/cli/apiclient"
	"aegis/cli/output"

	"github.com/spf13/cobra"
)

// rateLimiterCmd groups `rate-limiter` subcommands (OperationsPAI/aegis#21).
var rateLimiterCmd = &cobra.Command{
	Use:     "rate-limiter",
	Aliases: []string{"rl"},
	Short:   "Inspect and manage token-bucket rate limiters",
	Long: `Inspect and manage the token-bucket rate limiters used by the consumer.

Each bucket is a Redis set keyed "token_bucket:<name>" whose members are
the task_ids currently holding a token. If a task crashes without
releasing its token, the bucket can get stuck at HELD=CAP and block all
later tasks.

COMMANDS:
  aegisctl rate-limiter status
  aegisctl rate-limiter reset --bucket NAME --force
  aegisctl rate-limiter gc`,
}

var rateLimiterStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "List token-bucket rate limiters, their holders, and DB state",
	RunE: func(cmd *cobra.Command, args []string) error {
		cli, ctx := newAPIClient()
		resp, _, err := cli.RateLimitersAPI.ListRateLimiters(ctx).Execute()
		if err != nil {
			return err
		}
		data := resp.GetData()
		items := data.GetItems()
		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(data)
			return nil
		}
		color := output.IsStdoutColor()
		headers := []string{"BUCKET", "HELD/CAP", "HOLDERS"}
		var rows [][]string
		for _, item := range items {
			var parts []string
			for _, h := range item.GetHolders() {
				s := fmt.Sprintf("%s[%s]", h.GetTaskId(), h.GetTaskState())
				if h.GetIsTerminal() && color {
					s = output.ColorRed(os.Stdout, s+" (LEAKED)")
				} else if h.GetIsTerminal() {
					s = s + " (LEAKED)"
				}
				parts = append(parts, s)
			}
			holders := strings.Join(parts, ", ")
			if holders == "" {
				holders = "-"
			}
			rows = append(rows, []string{
				item.GetBucket(), fmt.Sprintf("%d/%d", item.GetHeld(), item.GetCapacity()), holders,
			})
		}
		output.PrintTable(headers, rows)
		return nil
	},
}

var (
	rlResetBucket string
	rlResetForce  bool
)
var rlGCForce bool

var rateLimiterResetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Delete a rate-limiter bucket key (requires --force)",
	RunE: func(cmd *cobra.Command, args []string) error {
		if rlResetBucket == "" {
			return fmt.Errorf("--bucket is required")
		}
		if !rlResetForce {
			return fmt.Errorf("refusing to reset bucket %q without --force", rlResetBucket)
		}
		cli, ctx := newAPIClient()
		if _, _, err := cli.RateLimitersAPI.ResetRateLimiter(ctx, rlResetBucket).Execute(); err != nil {
			return err
		}
		output.PrintInfo(fmt.Sprintf("bucket %q reset", rlResetBucket))
		return nil
	},
}

var rateLimiterGCCmd = &cobra.Command{
	Use:   "gc",
	Short: "Release tokens held by terminal-state tasks across all buckets",
	RunE: func(cmd *cobra.Command, args []string) error {
		cli, ctx := newAPIClient()
		doMutation := rlGCForce && !flagDryRun

		listResp, _, err := cli.RateLimitersAPI.ListRateLimiters(ctx).Execute()
		if err != nil {
			return err
		}
		listData := listResp.GetData()
		items := listData.GetItems()
		leaked := leakedRateLimiterBuckets(items)
		if len(leaked) == 0 && !doMutation {
			if output.OutputFormat(flagOutput) == output.FormatJSON {
				output.PrintJSON(map[string]any{"dry_run": true, "items": []apiclient.RatelimiterRateLimiterItem{}, "count": 0})
				return nil
			}
			output.PrintInfo("No terminal-state buckets to clean")
			output.PrintInfo("Use --force to execute cleanup")
			return nil
		}

		if !doMutation {
			if output.OutputFormat(flagOutput) == output.FormatJSON {
				output.PrintJSON(map[string]any{
					"dry_run": true,
					"items":   leaked,
					"count":   len(leaked),
				})
				return nil
			}
			printRateLimiterGCPlan(leaked)
			return nil
		}

		gcResp, _, err := cli.RateLimitersAPI.GcRateLimiters(ctx).Execute()
		if err != nil {
			return err
		}
		gcData := gcResp.GetData()

		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(gcData)
			return nil
		}
		output.PrintInfo(fmt.Sprintf("released %d leaked tokens from %d buckets",
			gcData.GetReleased(), gcData.GetTouchedBuckets()))
		return nil
	},
}

func leakedRateLimiterBuckets(items []apiclient.RatelimiterRateLimiterItem) []apiclient.RatelimiterRateLimiterItem {
	out := make([]apiclient.RatelimiterRateLimiterItem, 0, len(items))
	for _, item := range items {
		leakedHolders := make([]apiclient.RatelimiterRateLimiterHolder, 0, len(item.GetHolders()))
		for _, holder := range item.GetHolders() {
			if holder.GetIsTerminal() {
				leakedHolders = append(leakedHolders, holder)
			}
		}
		if len(leakedHolders) == 0 {
			continue
		}
		filtered := item
		filtered.Holders = leakedHolders
		out = append(out, filtered)
	}
	return out
}

func printRateLimiterGCPlan(leaks []apiclient.RatelimiterRateLimiterItem) {
	if len(leaks) == 0 {
		fmt.Println("No terminal-state buckets to clean")
		output.PrintInfo("Use --force to execute cleanup")
		return
	}

	fmt.Println("The following buckets have terminal-state holders and would be cleaned:")
	headers := []string{"BUCKET", "HELD/CAP", "TERMINAL HOLDERS"}
	rows := make([][]string, 0, len(leaks))
	for _, item := range leaks {
		holders := make([]string, 0, len(item.GetHolders()))
		for _, holder := range item.GetHolders() {
			holders = append(holders, fmt.Sprintf("%s[%s]", holder.GetTaskId(), holder.GetTaskState()))
		}
		rows = append(rows, []string{
			item.GetBucket(),
			fmt.Sprintf("%d/%d", item.GetHeld(), item.GetCapacity()),
			strings.Join(holders, ", "),
		})
	}
	output.PrintTable(headers, rows)
	output.PrintInfo("Use --force to execute cleanup")
}

func init() {
	rateLimiterResetCmd.Flags().StringVar(&rlResetBucket, "bucket", "", "Bucket short name, e.g. restart_service")
	rateLimiterResetCmd.Flags().BoolVar(&rlResetForce, "force", false, "Required to actually perform the reset")
	rateLimiterGCCmd.Flags().BoolVar(&rlGCForce, "force", false, "Required to actually perform cleanup")
	rateLimiterCmd.AddCommand(rateLimiterStatusCmd)
	rateLimiterCmd.AddCommand(rateLimiterResetCmd)
	rateLimiterCmd.AddCommand(rateLimiterGCCmd)
	cobra.OnInitialize(func() {
		markDryRunSupported(rateLimiterGCCmd)
	})
	rootCmd.AddCommand(rateLimiterCmd)
}
