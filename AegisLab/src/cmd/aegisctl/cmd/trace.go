package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"time"

	"aegis/cmd/aegisctl/client"
	"aegis/cmd/aegisctl/output"

	"github.com/spf13/cobra"
)

// traceCmd is the top-level trace command.
var traceCmd = &cobra.Command{
	Use:   "trace",
	Short: "Monitor and inspect traces",
	Long: `Monitor and inspect execution traces in AegisLab.

A trace represents a top-level workflow (e.g. fault injection pipeline).
Each trace contains child tasks that execute sequentially or in parallel.

WORKFLOW:
  # List recent traces
  aegisctl trace list
  aegisctl trace list --project pair_diagnosis --state Running

  # Short, scriptable output (header + TSV rows on stdout)
  aegisctl trace list --format tsv --columns id,state,last_event

  # Get trace details (includes child tasks)
  aegisctl trace get <trace-id>

  # Watch trace events in real-time via SSE
  aegisctl trace watch <trace-id>

  # Cancel a running trace (best-effort)
  aegisctl trace cancel <trace-id>

TRACE STATES: Pending, Running, Completed, Failed
TRACE TYPES:  FullPipeline, FaultInjection, DatapackBuild, AlgorithmRun`,
}

// --- trace list ---

var (
	traceListProject string
	traceListState   string
	traceListGroupID string
	traceListPage    int
	traceListSize    int
	traceListFormat  string
	traceListColumns string
)

// traceColumnExtractors maps short column names to a function that pulls the
// value out of a trace map. Keep this list in sync with the --columns help.
var traceColumnExtractors = map[string]func(map[string]any) string{
	"id": func(m map[string]any) string {
		// Server returns `id`; fall back to `trace_id` for older payloads.
		if v := stringField(m, "id"); v != "" {
			return v
		}
		return stringField(m, "trace_id")
	},
	"type":         func(m map[string]any) string { return stringField(m, "type") },
	"state":        func(m map[string]any) string { return stringField(m, "state") },
	"status":       func(m map[string]any) string { return stringField(m, "status") },
	"project":      func(m map[string]any) string { return stringField(m, "project_name") },
	"project_id":   func(m map[string]any) string { return stringField(m, "project_id") },
	"group_id":     func(m map[string]any) string { return stringField(m, "group_id") },
	"last_event":   func(m map[string]any) string { return stringField(m, "last_event") },
	"final_event":  func(m map[string]any) string { return stringField(m, "last_event") }, // alias for terminal event
	"created_at":   func(m map[string]any) string { return stringField(m, "created_at") },
	"start_time":   func(m map[string]any) string { return stringField(m, "start_time") },
	"end_time":     func(m map[string]any) string { return stringField(m, "end_time") },
	"leaf_num":     func(m map[string]any) string { return stringField(m, "leaf_num") },
}

func validTraceColumns() []string {
	cols := make([]string, 0, len(traceColumnExtractors))
	for k := range traceColumnExtractors {
		cols = append(cols, k)
	}
	sort.Strings(cols)
	return cols
}

func parseTraceColumns(spec string) ([]string, error) {
	if spec == "" {
		return nil, nil
	}
	parts := strings.Split(spec, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		name := strings.TrimSpace(p)
		if name == "" {
			continue
		}
		if _, ok := traceColumnExtractors[name]; !ok {
			return nil, fmt.Errorf("invalid column %q; valid columns: %s",
				name, strings.Join(validTraceColumns(), ", "))
		}
		out = append(out, name)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("--columns must list at least one column")
	}
	return out, nil
}

