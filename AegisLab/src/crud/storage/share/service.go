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
func (s *Service) View(ctx context.Context, code string) (string, error) {
	link, err := s.repo.FindByCode(ctx, code)
	if err != nil {
		return "", err
	}
	if link.Status != 1 {
		return "", ErrShareGone
	}
	now := s.clock.Now()
	if link.ExpiresAt != nil && !link.ExpiresAt.After(now) {
		return "", ErrShareGone
	}
	if link.MaxViews != nil && link.ViewCount >= *link.MaxViews {
		return "", ErrShareGone
	}
	newCount, err := s.repo.IncrementViewCount(ctx, link.ID)
	if err != nil {
		return "", fmt.Errorf("share: bump view: %w", err)
	}
	if link.MaxViews != nil && newCount > *link.MaxViews {
		return "", ErrShareGone
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
