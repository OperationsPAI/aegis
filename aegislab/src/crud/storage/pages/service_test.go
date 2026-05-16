package pages

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"testing"
	"time"

	blobclient "aegis/clients/blob"
	"aegis/platform/testutil"
)

// fakeBlobClient is an in-memory blobclient.Client used by service tests.
// Mirrors the fake in core/domain/injection but kept local so the pages
// module has no cross-test dependency.
type fakeBlobClient struct {
	objects map[string][]byte
	metas   map[string]*blobclient.ObjectMeta
}

func newFakeBlobClient() *fakeBlobClient {
	return &fakeBlobClient{
		objects: map[string][]byte{},
		metas:   map[string]*blobclient.ObjectMeta{},
	}
}

func (f *fakeBlobClient) PresignPut(context.Context, string, blobclient.PresignPutReq) (*blobclient.PresignPutResult, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeBlobClient) PresignGet(context.Context, string, string, blobclient.PresignGetReq) (*blobclient.PresignedURL, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeBlobClient) Stat(_ context.Context, _ string, key string) (*blobclient.ObjectMeta, error) {
	m, ok := f.metas[key]
	if !ok {
		return nil, fmt.Errorf("not found: %s", key)
	}
	return m, nil
}

func (f *fakeBlobClient) Delete(_ context.Context, _ string, key string) error {
	delete(f.objects, key)
	delete(f.metas, key)
	return nil
}

func (f *fakeBlobClient) PutBytes(_ context.Context, _ string, body []byte, req blobclient.PresignPutReq) (*blobclient.ObjectMeta, error) {
	cp := append([]byte(nil), body...)
	f.objects[req.Key] = cp
	meta := &blobclient.ObjectMeta{Key: req.Key, Size: int64(len(cp)), ContentType: req.ContentType, UpdatedAt: time.Now().UTC()}
	f.metas[req.Key] = meta
	return meta, nil
}

func (f *fakeBlobClient) GetBytes(_ context.Context, _ string, key string) ([]byte, *blobclient.ObjectMeta, error) {
	body, ok := f.objects[key]
	if !ok {
		return nil, nil, fmt.Errorf("not found: %s", key)
	}
	return append([]byte(nil), body...), f.metas[key], nil
}

func (f *fakeBlobClient) GetReader(_ context.Context, _ string, key string) (io.ReadCloser, *blobclient.ObjectMeta, error) {
	body, ok := f.objects[key]
	if !ok {
		return nil, nil, fmt.Errorf("not found: %s", key)
	}
	return io.NopCloser(bytes.NewReader(body)), f.metas[key], nil
}

func (f *fakeBlobClient) List(_ context.Context, _ string, prefix string, _ blobclient.ListOpts) (*blobclient.ListResult, error) {
	keys := make([]string, 0)
	for k := range f.objects {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	res := &blobclient.ListResult{}
	for _, k := range keys {
		res.Objects = append(res.Objects, *f.metas[k])
	}
	return res, nil
}

// newServiceForTest builds a service backed by SQLite + the fake blob client.
func newServiceForTest(t *testing.T) (*Service, *fakeBlobClient) {
	t.Helper()
	db := testutil.NewSQLiteGormDB(t)
	if err := db.AutoMigrate(&PageSite{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	blob := newFakeBlobClient()
	repo := NewRepository(db)
	return NewService(repo, blob), blob
}

func mdFile(p, body string) UploadFile {
	return UploadFile{Path: p, ContentType: "text/markdown", Body: []byte(body)}
}

func TestSlugUniqueness_AutoSuffix(t *testing.T) {
	svc, _ := newServiceForTest(t)
	ctx := context.Background()
	files := []UploadFile{mdFile("index.md", "# hi")}

	s1, err := svc.CreateSite(ctx, 1, "docs", "", "", files)
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	if s1.Slug != "docs" {
		t.Fatalf("expected slug docs, got %q", s1.Slug)
	}

	// Same explicit slug → rejected.
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

func TestRejectsInvalidSlug(t *testing.T) {
	svc, _ := newServiceForTest(t)
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
	svc, _ := newServiceForTest(t)
	ctx := context.Background()
	files := []UploadFile{mdFile("index.md", "x")}
	if _, err := svc.CreateSite(ctx, 1, "ok", "wide-open", "", files); !errors.Is(err, ErrInvalidVisibility) {
		t.Fatalf("expected ErrInvalidVisibility, got %v", err)
	}
}

func TestOwnerCheck_Denial(t *testing.T) {
	svc, _ := newServiceForTest(t)
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

func TestRejectsPathTraversal(t *testing.T) {
	svc, _ := newServiceForTest(t)
	ctx := context.Background()
	cases := []string{"../escape.md", "/abs.md", "a/../b.md", "..", "\\windows.md"}
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

func TestRejectsTooManyFiles(t *testing.T) {
	svc, _ := newServiceForTest(t)
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
	svc, _ := newServiceForTest(t)
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
	svc, _ := newServiceForTest(t)
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
	svc, _ := newServiceForTest(t)
	ctx := context.Background()
	files := []UploadFile{{Path: "logo.png", Body: []byte{1, 2, 3}}}
	if _, err := svc.CreateSite(ctx, 1, "", "", "nomd", files); !errors.Is(err, ErrNoFiles) {
		t.Fatalf("expected ErrNoFiles, got %v", err)
	}
}

func ptr(s string) *string { return &s }
