package output

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"aegis/cmd/aegisctl/internal/cli/clierr"
)

func captureStderr(fn func()) string {
	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	fn()
	w.Close()
	out, _ := io.ReadAll(r)
	os.Stderr = old
	return string(out)
}

func captureStdout(fn func()) string {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	fn()
	w.Close()
	out, _ := io.ReadAll(r)
	os.Stdout = old
	return string(out)
}

func TestOutputFormat_Constants(t *testing.T) {
	if FormatJSON != "json" {
		t.Errorf("FormatJSON = %q, want %q", FormatJSON, "json")
	}
	if FormatNDJSON != "ndjson" {
		t.Errorf("FormatNDJSON = %q, want %q", FormatNDJSON, "ndjson")
	}
}

func TestOutputFormat_Conversion(t *testing.T) {
	if OutputFormat("json") != FormatJSON {
		t.Errorf("OutputFormat(\"json\") != FormatJSON")
	}
	if OutputFormat("table") == FormatJSON {
		t.Errorf("OutputFormat(\"table\") should not equal FormatJSON")
	}
	if OutputFormat("ndjson") != FormatNDJSON {
		t.Errorf("OutputFormat(\"ndjson\") != FormatNDJSON")
	}
}

func TestOutputNoColorRespectsNoColorEnv(t *testing.T) {
	oldTerminal := isTerminal
	isTerminal = func(_ int) bool { return true }
	defer func() {
		isTerminal = oldTerminal
	}()

	previous, hadPrevious := os.LookupEnv("NO_COLOR")
	if hadPrevious {
		defer os.Setenv("NO_COLOR", previous)
	} else {
		defer os.Unsetenv("NO_COLOR")
	}
	if err := os.Setenv("NO_COLOR", "1"); err != nil {
		t.Fatalf("set NO_COLOR: %v", err)
	}

	SetNoColor(false)
	if IsStdoutColor() {
		t.Fatalf("NO_COLOR should disable stdout ANSI")
	}
}

func TestPrintInfo_Normal(t *testing.T) {
	Quiet = false
	got := captureStderr(func() {
		PrintInfo("hello info")
	})
	if !strings.Contains(got, "hello info") {
		t.Errorf("PrintInfo output = %q, want it to contain %q", got, "hello info")
	}
}

func TestPrintInfo_Quiet(t *testing.T) {
	Quiet = true
	defer func() { Quiet = false }()

	got := captureStderr(func() {
		PrintInfo("should not appear")
	})
	if got != "" {
		t.Errorf("PrintInfo with Quiet=true produced output: %q", got)
	}
}

func TestPrintError(t *testing.T) {
	got := captureStderr(func() {
		PrintError("something went wrong")
	})
	if !strings.Contains(got, "Error:") {
		t.Errorf("PrintError output = %q, want prefix containing Error:", got)
	}
	if !strings.Contains(got, "something went wrong") {
		t.Errorf("PrintError output = %q, want it to contain %q", got, "something went wrong")
	}
}

func TestPrintCLIError_JSON(t *testing.T) {
	payload := &clierr.CLIError{
		Type:       "server",
		Message:    "server returned HTTP 500; cause: boom; request_id=req-1",
		Cause:      "boom",
		RequestID:  "req-1",
		Suggestion: "retry later",
		Retryable:  true,
		ExitCode:   10,
	}

	got := captureStderr(func() {
		PrintCLIError(payload, FormatJSON)
	})

	var decoded clierr.CLIError
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatalf("expected JSON stderr, got %q: %v", got, err)
	}
	if decoded.Type != payload.Type {
		t.Fatalf("decoded.Type = %q, want %q", decoded.Type, payload.Type)
	}
	if decoded.ExitCode != payload.ExitCode {
		t.Fatalf("decoded.ExitCode = %d, want %d", decoded.ExitCode, payload.ExitCode)
	}
	if decoded.RequestID != payload.RequestID {
		t.Fatalf("decoded.RequestID = %q, want %q", decoded.RequestID, payload.RequestID)
	}
}

func TestPrintCLIError_HumanReadable(t *testing.T) {
	payload := &clierr.CLIError{
		Type:       "decode",
		Message:    "schema mismatch",
		Cause:      "field=id expected=int got=string",
		Suggestion: "align schema",
		RequestID:  "req-2",
		Retryable:  false,
		ExitCode:   11,
	}

	got := captureStderr(func() {
		PrintCLIError(payload, FormatTable)
	})
	if !strings.Contains(got, "Error [decode]: schema mismatch") {
		t.Fatalf("human output = %q, want first line prefix", got)
	}
	if !strings.Contains(got, "cause: field=id expected=int got=string") {
		t.Fatalf("human output = %q, want cause", got)
	}
	if !strings.Contains(got, "hint: align schema") {
		t.Fatalf("human output = %q, want hint", got)
	}
	if !strings.Contains(got, "request_id=req-2") {
		t.Fatalf("human output = %q, want request_id", got)
	}
}

