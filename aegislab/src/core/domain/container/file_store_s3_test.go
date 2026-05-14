package container

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/textproto"
	"sort"
	"strings"
	"testing"
	"time"

	blobclient "aegis/clients/blob"
)

type fakeBlobClient struct {
	objects map[string][]byte
	metas   map[string]*blobclient.ObjectMeta
	puts    []putRecord
}

type putRecord struct {
	key  string
	body []byte
	ct   string
}

func newFakeBlobClient() *fakeBlobClient {
	return &fakeBlobClient{objects: map[string][]byte{}, metas: map[string]*blobclient.ObjectMeta{}}
}

func (f *fakeBlobClient) PresignPut(context.Context, string, blobclient.PresignPutReq) (*blobclient.PresignPutResult, error) {
	return nil, fmt.Errorf("not implemented")
}
func (f *fakeBlobClient) PresignGet(context.Context, string, string, blobclient.PresignGetReq) (*blobclient.PresignedURL, error) {
	return nil, fmt.Errorf("not implemented")
}
func (f *fakeBlobClient) Stat(_ context.Context, _b, k string) (*blobclient.ObjectMeta, error) {
	m, ok := f.metas[k]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return m, nil
}
func (f *fakeBlobClient) Delete(_ context.Context, _b, k string) error {
	delete(f.objects, k)
	delete(f.metas, k)
	return nil
}
func (f *fakeBlobClient) PutBytes(_ context.Context, _b string, body []byte, req blobclient.PresignPutReq) (*blobclient.ObjectMeta, error) {
	cp := append([]byte(nil), body...)
	f.objects[req.Key] = cp
	m := &blobclient.ObjectMeta{Key: req.Key, Size: int64(len(cp)), ContentType: req.ContentType, UpdatedAt: time.Now()}
	f.metas[req.Key] = m
	f.puts = append(f.puts, putRecord{key: req.Key, body: cp, ct: req.ContentType})
	return m, nil
}
func (f *fakeBlobClient) GetBytes(_ context.Context, _b, k string) ([]byte, *blobclient.ObjectMeta, error) {
	body, ok := f.objects[k]
	if !ok {
		return nil, nil, fmt.Errorf("not found")
	}
	return append([]byte(nil), body...), f.metas[k], nil
}
func (f *fakeBlobClient) GetReader(_ context.Context, _b, k string) (io.ReadCloser, *blobclient.ObjectMeta, error) {
	body, ok := f.objects[k]
	if !ok {
		return nil, nil, fmt.Errorf("not found")
	}
	return io.NopCloser(bytes.NewReader(body)), f.metas[k], nil
}
func (f *fakeBlobClient) List(_ context.Context, _b, prefix string, _ blobclient.ListOpts) (*blobclient.ListResult, error) {
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

// fakeMultipartHeader builds a *multipart.FileHeader serving the given
// bytes — enough for SaveChart/SaveValueFile to read.
func fakeMultipartHeader(t *testing.T, name string, body []byte) *multipart.FileHeader {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	hdr := make(textproto.MIMEHeader)
	hdr.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, name))
	hdr.Set("Content-Type", "application/octet-stream")
	part, err := mw.CreatePart(hdr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(body); err != nil {
		t.Fatal(err)
	}
	_ = mw.Close()

	mr := multipart.NewReader(&buf, mw.Boundary())
	form, err := mr.ReadForm(int64(len(body) + 1024))
	if err != nil {
		t.Fatal(err)
	}
	files := form.File["file"]
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	return files[0]
}

func TestS3HelmFileStoreSaveChart(t *testing.T) {
	c := newFakeBlobClient()
	store := NewS3HelmFileStore(c, "bucket")
	hdr := fakeMultipartHeader(t, "mychart-1.0.0.tgz", []byte("chart-bytes"))

	key, checksum, err := store.SaveChart("svc-a", hdr)
	if err != nil {
		t.Fatalf("SaveChart: %v", err)
	}
	if !strings.HasPrefix(key, "helm-charts/svc-a_chart_") || !strings.HasSuffix(key, ".tgz") {
		t.Errorf("unexpected key: %q", key)
	}
	if len(checksum) != 64 {
		t.Errorf("unexpected checksum: %q", checksum)
	}
	if len(c.puts) != 1 || string(c.puts[0].body) != "chart-bytes" {
		t.Errorf("puts=%+v", c.puts)
	}
}

func TestS3HelmFileStoreSaveValueFile(t *testing.T) {
	c := newFakeBlobClient()
	store := NewS3HelmFileStore(c, "bucket")
	hdr := fakeMultipartHeader(t, "values.yaml", []byte("foo: bar\n"))

	key, err := store.SaveValueFile("svc-b", hdr, "")
	if err != nil {
		t.Fatalf("SaveValueFile: %v", err)
	}
	if !strings.HasPrefix(key, "helm-values/svc-b_values_") || !strings.HasSuffix(key, ".yaml") {
		t.Errorf("unexpected key: %q", key)
	}
	if string(c.puts[0].body) != "foo: bar\n" {
		t.Errorf("body=%q", c.puts[0].body)
	}
}

func TestS3HelmFileStoreRejectsEmpty(t *testing.T) {
	c := newFakeBlobClient()
	store := NewS3HelmFileStore(c, "bucket")
	hdr := fakeMultipartHeader(t, "values.yaml", []byte(""))
	if _, err := store.SaveValueFile("svc", hdr, ""); err == nil {
		t.Errorf("expected error for empty file")
	}
}
