package injection

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"aegis/platform/consts"

	"gorm.io/gorm"
)

const (
	DatapackHealthHealthy           = "healthy"
	DatapackHealthEmptyParquets     = "empty_parquets"
	DatapackHealthIncomplete        = "incomplete"
	DatapackHealthStateInconsistent = "state_inconsistent"
	DatapackHealthNotBuilt          = "not_built"
)

// Required artifacts per docs/troubleshooting/datapack-schema.md.
var (
	datapackRequiredParquets = []string{
		"normal_logs.parquet",
		"normal_metrics.parquet",
		"normal_metrics_histogram.parquet",
		"normal_metrics_sum.parquet",
		"normal_trace_id_ts.parquet",
		"normal_traces.parquet",
		"abnormal_logs.parquet",
		"abnormal_metrics.parquet",
		"abnormal_metrics_histogram.parquet",
		"abnormal_metrics_sum.parquet",
		"abnormal_trace_id_ts.parquet",
		"abnormal_traces.parquet",
	}
	datapackRequiredJSON   = []string{"env.json", "k8s.json", "injection.json"}
	datapackRequiredMarker = "sha256sum.txt"
)

type DatapackFileHealth struct {
	Name    string `json:"name"`
	Present bool   `json:"present"`
	Rows    *int64 `json:"rows,omitempty"`
	Size    string `json:"size,omitempty"`
}

type DatapackDiagnoseResp struct {
	InjectionID    int                  `json:"injection_id"`
	InjectionName  string               `json:"injection_name"`
	State          string               `json:"state"`
	Health         string               `json:"health"`
	MissingFiles   []string             `json:"missing_files,omitempty"`
	EmptyParquets  []string             `json:"empty_parquets,omitempty"`
	UnexpectedXtra []string             `json:"unexpected_extras,omitempty"`
	Parquets       []DatapackFileHealth `json:"parquets"`
	JSON           []DatapackFileHealth `json:"json"`
	Marker         DatapackFileHealth   `json:"marker"`
	Notes          []string             `json:"notes,omitempty"`
}

// datapackFileStat is the storage-level signal feeding evaluateDatapackHealth.
// `Rows` is nil when row counting was not attempted (e.g. file missing or
// row count failed). A present parquet with Rows=&0 is the canonical "empty
// parquet" failure that motivates this command.
type datapackFileStat struct {
	Name    string
	Present bool
	Size    string
	Rows    *int64
}

// evaluateDatapackHealth maps DB state + storage facts to a health verdict.
// Pure function (no I/O, no logging) so it can be unit-tested.
func evaluateDatapackHealth(state consts.DatapackState, parquets, jsonFiles []datapackFileStat, marker datapackFileStat) DatapackDiagnoseResp {
	resp := DatapackDiagnoseResp{
		State:    consts.GetDatapackStateName(state),
		Parquets: toFileHealth(parquets),
		JSON:     toFileHealth(jsonFiles),
		Marker: DatapackFileHealth{
			Name:    marker.Name,
			Present: marker.Present,
			Size:    marker.Size,
		},
	}

	for _, p := range parquets {
		if !p.Present {
			resp.MissingFiles = append(resp.MissingFiles, p.Name)
			continue
		}
		if p.Rows != nil && *p.Rows == 0 {
			resp.EmptyParquets = append(resp.EmptyParquets, p.Name)
		}
	}
	for _, j := range jsonFiles {
		if !j.Present {
			resp.MissingFiles = append(resp.MissingFiles, j.Name)
		}
	}
	if !marker.Present {
		resp.MissingFiles = append(resp.MissingFiles, marker.Name)
	}

	switch {
	case state < consts.DatapackBuildSuccess:
		// The build has not (successfully) run. Any storage residue is
		// expected to be partial; don't promote it to "broken".
		resp.Health = DatapackHealthNotBuilt
	case len(resp.MissingFiles) > 0:
		// DB says built, storage disagrees — the inconsistency case operators
		// currently spot via log spelunking.
		if state == consts.DatapackBuildSuccess {
			resp.Health = DatapackHealthStateInconsistent
		} else {
			resp.Health = DatapackHealthIncomplete
		}
	case len(resp.EmptyParquets) > 0:
		resp.Health = DatapackHealthEmptyParquets
	default:
		resp.Health = DatapackHealthHealthy
	}

	return resp
}

