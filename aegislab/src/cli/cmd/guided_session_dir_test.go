package cmd

import (
	"path/filepath"
	"testing"

	chaos "aegis/platform/chaos"
)

// TestGuidedSessionDirIsolation is the regression guard for issue #544:
// two loops pointed at different --session-dir values must not share or clobber
// each other's session/batch files.
func TestGuidedSessionDirIsolation(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()

	sessA, err := resolveGuidedConfigPath("", dirA)
	if err != nil {
		t.Fatalf("resolve session A: %v", err)
	}
	sessB, err := resolveGuidedConfigPath("", dirB)
	if err != nil {
		t.Fatalf("resolve session B: %v", err)
	}
	if sessA == sessB {
		t.Fatalf("session paths collide: %q", sessA)
	}

	batchA, err := resolveGuidedBatchPath("", dirA)
	if err != nil {
		t.Fatalf("resolve batch A: %v", err)
	}
	if filepath.Dir(batchA) != dirA {
		t.Fatalf("batch A not under session dir: %q", batchA)
	}

	if err := saveGuidedConfigFile(sessA, &chaos.ConfigFile{Version: 1}, chaos.GuidedConfig{System: "sn"}); err != nil {
		t.Fatalf("save A: %v", err)
	}
	if err := saveGuidedConfigFile(sessB, &chaos.ConfigFile{Version: 1}, chaos.GuidedConfig{System: "teastore"}); err != nil {
		t.Fatalf("save B: %v", err)
	}

	gotA, err := loadGuidedConfigFile(sessA)
	if err != nil {
		t.Fatalf("load A: %v", err)
	}
	if gotA.GuidedSession.Config.System != "sn" {
		t.Fatalf("session A clobbered: got system %q, want sn", gotA.GuidedSession.Config.System)
	}
}

// TestGuidedConfigPathOverrideWins ensures an explicit --config still beats
// --session-dir, preserving the documented precedence.
func TestGuidedConfigPathOverrideWins(t *testing.T) {
	explicit := filepath.Join(t.TempDir(), "custom.yaml")
	got, err := resolveGuidedConfigPath(explicit, t.TempDir())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != explicit {
		t.Fatalf("override ignored: got %q want %q", got, explicit)
	}
}