func TestPrintJSON(t *testing.T) {
	data := map[string]string{"key": "value"}
	got := captureStdout(func() {
		PrintJSON(data)
	})

	var parsed map[string]string
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("PrintJSON output is not valid JSON: %v\nOutput: %q", err, got)
	}
	if parsed["key"] != "value" {
		t.Errorf("parsed[\"key\"] = %q, want %q", parsed["key"], "value")
	}
	if !strings.Contains(got, "  ") {
		t.Errorf("PrintJSON output should be indented, got: %q", got)
	}
}

func TestPrintJSON_NilAndEmpty(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		got := captureStdout(func() {
			PrintJSON(nil)
		})
		trimmed := strings.TrimSpace(got)
		if trimmed != "null" {
			t.Errorf("PrintJSON(nil) = %q, want %q", trimmed, "null")
		}
	})

	t.Run("empty map", func(t *testing.T) {
		got := captureStdout(func() {
			PrintJSON(map[string]any{})
		})
		trimmed := strings.TrimSpace(got)
		if trimmed != "{}" {
			t.Errorf("PrintJSON(empty map) = %q, want %q", trimmed, "{}")
		}
	})

	t.Run("empty slice", func(t *testing.T) {
		got := captureStdout(func() {
			PrintJSON([]string{})
		})
		trimmed := strings.TrimSpace(got)
		if trimmed != "[]" {
			t.Errorf("PrintJSON(empty slice) = %q, want %q", trimmed, "[]")
		}
	})
}

func TestPrintNDJSON(t *testing.T) {
	got := captureStdout(func() {
		if err := PrintNDJSON([]map[string]string{
			{"name": "one"},
			{"name": "two"},
		}); err != nil {
			t.Fatalf("PrintNDJSON returned error: %v", err)
		}
	})
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d; got: %q", len(lines), got)
	}
	for _, line := range lines {
		var payload map[string]any
		if err := json.Unmarshal([]byte(line), &payload); err != nil {
			t.Fatalf("line is not json: %v; line=%q", err, line)
		}
	}
}

func TestPrintMetaJSON(t *testing.T) {
	got := captureStderr(func() {
		if err := PrintMetaJSON(map[string]any{"page": 1, "total": 2}); err != nil {
			t.Fatalf("PrintMetaJSON returned error: %v", err)
		}
	})

	var payload map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(got)), &payload); err != nil {
		t.Fatalf("metadata output is not json: %v; output=%q", err, got)
	}
	meta, ok := payload["_meta"].(map[string]any)
	if !ok {
		t.Fatalf("missing _meta envelope: %q", got)
	}
	if gotPage, ok := meta["page"].(float64); !ok || int(gotPage) != 1 {
		t.Fatalf("meta page = %v (ok=%v), want 1", meta["page"], ok)
	}
	if gotTotal, ok := meta["total"].(float64); !ok || int(gotTotal) != 2 {
		t.Fatalf("meta total = %v (ok=%v), want 2", meta["total"], ok)
	}
}

func TestPrintTable(t *testing.T) {
	headers := []string{"NAME", "AGE", "CITY"}
	rows := [][]string{
		{"Alice", "30", "NYC"},
		{"Bob", "25", "LA"},
	}
	got := captureStdout(func() {
		PrintTable(headers, rows)
	})

	if !strings.Contains(got, "NAME") {
		t.Errorf("PrintTable output missing header NAME, got: %q", got)
	}
	if !strings.Contains(got, "AGE") {
		t.Errorf("PrintTable output missing header AGE, got: %q", got)
	}
	if !strings.Contains(got, "Alice") {
		t.Errorf("PrintTable output missing row data Alice, got: %q", got)
	}
	if !strings.Contains(got, "Bob") {
		t.Errorf("PrintTable output missing row data Bob, got: %q", got)
	}
}

func TestPrintTable_EmptyRows(t *testing.T) {
	headers := []string{"NAME", "STATUS"}
	got := captureStdout(func() {
		PrintTable(headers, nil)
	})

	if !strings.Contains(got, "NAME") {
		t.Errorf("PrintTable with empty rows missing header NAME, got: %q", got)
	}
	if !strings.Contains(got, "STATUS") {
		t.Errorf("PrintTable with empty rows missing header STATUS, got: %q", got)
	}
}
