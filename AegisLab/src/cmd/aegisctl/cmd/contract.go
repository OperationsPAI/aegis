package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"aegis/cmd/aegisctl/client"
	"aegis/cmd/aegisctl/output"

	"github.com/spf13/cobra"
)

const (
	ExitCodeSuccess         = 0
	ExitCodeUnexpected      = 1
	ExitCodeUsage           = 2
	ExitCodeAuthFailure     = 3
	ExitCodeMissingEnv      = 4
	ExitCodeWorkflowFailure = 5
	ExitCodeTimeout         = 6
	ExitCodeNotFound        = 7
	ExitCodeConflict        = 8
	// ExitCodeDedupeSuppressed signals that an inject/regression submission
	// returned HTTP 200 but every batch was deduplicated against an existing
	// injection. No trace_id was produced; the caller should change a spec
	// field or wait for cooldown. See issues #91/#92.
	ExitCodeDedupeSuppressed = 9
)

type exitError struct {
	Code    int
	Message string
	Cause   error
}

func (e *exitError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return fmt.Sprintf("command failed with exit code %d", e.Code)
}

func (e *exitError) Unwrap() error {
	return e.Cause
}

func usageErrorf(format string, args ...any) error {
	return &exitError{Code: ExitCodeUsage, Message: fmt.Sprintf(format, args...)}
}

func authErrorf(format string, args ...any) error {
	return &exitError{Code: ExitCodeAuthFailure, Message: fmt.Sprintf(format, args...)}
}

func missingEnvErrorf(format string, args ...any) error {
	return &exitError{Code: ExitCodeMissingEnv, Message: fmt.Sprintf(format, args...)}
}

func workflowFailureErrorf(format string, args ...any) error {
	return &exitError{Code: ExitCodeWorkflowFailure, Message: fmt.Sprintf(format, args...)}
}

func timeoutErrorf(format string, args ...any) error {
	return &exitError{Code: ExitCodeTimeout, Message: fmt.Sprintf(format, args...)}
}

func notFoundErrorf(format string, args ...any) error {
	return &exitError{Code: ExitCodeNotFound, Message: fmt.Sprintf(format, args...)}
}

func conflictErrorf(format string, args ...any) error {
	return &exitError{Code: ExitCodeConflict, Message: fmt.Sprintf(format, args...)}
}

func silentExit(code int) error {
	return &exitError{Code: code}
}

func exactArgs(count int, usage string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) != count {
			if usage == "" {
				usage = cmd.Use
			}
			return usageErrorf("expected %d argument(s); usage: %s", count, usage)
		}
		return nil
	}
}

func requireNoArgs(cmd *cobra.Command, args []string) error {
	if len(args) != 0 {
		return usageErrorf("unexpected arguments: %s", strings.Join(args, " "))
	}
	return nil
}

func requireServer() error {
	if flagServer == "" {
		return missingEnvErrorf("--server or AEGIS_SERVER is required")
	}
	return nil
}

func requireToken() error {
	if flagToken == "" {
		return authErrorf("--token, AEGIS_TOKEN, or an authenticated aegisctl context is required")
	}
	return nil
}

func requireAPIContext(needsToken bool) error {
	if err := requireServer(); err != nil {
		return err
	}
	if needsToken {
		return requireToken()
	}
	return nil
}

func executeArgs(args []string) int {
	setupDryRunRegistry()
	rootCmd.SetArgs(args)
	err := rootCmd.Execute()
	rootCmd.SetArgs(nil)
	if err == nil {
		return ExitCodeSuccess
	}

	if msg := errorMessage(err); msg != "" {
		output.PrintError(msg)
	}
	return exitCodeFor(err)
}

func errorMessage(err error) string {
	var ee *exitError
	if errors.As(err, &ee) {
		return ee.Message
	}
	return err.Error()
}

func exitCodeFor(err error) int {
	var ee *exitError
	if errors.As(err, &ee) {
		return ee.Code
	}

	var apiErr *client.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case 401, 403:
			return ExitCodeAuthFailure
		case 404:
			return ExitCodeNotFound
		case 409:
			return ExitCodeConflict
		}
	}

	var execErr *exec.Error
	if errors.As(err, &execErr) || errors.Is(err, exec.ErrNotFound) {
		return ExitCodeMissingEnv
	}
	if os.IsNotExist(err) {
		return ExitCodeMissingEnv
	}

	if strings.Contains(err.Error(), "unknown flag") ||
		strings.Contains(err.Error(), "unknown command") ||
		strings.Contains(err.Error(), "requires a subcommand") ||
		strings.Contains(err.Error(), "expected ") ||
		strings.Contains(err.Error(), "requires at least") ||
		strings.Contains(err.Error(), "accepts ") {
		return ExitCodeUsage
	}

	return ExitCodeUnexpected
}
