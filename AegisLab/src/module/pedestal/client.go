package pedestal

import (
	"context"

	"aegis/model"
)

// Reader is the forward-compatible cross-module access surface for pedestal
// data. Phase 4 follow-up PRs can switch consumers away from direct SQL/table
// access onto this interface without widening the dependency graph.
type Reader interface {
	GetHelmConfigByContainerVersionID(context.Context, int) (*model.HelmConfig, error)
}

func AsReader(repo *Repository) Reader {
	return repo
}