func toFileHealth(in []datapackFileStat) []DatapackFileHealth {
	out := make([]DatapackFileHealth, 0, len(in))
	for _, s := range in {
		item := DatapackFileHealth{Name: s.Name, Present: s.Present, Size: s.Size}
		if s.Rows != nil {
			r := *s.Rows
			item.Rows = &r
		}
		out = append(out, item)
	}
	return out
}

// DiagnoseDatapack inspects an injection's datapack and returns a health
// report. Unlike GetDatapackFiles / GetDatapackSchema this works for any
// injection regardless of build state — the whole point is to surface
// partial / failed builds.
func (s *Service) DiagnoseDatapack(ctx context.Context, id int) (*DatapackDiagnoseResp, error) {
	injection, err := s.repo.loadInjection(id)
	if err != nil {
		if errors.Is(err, consts.ErrNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: injection id: %d", consts.ErrNotFound, id)
		}
		return nil, fmt.Errorf("failed to get injection: %w", err)
	}

	parquets, jsonFiles, marker, extras := s.statDatapackArtifacts(ctx, injection.Name)
	resp := evaluateDatapackHealth(consts.DatapackState(injection.State), parquets, jsonFiles, marker)
	resp.InjectionID = injection.ID
	resp.InjectionName = injection.Name
	resp.UnexpectedXtra = extras
	return &resp, nil
}

// statDatapackArtifacts inspects on-disk / object-store contents for the
// 15-file datapack manifest. Row counts are populated for parquets via
// countParquetRows (only when the duckdb_arrow build tag is set); on
// non-arrow builds Rows stays nil and `empty_parquets` cannot be detected.
func (s *Service) statDatapackArtifacts(ctx context.Context, datapackName string) (parquets, jsonFiles []datapackFileStat, marker datapackFileStat, extras []string) {
	tree, err := s.store.BuildFileTree(datapackName, "", 0)
	present := map[string]string{}
	if err == nil && tree != nil {
		collectPresent(tree.Files, "", present)
	}

	requiredSet := map[string]struct{}{}
	for _, name := range datapackRequiredParquets {
		requiredSet[name] = struct{}{}
		stat := datapackFileStat{Name: name}
		if size, ok := present[name]; ok {
			stat.Present = true
			stat.Size = size
			if rows, ok := s.countParquetRows(ctx, datapackName, name); ok {
				stat.Rows = &rows
			}
		}
		parquets = append(parquets, stat)
	}
	for _, name := range datapackRequiredJSON {
		requiredSet[name] = struct{}{}
		stat := datapackFileStat{Name: name}
		if size, ok := present[name]; ok {
			stat.Present = true
			stat.Size = size
		}
		jsonFiles = append(jsonFiles, stat)
	}
	requiredSet[datapackRequiredMarker] = struct{}{}
	marker = datapackFileStat{Name: datapackRequiredMarker}
	if size, ok := present[datapackRequiredMarker]; ok {
		marker.Present = true
		marker.Size = size
	}

	for name := range present {
		if _, expected := requiredSet[name]; !expected {
			extras = append(extras, name)
		}
	}
	return parquets, jsonFiles, marker, extras
}

func collectPresent(items []DatapackFileItem, prefix string, out map[string]string) {
	for _, it := range items {
		if len(it.Children) > 0 {
			collectPresent(it.Children, filepath.Join(prefix, it.Name), out)
			continue
		}
		key := filepath.ToSlash(filepath.Join(prefix, it.Name))
		out[key] = it.Size
	}
}
