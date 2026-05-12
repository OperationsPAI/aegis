package stdin

import (
	"strings"
	"testing"
)

func TestBatchStdinParser(t *testing.T) {
	t.Run("raw lines pass through", func(t *testing.T) {
		items, err := Parse(strings.NewReader("trace-a\ntrace-b\n"), ParseConfig{})
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if got, want := strings.Join(items, ","), "trace-a,trace-b"; got != want {
			t.Fatalf("items = %q, want %q", got, want)
		}
	})

	t.Run("ndjson uses inferred fallback field", func(t *testing.T) {
		items, err := Parse(strings.NewReader("{\"trace_id\":\"trace-a\"}\n{\"id\":\"trace-b\"}\n"), ParseConfig{
			FallbackFields: DefaultFields("wait"),
		})
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if got, want := strings.Join(items, ","), "trace-a,trace-b"; got != want {
			t.Fatalf("items = %q, want %q", got, want)
		}
	})

	t.Run("ndjson missing field fails clearly", func(t *testing.T) {
		_, err := Parse(strings.NewReader("{\"name\":\"inject-a\"}\n"), ParseConfig{
			FallbackFields: DefaultFields("trace get"),
		})
		if err == nil || !strings.Contains(err.Error(), "missing field") {
			t.Fatalf("err = %v, want missing field diagnostic", err)
		}
	})
}
