package share

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"aegis/crud/storage/blob"
)

type fakeRepo struct {
	rows      []*ShareLink
	nextID    int64
	userBytes map[int]int64
}

func newFakeRepo() *fakeRepo { return &fakeRepo{userBytes: map[int]int64{}} }

func (f *fakeRepo) Create(_ context.Context, l *ShareLink) error {
	f.nextID++
	l.ID = f.nextID
	l.CreatedAt = time.Now()
	f.rows = append(f.rows, l)
	if l.Status == 1 {
		f.userBytes[l.OwnerUserID] += l.SizeBytes
	}
	return nil
}

func (f *fakeRepo) FindByCode(_ context.Context, code string) (*ShareLink, error) {
	for _, r := range f.rows {
		if r.ShortCode == code {
			return r, nil
		}
	}
	return nil, ErrShareNotFound
}

func (f *fakeRepo) IncrementViewCount(_ context.Context, id int64) (int, error) {
	for _, r := range f.rows {
		if r.ID == id {
			r.ViewCount++
			return r.ViewCount, nil
		}
	}
	return 0, ErrShareNotFound
}

func (f *fakeRepo) SetStatus(_ context.Context, id int64, status int) error {
	for _, r := range f.rows {
		if r.ID == id {
			if r.Status == 1 && status != 1 {
				f.userBytes[r.OwnerUserID] -= r.SizeBytes
			}
			r.Status = status
			return nil
		}
	}
	return ErrShareNotFound
}

func (f *fakeRepo) SoftDelete(_ context.Context, _ int64) error { return nil }

func (f *fakeRepo) CommitUpdate(_ context.Context, id int64, lifecycle string, size int64, ct string) error {
	for _, r := range f.rows {
		if r.ID == id {
			prev := r.LifecycleState
			r.LifecycleState = lifecycle
			if size > 0 {
				if prev != LifecycleLive && lifecycle == LifecycleLive {
					f.userBytes[r.OwnerUserID] += size - r.SizeBytes
				}
				r.SizeBytes = size
			}
			if ct != "" {
				r.ContentType = ct
			}
			return nil
		}
	}
	return ErrShareNotFound
}

func (f *fakeRepo) ListByOwner(_ context.Context, fi ListFilter) ([]ShareLink, int64, error) {
	out := []ShareLink{}
	for _, r := range f.rows {
		if r.OwnerUserID == fi.OwnerUserID {
			out = append(out, *r)
		}
	}
	return out, int64(len(out)), nil
}

func (f *fakeRepo) SumUserBytes(_ context.Context, uid int) (int64, error) {
	return f.userBytes[uid], nil
}

type fakeBackend struct {
	puts    map[string][]byte
	deleted map[string]bool
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{puts: map[string][]byte{}, deleted: map[string]bool{}}
}

func (b *fakeBackend) Put(_ context.Context, bucket, key string, r io.Reader, _ blob.PutOpts) (*blob.ObjectMeta, error) {
	body, _ := io.ReadAll(r)
	b.puts[bucket+"/"+key] = body
	return &blob.ObjectMeta{Key: key, Size: int64(len(body))}, nil
}

func (b *fakeBackend) Get(_ context.Context, bucket, key string) (io.ReadCloser, *blob.ObjectMeta, error) {
	body, ok := b.puts[bucket+"/"+key]
	if !ok {
		return nil, nil, blob.ErrObjectNotFound
	}
	return io.NopCloser(bytes.NewReader(body)), &blob.ObjectMeta{Key: key, Size: int64(len(body))}, nil
}

func (b *fakeBackend) Stat(_ context.Context, bucket, key string) (*blob.ObjectMeta, error) {
	body, ok := b.puts[bucket+"/"+key]
	if !ok {
		return nil, blob.ErrObjectNotFound
	}
	return &blob.ObjectMeta{Key: key, Size: int64(len(body))}, nil
}

func (b *fakeBackend) Delete(_ context.Context, bucket, key string) error {
	b.deleted[bucket+"/"+key] = true
	return nil
}

