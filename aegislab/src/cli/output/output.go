package output

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"aegis/cli/internal/cli/clierr"

	"golang.org/x/term"
)

// Quiet suppresses informational messages when true.
var Quiet bool

var colorDisabled = false

// isTerminal is overridden in tests to emulate tty behavior.
var isTerminal = term.IsTerminal

// SetNoColor disables ANSI color codes regardless of TTY status.
func SetNoColor(v bool) {
	colorDisabled = v
}

// IsStdoutColor returns true when stdout should be colored.
func IsStdoutColor() bool {
	return supportsANSI(os.Stdout)
}

// IsStderrColor returns true when stderr should be colored.
func IsStderrColor() bool {
	return supportsANSI(os.Stderr)
}

func supportsANSI(file *os.File) bool {
	if colorDisabled || os.Getenv("NO_COLOR") != "" {
		return false
	}
	return isTerminal(int(file.Fd()))
}

// Colorize wraps s with ANSI color code when enabled for file.
func Colorize(file *os.File, code, value string) string {
	if !supportsANSI(file) {
		return value
	}
	return "\x1b[" + code + "m" + value + "\x1b[0m"
}

// ColorGreen returns a green colored value when allowed.
func ColorGreen(file *os.File, value string) string {
	return Colorize(file, "32", value)
}

// ColorRed returns a red colored value when allowed.
func ColorRed(file *os.File, value string) string {
	return Colorize(file, "31", value)
}

// OutputFormat represents the output format type.
type OutputFormat string

const (
	FormatTable  OutputFormat = "table"
	FormatJSON   OutputFormat = "json"
	FormatNDJSON OutputFormat = "ndjson"
)

func IsJSONOutput(format OutputFormat) bool {
	return format == FormatJSON || format == FormatNDJSON
}

// PrintJSON writes v as indented JSON to stdout.
func PrintJSON(v any) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		PrintError(err.Error())
		return
	}
	fmt.Fprintln(os.Stdout, string(data))
}

// PrintNDJSON writes each item as one line of compact JSON to stdout.
func PrintNDJSON[T any](items []T) error {
	for _, item := range items {
		data, err := json.Marshal(item)
		if err != nil {
			PrintError(err.Error())
			return err
		}
		fmt.Fprintln(os.Stdout, string(data))
	}
	return nil
}

// PrintMetaJSON emits metadata as a single-line JSON object on stderr.
func PrintMetaJSON(meta any) error {
	envelope := map[string]any{"_meta": meta}
	data, err := json.Marshal(envelope)
	if err != nil {
		PrintError(err.Error())
		return err
	}
	fmt.Fprintln(os.Stderr, string(data))
	return nil
}

// PrintTable renders a simple ASCII table to stdout.
func PrintTable(headers []string, rows [][]string) {
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, strings.Join(headers, "\t"))
	for _, row := range rows {
		fmt.Fprintln(w, strings.Join(row, "\t"))
	}
	_ = w.Flush()
}

// PrintInfo writes an informational message to stderr (suppressed when Quiet).
func PrintInfo(msg string) {
	if !Quiet {
		fmt.Fprintln(os.Stderr, msg)
	}
}

// PrintError writes an error message to stderr.
func PrintError(msg string) {
	fmt.Fprintf(os.Stderr, "Error: %s\n", msg)
}

// PrintCLIError renders a structured CLI error according to output format.
//
//   - JSON / NDJSON: single-line machine-readable payload on stderr.
//   - table/text: human-readable multiline output with cause/hint hints.
func PrintCLIError(e *clierr.CLIError, format OutputFormat) {
	if e == nil {
		return
	}

	switch {
	case IsJSONOutput(format):
		payload, err := json.Marshal(e)
		if err != nil {
			fmt.Fprintf(os.Stderr, "{\"type\":\"internal\",\"message\":\"encoding error\"}\n")
			return
		}
		fmt.Fprintln(os.Stderr, string(payload))
	default:
		fmt.Fprintf(os.Stderr, "Error [%s]: %s", e.Type, e.Message)
		if e.Cause != "" {
			fmt.Fprintf(os.Stderr, "\n  cause: %s", e.Cause)
		}
		if e.Suggestion != "" {
			fmt.Fprintf(os.Stderr, "\n  hint: %s", e.Suggestion)
		}
		if e.RequestID != "" && strings.TrimSpace(e.Message) != "" && !strings.Contains(e.Message, "request_id=") {
			fmt.Fprintf(os.Stderr, "\n  request_id=%s", e.RequestID)
		}
		if e.Retryable {
			fmt.Fprintf(os.Stderr, "\n  retryable: true")
		}
		fmt.Fprintln(os.Stderr)
	}
}
