package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"time"

	"aegis/cli/apiclient"
	"aegis/cli/client"
	"aegis/cli/output"
	"aegis/platform/consts"

	"github.com/spf13/cobra"
)

// traceCmd is the top-level trace command.
var traceCmd = &cobra.Command{
	Use:   "trace",
	Short: "Monitor and inspect traces",
	Long: `Monitor and inspect execution traces in aegislab.

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
	traceListAll     bool
)

// traceColumnExtractors maps short column names to a function that pulls the
// value out of a typed TraceTraceResp. Keep this list in sync with --columns.
var traceColumnExtractors = map[string]func(apiclient.TraceTraceResp) string{
	"id":          func(t apiclient.TraceTraceResp) string { return t.GetId() },
	"type":        func(t apiclient.TraceTraceResp) string { return t.GetType() },
	"state":       func(t apiclient.TraceTraceResp) string { return t.GetState() },
	"status":      func(t apiclient.TraceTraceResp) string { return t.GetStatus() },
	"project":     func(t apiclient.TraceTraceResp) string { return t.GetProjectName() },
	"project_id":  func(t apiclient.TraceTraceResp) string { return strconv.Itoa(int(t.GetProjectId())) },
	"group_id":    func(t apiclient.TraceTraceResp) string { return t.GetGroupId() },
	"last_event":  func(t apiclient.TraceTraceResp) string { return t.GetLastEvent() },
	"final_event": func(t apiclient.TraceTraceResp) string { return t.GetLastEvent() },
	"created_at":  func(t apiclient.TraceTraceResp) string { return t.GetCreatedAt() },
	"start_time":  func(t apiclient.TraceTraceResp) string { return t.GetStartTime() },
	"end_time":    func(t apiclient.TraceTraceResp) string { return t.GetEndTime() },
	"leaf_num":    func(t apiclient.TraceTraceResp) string { return strconv.Itoa(int(t.GetLeafNum())) },
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

// resolveTraceStateFilter converts a name like "Running" or numeric form to
// the int32 the typed client wants. Trace state enum lives alongside task
// state in consts (RunStatus / TaskState are interchangeable for trace
// reporting); fall back to numeric parse if the name is unknown.
func resolveTraceStateFilter(raw string) (*int32, error) {
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

var traceListCmd = &cobra.Command{
	Use:   "list",
	Short: "List traces with optional filters",
	Long: `List traces with optional filters.

Output formats:
  --format table   (default) human-readable aligned table
  --format json    raw JSON response data
  --format ndjson  one JSON object per line (envelope omitted)
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
		case "table", "json", "ndjson", "tsv":
		default:
			return fmt.Errorf("invalid --format %q; expected table|json|ndjson|tsv", format)
		}

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

		// Resolve project name to ID if given.
		projectIDStr := ""
		var projectIDInt int32
		hasProjectID := false
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
				projectIDInt = int32(id)
				hasProjectID = true
			}
		}

		if traceListAll {
			if format != "ndjson" {
				return fmt.Errorf("--all requires --format/--output ndjson (table/json/tsv buffer the full result set; use ndjson for streaming)")
			}
			c := newClient()
			basePath := consts.APIPathTraces
			baseParams := map[string]string{
				"project_id": projectIDStr,
				"state":      traceListState,
				"group_id":   traceListGroupID,
			}
			return streamListAllNDJSON[map[string]any](c, basePath, baseParams)
		}

		stateFilter, err := resolveTraceStateFilter(traceListState)
		if err != nil {
			return err
		}

		cli, ctx := newAPIClient()
		req := cli.TracesAPI.ListTraces(ctx)
		if traceListPage > 0 {
			req = req.Page(int32(traceListPage))
		}
		if traceListSize > 0 {
			req = req.Size(int32(traceListSize))
		}
		if hasProjectID {
			req = req.ProjectId(projectIDInt)
		}
		if stateFilter != nil {
			req = req.State(*stateFilter)
		}
		if traceListGroupID != "" {
			req = req.GroupId(traceListGroupID)
		}
		resp, _, err := req.Execute()
		if err != nil {
			return err
		}
		data := resp.GetData()
		items := data.GetItems()

		switch format {
		case "json":
			output.PrintJSON(data)
			return nil
		case "tsv":
			return printTracesTSV(columns, items)
		case "ndjson":
			if pg := data.GetPagination(); pg.HasPage() {
				if err := output.PrintMetaJSON(pg); err != nil {
					return err
				}
			}
			return output.PrintNDJSON(items)
		}

		// Default: table.
		headers := []string{"TRACE-ID", "TYPE", "STATE", "PROJECT", "START-TIME", "LEAF-NUM"}
		var rows [][]string
		for _, item := range items {
			rows = append(rows, []string{
				item.GetId(),
				item.GetType(),
				item.GetState(),
				strconv.Itoa(int(item.GetProjectId())),
				item.GetStartTime(),
				strconv.Itoa(int(item.GetLeafNum())),
			})
		}

		output.PrintTable(headers, rows)
		p := data.GetPagination()
		output.PrintInfo(fmt.Sprintf("Page %d/%d (total: %d)", p.GetPage(), p.GetTotalPages(), p.GetTotal()))
		return nil
	},
}

