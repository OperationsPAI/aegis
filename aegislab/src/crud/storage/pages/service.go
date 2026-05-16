package pages

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"
	"time"

	blobclient "aegis/clients/blob"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// resolveSlugMaxAttempts caps the auto-suffix loop on derived slugs so a
// pathological collision spike (or buggy unique-index translation) cannot
// stall the request indefinitely.
const resolveSlugMaxAttempts = 1000

// Domain errors. The handler maps these to HTTP codes; do not return raw
// repo / blob errors above the service layer.
var (
	ErrInvalidSlug       = errors.New("invalid_slug")
	ErrSlugTaken         = errors.New("slug_taken")
	ErrInvalidVisibility = errors.New("invalid_visibility")
	ErrNoFiles           = errors.New("no_files")
	ErrPathTraversal     = errors.New("path_traversal")
	ErrFileTooLarge      = errors.New("file_too_large")
	ErrTotalTooLarge     = errors.New("total_too_large")
	ErrTooManyFiles      = errors.New("too_many_files")
	ErrForbidden         = errors.New("forbidden")
)

// UploadFile is one item in a create / replace upload batch. Path is the
// site-relative key (e.g. "index.md", "assets/logo.png"). Path is the
// caller's responsibility; the service validates it again.
type UploadFile struct {
	Path        string
	ContentType string
	Body        []byte
}

// FileEntry is the listing shape returned by Detail.
type FileEntry struct {
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
}

// Service holds the business rules. The blob client is the producer-side
// interface; that is the only seam between this module and storage.
type Service struct {
	repo *Repository
	blob blobclient.Client
}

func NewService(repo *Repository, blob blobclient.Client) *Service {
	return &Service{repo: repo, blob: blob}
}

// CreateSite validates input, claims a slug + UUID via DB unique-violation
// (no read-then-write race), then writes blobs, then updates the row's
// counters. On any failure after the row is committed we best-effort clean
// the partial blob prefix; the row stays so the caller can retry via
// /upload to refill.
func (s *Service) CreateSite(ctx context.Context, ownerID int, slug, visibility, title string, files []UploadFile) (*PageSite, error) {
	if visibility == "" {
		visibility = VisibilityPublicUnlisted
	}
	if !IsValidVisibility(visibility) {
		return nil, ErrInvalidVisibility
	}
	if len(files) == 0 {
		return nil, ErrNoFiles
	}
	if err := s.checkFiles(files); err != nil {
		return nil, err
	}
	cleaned, err := s.cleanFiles(files)
	if err != nil {
		return nil, err
	}
	if !hasMarkdown(cleaned) {
		return nil, ErrNoFiles
	}

	site, err := s.claimSlugRow(ctx, ownerID, slug, visibility, title, cleaned)
	if err != nil {
		return nil, err
	}

	if err := s.writeFiles(ctx, site, cleaned); err != nil {
		// Best-effort cleanup of any partial prefix; row stays for retry.
		s.cleanupOrphanBlobs(ctx, site.SiteUUID)
		return nil, err
	}
	site.FileCount = int32(len(cleaned))
	site.SizeBytes = totalSize(cleaned)
	site.UpdatedAt = time.Now().UTC()
	if err := s.repo.Update(ctx, site); err != nil {
		s.cleanupOrphanBlobs(ctx, site.SiteUUID)
		return nil, err
	}
	return site, nil
}

// claimSlugRow inserts the row that owns the desired slug. If the caller
// passed an explicit slug, that's used verbatim; an empty slug is
// auto-derived from title/filename. Slug collisions are detected by the
// DB's unique index — for derived slugs we retry with -2, -3, … up to
// resolveSlugMaxAttempts; for an explicit slug we surface ErrSlugTaken
// on the first collision.
func (s *Service) claimSlugRow(ctx context.Context, ownerID int, requestedSlug, visibility, title string, cleaned []UploadFile) (*PageSite, error) {
	if requestedSlug != "" {
		if !SlugRegex.MatchString(requestedSlug) {
			return nil, ErrInvalidSlug
		}
		return s.insertRow(ctx, ownerID, requestedSlug, visibility, title)
	}
	base := deriveSlug(title, cleaned)
	if base == "" {
		base = "site"
	}
	if !SlugRegex.MatchString(base) {
		base = sanitiseSlug(base)
		if !SlugRegex.MatchString(base) {
			base = "site"
		}
	}
	candidate := base
	for i := 2; i <= resolveSlugMaxAttempts; i++ {
		site, err := s.insertRow(ctx, ownerID, candidate, visibility, title)
		if err == nil {
			return site, nil
		}
		if !errors.Is(err, ErrSlugTaken) {
			return nil, err
		}
		candidate = fmt.Sprintf("%s-%d", base, i)
		if !SlugRegex.MatchString(candidate) {
			return nil, ErrInvalidSlug
		}
	}
	return nil, ErrSlugTaken
}

