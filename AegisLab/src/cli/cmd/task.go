package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"aegis/cli/apiclient"
	"aegis/cli/client"
	"aegis/cli/output"
	"aegis/platform/consts"

	"github.com/spf13/cobra"
)

// taskCmd is the top-level task command.
var taskCmd = &cobra.Command{
	Use:   "task",
	Short: "Monitor and inspect tasks",
	Long: `Monitor and inspect individual tasks in AegisLab.

Tasks are the atomic units of work executed by the consumer (background worker).
They are typically created as children of a trace.

WORKFLOW:
  # List tasks with filters
  aegisctl task list
  aegisctl task list --state Running --type FaultInjection

  # Get task details
  aegisctl task get <task-id>

  # Stream task logs via WebSocket
  aegisctl task logs <task-id> --follow

TASK STATES: Pending, Rescheduled, Running, Completed, Error, Cancelled
TASK TYPES:  BuildContainer, RestartPedestal, FaultInjection, RunAlgorithm,
             BuildDatapack, CollectResult, CronJob`,
}

// --- task list ---

var taskListState string
var taskListType string
var taskListPage int
var taskListSize int
var taskListOverdue bool
var taskListAll bool

// resolveTaskStateFilter accepts either a TaskState name ("Running") or its
// numeric form ("2") and returns the int32 the typed client expects. Empty
// input means "no filter".
func resolveTaskStateFilter(raw string) (*int32, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, nil
	}
	if state := consts.GetTaskStateByName(s); state != nil {
		v := int32(*state)
		return &v, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return nil, fmt.Errorf("invalid --state %q: want a name (e.g. Running) or int", s)
	}
	v := int32(n)
	return &v, nil
}

// resolveTaskTypeFilter accepts either a TaskType name or numeric form and
// returns the int32 query value.
func resolveTaskTypeFilter(raw string) (*int32, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, nil
	}
	if t := consts.GetTaskTypeByName(s); t != nil {
		v := int32(*t)
		return &v, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return nil, fmt.Errorf("invalid --type %q: want a name (e.g. FaultInjection) or int", s)
	}
	v := int32(n)
	return &v, nil
}

var taskListCmd = &cobra.Command{
	Use:   "list",
	Short: "List tasks with optional filters",
	RunE: func(cmd *cobra.Command, args []string) error {
		if taskListAll {
			if output.OutputFormat(flagOutput) != output.FormatNDJSON {
				return fmt.Errorf("--all requires --output ndjson (table/json buffer the full result set; use ndjson for streaming)")
			}
			c := newClient()
			basePath := consts.APIPathTasks
			baseParams := map[string]string{
				"state": taskListState,
				"type":  taskListType,
			}
			var keep func(map[string]any) bool
			if taskListOverdue {
				nowEpoch := time.Now().Unix()
				keep = func(item map[string]any) bool {
					return stringField(item, "state") == "Pending" && execTimeField(item) <= nowEpoch
				}
			}
			return streamListAllNDJSONFiltered[map[string]any](c, basePath, baseParams, keep)
		}

		stateFilter, err := resolveTaskStateFilter(taskListState)
		if err != nil {
			return err
		}
		typeFilter, err := resolveTaskTypeFilter(taskListType)
		if err != nil {
			return err
		}

		cli, ctx := newAPIClient()
		req := cli.TasksAPI.ListTasks(ctx)
		if taskListPage > 0 {
			req = req.Page(int32(taskListPage))
		}
		if taskListSize > 0 {
			req = req.Size(int32(taskListSize))
		}
		if stateFilter != nil {
			req = req.State(*stateFilter)
		}
		if typeFilter != nil {
			req = req.TaskType(*typeFilter)
		}
		resp, _, err := req.Execute()
		if err != nil {
			return err
		}
		data := resp.GetData()
		items := data.GetItems()

		// Apply --overdue filter client-side: only keep Pending tasks whose
		// execute_time is in the past.
		if taskListOverdue {
			nowEpoch := int32(time.Now().Unix())
			filtered := items[:0]
			for _, item := range items {
				if item.GetState() != "Pending" {
					continue
				}
				if item.GetExecuteTime() <= nowEpoch {
					filtered = append(filtered, item)
				}
			}
			items = filtered
		}

		switch output.OutputFormat(flagOutput) {
		case output.FormatJSON:
			data.Items = items
			output.PrintJSON(data)
			return nil
		case output.FormatNDJSON:
			if pg := data.GetPagination(); pg.HasPage() {
				if err := output.PrintMetaJSON(pg); err != nil {
					return err
				}
			}
			return output.PrintNDJSON(items)
		}

		headers := []string{"TASK-ID", "TYPE", "STATE", "WAIT", "TRACE-ID", "PROJECT-ID", "CREATED"}
		var rows [][]string
		nowEpoch := time.Now().Unix()
		for _, item := range items {
			state := item.GetState()
			wait := "-"
			if state == "Pending" {
				wait = formatWait(int64(item.GetExecuteTime()) - nowEpoch)
			}
			rows = append(rows, []string{
				item.GetId(),
				item.GetType(),
				state,
				wait,
				item.GetTraceId(),
				strconv.Itoa(int(item.GetProjectId())),
				item.GetCreatedAt(),
			})
		}

		output.PrintTable(headers, rows)
		p := data.GetPagination()
		output.PrintInfo(fmt.Sprintf("Page %d/%d (total: %d)", p.GetPage(), p.GetTotalPages(), p.GetTotal()))
		return nil
	},
}

