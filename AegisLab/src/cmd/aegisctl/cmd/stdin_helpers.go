package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	clistdin "aegis/internal/cli/stdin"

	"github.com/spf13/cobra"
)

var commandStdin io.Reader = os.Stdin

type stdinOptions struct {
	enabled bool
	field   string
}

func addStdinFlags(cmd *cobra.Command, enabled *bool, field *string) {
	cmd.Flags().BoolVar(enabled, "stdin", false, "Read batch input from stdin")
	cmd.Flags().StringVar(field, "stdin-field", "", "Field to read from NDJSON stdin objects")
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
	for _, item := range items {
		if err := fn(item); err != nil {
			return fmt.Errorf("%s %s: %w", commandPath, item, err)
		}
	}
	return nil
}
