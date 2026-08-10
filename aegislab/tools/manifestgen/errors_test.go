package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Regression: the var-not-found case used to be a bare fmt.Errorf string
// matched via strings.Contains(err, "no such file"), which both
// false-negatived (different message wording) and false-positived (any
// var name containing "no such file" would slip through the file-missing
// branch). The sentinel makes the loader's "AST var not present" outcome
// safe to assert with errors.Is.
func TestParseMapLiteralReturnsSentinelOnMissingVar(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fake.go")
	src := []byte(`package fake

var SomethingElse = map[string]int{"a": 1}
`)
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := parseMapLiteral(path, "ServiceEndpoints")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrVarNotFound) {
		t.Fatalf("expected errors.Is(err, ErrVarNotFound) true; err=%v", err)
	}
}
