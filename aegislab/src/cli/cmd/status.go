package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"aegis/cli/output"

	"github.com/spf13/cobra"
)

// healthServiceInfo decodes one entry of SystemHealthCheckResp.Services
// (which the generated client exposes as map[string]interface{}).
type healthServiceInfo struct {
	Status       string `json:"status"`
	ResponseTime string `json:"response_time"`
	Error        string `json:"error,omitempty"`
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show cluster status and infrastructure health",
	RunE: func(cmd *cobra.Command, args []string) error {
		cli, ctx := newAPIClient()

		// Determine context name.
		ctxName := "unknown"
		if cfg != nil && cfg.CurrentContext != "" {
			ctxName = cfg.CurrentContext
		}

		// --- User info ---
		connected := "yes"
		username := "unknown"
		if profileResp, _, err := cli.AuthenticationAPI.GetCurrentUserProfile(ctx).Execute(); err != nil {
			connected = "no (not logged in)"
		} else {
			profile := profileResp.GetData()
			username = profile.GetUsername()
		}

		if output.OutputFormat(flagOutput) == output.FormatJSON {
			result := map[string]any{
				"server":    flagServer,
				"context":   ctxName,
				"connected": connected == "yes",
				"username":  username,
			}

			// Tasks
			if taskResp, _, err := cli.TasksAPI.ListTasks(ctx).Page(1).Size(100).Execute(); err == nil {
				counts := map[string]int{}
				taskData := taskResp.GetData()
				for _, t := range taskData.GetItems() {
					counts[t.GetState()]++
				}
				result["tasks"] = counts
			}

			// Traces
			if traceResp, _, err := cli.TracesAPI.ListTraces(ctx).Page(1).Size(10).Execute(); err == nil {
				traceData := traceResp.GetData()
				result["recent_traces"] = traceData.GetItems()
			}

			// Health
			if healthResp, _, err := cli.SystemAPI.GetSystemHealth(ctx).Execute(); err == nil {
				result["health"] = healthResp.GetData()
			} else {
				result["health"] = map[string]any{"status": "unreachable", "error": err.Error()}
			}

			output.PrintJSON(result)
			return nil
		}

		// --- Table output ---
		fmt.Printf("Server:    %s (%s)\n", flagServer, ctxName)
		fmt.Printf("User:      %s\n", username)
		fmt.Printf("Connected: %s\n", connected)
		fmt.Println()

		// --- Tasks ---
		if taskResp, _, err := cli.TasksAPI.ListTasks(ctx).Page(1).Size(100).Execute(); err == nil {
			counts := map[string]int{}
			total := 0
			taskData := taskResp.GetData()
			for _, t := range taskData.GetItems() {
				counts[t.GetState()]++
				total++
			}
			fmt.Printf("Active Tasks:     %d\n", total)
			for state, count := range counts {
				fmt.Printf("  %-14s  %d\n", state+":", count)
			}
		}
		fmt.Println()

		// --- Recent Traces ---
		if traceResp, _, err := cli.TracesAPI.ListTraces(ctx).Page(1).Size(10).Execute(); err == nil {
			traceData := traceResp.GetData()
			items := traceData.GetItems()
			if len(items) > 0 {
				fmt.Println("Recent Traces:")

				rows := make([][]string, 0, len(items))
				for _, t := range items {
					project := t.GetProjectName()
					if project == "" {
						project = fmt.Sprintf("%d", t.GetProjectId())
					}
					rows = append(rows, []string{t.GetId(), t.GetState(), t.GetType(), project})
				}
				output.PrintTable([]string{"Trace-ID", "State", "Type", "Project"}, rows)
			}
		}
		fmt.Println()

		// --- Infrastructure Health ---
		fmt.Println("Infrastructure Health:")
		healthResp, _, err := cli.SystemAPI.GetSystemHealth(ctx).Execute()
		if err != nil {
			fmt.Printf("  %s Could not reach health endpoint: %v\n", output.ColorRed(os.Stdout, "✗"), err)
		} else {
			health := healthResp.GetData()
			services := health.GetServices()
			names := make([]string, 0, len(services))
			for name := range services {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				var svc healthServiceInfo
				if raw, _ := json.Marshal(services[name]); len(raw) > 0 {
					_ = json.Unmarshal(raw, &svc)
				}
				if svc.Status == "healthy" {
					fmt.Printf("  %s %-12s %s\n", output.ColorGreen(os.Stdout, "✓"), name, svc.ResponseTime)
				} else {
					errMsg := svc.Error
					if errMsg == "" {
						errMsg = "unhealthy"
					}
					fmt.Printf("  %s %-12s %s (%s)\n", output.ColorRed(os.Stdout, "✗"), name, svc.ResponseTime, errMsg)
				}
			}
		}

		return nil
	},
}
