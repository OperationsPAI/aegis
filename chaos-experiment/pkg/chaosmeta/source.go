// Package chaosmeta is the public facade for installing a metadata source
// into chaos-experiment's resourcelookup package.
package chaosmeta

import (
	"context"

	"github.com/OperationsPAI/chaos-experiment/internal/resourcelookup"
)

// ChaosPointRow mirrors resourcelookup.ChaosPointRow for callers outside
// the internal/ tree.
type ChaosPointRow = resourcelookup.ChaosPointRow

// ChaosPointStore is the minimal contract for sourcing chaos_points by
// system. SetChaosPointStore wires an implementation in process-wide.
type ChaosPointStore interface {
	QueryPoints(ctx context.Context, system string) ([]ChaosPointRow, error)
}

// SetChaosPointStore installs a process-wide store. Passing nil restores
// the static internal/<sys>/* providers.
func SetChaosPointStore(s ChaosPointStore) {
	if s == nil {
		resourcelookup.SetChaosPointStore(nil)
		return
	}
	resourcelookup.SetChaosPointStore(storeAdapter{s})
}

type storeAdapter struct{ s ChaosPointStore }

func (a storeAdapter) QueryPoints(ctx context.Context, system string) ([]resourcelookup.ChaosPointRow, error) {
	return a.s.QueryPoints(ctx, system)
}
