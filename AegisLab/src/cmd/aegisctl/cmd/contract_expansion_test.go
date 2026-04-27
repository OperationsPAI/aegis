package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestDryRunOnUnsupportedCommandErrors — `status --dry-run` must exit 2 and
// mention --dry-run in stderr. status never consumed flagDryRun before, so
// silently succeeding would give a false sense of having dry-run worked.
func TestDryRunOnUnsupportedCommandErrors(t *testing.T) {
	res := runCLI(t, "status", "--dry-run")
	if res.code != ExitCodeUsage {
		t.Fatalf("exit code = %d, want %d; stderr=%q", res.code, ExitCodeUsage, res.stderr)
	}
	if !strings.Contains(res.stderr, "--dry-run") {
		t.Fatalf("stderr = %q, want --dry-run diagnostic", res.stderr)
	}
}

// TestSchemaDumpEmitsValidJSON — `schema dump` must emit parseable JSON with a
// `commands` array that at least contains `aegisctl auth login`.
func TestSchemaDumpEmitsValidJSON(t *testing.T) {
	res := runCLI(t, "schema", "dump")
	if res.code != ExitCodeSuccess {
		t.Fatalf("exit code = %d, want %d; stderr=%q", res.code, ExitCodeSuccess, res.stderr)
	}
	var doc struct {
		Version   string `json:"version"`
		Commands  []struct {
			Path string `json:"path"`
		} `json:"commands"`
		ExitCodes map[string]string `json:"exit_codes"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &doc); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\nstdout=%q", err, res.stdout)
	}
	if doc.Version == "" {
		t.Fatalf("schema document missing version; doc=%+v", doc)
	}
	if len(doc.Commands) == 0 {
		t.Fatalf("schema document has no commands")
	}
	found := false
	for _, c := range doc.Commands {
		if c.Path == "aegisctl auth login" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("schema document missing 'aegisctl auth login'; commands=%+v", doc.Commands)
	}
	if _, ok := doc.ExitCodes["7"]; !ok {
		t.Fatalf("schema document missing exit code 7 entry")
	}
}

// TestSchemaDumpCommandsPathsAreUnique — every command path in schema output must
// be unique; duplicates indicate duplicated registration in command tree.
func TestSchemaDumpCommandsPathsAreUnique(t *testing.T) {
	res := runCLI(t, "schema", "dump")
	if res.code != ExitCodeSuccess {
		t.Fatalf("exit code = %d, want %d; stderr=%q", res.code, ExitCodeSuccess, res.stderr)
	}

	var doc struct {
		Commands []struct {
			Path string `json:"path"`
		} `json:"commands"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &doc); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\nstdout=%q", err, res.stdout)
	}

	paths := make(map[string]struct{})
	for _, c := range doc.Commands {
		if _, ok := paths[c.Path]; ok {
			t.Fatalf("duplicate schema command path: %q", c.Path)
		}
		paths[c.Path] = struct{}{}
	}
}
