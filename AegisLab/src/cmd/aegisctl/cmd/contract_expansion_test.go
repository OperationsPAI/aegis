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
		Version  string `json:"version"`
		Commands []struct {
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

func TestSchemaDumpFlagMetadataContainsTypeDefaultRequiredEnumValues(t *testing.T) {
	res := runCLI(t, "schema", "dump")
	if res.code != ExitCodeSuccess {
		t.Fatalf("exit code = %d, want %d; stderr=%q", res.code, ExitCodeSuccess, res.stderr)
	}

	type schemaCommandDump struct {
		Path  string            `json:"path"`
		Flags []json.RawMessage `json:"flags"`
	}
	var doc struct {
		Commands []schemaCommandDump `json:"commands"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &doc); err != nil {
		t.Fatalf("schema JSON decode failed: %v\nstdout=%q", err, res.stdout)
	}
	if len(doc.Commands) == 0 {
		t.Fatalf("schema dump missing commands")
	}

	var outputSeen bool
	var outputEnumValues []string
	for _, c := range doc.Commands {
		for _, flagRaw := range c.Flags {
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(flagRaw, &raw); err != nil {
				t.Fatalf("invalid flag object in schema dump: %v", err)
			}
			for _, key := range []string{"type", "default", "required", "enum_values", "name"} {
				if _, ok := raw[key]; !ok {
					t.Fatalf("schema flag missing %q in command %q", key, c.Path)
				}
			}
			type flagMeta struct {
				Name       string   `json:"name"`
				Type       string   `json:"type"`
				Default    string   `json:"default"`
				Required   bool     `json:"required"`
				EnumValues []string `json:"enum_values"`
			}
			var meta flagMeta
			if err := json.Unmarshal(flagRaw, &meta); err != nil {
				t.Fatalf("decode schema flag: %v", err)
			}
			if meta.Type == "" {
				t.Fatalf("schema flag %q in command %q missing type", meta.Name, c.Path)
			}
			if meta.Required && meta.Name == "" {
				t.Fatalf("schema flag in command %q has required=true with missing name", c.Path)
			}
			if c.Path == "aegisctl" && meta.Name == "output" {
				outputSeen = true
				outputEnumValues = append(outputEnumValues, meta.EnumValues...)
			}
		}
	}

	if !outputSeen {
		t.Fatalf("schema dump missing top-level --output flag")
	}
	if len(outputEnumValues) == 0 {
		t.Fatalf("expected top-level --output enum values in schema")
	}
}
