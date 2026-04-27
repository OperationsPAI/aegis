package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"aegis/cmd/aegisctl/output"
	clistdin "aegis/internal/cli/stdin"

	"github.com/spf13/cobra"
)

var commandStdin io.Reader = os.Stdin

type stdinOptions struct {
	enabled  bool
	field    string
	failFast bool
}

func addStdinFlags(cmd *cobra.Command, enabled *bool, field *string, failFast *bool) {
	cmd.Flags().BoolVar(enabled, "stdin", false, "Read batch input from stdin")
	cmd.Flags().StringVar(field, "stdin-field", "", "Field to read from NDJSON stdin objects")
	cmd.Flags().BoolVar(failFast, "fail-fast", false, "Stop stdin batch execution on the first failed item")
}

func stdinItems(commandPath string, args []string, opts stdinOptions) ([]string, bool, error) {
	if !opts.enabled {
		return nil, false, nil
	}
	if len(args) > 0 {
		return nil, true, usageErrorf("--stdin cannot be combined with positional arguments")
	}

	items, err := clistdin.Parse(commandStdin, clistdin.ParseConfig{
		Field:          opts.field,
		FallbackFields: clistdin.DefaultFields(commandPath),
	})
	if err != nil {
		return nil, true, usageErrorf("%v", err)
	}
	if len(items) == 0 {
		return nil, true, usageErrorf("stdin did not provide any items")
	}
	return items, true, nil
}

func singleArgOrStdin(commandPath, usage string, args []string, opts stdinOptions) ([]string, error) {
	if items, handled, err := stdinItems(commandPath, args, opts); handled {
		return items, err
	}
	if len(args) != 1 {
		return nil, usageErrorf("expected 1 argument(s); usage: %s", usage)
	}
	return []string{strings.TrimSpace(args[0])}, nil
}

func runStdinItems(commandPath, usage string, args []string, opts stdinOptions, fn func(string) error) error {
	items, err := singleArgOrStdin(commandPath, usage, args, opts)
	if err != nil {
		return err
	}
	// Single positional invocation: behave as a regular CLI command (no batch
	// progress lines, return the underlying error directly so error renderers
	// see the original type).
	if !opts.enabled && len(items) == 1 {
		return fn(items[0])
	}
	firstErr := error(nil)
	successes := 0
	failures := 0
	verb := batchVerb(commandPath)
	for i, item := range items {
		if err := fn(item); err != nil {
			wrapped := fmt.Errorf("%s %s: %w", commandPath, item, err)
			failures++
			output.PrintInfo(fmt.Sprintf("[%d/%d] %s %s: failed (%s)", i+1, len(items), verb, item, exitType(exitCodeFor(err))))
			if firstErr == nil {
				firstErr = wrapped
			}
			if opts.failFast {
				return firstErr
			}
			continue
		}
		successes++
		output.PrintInfo(fmt.Sprintf("[%d/%d] %s %s: ok", i+1, len(items), verb, item))
	}
	if failures == 0 {
		return nil
	}
	if successes == 0 {
		return firstErr
	}
	return silentExit(ExitCodeDedupeSuppressed)
}

func batchVerb(commandPath string) string {
	parts := strings.Fields(commandPath)
	if len(parts) == 0 {
		return "run"
	}
	return parts[len(parts)-1]
}

func exitType(code int) string {
	switch code {
	case ExitCodeUsage:
		return "usage"
	case ExitCodeAuthFailure:
		return "auth"
	case ExitCodeMissingEnv:
		return "missing-env"
	case ExitCodeWorkflowFailure:
		return "workflow"
	case ExitCodeTimeout:
		return "timeout"
	case ExitCodeNotFound:
		return "not-found"
	case ExitCodeConflict:
		return "conflict"
	default:
		return "unexpected"
	}
}
