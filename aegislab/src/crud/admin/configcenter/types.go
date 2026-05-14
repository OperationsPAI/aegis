// Package configcenter is the in-process configuration-center
// implementation. It layers etcd (top, hot) over env vars and TOML
// defaults, exposes a typed Bind/Get/Set/Delete/List API, fans out
// watch events to subscribers, and persists an admin-driven audit
// log to the application database.
//
// The standalone `aegis-configcenter` microservice is the only
// binary that wires this module + holds etcd write credentials.
// Every other service consumes it via `module/configcenterclient`.
package configcenter

import (
	"context"
	"errors"
)

// Layer identifies which source resolved a value.
type Layer string

const (
	LayerEtcd    Layer = "etcd"
	LayerEnv     Layer = "env"
	LayerTOML    Layer = "toml"
	LayerDefault Layer = "default"
)

// Action enumerates audit-log actions written by the admin handler.
type Action string

const (
	ActionSet    Action = "set"
	ActionDelete Action = "delete"
)

// Entry is a single key's resolved view, returned by List/Get.
type Entry struct {
	Namespace string `json:"namespace"`
	Key       string `json:"key"`
	Value     any    `json:"value"`
	Layer     Layer  `json:"layer"`
}

// BindOpt customises Bind behaviour. Use the With* constructors.
type BindOpt func(*bindOptions)

type bindOptions struct {
	defaultValue  any
	validator     func(any) error
	schemaVersion int
}

func WithDefault(v any) BindOpt {
	return func(o *bindOptions) { o.defaultValue = v }
}

func WithValidator(fn func(any) error) BindOpt {
	return func(o *bindOptions) { o.validator = fn }
}

func WithSchemaVersion(v int) BindOpt {
	return func(o *bindOptions) { o.schemaVersion = v }
}

// SetOpt customises Set behaviour (audit fields).
type SetOpt func(*setOptions)

type setOptions struct {
	actorUserID *int
	actorToken  string
	reason      string
}

// WithActorUser tags the audit row with a human actor.
func WithActorUser(id int) SetOpt {
	return func(o *setOptions) { o.actorUserID = &id }
}

// WithActorToken tags the audit row with a service-token sub.
func WithActorToken(sub string) SetOpt {
	return func(o *setOptions) { o.actorToken = sub }
}

// WithReason carries a human-facing reason into the audit row.
func WithReason(reason string) SetOpt {
	return func(o *setOptions) { o.reason = reason }
}

// Errors surfaced by the center. Callers may use errors.Is/As.
var (
	ErrNotFound       = errors.New("configcenter: key not found")
	ErrForbiddenKey   = errors.New("configcenter: key path rejected (secret-like)")
	ErrInvalidValue   = errors.New("configcenter: value failed validation")
	ErrEncode         = errors.New("configcenter: value encode failed")
	ErrDecode         = errors.New("configcenter: value decode failed")
)

// PubSub is a tiny pub/sub the watcher dispatches changes onto.
// Public so the HTTP handler can subscribe for SSE without touching
// the internal multiplexer directly.
type PubSub interface {
	Subscribe(ctx context.Context, namespace string) (<-chan Entry, func())
}
