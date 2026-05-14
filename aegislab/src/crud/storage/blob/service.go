package blob

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
)

// Service is the in-process producer API. The HTTP handler is a thin
// translator that calls these methods; the LocalClient SDK in
// module/blobclient calls them directly.
//
// Service composes registry (routing), repository (metadata), clock
// (testability) and is the single seam producers depend on.
type Service struct {
	registry *Registry
	repo     *Repository
	clock    Clock
}

func NewService(reg *Registry, repo *Repository, clock Clock) *Service {
	return &Service{registry: reg, repo: repo, clock: clock}
}

// PresignPutInput captures everything a caller needs to mint a put URL.
type PresignPutInput struct {
	Bucket        string
	Key           string // optional — service fills with {entity_kind}/{ulid}
	ContentType   string
	ContentLength int64
	EntityKind    string
	EntityID      string
	UploadedBy    *int
	Metadata      map[string]string
	TTL           time.Duration
}

// PresignPutResult is what the handler returns to the producer.
type PresignPutResult struct {
	ObjectID  int64             `json:"object_id"`
	Bucket    string            `json:"bucket"`
	Key       string            `json:"key"`
	Presigned *PresignedRequest `json:"presigned"`
}

// PresignPut routes to the bucket's driver, mints the presigned URL,
// then writes a placeholder metadata row. Metadata is written after
// the presign succeeds so failures don't leave orphan rows.
func (s *Service) PresignPut(ctx context.Context, in PresignPutInput) (*PresignPutResult, error) {
	b, err := s.registry.Lookup(in.Bucket)
	if err != nil {
		return nil, err
	}
	if in.ContentLength > 0 && b.Config.MaxObjectBytes > 0 && in.ContentLength > b.Config.MaxObjectBytes {
		return nil, ErrObjectTooLarge
	}
	if !b.Config.AllowsContentType(in.ContentType) {
		return nil, ErrContentTypeRejected
	}
	if in.Key == "" {
		in.Key = GenerateKey(in.EntityKind)
	}

	pr, err := b.Driver.PresignPut(ctx, in.Key, PutOpts{
		ContentType:   in.ContentType,
		ContentLength: in.ContentLength,
		Metadata:      in.Metadata,
		TTL:           in.TTL,
	})
	if err != nil {
		return nil, fmt.Errorf("presign put: %w", err)
	}

	rec := &ObjectRecord{
		Bucket:      in.Bucket,
		StorageKey:  in.Key,
		SizeBytes:   in.ContentLength,
		ContentType: in.ContentType,
		EntityKind:  in.EntityKind,
		EntityID:    in.EntityID,
		UploadedBy:  in.UploadedBy,
	}
	if b.Config.RetentionDays > 0 {
		exp := s.clock.Now().Add(time.Duration(b.Config.RetentionDays) * 24 * time.Hour)
		rec.ExpiresAt = &exp
	}
	if err := s.repo.Create(ctx, rec); err != nil {
		return nil, fmt.Errorf("persist metadata: %w", err)
	}
	return &PresignPutResult{
		ObjectID:  rec.ID,
		Bucket:    in.Bucket,
		Key:       in.Key,
		Presigned: pr,
	}, nil
}

// PresignGet routes to the bucket's driver after a metadata check.
func (s *Service) PresignGet(ctx context.Context, bucket, key string, opts GetOpts) (*PresignedRequest, error) {
	b, err := s.registry.Lookup(bucket)
	if err != nil {
		return nil, err
	}
	if _, err := s.repo.FindByKey(ctx, bucket, key); err != nil {
		return nil, err
	}
	return b.Driver.PresignGet(ctx, key, opts)
}

// Stat returns metadata for a single object — DB row enriched with
// the driver's live stat.
func (s *Service) Stat(ctx context.Context, bucket, key string) (*ObjectMeta, error) {
	b, err := s.registry.Lookup(bucket)
	if err != nil {
		return nil, err
	}
	if _, err := s.repo.FindByKey(ctx, bucket, key); err != nil {
		return nil, err
	}
	return b.Driver.Stat(ctx, key)
}

