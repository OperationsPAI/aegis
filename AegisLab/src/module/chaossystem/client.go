package chaossystem

import (
	"context"

	"aegis/model"
)

// Reader is the cross-module read contract for chaos-system metadata.
// Post-issue-75 the "system" aggregate lives in etcd/Viper, but
// system_metadata rows still live in MySQL so we expose them here for other
// modules that want to consume service-level metadata.
type Reader interface {
	ListSystemMetadata(systemName, metadataType string) ([]model.SystemMetadata, error)
}

// AsReader returns the repository as a Reader.
func AsReader(repo *Repository) *Repository { return repo }

var _ Reader = (*Repository)(nil)

// Writer is the cross-module write contract for chaos-system mutations
// other modules need to perform during a request flow. Today this is
// limited to "make sure the system's `Count` is high enough to cover this
// namespace" — the seam that #156's `--install` path was missing.
//
// Kept narrow on purpose: the full UpdateSystem API is admin-scoped and
// records change history per field, while EnsureCountForNamespace is an
// idempotent submit-time bump that should not require admin permissions.
type Writer interface {
	// EnsureCountForNamespace bumps `injection.system.<system>.count` so that
	// `namespace` falls within the system's enumerated namespace range.
	// Returns (true, nil) if the count was actually bumped, (false, nil) if
	// no change was needed. Returns an error when:
	//   - the system does not exist
	//   - the namespace does not match the system's NsPattern/ExtractPattern
	//
	// This is the missing piece behind #156: `aegisctl inject guided
	// --install --namespace sockshop14` creates the workload but leaves
	// `count=1`, so AcquireLock for `sockshop14` fails with "not found in
	// current configuration" and the runtime falls back to the pool.
	EnsureCountForNamespace(ctx context.Context, systemName, namespace string) (bool, error)
}

// AsWriter returns the service as a Writer.
func AsWriter(svc *Service) *Service { return svc }

var _ Writer = (*Service)(nil)