var traceListCmd = &cobra.Command{
	Use:   "list",
	Short: "List traces with optional filters",
	Long: `List traces with optional filters.

Output formats:
  --format table   (default) human-readable aligned table
  --format json    raw JSON response data
  --format tsv     tab-separated values with a single header row, safe for
                   piping into awk/cut/sort. Uses --columns to pick fields.

Columns available for --columns (TSV only):
  id, type, state, status, project, project_id, group_id,
  last_event, final_event, created_at, start_time, end_time, leaf_num`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAPIContext(true); err != nil {
			return err
		}

		// Resolve --format precedence: explicit flag > -o/--output > "table".
		format := strings.ToLower(strings.TrimSpace(traceListFormat))
		if format == "" {
			format = strings.ToLower(strings.TrimSpace(flagOutput))
		}
		if format == "" {
			format = "table"
		}
		switch format {
		case "table", "json", "tsv":
		default:
			return fmt.Errorf("invalid --format %q; expected table|json|tsv", format)
		}

		// Resolve columns up-front so invalid values fail before the HTTP call.
		columnsSpec := traceListColumns
		if columnsSpec == "" && format == "tsv" {
			columnsSpec = "id,state,last_event"
		}
		columns, err := parseTraceColumns(columnsSpec)
		if err != nil {
			return err
		}
		if format != "tsv" && traceListColumns != "" {
			output.PrintInfo("--columns is only applied to --format tsv; ignored for " + format)
		}

		c := newClient()

		// Resolve project name to ID if given.
		projectIDStr := ""
		projectName := traceListProject
		if projectName == "" {
			projectName = flagProject
		}
		if projectName != "" {
			id, err := newResolver().ProjectID(projectName)
			if err != nil {
				output.PrintInfo(fmt.Sprintf("Warning: could not resolve project %q, using as-is: %v", projectName, err))
				projectIDStr = projectName
			} else {
				projectIDStr = fmt.Sprintf("%d", id)
			}
		}

		path := "/api/v2/traces"
		params := buildQueryParams(map[string]string{
			"project_id": projectIDStr,
			"state":      traceListState,
			"group_id":   traceListGroupID,
			"page":       intToString(traceListPage),
			"size":       intToString(traceListSize),
		})
		if params != "" {
			path += "?" + params
		}

		var resp client.APIResponse[client.PaginatedData[map[string]any]]
		if err := c.Get(path, &resp); err != nil {
			return err
		}

		switch format {
		case "json":
			output.PrintJSON(resp.Data)
			return nil
		case "tsv":
			return printTracesTSV(columns, resp.Data.Items)
		}

		// Default: table.
		headers := []string{"TRACE-ID", "TYPE", "STATE", "PROJECT", "START-TIME", "LEAF-NUM"}
		var rows [][]string
		for _, item := range resp.Data.Items {
			rows = append(rows, []string{
				traceColumnExtractors["id"](item),
				stringField(item, "type"),
				stringField(item, "state"),
				stringField(item, "project_id"),
				stringField(item, "start_time"),
				stringField(item, "leaf_num"),
			})
		}

		output.PrintTable(headers, rows)
		p := resp.Data.Pagination
		output.PrintInfo(fmt.Sprintf("Page %d/%d (total: %d)", p.Page, p.TotalPages, p.Total))
		return nil
	},
}

// printTracesTSV emits a plain header row followed by tab-separated data rows,
// Kubernetes-style (no leading `#`), so operators can pipe through awk/cut.
func printTracesTSV(columns []string, items []map[string]any) error {
	header := make([]string, len(columns))
	for i, c := range columns {
		header[i] = strings.ToUpper(c)
	}
	if _, err := fmt.Fprintln(os.Stdout, strings.Join(header, "\t")); err != nil {
		return err
	}
	for _, item := range items {
		row := make([]string, len(columns))
		for i, c := range columns {
			row[i] = traceColumnExtractors[c](item)
		}
		if _, err := fmt.Fprintln(os.Stdout, strings.Join(row, "\t")); err != nil {
			return err
		}
	}
	return nil
}

// --- trace get ---

var traceGetCmd = &cobra.Command{
	Use:   "get <trace-id>",
	Short: "Show detailed trace information",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runStdinItems("trace get", "trace get <trace-id>", args, stdinOptions{
			enabled:  traceGetStdin,
			field:    traceGetStdinField,
			failFast: traceGetStdinFailFast,
		}, runTraceGet)
	},
}

var (
	traceGetStdin         bool
	traceGetStdinField    string
	traceGetStdinFailFast bool
)