// Get streams bytes back from the driver. Used by the inline-GET
// handler for small objects.
func (s *Service) Get(ctx context.Context, bucket, key string) (io.ReadCloser, *ObjectMeta, error) {
	b, err := s.registry.Lookup(bucket)
	if err != nil {
		return nil, nil, err
	}
	if _, err := s.repo.FindByKey(ctx, bucket, key); err != nil {
		return nil, nil, err
	}
	return b.Driver.Get(ctx, key)
}

// Delete soft-deletes the metadata row and best-effort removes the
// driver-side bytes synchronously. The lifecycle worker (DeletionWorker)
// re-runs the driver delete for rows where the inline call failed.
func (s *Service) Delete(ctx context.Context, bucket, key string) error {
	b, err := s.registry.Lookup(bucket)
	if err != nil {
		return err
	}
	if err := s.repo.SoftDelete(ctx, bucket, key); err != nil {
		return err
	}
	return b.Driver.Delete(ctx, key)
}

// List returns metadata rows matching the filter. Driver-side listing
// is not exposed through this method because the DB is the source of
// truth for "what objects exist as far as the platform knows".
func (s *Service) List(ctx context.Context, f ListFilter) ([]ObjectRecord, error) {
	return s.repo.List(ctx, f)
}

// ListObjects performs a driver-level paginated list and returns the
// raw storage view (S3-style continuation tokens, optional delimiter).
// Distinct from List(ctx, ListFilter) which reads from the metadata
// DB — this method is the source of truth for "what bytes does the
// backend actually hold under this prefix".
func (s *Service) ListObjects(ctx context.Context, bucket string, opts ListObjectsOpts) (*ListResult, error) {
	b, err := s.registry.Lookup(bucket)
	if err != nil {
		return nil, err
	}
	return b.Driver.List(ctx, opts)
}

// GetReader is the streaming counterpart of GetBytes. It returns a
// reader the caller is responsible for closing. Callers use it for
// HTTP range responses and zip streaming where loading into memory is
// undesirable.
func (s *Service) GetReader(ctx context.Context, bucket, key string) (io.ReadCloser, *ObjectMeta, error) {
	return s.Get(ctx, bucket, key)
}

// PutBytes is the small-payload helper used by LocalClient.
func (s *Service) PutBytes(ctx context.Context, in PresignPutInput, body io.Reader) (*ObjectRecord, *ObjectMeta, error) {
	b, err := s.registry.Lookup(in.Bucket)
	if err != nil {
		return nil, nil, err
	}
	if in.Key == "" {
		in.Key = GenerateKey(in.EntityKind)
	}
	if !b.Config.AllowsContentType(in.ContentType) {
		return nil, nil, ErrContentTypeRejected
	}
	meta, err := b.Driver.Put(ctx, in.Key, body, PutOpts{
		ContentType:   in.ContentType,
		ContentLength: in.ContentLength,
		Metadata:      in.Metadata,
	})
	if err != nil {
		return nil, nil, err
	}
	rec := &ObjectRecord{
		Bucket: in.Bucket, StorageKey: in.Key, SizeBytes: meta.Size,
		ContentType: meta.ContentType, ETag: meta.ETag,
		EntityKind: in.EntityKind, EntityID: in.EntityID, UploadedBy: in.UploadedBy,
	}
	if b.Config.RetentionDays > 0 {
		exp := s.clock.Now().Add(time.Duration(b.Config.RetentionDays) * 24 * time.Hour)
		rec.ExpiresAt = &exp
	}
	if err := s.repo.Create(ctx, rec); err != nil {
		return nil, nil, err
	}
	return rec, meta, nil
}

// Registry exposes the routing role for the handler to look up bucket
// policy without going through every service method.
func (s *Service) Registry() *Registry { return s.registry }

// GenerateKey is the default key generator: `{entity_kind}/{ulid}`.
// Producers may override by setting PresignPutInput.Key.
func GenerateKey(entityKind string) string {
	id := ulid.MustNew(ulid.Timestamp(time.Now()), randReader{})
	kind := strings.TrimSpace(entityKind)
	if kind == "" {
		kind = "object"
	}
	return kind + "/" + id.String()
}

type randReader struct{}

func (randReader) Read(p []byte) (int, error) { return rand.Read(p) }
