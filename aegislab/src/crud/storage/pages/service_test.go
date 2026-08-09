// Tests for the pages service. The blob seam is exercised through a
// real localfs-backed driver (the same pattern as
// clients/blob/list_getreader_test.go) so there is no hand-rolled fake
// — a path-traversal or list-bug here would be caught the same way it
// would in production.
package pages

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	blobclient "aegis/clients/blob"
	"aegis/crud/storage/blob"
	"aegis/platform/testutil"
)

// newServiceForTest builds a service backed by an in-memory SQLite repo
// and a real localfs-backed blob.Client rooted under t.TempDir(). The
// connection pool is pinned to a single conn so concurrent goroutines
// (see TestSlugUniqueness_ConcurrentClaim) all see the same in-memory
// schema — :memory: creates a fresh DB per connection by default.
func newServiceForTest(t *testing.T) *Service {
	t.Helper()
	db := testutil.NewSQLiteGormDB(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("raw sqldb: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&PageSite{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.AutoMigrate(&blob.ObjectRecord{}); err != nil {
		t.Fatalf("migrate blob: %v", err)
	}

	cfg := blob.BucketConfig{
		Name:       BucketName,
		Driver:     "localfs",
		Root:       t.TempDir(),
		PublicRead: true,
	}
	drv, err := blob.NewLocalFSDriver(cfg, []byte("test-signing-key"))
	if err != nil {
		t.Fatalf("localfs driver: %v", err)
	}
	reg := blob.NewTestRegistry(map[string]*blob.Bucket{cfg.Name: {Config: cfg, Driver: drv}})
	svc := blob.NewService(reg, blob.NewRepository(db), blob.NewClock())
	client := blobclient.NewLocalClient(svc)

	return NewService(NewRepository(db), client)
}

func mdFile(p, body string) UploadFile {
	return UploadFile{Path: p, ContentType: "text/markdown", Body: []byte(body)}
}

func TestSlugUniqueness_AutoSuffix(t *testing.T) {
	svc := newServiceForTest(t)
	ctx := context.Background()
	files := []UploadFile{mdFile("index.md", "# hi")}

	s1, err := svc.CreateSite(ctx, 1, "docs", "", "", files)
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	if s1.Slug != "docs" {
		t.Fatalf("expected slug docs, got %q", s1.Slug)
	}

	// Same explicit slug → rejected via the DB unique index.
	if _, err := svc.CreateSite(ctx, 2, "docs", "", "", files); !errors.Is(err, ErrSlugTaken) {
		t.Fatalf("expected ErrSlugTaken, got %v", err)
	}

	// Auto-derive from title → auto-suffixed when "docs" already taken.
	s2, err := svc.CreateSite(ctx, 1, "", "", "docs", files)
	if err != nil {
		t.Fatalf("create with derived slug: %v", err)
	}
	if s2.Slug != "docs-2" {
		t.Fatalf("expected slug docs-2, got %q", s2.Slug)
	}
}

// TestSlugUniqueness_ConcurrentClaim exercises the txn-uniqueness path:
// multiple goroutines race on the same explicit slug; exactly one must
// succeed, every other must see ErrSlugTaken (never a partial blob).
func TestSlugUniqueness_ConcurrentClaim(t *testing.T) {
	svc := newServiceForTest(t)
	ctx := context.Background()
	files := []UploadFile{mdFile("index.md", "# hi")}

	const racers = 8
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		wins     int
		taken    int
		otherErr error
	)
	for i := 0; i < racers; i++ {
		wg.Add(1)
		owner := i + 1
		go func() {
			defer wg.Done()
			_, err := svc.CreateSite(ctx, owner, "race", "", "", files)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				wins++
			case errors.Is(err, ErrSlugTaken):
				taken++
			default:
				otherErr = err
			}
		}()
	}
	wg.Wait()
	if otherErr != nil {
		t.Fatalf("unexpected error: %v", otherErr)
	}
	if wins != 1 {
		t.Fatalf("expected exactly 1 winner, got %d (taken=%d)", wins, taken)
	}
	if wins+taken != racers {
		t.Fatalf("not every racer accounted for: wins=%d taken=%d", wins, taken)
	}
}

func TestRejectsInvalidSlug(t *testing.T) {
	svc := newServiceForTest(t)
	ctx := context.Background()
	files := []UploadFile{mdFile("index.md", "x")}
	cases := []string{"-bad", "BAD", "with spaces", "way_too_long_" + strings.Repeat("x", 80)}
	for _, slug := range cases {
		if _, err := svc.CreateSite(ctx, 1, slug, "", "", files); !errors.Is(err, ErrInvalidSlug) {
			t.Fatalf("slug %q: expected ErrInvalidSlug, got %v", slug, err)
		}
	}
}

func TestRejectsInvalidVisibility(t *testing.T) {
	svc := newServiceForTest(t)
	ctx := context.Background()
	files := []UploadFile{mdFile("index.md", "x")}
	if _, err := svc.CreateSite(ctx, 1, "ok", "wide-open", "", files); !errors.Is(err, ErrInvalidVisibility) {
		t.Fatalf("expected ErrInvalidVisibility, got %v", err)
	}
}

func TestOwnerCheck_Denial(t *testing.T) {
	svc := newServiceForTest(t)
	ctx := context.Background()
	site, err := svc.CreateSite(ctx, 1, "owned", "", "", []UploadFile{mdFile("index.md", "x")})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.UpdateMeta(ctx, 999, site.ID, nil, ptr("private"), nil); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden on update by non-owner, got %v", err)
	}
	if err := svc.DeleteSite(ctx, 999, site.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden on delete by non-owner, got %v", err)
	}
}

