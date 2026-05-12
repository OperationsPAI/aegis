package blob

import (
	"context"

	"github.com/sirupsen/logrus"
)

// DeletionWorker is the lifecycle role's v1 stub. The full worker
// (hourly job, retention sweep, orphan reconcile against driver Stat)
// is Phase B+ — until then we just expose a Run() method so the fx
// graph compiles and a future cron wires it in.
//
// TODO(blob/lifecycle): walk blob_objects where
//   - deleted_at IS NOT NULL → driver.Delete, then hard-delete the row.
//   - expires_at <= now      → soft-delete then queue for driver delete.
//   - size_bytes = 0 AND created_at < now-1h → driver.Stat to reconcile
//     orphan presigns (frontend never confirmed upload).
type DeletionWorker struct {
	svc *Service
}

func NewDeletionWorker(svc *Service) *DeletionWorker {
	return &DeletionWorker{svc: svc}
}

// Run is a no-op in v1 — keeping the surface so producers / cron can
// invoke it once the real sweep lands.
func (w *DeletionWorker) Run(_ context.Context) error {
	logrus.Debug("blob: DeletionWorker.Run is a stub (no retention sweep yet)")
	return nil
}