// insertRow tries a single insert; gorm.ErrDuplicatedKey collapses to
// ErrSlugTaken so callers can react without inspecting raw DB errors.
func (s *Service) insertRow(ctx context.Context, ownerID int, slug, visibility, title string) (*PageSite, error) {
	now := time.Now().UTC()
	site := &PageSite{
		SiteUUID:   uuid.NewString(),
		OwnerID:    ownerID,
		Slug:       slug,
		Visibility: visibility,
		Title:      title,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := s.repo.Create(ctx, site); err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, ErrSlugTaken
		}
		return nil, err
	}
	return site, nil
}

// ReplaceFiles is an atomic-ish replace: list+delete current prefix, then
// write the new set. The DB row is updated on success.
func (s *Service) ReplaceFiles(ctx context.Context, callerID int, siteID int64, files []UploadFile) (*PageSite, error) {
	site, err := s.repo.FindByID(ctx, siteID)
	if err != nil {
		return nil, err
	}
	if site.OwnerID != callerID {
		return nil, ErrForbidden
	}
	if len(files) == 0 {
		return nil, ErrNoFiles
	}
	if err := s.checkFiles(files); err != nil {
		return nil, err
	}
	cleaned, err := s.cleanFiles(files)
	if err != nil {
		return nil, err
	}
	if !hasMarkdown(cleaned) {
		return nil, ErrNoFiles
	}
	if err := s.deletePrefix(ctx, site.SiteUUID); err != nil {
		return nil, fmt.Errorf("delete prefix: %w", err)
	}
	if err := s.writeFiles(ctx, site, cleaned); err != nil {
		return nil, err
	}
	site.FileCount = int32(len(cleaned))
	site.SizeBytes = totalSize(cleaned)
	site.UpdatedAt = time.Now().UTC()
	if err := s.repo.Update(ctx, site); err != nil {
		return nil, err
	}
	return site, nil
}

// UpdateMeta patches slug / visibility / title. Nil pointers leave the
// field untouched. Slug changes are validated against the regex + taken-set.
func (s *Service) UpdateMeta(ctx context.Context, callerID int, siteID int64, slug, visibility, title *string) (*PageSite, error) {
	site, err := s.repo.FindByID(ctx, siteID)
	if err != nil {
		return nil, err
	}
	if site.OwnerID != callerID {
		return nil, ErrForbidden
	}
	if slug != nil && *slug != site.Slug {
		if !SlugRegex.MatchString(*slug) {
			return nil, ErrInvalidSlug
		}
		taken, err := s.repo.SlugExists(ctx, *slug)
		if err != nil {
			return nil, err
		}
		if taken {
			return nil, ErrSlugTaken
		}
		site.Slug = *slug
	}
	if visibility != nil {
		if !IsValidVisibility(*visibility) {
			return nil, ErrInvalidVisibility
		}
		site.Visibility = *visibility
	}
	if title != nil {
		site.Title = *title
	}
	site.UpdatedAt = time.Now().UTC()
	if err := s.repo.Update(ctx, site); err != nil {
		return nil, err
	}
	return site, nil
}

