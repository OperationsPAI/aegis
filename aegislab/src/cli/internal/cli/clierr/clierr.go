package clierr

import "fmt"

// CLIError is a structured CLI-level error that carries machine-readable
// metadata for both human-facing and programmatic error handling.
type CLIError struct {
	Type       string `json:"type"`
	Message    string `json:"message"`
	Cause      string `json:"cause"`
	RequestID  string `json:"request_id"`
	Suggestion string `json:"suggestion"`
	Retryable  bool   `json:"retryable"`
	ExitCode   int    `json:"exit_code"`
}

func (e *CLIError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("type=%s exit_code=%d", e.Type, e.ExitCode)
}
