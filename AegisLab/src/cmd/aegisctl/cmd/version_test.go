package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestVersionJSONIncludesRequiredFields(t *testing.T) {
	oldVersion := version
	oldCommit := commit
	oldBuildTime := buildTime
	oldMinServerAPIVersion := minServerAPIVersion

	version = "v0.0.0-test"
	commit = "abc1234"
	buildTime = "2026-04-27T10:00:00Z"
	minServerAPIVersion = "2"

	t.Cleanup(func() {
		version = oldVersion
		commit = oldCommit
		buildTime = oldBuildTime
		minServerAPIVersion = oldMinServerAPIVersion
	})

	t.Run("json_payload_has_non_empty_required_fields", func(t *testing.T) {
		res := runCLI(t, "version", "--output", "json")
		if res.code != ExitCodeSuccess {
			t.Fatalf("version command code = %d, stderr=%q", res.code, res.stderr)
		}

		var payload map[string]any
		if err := json.Unmarshal([]byte(res.stdout), &payload); err != nil {
			t.Fatalf("stdout is not valid JSON: %v", err)
		}

		required := []string{"version", "commit", "build_time", "min_server_api"}
		for _, key := range required {
			val, ok := payload[key]
			if !ok {
				t.Fatalf("required field %q missing or empty in payload: %v", key, payload)
			}
			s, ok := val.(string)
			if !ok || strings.TrimSpace(s) == "" {
				t.Fatalf("required field %q is blank after trim", key)
			}
		}
	})

	t.Run("version_flag_alias_matches_version_command", func(t *testing.T) {
		resVersion := runCLI(t, "version", "-o", "json")
		resFlag := runCLI(t, "--version", "--output", "json")

		if resVersion.code != ExitCodeSuccess {
			t.Fatalf("version command code = %d, stderr=%q", resVersion.code, resVersion.stderr)
		}
		if resFlag.code != ExitCodeSuccess {
			t.Fatalf("--version code = %d, stderr=%q", resFlag.code, resFlag.stderr)
		}
		if resVersion.stdout != resFlag.stdout {
			t.Fatalf("stdout mismatch\nversion=%q\n--version=%q", resVersion.stdout, resFlag.stdout)
		}
	})

	t.Run("missing_output_argument_reports_error", func(t *testing.T) {
		res := runCLI(t, "version", "--output")
		if res.code == ExitCodeSuccess {
			t.Fatalf("expected non-zero code, got %d; stderr=%q", res.code, res.stderr)
		}
		if !strings.Contains(res.stderr, "flag needs an argument: --output") {
			t.Fatalf("stderr missing expected parse error: %q", res.stderr)
		}
	})
}
