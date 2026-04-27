package output

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

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
	FormatTable OutputFormat = "table"
	FormatJSON  OutputFormat = "json"
)

// PrintJSON writes v as indented JSON to stdout.
func PrintJSON(v any) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		PrintError(err.Error())
		return
	}
	fmt.Fprintln(os.Stdout, string(data))
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
