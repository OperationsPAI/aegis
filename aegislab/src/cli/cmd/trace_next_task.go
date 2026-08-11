package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"

	"aegis/cli/apiclient"
	"aegis/cli/output"

	"github.com/spf13/cobra"
)

// traceNextTaskStdout / traceNextTaskStderr are hooks for tests so they can
// redirect the scriptable stdout stream (task-id) vs. the human-readable
// stderr stream (info messages) independently.
var (
	traceNextTaskStdout io.Writer = os.Stdout
	traceNextTaskStderr io.Writer = os.Stderr
)

// traceTask is the minimum shape we need from a task entry in the
// `GET /api/v2/traces/{id}` response to pick the next pending one.
type traceTask struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	State       string `json:"state"`
	ExecuteTime int32  `json:"execute_time"`
	CreatedAt   string `json:"created_at"`
}

// resolveNextPendingTask fetches a trace by ID and returns the next task with
// state == "Pending". "Next" is ordered by execute_time (ascending) with
// created_at as a stable tiebreaker — matches how the scheduler picks tasks
// off the delayed/ready queues. Returns a notFoundErrorf-wrapped error (exit
// code 7) when no pending task exists so scripts can branch on it.
func resolveNextPendingTask(traceID string) (*traceTask, error) {
	cli, ctx := newAPIClient()
	resp, _, err := cli.TracesAPI.GetTraceById(ctx, traceID).Execute()
	if err != nil {
		return nil, err
	}
	d := resp.GetData()
	tasks := d.GetTasks()
	if len(tasks) == 0 {
		return nil, notFoundErrorf("trace %s has no tasks", traceID)
	}

	pending := make([]apiclient.DtoTaskResp, 0, len(tasks))
	for _, t := range tasks {
		if t.GetState() == "Pending" {
			pending = append(pending, t)
		}
	}
	if len(pending) == 0 {
		return nil, notFoundErrorf("trace %s has no pending task", traceID)
	}

	sort.SliceStable(pending, func(i, j int) bool {
		if pending[i].GetExecuteTime() != pending[j].GetExecuteTime() {
			return pending[i].GetExecuteTime() < pending[j].GetExecuteTime()
		}
		return pending[i].GetCreatedAt() < pending[j].GetCreatedAt()
	})

	t := pending[0]
	return &traceTask{
		ID:          t.GetId(),
		Type:        t.GetType(),
		State:       t.GetState(),
		ExecuteTime: t.GetExecuteTime(),
		CreatedAt:   t.GetCreatedAt(),
	}, nil
}

// --- trace next-task ---

var traceNextTaskCmd = &cobra.Command{
	Use:   "next-task <trace-id>",
	Short: "Print the next pending task id for a trace",
	Long: `Resolve the next pending child task for a trace and print its ID.

The task ID is written to stdout (scriptable). Any human-readable messages
(including JSON payloads when -o json is passed) are written to stderr-only
when they would interfere with scripts — with -o json the JSON goes to stdout
because that is the only output the caller asked for.

Exits with ExitCodeNotFound (7) when the trace has no pending task.`,
	Args: exactArgs(1, "trace next-task <trace-id>"),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAPIContext(true); err != nil {
			return err
		}

		task, err := resolveNextPendingTask(args[0])
		if err != nil {
			return err
		}

		if output.OutputFormat(flagOutput) == output.FormatJSON {
			enc := json.NewEncoder(traceNextTaskStdout)
			enc.SetIndent("", "  ")
			return enc.Encode(task)
		}

		fmt.Fprintln(traceNextTaskStdout, task.ID)
		fmt.Fprintf(traceNextTaskStderr, "next pending task: id=%s type=%s\n", task.ID, task.Type)
		return nil
	},
}

// --- trace expedite ---

var traceExpediteCmd = &cobra.Command{
	Use:   "expedite <trace-id>",
	Short: "Expedite the next pending task of a trace",
	Long: `Compose trace next-task + task expedite.

Resolves the next pending child task of the trace, then POSTs to
/api/v2/tasks/{id}/expedite (same endpoint as 'aegisctl task expedite').
Exits with ExitCodeNotFound (7) without posting if the trace has no pending
task.`,
	Args: exactArgs(1, "trace expedite <trace-id>"),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAPIContext(true); err != nil {
			return err
		}

		task, err := resolveNextPendingTask(args[0])
		if err != nil {
			return err
		}

		cli, ctx := newAPIClient()
		resp, _, err := cli.TasksAPI.ExpediteTask(ctx, task.ID).Execute()
		if err != nil {
			return err
		}

		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(map[string]any{
				"trace_id": args[0],
				"task_id":  task.ID,
				"type":     task.Type,
				"response": resp.GetData(),
			})
			return nil
		}

		output.PrintInfo(fmt.Sprintf("Trace %s: expedited task %s (type=%s)", args[0], task.ID, task.Type))
		return nil
	},
}

func init() {
	traceCmd.AddCommand(traceNextTaskCmd)
	traceCmd.AddCommand(traceExpediteCmd)
}