func (b *fakeBackend) PresignGet(_ context.Context, bucket, key string, _ blob.GetOpts) (*blob.PresignedRequest, error) {
	return &blob.PresignedRequest{Method: "GET", URL: "https://signed/" + bucket + "/" + key}, nil
}

func (b *fakeBackend) PresignPut(_ context.Context, bucket, key string, _ blob.PutOpts) (*blob.PresignedRequest, error) {
	return &blob.PresignedRequest{Method: "PUT", URL: "https://signed/" + bucket + "/" + key}, nil
}

func newTestService(t *testing.T) (*Service, *fakeRepo, *fakeBackend) {
	t.Helper()
	cfg := Config{
		Bucket:            "shared",
		PublicBaseURL:     "https://example.com",
		DefaultTTLSeconds: 3600,
		MaxTTLSeconds:     7200,
		MaxViews:          10,
		MaxUploadBytes:    1024,
		UserQuotaBytes:    2048,
	}
	repo := newFakeRepo()
	be := newFakeBackend()
	return NewServiceWith(cfg, repo, be, blob.NewClock()), repo, be
}

func TestUploadSuccess(t *testing.T) {
	svc, repo, be := newTestService(t)
	body := bytes.NewBufferString("hello world")
	res, err := svc.Upload(context.Background(), UploadInput{
		OwnerUserID: 42, Filename: "hi.txt", ContentType: "text/plain",
		Size: int64(body.Len()), Body: body, TTLSeconds: 60, MaxViews: 5,
	})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if len(res.ShortCode) != shortCodeLength {
		t.Fatalf("short_code len=%d", len(res.ShortCode))
	}
	if res.ShareURL == "" || res.ExpiresAt == nil {
		t.Fatalf("bad result: %+v", res)
	}
	if len(repo.rows) != 1 {
		t.Fatalf("rows=%d", len(repo.rows))
	}
	if len(be.puts) != 1 {
		t.Fatalf("backend puts=%d", len(be.puts))
	}
}

func TestUploadRejectsOversize(t *testing.T) {
	svc, _, _ := newTestService(t)
	big := bytes.NewReader(make([]byte, 4096))
	_, err := svc.Upload(context.Background(), UploadInput{
		OwnerUserID: 1, Filename: "big.bin", Size: 4096, Body: big,
	})
	if err != ErrUploadTooLarge {
		t.Fatalf("want ErrUploadTooLarge, got %v", err)
	}
}

func TestUploadRejectsOverQuota(t *testing.T) {
	svc, repo, _ := newTestService(t)
	repo.userBytes[7] = 2040
	body := bytes.NewBufferString("xxxxxxxxxxxx")
	_, err := svc.Upload(context.Background(), UploadInput{
		OwnerUserID: 7, Filename: "x.txt", Size: int64(body.Len()), Body: body,
	})
	if err != ErrQuotaExceeded {
		t.Fatalf("want ErrQuotaExceeded, got %v", err)
	}
}

func TestViewIncrementsAndExpires(t *testing.T) {
	svc, _, _ := newTestService(t)
	body := bytes.NewBufferString("data")
	res, err := svc.Upload(context.Background(), UploadInput{
		OwnerUserID: 1, Filename: "a.txt", Size: 4, Body: body, MaxViews: 2,
	})
	if err != nil {
		t.Fatalf("%v", err)
	}
	if _, err := svc.View(context.Background(), res.ShortCode); err != nil {
		t.Fatalf("view1: %v", err)
	}
	if _, err := svc.View(context.Background(), res.ShortCode); err != nil {
		t.Fatalf("view2: %v", err)
	}
	if _, err := svc.View(context.Background(), res.ShortCode); err != ErrShareGone {
		t.Fatalf("view3 want ErrShareGone got %v", err)
	}
}

