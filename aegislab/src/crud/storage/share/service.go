package share

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"aegis/crud/storage/blob"

	"github.com/oklog/ulid/v2"
)

// Repo is the persistence surface Service depends on. Implemented by
// *Repository in production; tests pass an in-memory fake.
type Repo interface {
	Create(ctx context.Context, link *ShareLink) error
	FindByCode(ctx context.Context, code string) (*ShareLink, error)
	IncrementViewCount(ctx context.Context, id int64) (int, error)
	SetStatus(ctx context.Context, id int64, status int) error
	SoftDelete(ctx context.Context, id int64) error
	ListByOwner(ctx context.Context, f ListFilter) ([]ShareLink, int64, error)
	SumUserBytes(ctx context.Context, userID int) (int64, error)
	CommitUpdate(ctx context.Context, id int64, lifecycle string, size int64, contentType string) error
}

type Service struct {
	cfg     Config
	repo    Repo
	backend BlobBackend
	clock   blob.Clock
}

func NewService(cfg Config, repo *Repository, backend BlobBackend, clock blob.Clock) *Service {
	return &Service{cfg: cfg, repo: repo, backend: backend, clock: clock}
}

// NewServiceWith allows tests to inject a fake Repo without going
// through gorm.
func NewServiceWith(cfg Config, repo Repo, backend BlobBackend, clock blob.Clock) *Service {
	return &Service{cfg: cfg, repo: repo, backend: backend, clock: clock}
}

func (s *Service) Config() Config { return s.cfg }

type UploadInput struct {
	OwnerUserID int
	Filename    string
	ContentType string
	Size        int64
	Body        io.Reader
	TTLSeconds  int64
	MaxViews    int
}