// --- task expedite ---

var taskExpediteCmd = &cobra.Command{
	Use:   "expedite <task-id>",
	Short: "Expedite a Pending task so it runs on the next scheduler tick",
	Long: `Expedite a Pending task.

Resets execute_time to now in both MySQL and the Redis delayed queue, so the
scheduler picks the task up on its next tick. Rejects the call if the task is
in any state other than Pending. Idempotent: expediting an already-due task
succeeds silently.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cli, ctx := newAPIClient()
		resp, _, err := cli.TasksAPI.ExpediteTask(ctx, args[0]).Execute()
		if err != nil {
			return err
		}

		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(resp.GetData())
			return nil
		}

		output.PrintInfo(fmt.Sprintf("Task %s expedited", args[0]))
		return nil
	},
}

// formatWait renders a remaining-time int64 second count as "+MM:SS" or
// "-MM:SS" (overdue). Used only for Pending rows.
func formatWait(deltaSec int64) string {
	sign := "+"
	abs := deltaSec
	if deltaSec < 0 {
		sign = "-"
		abs = -deltaSec
	}
	return fmt.Sprintf("%s%02d:%02d", sign, abs/60, abs%60)
}

// execTimeField extracts execute_time from a task response map. Used by the
// --all NDJSON streaming path which still works on map[string]any items.
func execTimeField(m map[string]any) int64 {
	v, ok := m["execute_time"]
	if !ok || v == nil {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	}
	return 0
}

// --- task get ---

var taskGetCmd = &cobra.Command{
	Use:   "get <task-id>",
	Short: "Show detailed task information",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runStdinItems("task get", "task get <task-id>", args, stdinOptions{
			enabled:  taskGetStdin,
			field:    taskGetStdinField,
			failFast: taskGetStdinFailFast,
		}, runTaskGet)
	},
}

var (
	taskGetStdin         bool
	taskGetStdinField    string
	taskGetStdinFailFast bool
)

func runTaskGet(taskID string) error {
	cli, ctx := newAPIClient()
	resp, _, err := cli.TasksAPI.GetTaskById(ctx, taskID).Execute()
	if err != nil {
		return err
	}
	d := resp.GetData()

	if output.OutputFormat(flagOutput) == output.FormatJSON {
		output.PrintJSON(d)
		return nil
	}

	printTaskDetail(d)
	return nil
}

func printTaskDetail(d apiclient.DtoTaskDetailResp) {
	row := func(k, v string) {
		if v != "" {
			fmt.Printf("%-20s %s\n", k+":", v)
		}
	}
	row("id", d.GetId())
	row("type", d.GetType())
	row("state", d.GetState())
	row("status", d.GetStatus())
	row("trace_id", d.GetTraceId())
	row("group_id", d.GetGroupId())
	row("project_id", strconv.Itoa(int(d.GetProjectId())))
	row("project_name", d.GetProjectName())
	if d.GetExecuteTime() != 0 {
		row("execute_time", strconv.Itoa(int(d.GetExecuteTime())))
	}
	row("cron_expr", d.GetCronExpr())
	if d.HasImmediate() {
		row("immediate", strconv.FormatBool(d.GetImmediate()))
	}
	row("created_at", d.GetCreatedAt())
	row("updated_at", d.GetUpdatedAt())
	if payload := d.GetPayload(); len(payload) > 0 {
		fmt.Printf("%-20s %v\n", "payload:", payload)
	}
	if logs := d.GetLogs(); len(logs) > 0 {
		fmt.Printf("%-20s %d entries\n", "logs:", len(logs))
	}
}

// --- task logs ---

var taskLogsFollow bool

var taskLogsCmd = &cobra.Command{
	Use:   "logs <task-id>",
	Short: "Stream task logs via WebSocket",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runStdinItems("task logs", "task logs <task-id>", args, stdinOptions{
			enabled:  taskLogsStdin,
			field:    taskLogsStdinField,
			failFast: taskLogsStdinFailFast,
		}, runTaskLogs)
	},
}

var (
	taskLogsStdin         bool
	taskLogsStdinField    string
	taskLogsStdinFailFast bool
)

func runTaskLogs(taskID string) error {
	// Task logs are a WebSocket stream; the generated client cannot deliver
	// the message channel, so keep the manual WS reader path here.
	wsPath := consts.APIPathTaskLogsWS(taskID)
	reader := client.NewWSReader(flagServer, wsPath, flagToken)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle Ctrl+C.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		cancel()
	}()

	messages, errs := reader.Stream(ctx)

	if taskLogsFollow {
		// Follow mode: keep reading until cancelled.
		for {
			select {
			case msg, ok := <-messages:
				if !ok {
					return nil
				}
				fmt.Println(msg)
			case err, ok := <-errs:
				if !ok {
					return nil
				}
				return err
			case <-ctx.Done():
				return nil
			}
		}
	} else {
		// Non-follow mode: read available messages with a timeout.
		timeout := time.After(5 * time.Second)
		for {
			select {
			case msg, ok := <-messages:
				if !ok {
					return nil
				}
				fmt.Println(msg)
			case err, ok := <-errs:
				if !ok {
					return nil
				}
				return err
			case <-timeout:
				return nil
			case <-ctx.Done():
				return nil
			}
		}
	}
}

// Helper functions shared by task and trace commands.

func buildQueryParams(params map[string]string) string {
	var parts []string
	for k, v := range params {
		if v != "" && v != "0" {
			parts = append(parts, k+"="+v)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	result := parts[0]
	for _, p := range parts[1:] {
		result += "&" + p
	}
	return result
}

func intToString(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%d", n)
}

func stringField(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

func init() {
	taskListCmd.Flags().StringVar(&taskListState, "state", "", "Filter by state (Pending, Running, Completed, Error, Cancelled, Rescheduled)")
	taskListCmd.Flags().StringVar(&taskListType, "type", "", "Filter by type (BuildContainer, RestartPedestal, FaultInjection, RunAlgorithm, BuildDatapack, CollectResult, CronJob)")
	taskListCmd.Flags().IntVar(&taskListPage, "page", 0, "Page number")
	taskListCmd.Flags().IntVar(&taskListSize, "size", 0, "Page size")
	taskListCmd.Flags().BoolVar(&taskListAll, "all", false, "Stream every page as NDJSON to stdout (ignores --page/--size; requires --output ndjson)")
	taskListCmd.Flags().BoolVar(&taskListOverdue, "overdue", false, "Show only Pending tasks whose execute_time has passed (WAIT < 0)")

	taskLogsCmd.Flags().BoolVarP(&taskLogsFollow, "follow", "f", false, "Follow log output")
	addStdinFlags(taskGetCmd, &taskGetStdin, &taskGetStdinField, &taskGetStdinFailFast)
	addStdinFlags(taskLogsCmd, &taskLogsStdin, &taskLogsStdinField, &taskLogsStdinFailFast)

	taskCmd.AddCommand(taskListCmd)
	taskCmd.AddCommand(taskGetCmd)
	taskCmd.AddCommand(taskLogsCmd)
	taskCmd.AddCommand(taskExpediteCmd)
}