func TestViewExpiredReturnsGone(t *testing.T) {
	svc, repo, _ := newTestService(t)
	past := time.Now().Add(-time.Hour)
	repo.rows = append(repo.rows, &ShareLink{
		ID: 1, ShortCode: "expired1", Status: 1, ExpiresAt: &past,
		Bucket: "shared", ObjectKey: "k",
	})
	if _, err := svc.View(context.Background(), "expired1"); err != ErrShareGone {
		t.Fatalf("want gone, got %v", err)
	}
}

func TestRevokeFlipsStatusAndDeletesObject(t *testing.T) {
	svc, repo, be := newTestService(t)
	body := bytes.NewBufferString("data")
	res, _ := svc.Upload(context.Background(), UploadInput{
		OwnerUserID: 9, Filename: "x.txt", Size: 4, Body: body,
	})
	if err := svc.Revoke(context.Background(), res.ShortCode, 9, false); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if repo.rows[0].Status != 0 {
		t.Fatalf("status=%d", repo.rows[0].Status)
	}
	if len(be.deleted) != 1 {
		t.Fatalf("deletes=%d", len(be.deleted))
	}
	if _, err := svc.View(context.Background(), res.ShortCode); err != ErrShareGone {
		t.Fatalf("post-revoke view want gone, got %v", err)
	}
}

// simulateClientPut puts bytes for a pending code by writing directly
// into the fake backend, mimicking what the client would do against the
// presigned PUT URL.
func simulateClientPut(t *testing.T, repo *fakeRepo, be *fakeBackend, code string, body []byte) {
	t.Helper()
	for _, r := range repo.rows {
		if r.ShortCode == code {
			be.puts[r.Bucket+"/"+r.ObjectKey] = body
			return
		}
	}
	t.Fatalf("pending row for code %q not found", code)
}

