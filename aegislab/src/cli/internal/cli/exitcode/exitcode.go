package exitcode

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"aegis/cli/apiclient"
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
		if c := codeForHTTPStatus(apiErr.StatusCode); c != 0 {
			return c
		}
	}

	// The generated apiclient surfaces HTTP errors as *GenericOpenAPIError
	// whose Error() string starts with the status line (e.g. "404 Not Found").
	// Parse the leading status code so the same exit-code contract holds.
	var apiErr2 *apiclient.GenericOpenAPIError
	if errors.As(err, &apiErr2) {
		if status := leadingHTTPStatus(apiErr2.Error()); status != 0 {
			if c := codeForHTTPStatus(status); c != 0 {
				return c
			}
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

	// JSON decode errors from the generated apiclient surface as bare
	// "invalid character ..." / "json: cannot unmarshal ..." strings.
	msg := err.Error()
	if strings.HasPrefix(msg, "invalid character ") || strings.HasPrefix(msg, "json: cannot unmarshal") {
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

// codeForHTTPStatus maps an HTTP status code to the CLI's exit-code contract.
// Returns 0 if the status doesn't fall in the 4xx/5xx error band.
func codeForHTTPStatus(status int) int {
	switch {
	case status == 401 || status == 403:
		return CodeAuthFailure
	case status == 404:
		return CodeNotFound
	case status == 409:
		return CodeConflict
	case status >= 400 && status <= 499:
		return CodeUsage
	case status >= 500 && status <= 599:
		return CodeServerError
	}
	return 0
}

// leadingHTTPStatus parses the leading status code from an apiclient
// error string like "404 Not Found" or "500 Internal Server Error".
// Returns 0 if the prefix isn't a recognizable status.
func leadingHTTPStatus(s string) int {
	first := s
	if idx := strings.IndexByte(s, ' '); idx > 0 {
		first = s[:idx]
	}
	n, err := strconv.Atoi(first)
	if err != nil || n < 100 || n > 599 {
		return 0
	}
	return n
}
