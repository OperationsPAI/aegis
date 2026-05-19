package injection

import (
	"testing"

	"aegis/platform/consts"
)

// fullDatapack returns parquets/jsonFiles/marker that satisfy the schema
// (all 15 files present, parquets non-empty). Callers can mutate the
// returned slices to construct failure cases.
func fullDatapack() (parquets, jsonFiles []datapackFileStat, marker datapackFileStat) {
	rows := int64(100)
	for _, name := range datapackRequiredParquets {
		r := rows
		parquets = append(parquets, datapackFileStat{Name: name, Present: true, Rows: &r})
	}
	for _, name := range datapackRequiredJSON {
		jsonFiles = append(jsonFiles, datapackFileStat{Name: name, Present: true})
	}
	marker = datapackFileStat{Name: datapackRequiredMarker, Present: true}
	return parquets, jsonFiles, marker
}

func TestEvaluateDatapackHealth(t *testing.T) {
	t.Run("healthy when all files present and parquets non-empty", func(t *testing.T) {
		p, j, m := fullDatapack()
		got := evaluateDatapackHealth(consts.DatapackBuildSuccess, p, j, m)
		if got.Health != DatapackHealthHealthy {
			t.Errorf("Health = %q, want %q", got.Health, DatapackHealthHealthy)
		}
		if len(got.MissingFiles) != 0 || len(got.EmptyParquets) != 0 {
			t.Errorf("expected no missing/empty, got missing=%v empty=%v", got.MissingFiles, got.EmptyParquets)
		}
	})

	t.Run("empty_parquets when state=BuildSuccess and a required parquet has 0 rows", func(t *testing.T) {
		p, j, m := fullDatapack()
		zero := int64(0)
		p[0].Rows = &zero
		got := evaluateDatapackHealth(consts.DatapackBuildSuccess, p, j, m)
		if got.Health != DatapackHealthEmptyParquets {
			t.Errorf("Health = %q, want %q", got.Health, DatapackHealthEmptyParquets)
		}
		if len(got.EmptyParquets) != 1 || got.EmptyParquets[0] != p[0].Name {
			t.Errorf("EmptyParquets = %v, want [%s]", got.EmptyParquets, p[0].Name)
		}
	})

	t.Run("state_inconsistent when state=BuildSuccess but required file missing", func(t *testing.T) {
		p, j, m := fullDatapack()
		m.Present = false
		got := evaluateDatapackHealth(consts.DatapackBuildSuccess, p, j, m)
		if got.Health != DatapackHealthStateInconsistent {
			t.Errorf("Health = %q, want %q", got.Health, DatapackHealthStateInconsistent)
		}
		if len(got.MissingFiles) != 1 || got.MissingFiles[0] != datapackRequiredMarker {
			t.Errorf("MissingFiles = %v, want [%s]", got.MissingFiles, datapackRequiredMarker)
		}
	})

	t.Run("not_built when state < BuildSuccess regardless of storage", func(t *testing.T) {
		p, j, m := fullDatapack()
		// Even a fully-present datapack on disk shouldn't be called "broken"
		// if the DB hasn't transitioned to BuildSuccess yet — partial residue
		// is expected.
		got := evaluateDatapackHealth(consts.DatapackInjectSuccess, p, j, m)
		if got.Health != DatapackHealthNotBuilt {
			t.Errorf("Health = %q, want %q (state pre-build)", got.Health, DatapackHealthNotBuilt)
		}
	})

	t.Run("unknown row count does not promote to empty_parquets", func(t *testing.T) {
		p, j, m := fullDatapack()
		p[0].Rows = nil
		got := evaluateDatapackHealth(consts.DatapackBuildSuccess, p, j, m)
		if got.Health != DatapackHealthHealthy {
			t.Errorf("Health = %q, want %q (nil rows == unknown, not zero)", got.Health, DatapackHealthHealthy)
		}
	})
}
