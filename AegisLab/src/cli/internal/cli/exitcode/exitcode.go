package exitcode

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"aegis/cli/client"
	"aegis/cli/internal/cli/clierr"
)

const (
	CodeSuccess          = 0
	CodeUnexpected       = 1
	CodeUsage            = 2
	CodeAuthFailure      = 3
	CodeMissingEnv       = 4
	CodeWorkflowFailure  = 5
	CodeTimeout          = 6
	CodeNotFound         = 7
	CodeConflict         = 8
	CodeDedupeSuppressed = 9
	CodeServerError      = 10
	CodeDecodeFailure    = 11
)

type Error struct {
	Code    int
	Message string
	Cause   error
}

func (e *Error) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return fmt.Sprintf("command failed with exit code %d", e.Code)
}

func (e *Error) Unwrap() error {
	return e.Cause
}

func UsageErrorf(format string, args ...any) error {
	return &Error{Code: CodeUsage, Message: fmt.Sprintf(format, args...)}
}

func AuthErrorf(format string, args ...any) error {
	return &Error{Code: CodeAuthFailure, Message: fmt.Sprintf(format, args...)}
}

func MissingEnvErrorf(format string, args ...any) error {
	return &Error{Code: CodeMissingEnv, Message: fmt.Sprintf(format, args...)}
}

func WorkflowFailureErrorf(format string, args ...any) error {
	return &Error{Code: CodeWorkflowFailure, Message: fmt.Sprintf(format, args...)}
}

func TimeoutErrorf(format string, args ...any) error {
	return &Error{Code: CodeTimeout, Message: fmt.Sprintf(format, args...)}
}

func NotFoundErrorf(format string, args ...any) error {
	return &Error{Code: CodeNotFound, Message: fmt.Sprintf(format, args...)}
}

func ConflictErrorf(format string, args ...any) error {
	return &Error{Code: CodeConflict, Message: fmt.Sprintf(format, args...)}
}

func DedupeSuppressedError(message string) error {
	return &Error{Code: CodeDedupeSuppressed, Message: message}
}

func SilentExit(code int) error {
	return &Error{Code: code}
}

func ErrorMessage(err error) string {
	var ee *Error
	if errors.As(err, &ee) {
		return ee.Message
	}
	return err.Error()
}

func ForError(err error) int {
	if err == nil {
		return CodeSuccess
	}

	var ee *Error
	if errors.As(err, &ee) {
		return ee.Code
	}

	var apiErr *client.APIError
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.StatusCode == 401 || apiErr.StatusCode == 403:
			return CodeAuthFailure
		case apiErr.StatusCode == 404:
			return CodeNotFound
		case apiErr.StatusCode == 409:
			return CodeConflict
		case apiErr.StatusCode >= 400 && apiErr.StatusCode <= 499:
			return CodeUsage
		case apiErr.StatusCode >= 500 && apiErr.StatusCode <= 599:
			return CodeServerError
		}
	}

	var notFoundErr *client.NotFoundError
	if errors.As(err, &notFoundErr) {
		return CodeNotFound
	}

	var cliErr *clierr.CLIError
	if errors.As(err, &cliErr) {
		if cliErr.ExitCode != 0 {
			return cliErr.ExitCode
		}
		if cliErr.Type == "decode" {
			return CodeDecodeFailure
		}
	}

	var execErr *exec.Error
	if errors.As(err, &execErr) || errors.Is(err, exec.ErrNotFound) {
		return CodeMissingEnv
	}
	if os.IsNotExist(err) {
		return CodeMissingEnv
	}

	if strings.HasPrefix(err.Error(), "decode response:") {
		return CodeDecodeFailure
	}

	if strings.Contains(err.Error(), "unknown flag") ||
		strings.Contains(err.Error(), "unknown command") ||
		strings.Contains(err.Error(), "requires a subcommand") ||
		strings.Contains(err.Error(), "flag needs an argument") ||
		strings.Contains(err.Error(), "invalid argument ") ||
		strings.Contains(err.Error(), "expected ") ||
		strings.Contains(err.Error(), "requires at least") ||
		strings.Contains(err.Error(), "accepts ") {
		return CodeUsage
	}

	return CodeUnexpected
}
