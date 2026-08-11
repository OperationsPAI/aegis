package chaos

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// TestBundledCLISchemaMatchesServer guards against drift between the JSON
// Schema bundled in aegisctl (cli/cmd/manifest_schema.json) and the
// server-served manifestEnvelopeSchema. If the server tightens a required
// field or adds an enum value, offline `aegisctl manifest validate` would
// silently stay green while online dry-run rejected — chart authors would
// only discover the mismatch at install time.
func TestBundledCLISchemaMatchesServer(t *testing.T) {
	cliBytes, err := os.ReadFile("../../cli/cmd/manifest_schema.json")
	if err != nil {
		t.Fatalf("read bundled CLI schema: %v", err)
	}
	serverBytes, err := json.Marshal(manifestEnvelopeSchema)
	if err != nil {
		t.Fatalf("marshal server schema: %v", err)
	}
	var cli, server any
	if err := json.Unmarshal(cliBytes, &cli); err != nil {
		t.Fatalf("decode bundled CLI schema: %v", err)
	}
	if err := json.Unmarshal(serverBytes, &server); err != nil {
		t.Fatalf("decode server schema: %v", err)
	}
	if !reflect.DeepEqual(server, cli) {
		t.Fatalf("CLI bundled schema differs from server manifestEnvelopeSchema — " +
			"regenerate cli/cmd/manifest_schema.json from handler.go")
	}
}
