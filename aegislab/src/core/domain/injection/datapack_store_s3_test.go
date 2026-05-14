package injection

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"testing"
	"time"

	blobclient "aegis/clients/blob"
)

// fakeBlobClient is an in-memory blobclient.Client used to exercise the
// S3 store implementations without standing up rustfs.
type fakeBlobClient struct {
	objects map[string][]byte        // key -> bytes
	metas   map[string]*blobclient.ObjectMeta
	deletes []string                 // ordered delete log
	puts    []putRecord
}

type putRecord struct {
	key  string
	body []byte
}

func newFakeBlobClient() *fakeBlobClient {
	return &fakeBlobClient{
		objects: map[string][]byte{},
		metas:   map[string]*blobclient.ObjectMeta{},
	}
}

func (f *fakeBlobClient) seed(key string, body []byte) {
	f.objects[key] = body
	f.metas[key] = &blobclient.ObjectMeta{
		Key:       key,
		Size:      int64(len(body)),
		UpdatedAt: time.Unix(1_700_000_000, 0).UTC(),
	}
}

func (f *fakeBlobClient) PresignPut(context.Context, string, blobclient.PresignPutReq) (*blobclient.PresignPutResult, error) {
	return nil, fmt.Errorf("not implemented")
}
func (f *fakeBlobClient) PresignGet(context.Context, string, string, blobclient.PresignGetReq) (*blobclient.PresignedURL, error) {
	return nil, fmt.Errorf("not implemented")
}
func (f *fakeBlobClient) Stat(_ context.Context, _bucket, key string) (*blobclient.ObjectMeta, error) {
	m, ok := f.metas[key]
	if !ok {
		return nil, fmt.Errorf("not found: %s", key)
	}
	return m, nil
}
func (f *fakeBlobClient) Delete(_ context.Context, _bucket, key string) error {
	delete(f.objects, key)
	delete(f.metas, key)
	f.deletes = append(f.deletes, key)
	return nil
}
func (f *fakeBlobClient) PutBytes(_ context.Context, _bucket string, body []byte, req blobclient.PresignPutReq) (*blobclient.ObjectMeta, error) {
	cp := append([]byte(nil), body...)
	f.objects[req.Key] = cp
	meta := &blobclient.ObjectMeta{Key: req.Key, Size: int64(len(cp)), ContentType: req.ContentType, UpdatedAt: time.Now().UTC()}
	f.metas[req.Key] = meta
	f.puts = append(f.puts, putRecord{key: req.Key, body: cp})
	return meta, nil
}
func (f *fakeBlobClient) GetBytes(_ context.Context, _bucket, key string) ([]byte, *blobclient.ObjectMeta, error) {
	body, ok := f.objects[key]
	if !ok {
		return nil, nil, fmt.Errorf("not found: %s", key)
	}
	return append([]byte(nil), body...), f.metas[key], nil
}
func (f *fakeBlobClient) GetReader(_ context.Context, _bucket, key string) (io.ReadCloser, *blobclient.ObjectMeta, error) {
	body, ok := f.objects[key]
	if !ok {
		return nil, nil, fmt.Errorf("not found: %s", key)
	}
	return io.NopCloser(bytes.NewReader(body)), f.metas[key], nil
}
func (f *fakeBlobClient) List(_ context.Context, _bucket, prefix string, opts blobclient.ListOpts) (*blobclient.ListResult, error) {
	keys := make([]string, 0, len(f.objects))
	for k := range f.objects {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	res := &blobclient.ListResult{}
	cpSet := map[string]struct{}{}
	for _, k := range keys {
		rest := strings.TrimPrefix(k, prefix)
		if opts.Delimiter != "" {
			if i := strings.Index(rest, opts.Delimiter); i >= 0 {
				cp := prefix + rest[:i+len(opts.Delimiter)]
				if _, seen := cpSet[cp]; !seen {
					cpSet[cp] = struct{}{}
					res.CommonPrefixes = append(res.CommonPrefixes, cp)
				}
				continue
			}
		}
		res.Objects = append(res.Objects, *f.metas[k])
		if opts.MaxKeys > 0 && len(res.Objects) >= opts.MaxKeys {
			break
		}
	}
	sort.Strings(res.CommonPrefixes)
	return res, nil
}

// --- tests ---

func TestS3DatapackStorePackage(t *testing.T) {
	c := newFakeBlobClient()
	c.seed("dp-1/a.txt", []byte("AAA"))
	c.seed("dp-1/nested/b.txt", []byte("BBB"))
	c.seed("dp-1/c.json", []byte(`{"x":1}`))
	c.seed("other-dp/ignored.txt", []byte("nope"))
	store := NewS3DatapackStore(c, "bucket")

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if err := store.Package(zw, "dp-1", nil); err != nil {
		t.Fatalf("Package failed: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("zip read: %v", err)
	}
	got := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		_ = rc.Close()
		got[f.Name] = string(b)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 entries, got %d: %v", len(got), got)
	}
	want := map[string]string{
		"package/dp-1/a.txt":         "AAA",
		"package/dp-1/nested/b.txt":  "BBB",
		"package/dp-1/c.json":        `{"x":1}`,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("entry %s = %q want %q", k, got[k], v)
		}
	}
}