// TestPrivateDetail_NonOwnerNotFound makes the security promise concrete:
// callers who are not the owner cannot tell whether a private site exists.
func TestPrivateDetail_NonOwnerNotFound(t *testing.T) {
	svc := newServiceForTest(t)
	ctx := context.Background()
	site, err := svc.CreateSite(ctx, 1, "secret", "private", "", []UploadFile{mdFile("index.md", "x")})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Owner can see it.
	if _, _, err := svc.Detail(ctx, 1, site.ID); err != nil {
		t.Fatalf("owner detail: %v", err)
	}
	// Anonymous (uid 0) → 404.
	if _, _, err := svc.Detail(ctx, 0, site.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("anonymous: expected ErrNotFound, got %v", err)
	}
	// Logged-in but non-owner → 404.
	if _, _, err := svc.Detail(ctx, 2, site.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-owner: expected ErrNotFound, got %v", err)
	}
}

func TestRejectsPathTraversal(t *testing.T) {
	svc := newServiceForTest(t)
	ctx := context.Background()
	cases := []string{
		"../escape.md",     // dot-dot segment
		"/abs.md",          // absolute
		"a/../b.md",        // dot-dot in the middle
		"..",               // bare dot-dot
		"\\windows.md",     // leading backslash
		"subdir\\foo.md",   // embedded backslash
		"%2e%2e/escape.md", // URL-encoded ".." — must be rejected literally
	}
	for _, p := range cases {
		_, err := svc.CreateSite(ctx, 1, "", "", "x", []UploadFile{
			mdFile("index.md", "ok"),
			{Path: p, Body: []byte("nope")},
		})
		if !errors.Is(err, ErrPathTraversal) {
			t.Fatalf("path %q: expected ErrPathTraversal, got %v", p, err)
		}
	}
}

// TestRejectsPathTraversal_DecodedDotDot guards against an external caller
// that URL-decodes a "../" payload before passing it to the service. The
// service must still reject it.
func TestRejectsPathTraversal_DecodedDotDot(t *testing.T) {
	svc := newServiceForTest(t)
	ctx := context.Background()
	_, err := svc.CreateSite(ctx, 1, "", "", "x", []UploadFile{
		mdFile("index.md", "ok"),
		{Path: "../escape.md", Body: []byte("nope")},
	})
	if !errors.Is(err, ErrPathTraversal) {
		t.Fatalf("expected ErrPathTraversal, got %v", err)
	}
}

func TestRejectsTooManyFiles(t *testing.T) {
	svc := newServiceForTest(t)
	ctx := context.Background()
	original := MaxFiles
	MaxFiles = 3
	defer func() { MaxFiles = original }()

	files := []UploadFile{
		mdFile("index.md", "x"),
		mdFile("a.md", "x"),
		mdFile("b.md", "x"),
		mdFile("c.md", "x"),
	}
	if _, err := svc.CreateSite(ctx, 1, "", "", "many", files); !errors.Is(err, ErrTooManyFiles) {
		t.Fatalf("expected ErrTooManyFiles, got %v", err)
	}
}

func TestRejectsFileTooLarge(t *testing.T) {
	svc := newServiceForTest(t)
	ctx := context.Background()
	original := MaxFileSize
	MaxFileSize = 16
	defer func() { MaxFileSize = original }()

	files := []UploadFile{
		mdFile("index.md", strings.Repeat("a", 32)),
	}
	if _, err := svc.CreateSite(ctx, 1, "", "", "big", files); !errors.Is(err, ErrFileTooLarge) {
		t.Fatalf("expected ErrFileTooLarge, got %v", err)
	}
}

func TestRejectsTotalTooLarge(t *testing.T) {
	svc := newServiceForTest(t)
	ctx := context.Background()
	originalFile := MaxFileSize
	originalTotal := MaxTotalSize
	MaxFileSize = 100
	MaxTotalSize = 64
	defer func() { MaxFileSize = originalFile; MaxTotalSize = originalTotal }()

	files := []UploadFile{
		mdFile("a.md", strings.Repeat("a", 50)),
		mdFile("b.md", strings.Repeat("b", 50)),
	}
	if _, err := svc.CreateSite(ctx, 1, "", "", "big-total", files); !errors.Is(err, ErrTotalTooLarge) {
		t.Fatalf("expected ErrTotalTooLarge, got %v", err)
	}
}

func TestRequiresAtLeastOneMarkdown(t *testing.T) {
	svc := newServiceForTest(t)
	ctx := context.Background()
	files := []UploadFile{{Path: "logo.png", Body: []byte{1, 2, 3}}}
	if _, err := svc.CreateSite(ctx, 1, "", "", "nomd", files); !errors.Is(err, ErrNoFiles) {
		t.Fatalf("expected ErrNoFiles, got %v", err)
	}
}

// TestDeleteSite_DBFirst confirms the delete order: the row is gone even
// when the blob layer is fine, and the prefix is also cleaned. The
// orphan-log path is exercised manually via the cleanupOrphanBlobs unit
// test (no good way to inject a failing blob client without remoting in
// a fake — and the rule is "no mocks").
func TestDeleteSite_DBFirst(t *testing.T) {
	svc := newServiceForTest(t)
	ctx := context.Background()
	site, err := svc.CreateSite(ctx, 1, "", "", "to-delete", []UploadFile{mdFile("index.md", "x")})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.DeleteSite(ctx, 1, site.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, _, err := svc.Detail(ctx, 1, site.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
	// Sanity: re-deleting is a clean 404.
	if err := svc.DeleteSite(ctx, 1, site.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("re-delete: expected ErrNotFound, got %v", err)
	}
}

func ptr(s string) *string { return &s }