type UploadResult struct {
	ShortCode string     `json:"short_code"`
	ShareURL  string     `json:"share_url"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	Size      int64      `json:"size"`
}

func (s *Service) Upload(ctx context.Context, in UploadInput) (*UploadResult, error) {
	if s.cfg.MaxUploadBytes > 0 && in.Size > s.cfg.MaxUploadBytes {
		return nil, ErrUploadTooLarge
	}
	if s.cfg.UserQuotaBytes > 0 {
		used, err := s.repo.SumUserBytes(ctx, in.OwnerUserID)
		if err != nil {
			return nil, fmt.Errorf("share: read quota: %w", err)
		}
		if used+in.Size > s.cfg.UserQuotaBytes {
			return nil, ErrQuotaExceeded
		}
	}

	code, err := AllocateShortCode(ctx, s.repo)
	if err != nil {
		return nil, err
	}

	key := s.buildObjectKey(code, in.Filename)
	meta, err := s.backend.Put(ctx, s.cfg.Bucket, key, in.Body, blob.PutOpts{
		ContentType:   in.ContentType,
		ContentLength: in.Size,
	})
	if err != nil {
		return nil, fmt.Errorf("share: put object: %w", err)
	}

	expiresAt := s.computeExpiry(in.TTLSeconds)
	var maxViews *int
	if in.MaxViews > 0 {
		v := in.MaxViews
		if s.cfg.MaxViews > 0 && v > s.cfg.MaxViews {
			v = s.cfg.MaxViews
		}
		maxViews = &v
	}

	size := in.Size
	if size <= 0 && meta != nil {
		size = meta.Size
	}

	link := &ShareLink{
		ShortCode:        code,
		Bucket:           s.cfg.Bucket,
		ObjectKey:        key,
		OwnerUserID:      in.OwnerUserID,
		OriginalFilename: in.Filename,
		ContentType:      in.ContentType,
		SizeBytes:        size,
		ExpiresAt:        expiresAt,
		MaxViews:         maxViews,
		Status:           1,
		LifecycleState:   LifecycleLive,
	}
	if err := s.repo.Create(ctx, link); err != nil {
		_ = s.backend.Delete(ctx, s.cfg.Bucket, key)
		return nil, fmt.Errorf("share: persist link: %w", err)
	}
	return &UploadResult{
		ShortCode: code,
		ShareURL:  s.shareURL(code),
		ExpiresAt: expiresAt,
		Size:      size,
	}, nil
}

// View resolves a code to a presigned GET URL, after checking
// status/expiry/views and atomically incrementing view_count.
// resolveLink runs the View-side validation (status / TTL / view-cap)
// and bumps the view counter exactly once. Returns the live link row so
// the caller can either presign (View) or stream (Stream).
func (s *Service) resolveLink(ctx context.Context, code string) (*ShareLink, error) {
	link, err := s.repo.FindByCode(ctx, code)
	if err != nil {
		return nil, err
	}
	if link.Status != 1 {
		return nil, ErrShareGone
	}
	if link.LifecycleState != "" && link.LifecycleState != LifecycleLive {
		// Pending presigned uploads are not viewable until the PUT lands
		// and CommitUpload flips lifecycle_state to 'live'.
		return nil, ErrShareGone
	}
	now := s.clock.Now()
	if link.ExpiresAt != nil && !link.ExpiresAt.After(now) {
		return nil, ErrShareGone
	}
	if link.MaxViews != nil && link.ViewCount >= *link.MaxViews {
		return nil, ErrShareGone
	}
	newCount, err := s.repo.IncrementViewCount(ctx, link.ID)
	if err != nil {
		return nil, fmt.Errorf("share: bump view: %w", err)
	}
	if link.MaxViews != nil && newCount > *link.MaxViews {
		return nil, ErrShareGone
	}
	return link, nil
}

// Stream returns the live byte stream for a share code, used by the
// `/s/:code` handler when the underlying presigned URL host is internal
// (S3 / RustFS) and not externally reachable. Caller must close the
// returned reader.
func (s *Service) Stream(ctx context.Context, code string) (io.ReadCloser, *blob.ObjectMeta, *ShareLink, error) {
	link, err := s.resolveLink(ctx, code)
	if err != nil {
		return nil, nil, nil, err
	}
	rc, meta, err := s.backend.Get(ctx, link.Bucket, link.ObjectKey)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("share: get: %w", err)
	}
	return rc, meta, link, nil
}

func (s *Service) View(ctx context.Context, code string) (string, error) {
	link, err := s.resolveLink(ctx, code)
	if err != nil {
		return "", err
	}
	pr, err := s.backend.PresignGet(ctx, link.Bucket, link.ObjectKey, blob.GetOpts{
		ResponseContentType:        link.ContentType,
		ResponseContentDisposition: contentDisposition(link.OriginalFilename),
		TTL:                        5 * time.Minute,
	})
	if err != nil {
		return "", fmt.Errorf("share: presign: %w", err)
	}
	return pr.URL, nil
}

func (s *Service) Get(ctx context.Context, code string, callerID int, isAdmin bool) (*ShareLink, error) {
	link, err := s.repo.FindByCode(ctx, code)
	if err != nil {
		return nil, err
	}
	if !isAdmin && link.OwnerUserID != callerID {
		return nil, ErrForbidden
	}
	return link, nil
}

func (s *Service) Revoke(ctx context.Context, code string, callerID int, isAdmin bool) error {
	link, err := s.repo.FindByCode(ctx, code)
	if err != nil {
		return err
	}
	if !isAdmin && link.OwnerUserID != callerID {
		return ErrForbidden
	}
	if err := s.repo.SetStatus(ctx, link.ID, 0); err != nil {
		return err
	}
	if err := s.repo.SoftDelete(ctx, link.ID); err != nil {
		return err
	}
	_ = s.backend.Delete(ctx, link.Bucket, link.ObjectKey)
	return nil
}

func (s *Service) ListOwn(ctx context.Context, ownerID int, page, size int, includeExpired bool) ([]ShareLink, int64, error) {
	return s.repo.ListByOwner(ctx, ListFilter{
		OwnerUserID:    ownerID,
		IncludeExpired: includeExpired,
		Page:           page,
		Size:           size,
		Now:            s.clock.Now().Unix(),
	})
}

// InitUploadInput is what a client sends to /api/v2/share/init to
// reserve a code + presigned PUT URL before transferring the body.
type InitUploadInput struct {
	OwnerUserID int
	Filename    string
	ContentType string
	Size        int64
	TTLSeconds  int64
	MaxViews    int
}

// InitUploadResult is the server's response to InitUpload — code,
// presigned PUT URL, expiry, and the commit URL the client must call
// after the PUT completes.
type InitUploadResult struct {
	Code         string            `json:"code"`
	PresignedURL string            `json:"presigned_put_url"`
	Method       string            `json:"method"`
	Headers      map[string]string `json:"headers,omitempty"`
	ExpiresAt    time.Time         `json:"expires_at"`
	MaxSize      int64             `json:"max_size"`
	CommitURL    string            `json:"commit_url"`
	Bucket       string            `json:"bucket"`
	ObjectKey    string            `json:"object_key"`
}

// presignedPutTTL is the lifetime of the PUT URL returned to clients.
// 15 minutes is the trade-off between letting a slow international
// link finish (the original 7m54s case was ~38 KB/s for 17.9 MB) and
// not leaving forgotten reservations open forever.
const presignedPutTTL = 15 * time.Minute

func (s *Service) InitUpload(ctx context.Context, in InitUploadInput) (*InitUploadResult, error) {
	if s.cfg.MaxUploadBytes > 0 && in.Size > s.cfg.MaxUploadBytes {
		return nil, ErrUploadTooLarge
	}
	if in.Size < 0 {
		return nil, fmt.Errorf("share: negative size")
	}
	if s.cfg.UserQuotaBytes > 0 {
		used, err := s.repo.SumUserBytes(ctx, in.OwnerUserID)
		if err != nil {
			return nil, fmt.Errorf("share: read quota: %w", err)
		}
		if used+in.Size > s.cfg.UserQuotaBytes {
			return nil, ErrQuotaExceeded
		}
	}

	code, err := AllocateShortCode(ctx, s.repo)
	if err != nil {
		return nil, err
	}
	key := s.buildObjectKey(code, in.Filename)

	pr, err := s.backend.PresignPut(ctx, s.cfg.Bucket, key, blob.PutOpts{
		ContentType:   in.ContentType,
		ContentLength: in.Size,
		TTL:           presignedPutTTL,
	})
	if err != nil {
		return nil, fmt.Errorf("share: presign put: %w", err)
	}

	expiresAt := s.computeExpiry(in.TTLSeconds)
	var maxViews *int
	if in.MaxViews > 0 {
		v := in.MaxViews
		if s.cfg.MaxViews > 0 && v > s.cfg.MaxViews {
			v = s.cfg.MaxViews
		}
		maxViews = &v
	}

	link := &ShareLink{
		ShortCode:        code,
		Bucket:           s.cfg.Bucket,
		ObjectKey:        key,
		OwnerUserID:      in.OwnerUserID,
		OriginalFilename: in.Filename,
		ContentType:      in.ContentType,
		SizeBytes:        in.Size,
		ExpiresAt:        expiresAt,
		MaxViews:         maxViews,
		Status:           1,
		LifecycleState:   LifecyclePending,
	}
	if err := s.repo.Create(ctx, link); err != nil {
		return nil, fmt.Errorf("share: persist pending: %w", err)
	}

	prExpires := pr.Expires
	if prExpires.IsZero() {
		prExpires = s.clock.Now().Add(presignedPutTTL)
	}
	return &InitUploadResult{
		Code:         code,
		PresignedURL: pr.URL,
		Method:       pr.Method,
		Headers:      pr.Headers,
		ExpiresAt:    prExpires,
		MaxSize:      s.cfg.MaxUploadBytes,
		CommitURL:    "/api/v2/share/" + code + "/commit",
		Bucket:       s.cfg.Bucket,
		ObjectKey:    key,
	}, nil
}

// CommitUploadInput is the body of /api/v2/share/{code}/commit. All
// fields are optional verifications — the server uses them to detect a
// PUT that landed truncated or with a different content-type than the
// client expected.
type CommitUploadInput struct {
	OwnerUserID int
	Code        string
	Size        int64
	ContentType string
	SHA256      string
}

func (s *Service) CommitUpload(ctx context.Context, in CommitUploadInput) (*UploadResult, error) {
	link, err := s.repo.FindByCode(ctx, in.Code)
	if err != nil {
		return nil, err
	}
	if link.OwnerUserID != in.OwnerUserID {
		return nil, ErrForbidden
	}

	// Idempotency: if a previous commit already flipped to live, return
	// the existing row as-is. The client retried — that's fine.
	if link.LifecycleState == LifecycleLive {
		return &UploadResult{
			ShortCode: link.ShortCode,
			ShareURL:  s.shareURL(link.ShortCode),
			ExpiresAt: link.ExpiresAt,
			Size:      link.SizeBytes,
		}, nil
	}
	if link.LifecycleState != LifecyclePending {
		return nil, ErrShareGone
	}

	meta, err := s.backend.Stat(ctx, link.Bucket, link.ObjectKey)
	if err != nil {
		return nil, ErrCommitObjectMissing
	}

	finalSize := link.SizeBytes
	if meta.Size > 0 {
		finalSize = meta.Size
	}
	// Two mismatch sources: declared-at-init size vs. actual stored size,
	// and (if the client repeats it on commit) declared-at-commit vs.
	// stored. Both must match to detect a truncated PUT.
	if meta.Size > 0 && link.SizeBytes > 0 && link.SizeBytes != meta.Size {
		return nil, ErrCommitSizeMismatch
	}
	if in.Size > 0 && meta.Size > 0 && in.Size != meta.Size {
		return nil, ErrCommitSizeMismatch
	}
	if s.cfg.MaxUploadBytes > 0 && finalSize > s.cfg.MaxUploadBytes {
		// PUT exceeded the declared size — drop the object and the
		// pending row so the caller can't sneak past the quota check.
		_ = s.backend.Delete(ctx, link.Bucket, link.ObjectKey)
		_ = s.repo.SetStatus(ctx, link.ID, 0)
		return nil, ErrUploadTooLarge
	}

	finalContentType := link.ContentType
	if in.ContentType != "" {
		finalContentType = in.ContentType
	} else if meta.ContentType != "" {
		finalContentType = meta.ContentType
	}

	if err := s.repo.CommitUpdate(ctx, link.ID, LifecycleLive, finalSize, finalContentType); err != nil {
		return nil, fmt.Errorf("share: commit: %w", err)
	}
	link.LifecycleState = LifecycleLive
	link.SizeBytes = finalSize
	link.ContentType = finalContentType

	return &UploadResult{
		ShortCode: link.ShortCode,
		ShareURL:  s.shareURL(link.ShortCode),
		ExpiresAt: link.ExpiresAt,
		Size:      finalSize,
	}, nil
}

func (s *Service) computeExpiry(ttlSeconds int64) *time.Time {
	ttl := ttlSeconds
	if ttl <= 0 {
		ttl = s.cfg.DefaultTTLSeconds
	}
	if s.cfg.MaxTTLSeconds > 0 && ttl > s.cfg.MaxTTLSeconds {
		ttl = s.cfg.MaxTTLSeconds
	}
	if ttl <= 0 {
		return nil
	}
	t := s.clock.Now().Add(time.Duration(ttl) * time.Second)
	return &t
}

func (s *Service) shareURL(code string) string {
	base := strings.TrimRight(s.cfg.PublicBaseURL, "/")
	return base + "/s/" + code
}

func (s *Service) buildObjectKey(code, filename string) string {
	ext := strings.ToLower(path.Ext(filename))
	id := ulid.MustNew(ulid.Timestamp(s.clock.Now()), randReader{})
	return "share/" + code + "/" + id.String() + ext
}

func contentDisposition(filename string) string {
	if filename == "" {
		return "attachment"
	}
	safe := strings.ReplaceAll(filename, "\"", "")
	return fmt.Sprintf("attachment; filename=\"%s\"", safe)
}

type randReader struct{}

func (randReader) Read(p []byte) (int, error) { return rand.Read(p) }
