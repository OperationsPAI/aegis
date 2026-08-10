package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"aegis/cli/output"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var (
	systemDiffSeedName     string
	systemDiffSeedFromSeed string
	systemDiffSeedEnv      string
)

var systemDiffSeedCmd = &cobra.Command{
	Use:   "diff-seed",
	Short: "Compare a system's live DB helm values against a git seed and exit non-zero on drift",
	Long: `The DB helm_config_values rows (merged with the value_file snapshot) are
the canonical runtime source of truth for a pedestal chart install (see
aegis-chaos-design.md §15). The repo seed (data.yaml + per-system overlay)
is the genesis input. They drift.

diff-seed resolves the effective helm values both ways and reports the
difference:

  - git side:  overlay file <dir>/<name>.yaml (if present) overlaid by the
               data.yaml helm_config.values entries for the system.
  - live side: GET /api/v2/systems/by-name/<name>/chart .values, i.e. the
               DB value_file snapshot merged with helm_config_values.

Keys present only in one side, and keys whose values differ, are printed.
Exit code is non-zero (workflow-failure) when any drift is found, so CI
or an operator runbook can call it as a gate:

  aegisctl system diff-seed --name ts --from-seed manifests/byte-cluster/initial-data`,
	Args: requireNoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAPIContext(true); err != nil {
			return err
		}
		name := strings.TrimSpace(systemDiffSeedName)
		if name == "" {
			return usageErrorf("--name is required")
		}
		if strings.TrimSpace(systemDiffSeedFromSeed) == "" {
			return usageErrorf("--from-seed is required")
		}

		seedPath, err := resolveSeedPath(systemDiffSeedFromSeed, systemDiffSeedEnv)
		if err != nil {
			return err
		}
		want, err := effectiveSeedValues(seedPath, name)
		if err != nil {
			return err
		}

		cli, ctx := newAPIClient()
		resp, _, err := cli.SystemsAPI.GetChaosSystemChartByName(ctx, name).Execute()
		if err != nil {
			return fmt.Errorf("fetch live chart for %q: %w", name, err)
		}
		chart := resp.GetData()
		got := flattenHelmValues(chart.GetValues())

		diffs := diffSeedValues(want, got)
		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(map[string]any{
				"system":     name,
				"seed_path":  seedPath,
				"drift":      len(diffs) > 0,
				"mismatches": diffs,
			})
		} else {
			printSeedDiff(name, seedPath, diffs)
		}
		if len(diffs) > 0 {
			return silentExit(ExitCodeWorkflowFailure)
		}
		return nil
	},
}

// seedDiffKind classifies a single drifting key.
type seedDiffKind string

const (
	seedDiffOnlyGit  seedDiffKind = "only_in_seed"
	seedDiffOnlyDB   seedDiffKind = "only_in_db"
	seedDiffMismatch seedDiffKind = "value_mismatch"
)

type seedDiff struct {
	Key  string       `json:"key"`
	Kind seedDiffKind `json:"kind"`
	Seed string       `json:"seed,omitempty"`
	Live string       `json:"live,omitempty"`
}