func TestS3DatapackStoreBuildFileTree(t *testing.T) {
	c := newFakeBlobClient()
	c.seed("dp/a.txt", []byte("a"))
	c.seed("dp/nested/b.txt", []byte("b"))
	c.seed("dp/nested/c.txt", []byte("c"))
	store := NewS3DatapackStore(c, "bucket")

	resp, err := store.BuildFileTree("dp", "", 1)
	if err != nil {
		t.Fatalf("BuildFileTree: %v", err)
	}
	if resp.FileCount != 3 {
		t.Errorf("FileCount = %d, want 3", resp.FileCount)
	}
	if resp.DirCount != 1 {
		t.Errorf("DirCount = %d, want 1", resp.DirCount)
	}
	// expect one dir entry "nested" with 2 children, plus one file "a.txt"
	var sawNested, sawA bool
	for _, it := range resp.Files {
		switch it.Name {
		case "nested":
			sawNested = true
			if len(it.Children) != 2 {
				t.Errorf("nested children=%d want 2", len(it.Children))
			}
		case "a.txt":
			sawA = true
		}
	}
	if !sawNested || !sawA {
		t.Errorf("missing expected items: nested=%v a=%v", sawNested, sawA)
	}
}

func TestS3DatapackStoreBuildFileTreeMissing(t *testing.T) {
	c := newFakeBlobClient()
	store := NewS3DatapackStore(c, "bucket")
	if _, err := store.BuildFileTree("nope", "", 7); err == nil {
		t.Errorf("expected error for missing datapack")
	}
}

func TestS3DatapackStoreOpenFileRoundTrip(t *testing.T) {
	c := newFakeBlobClient()
	c.seed("dp/inner/data.json", []byte(`{"hello":"world"}`))
	store := NewS3DatapackStore(c, "bucket")

	name, ct, size, rsc, err := store.OpenFile("dp", "inner/data.json")
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer func() { _ = rsc.Close() }()
	if name != "data.json" {
		t.Errorf("name=%q want data.json", name)
	}
	if ct != "application/json" {
		t.Errorf("contentType=%q want application/json", ct)
	}
	if size != int64(len(`{"hello":"world"}`)) {
		t.Errorf("size=%d", size)
	}
	got, _ := io.ReadAll(rsc)
	if string(got) != `{"hello":"world"}` {
		t.Errorf("body=%q", got)
	}
}

func TestS3DatapackStoreResolveTraversal(t *testing.T) {
	c := newFakeBlobClient()
	store := NewS3DatapackStore(c, "bucket")
	if _, err := store.ResolveFilePath("dp", "../escape"); err == nil {
		t.Errorf("expected traversal rejection")
	}
}

func TestS3DatapackStoreExtractArchive(t *testing.T) {
	// Build an in-memory zip with two entries.
	tmp, err := os.CreateTemp("", "archive-*.zip")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	zw := zip.NewWriter(tmp)
	for _, e := range []struct{ n, b string }{
		{"one.txt", "hello"},
		{"sub/two.txt", "world"},
	} {
		w, _ := zw.Create(e.n)
		_, _ = w.Write([]byte(e.b))
	}
	_ = zw.Close()
	_ = tmp.Close()

	c := newFakeBlobClient()
	store := NewS3DatapackStore(c, "bucket")
	if err := store.ExtractArchive(tmp.Name(), "dp-extract"); err != nil {
		t.Fatalf("ExtractArchive: %v", err)
	}
	if len(c.puts) != 2 {
		t.Fatalf("expected 2 puts, got %d", len(c.puts))
	}
	gotKeys := map[string]string{}
	for _, p := range c.puts {
		gotKeys[p.key] = string(p.body)
	}
	if gotKeys["dp-extract/one.txt"] != "hello" {
		t.Errorf("one.txt got %q", gotKeys["dp-extract/one.txt"])
	}
	if gotKeys["dp-extract/sub/two.txt"] != "world" {
		t.Errorf("sub/two.txt got %q", gotKeys["dp-extract/sub/two.txt"])
	}
}

func TestS3DatapackStoreRemoveAll(t *testing.T) {
	c := newFakeBlobClient()
	for i := 0; i < 5; i++ {
		c.seed(path.Join("dp", fmt.Sprintf("f%d.txt", i)), []byte("x"))
	}
	c.seed("other/keep.txt", []byte("keep"))
	store := NewS3DatapackStore(c, "bucket")

	if err := store.RemoveAll("dp"); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	if len(c.deletes) != 5 {
		t.Errorf("expected 5 deletes, got %d", len(c.deletes))
	}
	if _, ok := c.objects["other/keep.txt"]; !ok {
		t.Errorf("RemoveAll bled into other prefixes")
	}
}

func TestS3DatapackStoreEnsureAvailable(t *testing.T) {
	c := newFakeBlobClient()
	store := NewS3DatapackStore(c, "bucket")

	if _, err := store.EnsureDatapackDirAvailable("brand-new"); err != nil {
		t.Errorf("empty prefix should be available: %v", err)
	}
	c.seed("brand-new/seed.txt", []byte("x"))
	if _, err := store.EnsureDatapackDirAvailable("brand-new"); err == nil {
		t.Errorf("expected error when prefix has objects")
	}
}
