package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"aegis/cli/client"
	"aegis/cli/output"

	"github.com/spf13/cobra"
)

// chaosInjectWatchTimeout matches the server-side sseStreamMaxDuration
// cap. The CLI uses it to refuse trivially-too-large values rather than
// silently waiting past the server's close.
const chaosInjectWatchServerCap = 30 * time.Minute

var chaosInjectWatchTimeout time.Duration

var chaosInjectWatchCmd = &cobra.Command{
	Use:   "watch <id>",
	Short: "Stream chaos injection status events (SSE) until terminal",
	Long: `Open the /v1beta/injections/<id>/events SSE stream and print each
status transition. Exits 0 if the terminal status is succeeded or
destroyed (deleted), 1 otherwise (failed, cancelled, timeout, or
network error).`,
	Args: cobra.ExactArgs(1),
	RunE: runChaosInjectWatch,
}

type chaosInjectWatchEvent struct {
	InjectionID string `json:"injection_id"`
	Status      string `json:"status"`
	ExecState   string `json:"exec_state,omitempty"`
	EmittedAt   string `json:"emitted_at"`
	Attempt     int    `json:"attempt"`
}

func runChaosInjectWatch(_ *cobra.Command, args []string) error {
	id := args[0]
	if chaosInjectWatchTimeout > chaosInjectWatchServerCap {
		return usageErrorf("--timeout %s exceeds server cap %s", chaosInjectWatchTimeout, chaosInjectWatchServerCap)
	}

	server, err := resolveChaosServer()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), chaosInjectWatchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(server, "/")+"/v1beta/injections/"+id+"/events", nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	if flagToken != "" {
		req.Header.Set("Authorization", "Bearer "+flagToken)
	}

	httpClient := &http.Client{Transport: client.TransportFor(resolveTLSOptions())}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("watch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned %d", resp.StatusCode)
	}

	asJSON := output.OutputFormat(flagOutput) == output.FormatJSON
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)

	var (
		eventName    string
		finalStatus  string
		sawTerminal  bool
		sawTimeout   bool
	)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			eventName = ""
		case strings.HasPrefix(line, "event: "):
			eventName = strings.TrimPrefix(line, "event: ")
			if eventName == "timeout" {
				sawTimeout = true
			}
		case strings.HasPrefix(line, "data: "):
			payload := strings.TrimPrefix(line, "data: ")
			if eventName == "timeout" {
				continue
			}
			var ev chaosInjectWatchEvent
			if err := json.Unmarshal([]byte(payload), &ev); err != nil {
				return fmt.Errorf("decode event: %w", err)
			}
			terminal := eventName == "terminal"
			if terminal {
				sawTerminal = true
				finalStatus = ev.Status
			}
			if flagQuiet && !terminal {
				continue
			}
			if asJSON {
				fmt.Println(payload)
			} else {
				printWatchEventHuman(ev, terminal)
			}
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("read stream: %w", err)
	}

	if sawTimeout {
		return timeoutErrorf("watch: server timed out the stream after 30m without terminal status")
	}
	if !sawTerminal {
		return workflowFailureErrorf("watch: stream closed without terminal event")
	}
	switch finalStatus {
	case "succeeded", "destroyed":
		return nil
	default:
		return silentExit(ExitCodeWorkflowFailure)
	}
}

func printWatchEventHuman(ev chaosInjectWatchEvent, terminal bool) {
	tag := ""
	if terminal {
		tag = " [terminal]"
	}
	exec := ""
	if ev.ExecState != "" {
		exec = " exec=" + ev.ExecState
	}
	fmt.Printf("%s attempt=%d status=%s%s%s\n",
		ev.EmittedAt, ev.Attempt, ev.Status, exec, tag)
}

func init() {
	chaosInjectWatchCmd.Flags().DurationVar(&chaosInjectWatchTimeout, "timeout", 30*time.Minute,
		"Client-side stream timeout; must not exceed the 30m server cap")
	chaosInjectCmd.AddCommand(chaosInjectWatchCmd)
}
