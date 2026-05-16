// Package share is the ephemeral file-sharing surface on top of the
// blob registry. A caller uploads (or presigns) a file to a configured
// blob bucket, gets back an 8-char short code, and shares the URL
// `<public_base_url>/s/<code>` until TTL or view-count expires.
package share

import (
	"context"
	"errors"
	"io"

	"aegis/crud/storage/blob"
)

// BlobBackend is the subset of blob.Registry-driven operations share
// needs. Extracted so tests can substitute a fake without spinning up
// real driver wiring.
type BlobBackend interface {
	Put(ctx context.Context, bucket, key string, r io.Reader, opts blob.PutOpts) (*blob.ObjectMeta, error)
	Get(ctx context.Context, bucket, key string) (io.ReadCloser, *blob.ObjectMeta, error)
	Stat(ctx context.Context, bucket, key string) (*blob.ObjectMeta, error)
	Delete(ctx context.Context, bucket, key string) error
	PresignGet(ctx context.Context, bucket, key string, opts blob.GetOpts) (*blob.PresignedRequest, error)
	PresignPut(ctx context.Context, bucket, key string, opts blob.PutOpts) (*blob.PresignedRequest, error)
}

type registryBackend struct{ reg *blob.Registry }

func NewBlobBackend(reg *blob.Registry) BlobBackend { return &registryBackend{reg: reg} }

func (b *registryBackend) Put(ctx context.Context, bucket, key string, r io.Reader, opts blob.PutOpts) (*blob.ObjectMeta, error) {
	bkt, err := b.reg.Lookup(bucket)
	if err != nil {
		return nil, err
	}
	return bkt.Driver.Put(ctx, key, r, opts)
}

func (b *registryBackend) Get(ctx context.Context, bucket, key string) (io.ReadCloser, *blob.ObjectMeta, error) {
	bkt, err := b.reg.Lookup(bucket)
	if err != nil {
		return nil, nil, err
	}
	return bkt.Driver.Get(ctx, key)
}

func (b *registryBackend) Stat(ctx context.Context, bucket, key string) (*blob.ObjectMeta, error) {
	bkt, err := b.reg.Lookup(bucket)
	if err != nil {
		return nil, err
	}
	return bkt.Driver.Stat(ctx, key)
}

func (b *registryBackend) Delete(ctx context.Context, bucket, key string) error {
	bkt, err := b.reg.Lookup(bucket)
	if err != nil {
		return err
	}
	return bkt.Driver.Delete(ctx, key)
}

func (b *registryBackend) PresignGet(ctx context.Context, bucket, key string, opts blob.GetOpts) (*blob.PresignedRequest, error) {
	bkt, err := b.reg.Lookup(bucket)
	if err != nil {
		return nil, err
	}
	return bkt.Driver.PresignGet(ctx, key, opts)
}

func (b *registryBackend) PresignPut(ctx context.Context, bucket, key string, opts blob.PutOpts) (*blob.PresignedRequest, error) {
	bkt, err := b.reg.Lookup(bucket)
	if err != nil {
		return nil, err
	}
	return bkt.Driver.PresignPut(ctx, key, opts)
}

// Config is the parsed `[share]` section. Defaults applied at load time.
type Config struct {
	Bucket            string
	PublicBaseURL     string
	DefaultTTLSeconds int64
	MaxTTLSeconds     int64
	MaxViews          int
	MaxUploadBytes    int64
	UserQuotaBytes    int64
}

var (
	ErrShareNotFound    = errors.New("share link not found")
	ErrShareGone        = errors.New("share link no longer available")
	ErrQuotaExceeded    = errors.New("share user quota exceeded")
	ErrUploadTooLarge   = errors.New("file exceeds share upload limit")
	ErrShortCodeFailure = errors.New("could not allocate short code")
	ErrForbidden        = errors.New("forbidden")
	// ErrCommitObjectMissing surfaces when the client tries to commit a
	// pending share but the object hasn't actually been PUT to the
	// backend yet (or has been GC'd).
	ErrCommitObjectMissing = errors.New("share object not found in backend; PUT not completed")
	// ErrCommitSizeMismatch flags a Stat mismatch between the size the
	// client declared and the size the backend reports.
	ErrCommitSizeMismatch = errors.New("share commit size mismatch with backend object")
)

// Lifecycle states for ShareLink.LifecycleState.
const (
	LifecyclePending = "pending"
	LifecycleLive    = "live"
)
