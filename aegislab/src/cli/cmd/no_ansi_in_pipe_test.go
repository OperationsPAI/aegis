package cmd

import (
	"regexp"
	"strings"
	"testing"
)

var ansiEscapeRE = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

func assertNoEscapeSequenceInStdout(t *testing.T, label string, output string) {
	t.Helper()
	if ansiEscapeRE.MatchString(output) {
		t.Fatalf("stdout for %s contains ANSI escape sequence:\n%q", label, output)
	}
}

func TestNoAnsiOutputInPipedTopLevelAndListGetCommands(t *testing.T) {
	for _, cmd := range rootCmd.Commands() {
		if cmd == nil || cmd.Hidden || cmd.Deprecated != "" || cmd.Name() == "help" {
			continue
		}

		t.Run("help-"+cmd.Name(), func(t *testing.T) {
			res := runCLI(t, cmd.Name(), "--help")
			if res.code != ExitCodeSuccess {
				t.Fatalf("%s --help failed: code=%d stderr=%q", cmd.Name(), res.code, res.stderr)
			}
			assertNoEscapeSequenceInStdout(t, cmd.Name()+" --help", res.stdout)
		})
	}

	typicalCalls := [][]string{
		{"project", "list"},
		{"project", "get", "sample"},
		{"container", "list"},
		{"container", "get", "sample"},
		{"dataset", "list"},
		{"dataset", "get", "sample"},
		{"eval", "list"},
		{"eval", "get", "1"},
		{"trace", "list"},
		{"trace", "get", "trace-sample"},
		{"task", "list"},
		{"task", "get", "1"},
	}

	for _, args := range typicalCalls {
		label := strings.Join(args, " ")
		t.Run(label, func(t *testing.T) {
			res := runCLI(t, args...)
			assertNoEscapeSequenceInStdout(t, label, res.stdout)
		})
	}
}
