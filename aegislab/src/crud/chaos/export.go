package chaos

import (
	"context"
	"fmt"
	"sort"
)

// ExportSystemPoints returns every Point for a system, grouped into
// PointManifest envelopes by (service, instance, chart_version). Round-trip
// safe: feeding the result back through ImportPoints reproduces the row set
// modulo timestamps. Only active rows by default; superseded rows are
// included when includeSuperseded is true.
func (s *Manager) ExportSystemPoints(ctx context.Context, systemName string, includeSuperseded bool) ([]PointManifest, error) {
	if systemName == "" {
		return nil, fmt.Errorf("chaos: system is required")
	}

	q := s.DB.WithContext(ctx).Model(&Point{}).Where("system_name = ?", systemName)
	if !includeSuperseded {
		q = q.Where("status = ?", PointActive)
	}
	var rows []Point
	if err := q.Order("id ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("chaos: list points for export: %w", err)
	}
	if len(rows) == 0 {
		return []PointManifest{}, nil
	}

	svcIDs := make(map[int64]struct{}, len(rows))
	for _, r := range rows {
		if r.ServiceID != nil {
			svcIDs[*r.ServiceID] = struct{}{}
		}
	}
	idList := make([]int64, 0, len(svcIDs))
	for id := range svcIDs {
		idList = append(idList, id)
	}
	var svcs []Service
	if len(idList) > 0 {
		if err := s.DB.WithContext(ctx).Where("id IN ?", idList).Find(&svcs).Error; err != nil {
			return nil, fmt.Errorf("chaos: resolve services for export: %w", err)
		}
	}
	svcByID := make(map[int64]Service, len(svcs))
	for _, sv := range svcs {
		svcByID[sv.ID] = sv
	}

	type groupKey struct{ service, instance, chartVersion string }
	groups := make(map[groupKey][]PointManifestEntry)
	for _, r := range rows {
		if r.ServiceID == nil {
			// Points without a service binding can't round-trip through
			// import (manifest requires metadata.service). Drop them and let
			// the operator notice via row-count drift if it ever happens —
			// production rows are always service-bound via upsertService.
			continue
		}
		sv, ok := svcByID[*r.ServiceID]
		if !ok {
			continue
		}
		k := groupKey{service: sv.Name, instance: sv.Instance, chartVersion: sv.ChartVersion}
		groups[k] = append(groups[k], PointManifestEntry{
			Capability:     r.CapabilityName,
			Target:         map[string]any(r.Target),
			ParamOverrides: map[string]any(r.ParamOverrides),
		})
	}

	keys := make([]groupKey, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].service != keys[j].service {
			return keys[i].service < keys[j].service
		}
		if keys[i].instance != keys[j].instance {
			return keys[i].instance < keys[j].instance
		}
		return keys[i].chartVersion < keys[j].chartVersion
	})

	out := make([]PointManifest, 0, len(keys))
	for _, k := range keys {
		entries := groups[k]
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Capability < entries[j].Capability
		})
		out = append(out, PointManifest{
			APIVersion: "aegis-chaos/v1beta",
			Kind:       "PointManifest",
			Metadata: PointManifestMetadata{
				System:       systemName,
				Service:      k.service,
				Instance:     k.instance,
				ChartVersion: k.chartVersion,
			},
			Spec: PointManifestSpec{
				ReplaceScope: ReplaceScopeService,
				Points:       entries,
			},
		})
	}
	return out, nil
}
