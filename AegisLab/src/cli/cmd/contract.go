package cmd

import (
	"errors"
	"strings"

	"aegis/cli/internal/cli/clierr"
	"aegis/cli/internal/cli/exitcode"
	"aegis/cli/output"

	"github.com/spf13/cobra"
)

const (
	ExitCodeSuccess         = exitcode.CodeSuccess
	ExitCodeUnexpected      = exitcode.CodeUnexpected
	ExitCodeUsage           = exitcode.CodeUsage
	ExitCodeAuthFailure     = exitcode.CodeAuthFailure
	ExitCodeMissingEnv      = exitcode.CodeMissingEnv
	ExitCodeWorkflowFailure = exitcode.CodeWorkflowFailure
	ExitCodeTimeout         = exitcode.CodeTimeout
	ExitCodeNotFound        = exitcode.CodeNotFound
	ExitCodeConflict        = exitcode.CodeConflict
	// ExitCodeDedupeSuppressed signals that an inject/regression submission
	// returned HTTP 200 but every batch was deduplicated against an existing
	// injection. No trace_id was produced; the caller should change a spec
	// field or wait for cooldown. See issues #91/#92.
	ExitCodeDedupeSuppressed = exitcode.CodeDedupeSuppressed
	// ExitCodeServerError maps to API 5xx failures.
	ExitCodeServerError = exitcode.CodeServerError
	// ExitCodeDecodeFailure maps to JSON decode failures while decoding API responses.
	ExitCodeDecodeFailure = exitcode.CodeDecodeFailure
)

type exitError = exitcode.Error

func usageErrorf(format string, args ...any) error {
	return exitcode.UsageErrorf(format, args...)
}

func authErrorf(format string, args ...any) error {
	return exitcode.AuthErrorf(format, args...)
}

func missingEnvErrorf(format string, args ...any) error {
	return exitcode.MissingEnvErrorf(format, args...)
}

func workflowFailureErrorf(format string, args ...any) error {
	return exitcode.WorkflowFailureErrorf(format, args...)
}

func timeoutErrorf(format string, args ...any) error {
	return exitcode.TimeoutErrorf(format, args...)
}

func notFoundErrorf(format string, args ...any) error {
	return exitcode.NotFoundErrorf(format, args...)
}

func conflictErrorf(format string, args ...any) error {
	return exitcode.ConflictErrorf(format, args...)
}

func silentExit(code int) error {
	return exitcode.SilentExit(code)
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
	return executeError(err)
}

func executeError(err error) int {
	if err == nil {
		return ExitCodeSuccess
	}
	var cliErr *clierr.CLIError
	if errors.As(err, &cliErr) {
		output.PrintCLIError(cliErr, output.OutputFormat(flagOutput))
		return exitCodeFor(err)
	}
	// Generated apiclient errors carry the response body + status; lift
	// them into the structured CLIError envelope so --output=json
	// consumers keep getting type/request_id/exit_code fields.
	if synth := apiClientCLIError(err); synth != nil {
		output.PrintCLIError(synth, output.OutputFormat(flagOutput))
		return synth.ExitCode
	}
	if msg := errorMessage(err); msg != "" {
		output.PrintError(msg)
	}
	return exitCodeFor(err)
}

func errorMessage(err error) string {
	if err == nil {
		return ""
	}
	return exitcode.ErrorMessage(err)
}

func exitCodeFor(err error) int {
	return exitcode.ForError(err)
}