// printTracesTSV emits a plain header row followed by tab-separated data rows,
// Kubernetes-style (no leading `#`), so operators can pipe through awk/cut.
func printTracesTSV(columns []string, items []apiclient.TraceTraceResp) error {
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
	cli, ctx := newAPIClient()
	resp, _, err := cli.TracesAPI.GetTraceById(ctx, traceID).Execute()
	if err != nil {
		return err
	}
	d := resp.GetData()

	if output.OutputFormat(flagOutput) == output.FormatJSON {
		output.PrintJSON(d)
		return nil
	}

	row := func(k, v string) {
		if v != "" {
			fmt.Printf("%-20s %s\n", k+":", v)
		}
	}
	row("id", d.GetId())
	row("type", d.GetType())
	row("state", d.GetState())
	row("status", d.GetStatus())
	row("project_id", strconv.Itoa(int(d.GetProjectId())))
	row("project_name", d.GetProjectName())
	row("group_id", d.GetGroupId())
	row("last_event", d.GetLastEvent())
	row("start_time", d.GetStartTime())
	row("end_time", d.GetEndTime())
	row("created_at", d.GetCreatedAt())
	row("updated_at", d.GetUpdatedAt())
	if d.HasLeafNum() {
		row("leaf_num", strconv.Itoa(int(d.GetLeafNum())))
	}

	if tasks := d.GetTasks(); len(tasks) > 0 {
		fmt.Println()
		fmt.Println("Tasks:")
		headers := []string{"TASK-ID", "TYPE", "STATE", "CREATED"}
		var rows [][]string
		for _, t := range tasks {
			rows = append(rows, []string{
				t.GetId(),
				t.GetType(),
				t.GetState(),
				t.GetCreatedAt(),
			})
		}
		output.PrintTable(headers, rows)
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
	// Trace watch is an SSE stream; the generated GetTraceEvents method
	// returns the body as a single string and cannot deliver an event channel,
	// so keep the manual SSE reader path here.
	ssePath := consts.APIPathTraceStream(traceID)
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
			_, _ = fmt.Fscanln(traceCancelStdin, &answer)
			answer = strings.ToLower(strings.TrimSpace(answer))
			if answer != "y" && answer != "yes" {
				output.PrintInfo("Cancellation aborted.")
				return nil
			}
		}

		cli, ctx := newAPIClient()
		resp, httpResp, err := cli.TracesAPI.CancelTrace(ctx, traceID).Execute()
		if err != nil {
			if httpResp != nil && (httpResp.StatusCode == 404 || httpResp.StatusCode == 405) {
				return fmt.Errorf(
					"server returned %d for cancel %s: cancel endpoint not implemented yet (see issue #91); "+
						"work around by restarting the producer pod",
					httpResp.StatusCode, traceID,
				)
			}
			return err
		}

		data := resp.GetData()
		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(data)
			return nil
		}

		stateMsg := data.GetState()
		if stateMsg == "" {
			stateMsg = "cancelled"
		}
		fmt.Fprintf(traceCancelStdout, "Trace %s: %s\n", traceID, stateMsg)
		if ct := data.GetCancelledTasks(); len(ct) > 0 {
			fmt.Fprintf(traceCancelStdout, "  cancelled tasks: %s\n", strings.Join(ct, ", "))
		}
		if rt := data.GetRemovedRedisTasks(); len(rt) > 0 {
			fmt.Fprintf(traceCancelStdout, "  removed redis tasks: %s\n", strings.Join(rt, ", "))
		}
		if dp := data.GetDeletedPodchaos(); len(dp) > 0 {
			fmt.Fprintf(traceCancelStdout, "  deleted PodChaos: %s\n", strings.Join(dp, ", "))
		}
		if msg := data.GetMessage(); msg != "" {
			fmt.Fprintf(traceCancelStdout, "  %s\n", msg)
		}
		return nil
	},
}

// printSSEEvent formats and prints an SSE event to stdout.
func printSSEEvent(evt client.SSEEvent) {
	ts := time.Now().Format("15:04:05")

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
	traceListCmd.Flags().BoolVar(&traceListAll, "all", false, "Stream every page as NDJSON to stdout (ignores --page/--size; requires --output ndjson)")
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
