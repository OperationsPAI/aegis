package cmd

import (
	"strings"
	"testing"

	"aegis/cmd/aegisctl/client"
)

func TestInjectTaskTraceWaitStdin(t *testing.T) {
	resetCLIState()

	t.Run("rejects positional args when stdin is enabled", func(t *testing.T) {
		cases := []struct {
			name string
			run  func() error
		}{
			{
				name: "inject get",
				run: func() error {
					injectGetStdin = true
					return injectGetCmd.RunE(injectGetCmd, []string{"inject-a"})
				},
			},
			{
				name: "task get",
				run: func() error {
					taskGetStdin = true
					return taskGetCmd.RunE(taskGetCmd, []string{"task-a"})
				},
			},
			{
				name: "trace get",
				run: func() error {
					traceGetStdin = true
					return traceGetCmd.RunE(traceGetCmd, []string{"trace-a"})
				},
			},
			{
				name: "wait",
				run: func() error {
					waitStdin = true
					return waitCmd.RunE(waitCmd, []string{"trace-a"})
				},
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				resetCLIState()
				err := tc.run()
				if code := exitCodeFor(err); code != ExitCodeUsage {
					t.Fatalf("exitCodeFor(%s) = %d, want %d", tc.name, code, ExitCodeUsage)
				}
			})
		}
	})

	t.Run("wait reads trace_id from ndjson by default", func(t *testing.T) {
		resetCLIState()
		commandStdin = strings.NewReader("{\"trace_id\":\"trace-123\"}\n")
		waitStdin = true
		flagServer = "http://example.test"
		flagToken = "tok"
		waitTimeout = 1
		waitInterval = 1

		oldDetect := waitDetectResourceType
		oldPoll := waitPollState
		t.Cleanup(func() {
			waitDetectResourceType = oldDetect
			waitPollState = oldPoll
			commandStdin = nil
		})

		waitDetectResourceType = func(c *client.Client, id string) (string, error) {
			if id != "trace-123" {
				t.Fatalf("id = %q, want trace-123", id)
			}
			return "trace", nil
		}
		waitPollState = func(c *client.Client, resourceType, id string) (string, any, error) {
			return "Completed", map[string]any{"id": id, "state": "Completed"}, nil
		}

		_, stderr, err := captureStdIO(t, func() error {
			return waitCmd.RunE(waitCmd, nil)
		})
		if err != nil {
			t.Fatalf("waitCmd.RunE: %v", err)
		}
		if !strings.Contains(stderr, "Waiting for trace-123") {
			t.Fatalf("stderr = %q, want wait progress for stdin item", stderr)
		}
	})
}
