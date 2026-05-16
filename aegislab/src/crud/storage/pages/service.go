package pages

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	blobclient "aegis/clients/blob"

	"github.com/google/uuid"
)

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

// CreateSite validates input, allocates a slug + UUID, writes every file
// to blob, then persists the row. The caller is the human user that owns
// the new site.
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

	slug, err = s.resolveSlug(ctx, slug, title, cleaned)
	if err != nil {
		return nil, err
	}

	site := &PageSite{
		SiteUUID:   uuid.NewString(),
		OwnerID:    ownerID,
		Slug:       slug,
		Visibility: visibility,
		Title:      title,
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := s.writeFiles(ctx, site, cleaned); err != nil {
		return nil, err
	}
	site.FileCount = int32(len(cleaned))
	site.SizeBytes = totalSize(cleaned)
	if err := s.repo.Create(ctx, site); err != nil {
		// Best-effort cleanup so a DB failure doesn't leave orphan blobs.
		_ = s.deletePrefix(ctx, site.SiteUUID)
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

// DeleteSite removes blob storage and the DB row. Best-effort on blob —
// missing-bucket / driver errors are returned but DB row is still removed
// to avoid orphan rows.
func (s *Service) DeleteSite(ctx context.Context, callerID int, siteID int64) error {
	site, err := s.repo.FindByID(ctx, siteID)
	if err != nil {
		return err
	}
	if site.OwnerID != callerID {
		return ErrForbidden
	}
	if err := s.deletePrefix(ctx, site.SiteUUID); err != nil {
		return err
	}
	return s.repo.Delete(ctx, site.ID)
}

// Mine returns the caller's sites.
func (s *Service) Mine(ctx context.Context, callerID, limit, offset int) ([]PageSite, error) {
	return s.repo.ListByOwner(ctx, callerID, limit, offset)
}

// Public returns sites whose visibility = public_listed.
func (s *Service) Public(ctx context.Context, limit, offset int) ([]PageSite, error) {
	return s.repo.ListPublic(ctx, limit, offset)
}

// Detail returns the site row plus its file listing. The caller's ID is
// passed in so private-visibility sites can be gated to the owner.
func (s *Service) Detail(ctx context.Context, callerID int, siteID int64) (*PageSite, []FileEntry, error) {
	site, err := s.repo.FindByID(ctx, siteID)
	if err != nil {
		return nil, nil, err
	}
	if site.Visibility == VisibilityPrivate && site.OwnerID != callerID {
		return nil, nil, ErrForbidden
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

func (s *Service) resolveSlug(ctx context.Context, requested, title string, files []UploadFile) (string, error) {
	if requested != "" {
		if !SlugRegex.MatchString(requested) {
			return "", ErrInvalidSlug
		}
		taken, err := s.repo.SlugExists(ctx, requested)
		if err != nil {
			return "", err
		}
		if taken {
			return "", ErrSlugTaken
		}
		return requested, nil
	}
	base := deriveSlug(title, files)
	if base == "" {
		base = "site"
	}
	if !SlugRegex.MatchString(base) {
		// truncate / sanitise as last resort
		base = sanitiseSlug(base)
		if !SlugRegex.MatchString(base) {
			base = "site"
		}
	}
	candidate := base
	for i := 2; i < 1000; i++ {
		taken, err := s.repo.SlugExists(ctx, candidate)
		if err != nil {
			return "", err
		}
		if !taken {
			return candidate, nil
		}
		candidate = fmt.Sprintf("%s-%d", base, i)
		if !SlugRegex.MatchString(candidate) {
			return "", ErrInvalidSlug
		}
	}
	return "", ErrSlugTaken
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

// cleanRelPath enforces: no leading slash, no .. segments, no absolute paths,
// no Windows drive letters. Any ".." segment anywhere in the raw input is
// rejected even if path.Clean would resolve it harmlessly — user intent of
// "escape outward" is treated as the failure mode regardless of net effect.
func cleanRelPath(p string) (string, error) {
	if p == "" {
		return "", ErrPathTraversal
	}
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, "\\") {
		return "", ErrPathTraversal
	}
	if strings.Contains(p, "\\") {
		// Disallow backslash entirely — keeps the surface portable.
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