func runTraceGet(traceID string) error {
		if err := requireAPIContext(true); err != nil {
			return err
		}
		c := newClient()

		path := fmt.Sprintf("/api/v2/traces/%s", traceID)
		var resp client.APIResponse[map[string]any]
		if err := c.Get(path, &resp); err != nil {
			return err
		}

		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(resp.Data)
			return nil
		}

		// Print trace header fields.
		for k, v := range resp.Data {
			if k == "tasks" {
				continue
			}
			fmt.Printf("%-20s %v\n", k+":", v)
		}

		// Print child tasks as a table if present.
		if tasksRaw, ok := resp.Data["tasks"]; ok && tasksRaw != nil {
			fmt.Println()
			fmt.Println("Tasks:")

			// Re-marshal and unmarshal to get typed slice.
			data, err := json.Marshal(tasksRaw)
			if err == nil {
				var tasks []map[string]any
				if json.Unmarshal(data, &tasks) == nil && len(tasks) > 0 {
					headers := []string{"TASK-ID", "TYPE", "STATE", "CREATED"}
					var rows [][]string
					for _, t := range tasks {
						rows = append(rows, []string{
							stringField(t, "task_id"),
							stringField(t, "type"),
							stringField(t, "state"),
							stringField(t, "created_at"),
						})
					}
					output.PrintTable(headers, rows)
				}
			}
		}
		return nil
}

// --- trace watch ---

var traceWatchCmd = &cobra.Command{
	Use:   "watch <trace-id>",
	Short: "Watch trace events via SSE stream",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runStdinItems("trace watch", "trace watch <trace-id>", args, stdinOptions{
			enabled:  traceWatchStdin,
			field:    traceWatchStdinField,
			failFast: traceWatchStdinFailFast,
		}, runTraceWatch)
	},
}

var (
	traceWatchStdin         bool
	traceWatchStdinField    string
	traceWatchStdinFailFast bool
)

func runTraceWatch(traceID string) error {
		ssePath := fmt.Sprintf("/api/v2/traces/%s/stream", traceID)
		reader := client.NewSSEReader(flagServer, ssePath, flagToken)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Handle Ctrl+C.
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt)
		go func() {
			<-sigCh
			cancel()
		}()

		output.PrintInfo(fmt.Sprintf("Watching trace %s (Ctrl+C to stop)...", traceID))

		events, errs := reader.Stream(ctx)

		for {
			select {
			case evt, ok := <-events:
				if !ok {
					return nil
				}
				printSSEEvent(evt)

				// Check for terminal state in event data.
				if isTerminalEvent(evt) {
					output.PrintInfo("Trace reached terminal state.")
					return nil
				}
			case err, ok := <-errs:
				if !ok {
					return nil
				}
				return err
			case <-ctx.Done():
				return nil
			}
		}
}

// --- trace cancel ---

var (
	traceCancelForce  bool
	traceCancelStdin  io.Reader = os.Stdin // hook point for tests
	traceCancelStdout io.Writer = os.Stdout
)

// traceCancelResponseData is the best-effort shape expected from a future
// cancel endpoint. Fields are all optional — we print whichever ones come back.
type traceCancelResponseData struct {
	TraceID           string   `json:"trace_id,omitempty"`
	State             string   `json:"state,omitempty"`
	CancelledTasks    []string `json:"cancelled_tasks,omitempty"`
	DeletedPodChaos   []string `json:"deleted_podchaos,omitempty"`
	RemovedRedisTasks []string `json:"removed_redis_tasks,omitempty"`
	Message           string   `json:"message,omitempty"`
}

var traceCancelCmd = &cobra.Command{
	Use:   "cancel <trace-id>",
	Short: "Cancel a running trace (best-effort)",
	Long: `Cancel a running trace and best-effort clean up its queued tasks and
Kubernetes PodChaos CRDs.

Posts to POST /api/v2/traces/{trace-id}/cancel on the backend. If the server
returns 404/405 the endpoint is not deployed yet (see github issue #91) — the
CLI surfaces a clear error and exits non-zero so automation can fall back to
restarting the producer.`,
	Args: exactArgs(1, "trace cancel <trace-id>"),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAPIContext(true); err != nil {
			return err
		}
		traceID := args[0]

		if !traceCancelForce {
			fmt.Fprintf(traceCancelStdout, "Cancel trace %s? [y/N]: ", traceID)
			var answer string
			// Read a single line; ignore errors (treated as no).
			_, _ = fmt.Fscanln(traceCancelStdin, &answer)
			answer = strings.ToLower(strings.TrimSpace(answer))
			if answer != "y" && answer != "yes" {
				output.PrintInfo("Cancellation aborted.")
				return nil
			}
		}

		c := newClient()
		path := fmt.Sprintf("/api/v2/traces/%s/cancel", traceID)

		var resp client.APIResponse[traceCancelResponseData]
		err := c.Post(path, map[string]any{}, &resp)
		if err != nil {
			// Surface a friendly hint for the "endpoint not there yet" case.
			if apiErr, ok := err.(*client.APIError); ok {
				if apiErr.StatusCode == 404 || apiErr.StatusCode == 405 {
					return fmt.Errorf(
						"server returned %d for %s: cancel endpoint not implemented yet (see issue #91); "+
							"work around by restarting the producer pod",
						apiErr.StatusCode, path,
					)
				}
			}
			return err
		}

		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(resp.Data)
			return nil
		}

		data := resp.Data
		stateMsg := data.State
		if stateMsg == "" {
			stateMsg = "cancelled"
		}
		fmt.Fprintf(traceCancelStdout, "Trace %s: %s\n", traceID, stateMsg)
		if len(data.CancelledTasks) > 0 {
			fmt.Fprintf(traceCancelStdout, "  cancelled tasks: %s\n", strings.Join(data.CancelledTasks, ", "))
		}
		if len(data.RemovedRedisTasks) > 0 {
			fmt.Fprintf(traceCancelStdout, "  removed redis tasks: %s\n", strings.Join(data.RemovedRedisTasks, ", "))
		}
		if len(data.DeletedPodChaos) > 0 {
			fmt.Fprintf(traceCancelStdout, "  deleted PodChaos: %s\n", strings.Join(data.DeletedPodChaos, ", "))
		}
		if data.Message != "" {
			fmt.Fprintf(traceCancelStdout, "  %s\n", data.Message)
		}
		return nil
	},
}

