// Package blob is the in-process producer surface for object storage.
// Mirrors module/notification: a small interface + a registry + a set
// of pluggable drivers, fronted by a thin HTTP handler so the package
// works equally well in the monolith and in the standalone aegis-blob
// microservice.
//
// Six-role separation:
//
//	Ingestion → Authorization → Routing (bucket) → Driver → Lifecycle → Observability
package blob

import (
	"context"
	"errors"
	"io"
	"time"
)

// Driver is the pluggable backend surface. v1 ships localfs (signed
// token URLs handled by /raw/:token) and an s3 stub. RustFS / MinIO
// / AWS / Aliyun OSS all reuse the s3 driver once it lands.
type Driver interface {
	Name() string
	PresignPut(ctx context.Context, key string, opts PutOpts) (*PresignedRequest, error)
	PresignGet(ctx context.Context, key string, opts GetOpts) (*PresignedRequest, error)
	Put(ctx context.Context, key string, r io.Reader, opts PutOpts) (*ObjectMeta, error)
	Get(ctx context.Context, key string) (io.ReadCloser, *ObjectMeta, error)
	Stat(ctx context.Context, key string) (*ObjectMeta, error)
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, opts ListObjectsOpts) (*ListResult, error)
}

// ListObjectsOpts is the driver-level paginated list request. The
// continuation token is opaque to callers — drivers may carry a key
// (localfs) or an S3-native continuation token. Delimiter switches the
// listing to hierarchical mode and surfaces CommonPrefixes on the
// result.
type ListObjectsOpts struct {
	Prefix            string
	ContinuationToken string
	Delimiter         string
	MaxKeys           int
}

// PresignedRequest is what a driver returns from PresignPut/PresignGet
// — the frontend hits URL directly with Method + Headers.
type PresignedRequest struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Expires time.Time         `json:"expires_at"`
}

// PutOpts captures everything a producer can ask of a Put / PresignPut.
type PutOpts struct {
	ContentType   string
	ContentLength int64
	Metadata      map[string]string
	CacheControl  string
	// TTL is how long the presigned URL stays valid. Driver may clamp.
	TTL time.Duration
}

// GetOpts captures PresignGet knobs (response headers, inline vs
// attachment, TTL).
type GetOpts struct {
	ResponseContentType        string
	ResponseContentDisposition string
	TTL                        time.Duration
}

// ObjectMeta is the storage-side metadata for one object. The DB row
// (ObjectRecord) carries the same fields plus platform-side
// bookkeeping (entity_kind/entity_id, uploaded_by, soft delete).
type ObjectMeta struct {
	Key         string            `json:"key"`
	Size        int64             `json:"size_bytes"`
	ContentType string            `json:"content_type"`
	ETag        string            `json:"etag,omitempty"`
	UpdatedAt   time.Time         `json:"updated_at"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// ListResult is one page of a List call. Drivers populate Items
// always; CommonPrefixes is non-empty only when ListObjectsOpts.Delimiter
// was set; NextContinuationToken is non-empty iff IsTruncated.
type ListResult struct {
	Items                 []ObjectMeta `json:"items"`
	CommonPrefixes        []string     `json:"common_prefixes,omitempty"`
	NextContinuationToken string       `json:"next_continuation_token,omitempty"`
	IsTruncated           bool         `json:"is_truncated,omitempty"`
}

// Operation tags a signed token's intent. Localfs driver embeds this
// in the HMAC payload so a GET token cannot be replayed as a PUT.
type Operation string

const (
	OpPut Operation = "put"
	OpGet Operation = "get"
)

// Common errors. Producers and the HTTP handler both branch on these.
var (
	ErrBucketNotFound       = errors.New("bucket not found")
	ErrObjectNotFound       = errors.New("object not found")
	ErrDriverNotImplemented = errors.New("driver not implemented")
	ErrTokenInvalid         = errors.New("invalid or expired token")
	ErrUnauthorized         = errors.New("unauthorized for bucket")
	ErrObjectTooLarge       = errors.New("object exceeds bucket size limit")
	ErrContentTypeRejected  = errors.New("content type not allowed for bucket")
)

// Clock is injectable so tests can pin time. Default impl is realClock.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// NewClock returns the wall-clock implementation.
func NewClock() Clock { return realClock{} }
