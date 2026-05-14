package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/OperationsPAI/chaos-experiment/pkg/guidedcli"
	"gopkg.in/yaml.v3"
)

// guidedBatchFile is the on-disk schema for staged guided configs that will
// be submitted as a single parallel batch via `aegisctl inject guided
// --apply --batch`. It is independent of guidedcli's session yaml so that
// staging does not perturb the working session state, and so that one stage
// file can be edited or shared without reaching into chaos-experiment's
// config layout.
type guidedBatchFile struct {
	Version     int                      `yaml:"version"`
	StagedSpecs []guidedcli.GuidedConfig `yaml:"staged_specs"`
}

func defaultGuidedBatchPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("determine home directory: %w", err)
	}
	return filepath.Join(home, ".aegisctl", "inject-guided-batch.yaml"), nil
}

func resolveGuidedBatchPath(override string) (string, error) {
	if strings.TrimSpace(override) != "" {
		return override, nil
	}
	return defaultGuidedBatchPath()
}

func loadGuidedBatch(path string) (*guidedBatchFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &guidedBatchFile{Version: 1}, nil
		}
		return nil, fmt.Errorf("read guided batch file %s: %w", path, err)
	}
	var batch guidedBatchFile
	if err := yaml.Unmarshal(data, &batch); err != nil {
		return nil, fmt.Errorf("parse guided batch file %s: %w", path, err)
	}
	if batch.Version == 0 {
		batch.Version = 1
	}
	return &batch, nil
}

func saveGuidedBatch(path string, batch *guidedBatchFile) error {
	if batch == nil {
		batch = &guidedBatchFile{Version: 1}
	}
	if batch.Version == 0 {
		batch.Version = 1
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create guided batch directory: %w", err)
	}
	data, err := yaml.Marshal(batch)
	if err != nil {
		return fmt.Errorf("marshal guided batch file: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write guided batch file %s: %w", path, err)
	}
	return nil
}

// validateBatchCompat enforces the cross-spec invariants that the backend
// would otherwise reject mid-flight: every staged config must target the
// same pedestal (system / system_type) and the same namespace, because a
// single experiment locks one namespace and parseBatchGuidedSpecs validates
// that every cfg's system_type equals the request's pedestal.
//
// Empty namespace is allowed as long as it is empty for every staged spec —
// this is the --auto path where the server picks the namespace at submit
// time. Mixing "set ns" with "empty ns" inside one batch is a usage error.
func validateBatchCompat(specs []guidedcli.GuidedConfig) error {
	if len(specs) == 0 {
		return nil
	}
	first := specs[0]
	for i, s := range specs[1:] {
		idx := i + 1
		if !strings.EqualFold(strings.TrimSpace(first.System), strings.TrimSpace(s.System)) {
			return usageErrorf("staged_specs[%d].system=%q differs from staged_specs[0].system=%q; a parallel batch must target one system",
				idx, s.System, first.System)
		}
		if !strings.EqualFold(strings.TrimSpace(first.SystemType), strings.TrimSpace(s.SystemType)) {
			return usageErrorf("staged_specs[%d].system_type=%q differs from staged_specs[0].system_type=%q; backend pedestal check would reject this",
				idx, s.SystemType, first.SystemType)
		}
		if strings.TrimSpace(first.Namespace) != strings.TrimSpace(s.Namespace) {
			return usageErrorf("staged_specs[%d].namespace=%q differs from staged_specs[0].namespace=%q; one experiment locks one namespace, so all parallel specs must share it (or all leave it empty for --auto)",
				idx, s.Namespace, first.Namespace)
		}
	}
	return nil
}
