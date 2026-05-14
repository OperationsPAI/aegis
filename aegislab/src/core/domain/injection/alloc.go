package injection

// AllocateNamespaceForRestart, AllocateResult, AllocateOptions, WorkloadProbe,
// ErrPoolExhausted, ErrNamespaceLocked, and related types live in
// internal/alloc and are re-exported here for callers within this package.

import (
	"aegis/core/domain/injection/internal/alloc"
)

// Re-exported types so internal package users (service.go) can reference
// them as plain injection-package symbols.

// ErrPoolExhausted is re-exported from internal/alloc.
var ErrPoolExhausted = alloc.ErrPoolExhausted

// ErrNamespaceLocked is re-exported from internal/alloc.
var ErrNamespaceLocked = alloc.ErrNamespaceLocked

// AllocateResult is re-exported from internal/alloc.
type AllocateResult = alloc.AllocateResult

// AllocateOptions is re-exported from internal/alloc.
type AllocateOptions = alloc.AllocateOptions

// WorkloadProbe is re-exported from internal/alloc.
type WorkloadProbe = alloc.WorkloadProbe

// AllocateNamespaceForRestart is re-exported from internal/alloc.
var AllocateNamespaceForRestart = alloc.AllocateNamespaceForRestart
