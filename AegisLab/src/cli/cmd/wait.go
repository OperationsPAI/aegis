package cmd

import (
	"fmt"
	"net/http"
	"time"

	"aegis/cli/client"
	"aegis/cli/output"

	"github.com/spf13/cobra"
)

var (
	waitTimeout       int
	waitInterval      int
	waitStdin         bool
	waitStdinField    string
	waitStdinFailFast bool
)

// waitDetectResourceType / waitPollState are package-level indirections so
// stdin_command_test can stub them. Their *client.Client argument is unused
// by the production implementations (which build their own typed client) but
// kept in the signature to preserve the test contract.
var (
	waitDetectResourceType = detectResourceType
	waitPollState          = pollState
)

var waitCmd = &cobra.Command{
	Use:   "wait <trace-id|task-id>",
	Short: "Block until a trace or task reaches a terminal state",
	Long: `Block until a trace or task reaches a terminal state.

Automatically detects whether the ID refers to a trace or task.
Polls the API at regular intervals and prints status updates to stderr.

EXIT CODES:
  0 — Completed successfully
  5 — Failed, Error, or Cancelled
  6 — Timeout (exceeded --timeout)

EXAMPLES:
  # Wait for a trace to complete (default timeout: 600s)
  aegisctl wait <trace-id>

  # Wait with custom timeout and poll interval
  aegisctl wait <trace-id> --timeout 1200 --interval 10

  # Use in scripts to chain operations (guided --apply prints trace_id in its
  # JSON response)
  aegisctl inject guided --apply --project my_project \
    --pedestal-name ts --pedestal-tag 1.0.0 \
    --benchmark-name otel-demo-bench --benchmark-tag 1.0.0 | \
    jq -r '.items[0].trace_id' | xargs aegisctl wait`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runStdinItems("wait", "wait <trace-id|task-id>", args, stdinOptions{
			enabled:  waitStdin,
			field:    waitStdinField,
			failFast: waitStdinFailFast,
		}, runWait)
	},
}

func runWait(id string) error {
	if err := requireAPIContext(true); err != nil {
		return err
	}
	c := newClient() // retained for the test-injected detect/poll hooks

	resourceType, err := waitDetectResourceType(c, id)
	if err != nil {
		return err
	}

	deadline := time.Now().Add(time.Duration(waitTimeout) * time.Second)
	interval := time.Duration(waitInterval) * time.Second
	start := time.Now()

	for {
		state, data, err := waitPollState(c, resourceType, id)
		if err != nil {
			return fmt.Errorf("poll %s %s: %w", resourceType, id, err)
		}

		elapsed := time.Since(start).Truncate(time.Second)
		output.PrintInfo(fmt.Sprintf("Waiting for %s... [%s] %s", id, state, elapsed))

		if isTerminal(resourceType, state) {
			if output.OutputFormat(flagOutput) == output.FormatJSON {
				output.PrintJSON(data)
			} else {
				output.PrintTable(
					[]string{"ID", "TYPE", "STATE", "ELAPSED"},
					[][]string{{id, resourceType, state, elapsed.String()}},
				)
			}

			switch state {
			case "Completed":
				return nil
			case "Failed", "Error":
				return workflowFailureErrorf("%s %s reached terminal state %s", resourceType, id, state)
			case "Cancelled":
				return workflowFailureErrorf("%s %s reached terminal state %s", resourceType, id, state)
			}
			return nil
		}

		if time.Now().After(deadline) {
			return timeoutErrorf("timeout after %ds waiting for %s %s (last state: %s)", waitTimeout, resourceType, id, state)
		}

		time.Sleep(interval)
	}
}

// detectResourceType probes the API to determine if the id refers to a trace
// or a task. Returns "trace" or "task". The legacy *client.Client argument
// is unused (kept so tests can swap the function var).
func detectResourceType(_ *client.Client, id string) (string, error) {
	cli, ctx := newAPIClient()

	_, traceHTTP, traceErr := cli.TracesAPI.GetTraceById(ctx, id).Execute()
	if traceErr == nil {
		return "trace", nil
	}
	if traceHTTP == nil || traceHTTP.StatusCode != http.StatusNotFound {
		return "", fmt.Errorf("lookup trace %s: %w", id, traceErr)
	}
	_, taskHTTP, taskErr := cli.TasksAPI.GetTaskById(ctx, id).Execute()
	if taskErr == nil {
		return "task", nil
	}
	if taskHTTP != nil && taskHTTP.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("neither trace nor task found for id %q", id)
	}
	return "", fmt.Errorf("lookup task %s: %w", id, taskErr)
}

// pollState fetches the current state and full data for the given resource.
// The *client.Client argument is unused (kept for test injection).
func pollState(_ *client.Client, resourceType, id string) (string, any, error) {
	cli, ctx := newAPIClient()
	switch resourceType {
	case "trace":
		resp, _, err := cli.TracesAPI.GetTraceById(ctx, id).Execute()
		if err != nil {
			return "", nil, err
		}
		d := resp.GetData()
		return d.GetState(), d, nil
	case "task":
		resp, _, err := cli.TasksAPI.GetTaskById(ctx, id).Execute()
		if err != nil {
			return "", nil, err
		}
		d := resp.GetData()
		return d.GetState(), d, nil
	default:
		return "", nil, fmt.Errorf("unknown resource type: %s", resourceType)
	}
}

// isTerminal returns true if the state is a terminal state for the given
// resource type.
func isTerminal(resourceType, state string) bool {
	switch resourceType {
	case "trace":
		return state == "Completed" || state == "Failed"
	case "task":
		return state == "Completed" || state == "Error" || state == "Cancelled"
	}
	return false
}

func init() {
	waitCmd.Flags().IntVar(&waitTimeout, "timeout", 600, "Maximum time to wait in seconds")
	waitCmd.Flags().IntVar(&waitInterval, "interval", 5, "Poll interval in seconds")
	addStdinFlags(waitCmd, &waitStdin, &waitStdinField, &waitStdinFailFast)
}
