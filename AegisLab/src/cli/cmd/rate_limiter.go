package cmd

import (
	"fmt"
	"os"
	"strings"

	"aegis/cli/client"
	"aegis/cli/output"
	"aegis/platform/consts"

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

type rlHolder struct {
	TaskID     string `json:"task_id"`
	TaskState  string `json:"task_state"`
	IsTerminal bool   `json:"is_terminal"`
}

type rlItem struct {
	Bucket   string     `json:"bucket"`
	Key      string     `json:"key"`
	Capacity int        `json:"capacity"`
	Held     int        `json:"held"`
	Holders  []rlHolder `json:"holders"`
}

type rlListResp struct {
	Items []rlItem `json:"items"`
}

type rlGCResp struct {
	Released       int `json:"released"`
	TouchedBuckets int `json:"touched_buckets"`
}

var rateLimiterStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "List token-bucket rate limiters, their holders, and DB state",
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newClient()
		var resp client.APIResponse[rlListResp]
		if err := c.Get(consts.APIPathRateLimiters, &resp); err != nil {
			return err
		}
		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(resp.Data)
			return nil
		}
		color := output.IsStdoutColor()
		headers := []string{"BUCKET", "HELD/CAP", "HOLDERS"}
		var rows [][]string
		for _, item := range resp.Data.Items {
			var parts []string
			for _, h := range item.Holders {
				s := fmt.Sprintf("%s[%s]", h.TaskID, h.TaskState)
				if h.IsTerminal && color {
					s = output.ColorRed(os.Stdout, s+" (LEAKED)")
				} else if h.IsTerminal {
					s = s + " (LEAKED)"
				}
				parts = append(parts, s)
			}
			holders := strings.Join(parts, ", ")
			if holders == "" {
				holders = "-"
			}
			rows = append(rows, []string{
				item.Bucket, fmt.Sprintf("%d/%d", item.Held, item.Capacity), holders,
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
		c := newClient()
		var resp client.APIResponse[any]
		if err := c.Delete(consts.APIPathRateLimiters+"/"+rlResetBucket, &resp); err != nil {
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
		c := newClient()
		doMutation := rlGCForce && !flagDryRun

		var listResp client.APIResponse[rlListResp]
		if err := c.Get(consts.APIPathRateLimiters, &listResp); err != nil {
			return err
		}
		leaked := leakedRateLimiterBuckets(listResp.Data.Items)
		if len(leaked) == 0 && !doMutation {
			if output.OutputFormat(flagOutput) == output.FormatJSON {
				output.PrintJSON(map[string]any{"dry_run": true, "items": []rlItem{}, "count": 0})
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

		var resp client.APIResponse[rlGCResp]
		if err := c.Post(consts.APIPathRateLimitersGC, nil, &resp); err != nil {
			return err
		}

		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(resp.Data)
			return nil
		}
		output.PrintInfo(fmt.Sprintf("released %d leaked tokens from %d buckets",
			resp.Data.Released, resp.Data.TouchedBuckets))
		return nil
	},
}

func leakedRateLimiterBuckets(items []rlItem) []rlItem {
	out := make([]rlItem, 0, len(items))
	for _, item := range items {
		leakedHolders := make([]rlHolder, 0, len(item.Holders))
		for _, holder := range item.Holders {
			if holder.IsTerminal {
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

func printRateLimiterGCPlan(leaks []rlItem) {
	if len(leaks) == 0 {
		fmt.Println("No terminal-state buckets to clean")
		output.PrintInfo("Use --force to execute cleanup")
		return
	}

	fmt.Println("The following buckets have terminal-state holders and would be cleaned:")
	headers := []string{"BUCKET", "HELD/CAP", "TERMINAL HOLDERS"}
	rows := make([][]string, 0, len(leaks))
	for _, item := range leaks {
		holders := make([]string, 0, len(item.Holders))
		for _, holder := range item.Holders {
			holders = append(holders, fmt.Sprintf("%s[%s]", holder.TaskID, holder.TaskState))
		}
		rows = append(rows, []string{
			item.Bucket,
			fmt.Sprintf("%d/%d", item.Held, item.Capacity),
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