// DeleteSite removes the DB row first, then best-effort cleans the blob
// prefix. A transient blob failure leaves an orphaned prefix that gets
// logged for later GC — the caller sees success because the row is gone
// (idempotent re-delete would 404). Reversing the order would expose a
// stale DB row with phantom files on transient blob errors.
func (s *Service) DeleteSite(ctx context.Context, callerID int, siteID int64) error {
	site, err := s.repo.FindByID(ctx, siteID)
	if err != nil {
		return err
	}
	if site.OwnerID != callerID {
		return ErrForbidden
	}
	if err := s.repo.Delete(ctx, site.ID); err != nil {
		return err
	}
	s.cleanupOrphanBlobs(ctx, site.SiteUUID)
	return nil
}

// Mine returns the caller's sites.
func (s *Service) Mine(ctx context.Context, callerID, limit, offset int) ([]PageSite, error) {
	return s.repo.ListByOwner(ctx, callerID, limit, offset)
}

// Public returns sites whose visibility = public_listed.
func (s *Service) Public(ctx context.Context, limit, offset int) ([]PageSite, error) {
	return s.repo.ListPublic(ctx, limit, offset)
}

// Detail returns the site row plus its file listing. Private sites
// collapse to ErrNotFound for non-owners (anonymous or otherwise) so the
// API does not leak existence — mirrors the SSR /p/:slug behaviour.
func (s *Service) Detail(ctx context.Context, callerID int, siteID int64) (*PageSite, []FileEntry, error) {
	site, err := s.repo.FindByID(ctx, siteID)
	if err != nil {
		return nil, nil, err
	}
	if site.Visibility == VisibilityPrivate && site.OwnerID != callerID {
		return nil, nil, ErrNotFound
	}
	files, err := s.listFiles(ctx, site.SiteUUID)
	if err != nil {
		return nil, nil, err
	}
	return site, files, nil
}

// FindBySlug is the SSR entrypoint — slug → site.
func (s *Service) FindBySlug(ctx context.Context, slug string) (*PageSite, error) {
	return s.repo.FindBySlug(ctx, slug)
}

// FetchBytes returns the bytes at {site.SiteUUID}/{cleanPath}.
func (s *Service) FetchBytes(ctx context.Context, site *PageSite, cleanPath string) ([]byte, *blobclient.ObjectMeta, error) {
	return s.blob.GetBytes(ctx, BucketName, path.Join(site.SiteUUID, cleanPath))
}

// FetchReader returns a streaming reader at {site.SiteUUID}/{cleanPath}.
func (s *Service) FetchReader(ctx context.Context, site *PageSite, cleanPath string) (io.ReadCloser, *blobclient.ObjectMeta, error) {
	return s.blob.GetReader(ctx, BucketName, path.Join(site.SiteUUID, cleanPath))
}

// ListMarkdownFiles returns just the .md paths under the site prefix, sorted.
// Used by the renderer to build the sidebar nav.
func (s *Service) ListMarkdownFiles(ctx context.Context, site *PageSite) ([]string, error) {
	files, err := s.listFiles(ctx, site.SiteUUID)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(files))
	for _, f := range files {
		if strings.HasSuffix(strings.ToLower(f.Path), ".md") {
			out = append(out, f.Path)
		}
	}
	return out, nil
}

// ---- internal helpers ----

func (s *Service) checkFiles(files []UploadFile) error {
	if len(files) > MaxFiles {
		return ErrTooManyFiles
	}
	var total int64
	for _, f := range files {
		size := int64(len(f.Body))
		if size > MaxFileSize {
			return ErrFileTooLarge
		}
		total += size
		if total > MaxTotalSize {
			return ErrTotalTooLarge
		}
	}
	return nil
}

func (s *Service) cleanFiles(files []UploadFile) ([]UploadFile, error) {
	out := make([]UploadFile, 0, len(files))
	for _, f := range files {
		cleaned, err := cleanRelPath(f.Path)
		if err != nil {
			return nil, err
		}
		f.Path = cleaned
		out = append(out, f)
	}
	return out, nil
}

func (s *Service) writeFiles(ctx context.Context, site *PageSite, files []UploadFile) error {
	for _, f := range files {
		key := path.Join(site.SiteUUID, f.Path)
		ct := f.ContentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		if _, err := s.blob.PutBytes(ctx, BucketName, f.Body, blobclient.PresignPutReq{
			Key:           key,
			ContentType:   ct,
			ContentLength: int64(len(f.Body)),
			EntityKind:    "page_site",
			EntityID:      site.SiteUUID,
		}); err != nil {
			return fmt.Errorf("put %s: %w", key, err)
		}
	}
	return nil
}