func diffSeedValues(want, got map[string]string) []seedDiff {
	keys := make(map[string]struct{}, len(want)+len(got))
	for k := range want {
		keys[k] = struct{}{}
	}
	for k := range got {
		keys[k] = struct{}{}
	}
	var out []seedDiff
	for k := range keys {
		w, inWant := want[k]
		g, inGot := got[k]
		switch {
		case inWant && !inGot:
			out = append(out, seedDiff{Key: k, Kind: seedDiffOnlyGit, Seed: w})
		case !inWant && inGot:
			out = append(out, seedDiff{Key: k, Kind: seedDiffOnlyDB, Live: g})
		case w != g:
			out = append(out, seedDiff{Key: k, Kind: seedDiffMismatch, Seed: w, Live: g})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func printSeedDiff(name, seedPath string, diffs []seedDiff) {
	if len(diffs) == 0 {
		output.PrintInfo(fmt.Sprintf("system %q: live DB helm values match seed %s (no drift)", name, seedPath))
		return
	}
	output.PrintError(fmt.Sprintf("system %q: %d helm value drift(s) vs seed %s", name, len(diffs), seedPath))
	for _, d := range diffs {
		switch d.Kind {
		case seedDiffOnlyGit:
			fmt.Fprintf(os.Stderr, "  only in seed: %s = %q\n", d.Key, d.Seed)
		case seedDiffOnlyDB:
			fmt.Fprintf(os.Stderr, "  only in DB:   %s = %q\n", d.Key, d.Live)
		case seedDiffMismatch:
			fmt.Fprintf(os.Stderr, "  mismatch:     %s: seed=%q live=%q\n", d.Key, d.Seed, d.Live)
		}
	}
}

// effectiveSeedValues computes the flattened dotted-key helm values a chart
// install would receive from the repo seed: the per-system overlay file
// (<dir>/<name>.yaml), overlaid by the data.yaml helm_config.values entries
// for the system's highest-versioned active pedestal. This mirrors the
// precedence in HelmConfigItem.GetValuesMap (file base, dynamic values win).
func effectiveSeedValues(seedPath, name string) (map[string]string, error) {
	flat := map[string]string{}

	overlayPath := filepath.Join(filepath.Dir(seedPath), name+".yaml")
	if raw, err := os.ReadFile(overlayPath); err == nil {
		var nested map[string]any
		if err := yaml.Unmarshal(raw, &nested); err != nil {
			return nil, fmt.Errorf("parse overlay %q: %w", overlayPath, err)
		}
		for k, v := range flattenHelmValues(nested) {
			flat[k] = v
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read overlay %q: %w", overlayPath, err)
	}

	values, err := seedHelmConfigValues(seedPath, name)
	if err != nil {
		return nil, err
	}
	for k, v := range values {
		flat[k] = v
	}
	return flat, nil
}

// seedDataDoc is the minimal slice of data.yaml diff-seed needs: the
// containers list with each version's helm_config.values entries.
type seedDataDoc struct {
	Containers []struct {
		Name     string `yaml:"name"`
		Versions []struct {
			Name       string `yaml:"name"`
			Status     int    `yaml:"status"`
			HelmConfig *struct {
				Values []struct {
					Key          string  `yaml:"key"`
					DefaultValue *string `yaml:"default_value"`
				} `yaml:"values"`
			} `yaml:"helm_config"`
		} `yaml:"versions"`
	} `yaml:"containers"`
}

// seedHelmConfigValues returns the data.yaml helm_config.values for the
// system's highest active pedestal version, as a dotted-key map. The
// highest-versioned active version wins, matching the backend's
// GetPedestalHelmConfigByName resolution when no explicit version is asked.
func seedHelmConfigValues(seedPath, name string) (map[string]string, error) {
	raw, err := os.ReadFile(seedPath)
	if err != nil {
		return nil, fmt.Errorf("read seed %q: %w", seedPath, err)
	}
	var doc seedDataDoc
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse seed %q: %w", seedPath, err)
	}
	for _, c := range doc.Containers {
		if c.Name != name {
			continue
		}
		var chosen *struct {
			Values []struct {
				Key          string  `yaml:"key"`
				DefaultValue *string `yaml:"default_value"`
			} `yaml:"values"`
		}
		var chosenName string
		for i := range c.Versions {
			v := &c.Versions[i]
			if v.HelmConfig == nil || v.Status != 1 {
				continue
			}
			if chosen == nil || v.Name > chosenName {
				chosen = v.HelmConfig
				chosenName = v.Name
			}
		}
		out := map[string]string{}
		if chosen == nil {
			return out, nil
		}
		for _, val := range chosen.Values {
			if val.DefaultValue == nil {
				continue
			}
			out[val.Key] = *val.DefaultValue
		}
		return out, nil
	}
	return nil, notFoundErrorf("no containers entry for %q in seed %s", name, seedPath)
}

// flattenHelmValues turns a nested helm values map into dotted leaf keys.
// Arrays are JSON-encoded as a single leaf value so an indexed override and
// a structural array both compare as one string — matching how a helm --set
// against an indexed slot is treated as one opaque value.
func flattenHelmValues(data map[string]any) map[string]string {
	out := map[string]string{}
	flattenHelmValuesInto(data, "", out)
	return out
}

func flattenHelmValuesInto(data map[string]any, prefix string, out map[string]string) {
	for k, v := range data {
		full := k
		if prefix != "" {
			full = prefix + "." + k
		}
		switch val := v.(type) {
		case map[string]any:
			flattenHelmValuesInto(val, full, out)
		case []any:
			b, _ := json.Marshal(val)
			out[full] = string(b)
		default:
			out[full] = fmt.Sprintf("%v", val)
		}
	}
}

func init() {
	systemDiffSeedCmd.Flags().StringVar(&systemDiffSeedName, "name", "", "Short code of the system to diff (required)")
	systemDiffSeedCmd.Flags().StringVar(&systemDiffSeedFromSeed, "from-seed", "", "Path to data.yaml (file, env dir, or initial_data/ root) (required)")
	systemDiffSeedCmd.Flags().StringVar(&systemDiffSeedEnv, "env", "", "When --from-seed is a directory: 'prod' or 'staging'")
	systemCmd.AddCommand(systemDiffSeedCmd)
}