// printSSEEvent formats and prints an SSE event to stdout.
func printSSEEvent(evt client.SSEEvent) {
	ts := time.Now().Format("15:04:05")

	// Try to parse structured data from the event.
	var data map[string]any
	if json.Unmarshal([]byte(evt.Data), &data) == nil {
		taskType := stringField(data, "task_type")
		taskID := stringField(data, "task_id")
		eventName := evt.Event
		if eventName == "" {
			eventName = stringField(data, "event")
		}

		payload := evt.Data
		if taskType != "" || taskID != "" {
			fmt.Printf("[%s] %-18s %-36s %-20s %s\n", ts, taskType, taskID, eventName, payload)
			return
		}
	}

	// Fallback: print raw event.
	if evt.Event != "" {
		fmt.Printf("[%s] event=%s %s\n", ts, evt.Event, evt.Data)
	} else {
		fmt.Printf("[%s] %s\n", ts, evt.Data)
	}
}

// isTerminalEvent checks if the SSE event indicates the trace has finished.
func isTerminalEvent(evt client.SSEEvent) bool {
	if evt.Event == "completed" || evt.Event == "failed" || evt.Event == "cancelled" {
		return true
	}

	var data map[string]any
	if json.Unmarshal([]byte(evt.Data), &data) == nil {
		state := stringField(data, "state")
		switch state {
		case "Completed", "Failed", "Cancelled", "Error":
			return true
		}
	}
	return false
}

func init() {
	traceListCmd.Flags().StringVar(&traceListProject, "project", "", "Filter by project name or ID")
	traceListCmd.Flags().StringVar(&traceListState, "state", "", "Filter by state (Pending, Running, Completed, Failed)")
	traceListCmd.Flags().StringVar(&traceListGroupID, "group-id", "", "Filter by group ID")
	traceListCmd.Flags().IntVar(&traceListPage, "page", 0, "Page number")
	traceListCmd.Flags().IntVar(&traceListSize, "size", 0, "Page size")
	traceListCmd.Flags().StringVar(&traceListFormat, "format", "", "Output format: table|json|tsv (overrides -o)")
	traceListCmd.Flags().StringVar(&traceListColumns, "columns", "",
		"Comma-separated columns for --format tsv (default: id,state,last_event). "+
			"Valid: id,type,state,status,project,project_id,group_id,last_event,final_event,created_at,start_time,end_time,leaf_num")

	traceCancelCmd.Flags().BoolVarP(&traceCancelForce, "force", "f", false, "Skip confirmation prompt")
	addStdinFlags(traceGetCmd, &traceGetStdin, &traceGetStdinField, &traceGetStdinFailFast)
	addStdinFlags(traceWatchCmd, &traceWatchStdin, &traceWatchStdinField, &traceWatchStdinFailFast)

	traceCmd.AddCommand(traceListCmd)
	traceCmd.AddCommand(traceGetCmd)
	traceCmd.AddCommand(traceWatchCmd)
	traceCmd.AddCommand(traceCancelCmd)
}