func (s *Service) deletePrefix(ctx context.Context, siteUUID string) error {
	prefix := siteUUID + "/"
	var token string
	for {
		page, err := s.blob.List(ctx, BucketName, prefix, blobclient.ListOpts{
			ContinuationToken: token,
		})
		if err != nil {
			return err
		}
		for _, obj := range page.Objects {
			if err := s.blob.Delete(ctx, BucketName, obj.Key); err != nil {
				return err
			}
		}
		if !page.IsTruncated {
			return nil
		}
		token = page.NextContinuationToken
	}
}

func (s *Service) listFiles(ctx context.Context, siteUUID string) ([]FileEntry, error) {
	prefix := siteUUID + "/"
	var (
		out   []FileEntry
		token string
	)
	for {
		page, err := s.blob.List(ctx, BucketName, prefix, blobclient.ListOpts{
			ContinuationToken: token,
		})
		if err != nil {
			return nil, err
		}
		for _, obj := range page.Objects {
			out = append(out, FileEntry{
				Path:      strings.TrimPrefix(obj.Key, prefix),
				SizeBytes: obj.Size,
			})
		}
		if !page.IsTruncated {
			break
		}
		token = page.NextContinuationToken
	}
	return out, nil
}

// cleanupOrphanBlobs best-effort deletes every key under {siteUUID}/.
// Errors are logged with siteUUID so an external GC can pick up the
// orphan prefix; callers never see the underlying error because the row
// state is already authoritative.
func (s *Service) cleanupOrphanBlobs(ctx context.Context, siteUUID string) {
	if err := s.deletePrefix(ctx, siteUUID); err != nil {
		logrus.WithFields(logrus.Fields{
			"site_uuid": siteUUID,
			"bucket":    BucketName,
			"error":     err.Error(),
		}).Warn("pages: orphaned blob prefix, manual GC required")
	}
}

func deriveSlug(title string, files []UploadFile) string {
	if t := sanitiseSlug(title); t != "" {
		return t
	}
	for _, f := range files {
		base := path.Base(f.Path)
		ext := path.Ext(base)
		name := strings.TrimSuffix(base, ext)
		if s := sanitiseSlug(name); s != "" {
			return s
		}
	}
	return ""
}

func sanitiseSlug(in string) string {
	var b strings.Builder
	prevDash := true
	for _, r := range strings.ToLower(in) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		case r == '-', r == '_', r == ' ', r == '.':
			if !prevDash && b.Len() > 0 {
				b.WriteRune('-')
				prevDash = true
			}
		}
		if b.Len() >= 63 {
			break
		}
	}
	out := strings.Trim(b.String(), "-")
	return out
}

func hasMarkdown(files []UploadFile) bool {
	for _, f := range files {
		if strings.HasSuffix(strings.ToLower(f.Path), ".md") {
			return true
		}
	}
	return false
}

func totalSize(files []UploadFile) int64 {
	var n int64
	for _, f := range files {
		n += int64(len(f.Body))
	}
	return n
}

// cleanRelPath enforces: no leading slash, no .. segments, no absolute
// paths, no Windows drive letters, no embedded backslashes. The input is
// URL-decoded first so a caller can't bypass the rules with `%2e%2e`.
// Any ".." segment anywhere in the (decoded) input is rejected even if
// path.Clean would resolve it harmlessly — the intent to "escape
// outward" is the failure mode regardless of net effect.
func cleanRelPath(p string) (string, error) {
	if p == "" {
		return "", ErrPathTraversal
	}
	decoded, err := url.PathUnescape(p)
	if err != nil {
		return "", ErrPathTraversal
	}
	p = decoded
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, "\\") {
		return "", ErrPathTraversal
	}
	if strings.Contains(p, "\\") {
		return "", ErrPathTraversal
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return "", ErrPathTraversal
		}
	}
	cleaned := path.Clean(p)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "/") {
		return "", ErrPathTraversal
	}
	return cleaned, nil
}
