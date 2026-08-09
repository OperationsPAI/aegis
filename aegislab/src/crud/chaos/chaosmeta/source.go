// Package chaosmeta is the public facade for installing a metadata source
// into the chaosengine resourcelookup package.
package chaosmeta

import (
	"context"
	"time"

	"aegis/platform/k8s/resourcelookup"
)

// ChaosPointRow mirrors resourcelookup.ChaosPointRow for callers outside
// the internal/ tree.
type ChaosPointRow = resourcelookup.ChaosPointRow

// ChaosPointStore is the minimal contract for sourcing chaos_points by
// system. SetChaosPointStore wires an implementation in process-wide.
type ChaosPointStore interface {
	QueryPoints(ctx context.Context, system string) ([]ChaosPointRow, error)
	LatestUpdate(ctx context.Context, system string) (time.Time, error)
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

func (a storeAdapter) LatestUpdate(ctx context.Context, system string) (time.Time, error) {
	return a.s.LatestUpdate(ctx, system)
}