func TestInitUploadHappyPath(t *testing.T) {
	svc, repo, _ := newTestService(t)
	res, err := svc.InitUpload(context.Background(), InitUploadInput{
		OwnerUserID: 42, Filename: "hi.txt", ContentType: "text/plain", Size: 100, TTLSeconds: 60, MaxViews: 2,
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if res.Code == "" || res.PresignedURL == "" || res.CommitURL == "" {
		t.Fatalf("bad init result: %+v", res)
	}
	if res.MaxSize != 1024 {
		t.Fatalf("MaxSize=%d, want 1024", res.MaxSize)
	}
	if len(repo.rows) != 1 {
		t.Fatalf("rows=%d, want 1", len(repo.rows))
	}
	if repo.rows[0].LifecycleState != LifecyclePending {
		t.Fatalf("lifecycle=%q, want pending", repo.rows[0].LifecycleState)
	}
}

func TestInitUploadRejectsOverQuota(t *testing.T) {
	svc, repo, _ := newTestService(t)
	repo.userBytes[7] = 2040
	_, err := svc.InitUpload(context.Background(), InitUploadInput{
		OwnerUserID: 7, Filename: "x", Size: 100,
	})
	if err != ErrQuotaExceeded {
		t.Fatalf("want ErrQuotaExceeded, got %v", err)
	}
}

func TestInitUploadRejectsOversize(t *testing.T) {
	svc, _, _ := newTestService(t)
	_, err := svc.InitUpload(context.Background(), InitUploadInput{
		OwnerUserID: 1, Filename: "x", Size: 4096,
	})
	if err != ErrUploadTooLarge {
		t.Fatalf("want ErrUploadTooLarge, got %v", err)
	}
}

func TestCommitUploadRequiresStatSuccess(t *testing.T) {
	svc, _, _ := newTestService(t)
	res, err := svc.InitUpload(context.Background(), InitUploadInput{
		OwnerUserID: 1, Filename: "a.txt", Size: 10,
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	_, err = svc.CommitUpload(context.Background(), CommitUploadInput{OwnerUserID: 1, Code: res.Code})
	if err != ErrCommitObjectMissing {
		t.Fatalf("want ErrCommitObjectMissing, got %v", err)
	}
}

func TestCommitUploadHappyPath(t *testing.T) {
	svc, repo, be := newTestService(t)
	init, err := svc.InitUpload(context.Background(), InitUploadInput{
		OwnerUserID: 9, Filename: "a.bin", Size: 5,
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	simulateClientPut(t, repo, be, init.Code, []byte("hello"))
	res, err := svc.CommitUpload(context.Background(), CommitUploadInput{OwnerUserID: 9, Code: init.Code, Size: 5})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if res.Size != 5 {
		t.Fatalf("size=%d", res.Size)
	}
	if repo.rows[0].LifecycleState != LifecycleLive {
		t.Fatalf("lifecycle=%q, want live", repo.rows[0].LifecycleState)
	}
	// Post-commit view should resolve.
	if _, err := svc.View(context.Background(), init.Code); err != nil {
		t.Fatalf("view post-commit: %v", err)
	}
}

func TestCommitUploadRejectsNonOwner(t *testing.T) {
	svc, repo, be := newTestService(t)
	init, _ := svc.InitUpload(context.Background(), InitUploadInput{
		OwnerUserID: 1, Filename: "a", Size: 3,
	})
	simulateClientPut(t, repo, be, init.Code, []byte("abc"))
	if _, err := svc.CommitUpload(context.Background(), CommitUploadInput{OwnerUserID: 2, Code: init.Code}); err != ErrForbidden {
		t.Fatalf("want forbidden, got %v", err)
	}
}

func TestCommitUploadIdempotent(t *testing.T) {
	svc, repo, be := newTestService(t)
	init, _ := svc.InitUpload(context.Background(), InitUploadInput{
		OwnerUserID: 5, Filename: "a", Size: 4,
	})
	simulateClientPut(t, repo, be, init.Code, []byte("data"))
	first, err := svc.CommitUpload(context.Background(), CommitUploadInput{OwnerUserID: 5, Code: init.Code})
	if err != nil {
		t.Fatalf("first commit: %v", err)
	}
	second, err := svc.CommitUpload(context.Background(), CommitUploadInput{OwnerUserID: 5, Code: init.Code})
	if err != nil {
		t.Fatalf("second commit must be no-op, got %v", err)
	}
	if first.ShortCode != second.ShortCode || first.Size != second.Size {
		t.Fatalf("commit not idempotent: %+v vs %+v", first, second)
	}
	if repo.rows[0].LifecycleState != LifecycleLive {
		t.Fatalf("lifecycle=%q", repo.rows[0].LifecycleState)
	}
}

func TestCommitUploadSizeMismatch(t *testing.T) {
	svc, repo, be := newTestService(t)
	init, _ := svc.InitUpload(context.Background(), InitUploadInput{
		OwnerUserID: 1, Filename: "a", Size: 10,
	})
	// Client uploaded fewer bytes than declared.
	simulateClientPut(t, repo, be, init.Code, []byte("abc"))
	_, err := svc.CommitUpload(context.Background(), CommitUploadInput{OwnerUserID: 1, Code: init.Code, Size: 10})
	if err != ErrCommitSizeMismatch {
		t.Fatalf("want ErrCommitSizeMismatch, got %v", err)
	}
}

func TestViewBeforeCommitReturnsGone(t *testing.T) {
	svc, _, _ := newTestService(t)
	init, _ := svc.InitUpload(context.Background(), InitUploadInput{
		OwnerUserID: 1, Filename: "a", Size: 4,
	})
	if _, err := svc.View(context.Background(), init.Code); err != ErrShareGone {
		t.Fatalf("want gone, got %v", err)
	}
}

func TestRevokeForbiddenForNonOwner(t *testing.T) {
	svc, _, _ := newTestService(t)
	body := bytes.NewBufferString("d")
	res, _ := svc.Upload(context.Background(), UploadInput{
		OwnerUserID: 1, Filename: "x", Size: 1, Body: body,
	})
	if err := svc.Revoke(context.Background(), res.ShortCode, 2, false); err != ErrForbidden {
		t.Fatalf("want forbidden, got %v", err)
	}
}
