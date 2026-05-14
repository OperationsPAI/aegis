package dataset

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"testing"
	"time"

	blobclient "aegis/clients/blob"
	"aegis/platform/consts"
	"aegis/platform/model"
)

// fakeBlobClient — in-memory store sufficient for the dataset packaging
// path (List + GetReader only).
type fakeBlobClient struct {
	objects map[string][]byte
	metas   map[string]*blobclient.ObjectMeta
}

func newFakeBlobClient() *fakeBlobClient {
	return &fakeBlobClient{objects: map[string][]byte{}, metas: map[string]*blobclient.ObjectMeta{}}
}

func (f *fakeBlobClient) seed(key string, body []byte) {
	f.objects[key] = body
	f.metas[key] = &blobclient.ObjectMeta{Key: key, Size: int64(len(body)), UpdatedAt: time.Unix(1_700_000_000, 0).UTC()}
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

func TestS3DatapackFileStorePackageToZip(t *testing.T) {
	c := newFakeBlobClient()
	c.seed("dp-a/abnormal_metrics.parquet", []byte("metrics"))
	c.seed("dp-a/abnormal_logs.parquet", []byte("logs"))
	c.seed("dp-b/abnormal_traces.parquet", []byte("traces"))
	store := NewS3DatapackFileStore(c, "bucket")

	dps := []model.FaultInjection{
		{Name: "dp-a", State: consts.DatapackBuildSuccess},
		{Name: "dp-b", State: consts.DatapackBuildSuccess},
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if err := store.PackageToZip(zw, dps, nil); err != nil {
		t.Fatalf("PackageToZip: %v", err)
	}
	_ = zw.Close()

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
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
	if got["package/dp-a/abnormal_metrics.parquet"] != "metrics" {
		t.Errorf("dp-a/abnormal_metrics.parquet=%q", got["package/dp-a/abnormal_metrics.parquet"])
	}
	if got["package/dp-b/abnormal_traces.parquet"] != "traces" {
		t.Errorf("dp-b/abnormal_traces.parquet=%q", got["package/dp-b/abnormal_traces.parquet"])
	}
}

func TestS3DatapackFileStoreRejectsUnbuilt(t *testing.T) {
	c := newFakeBlobClient()
	store := NewS3DatapackFileStore(c, "bucket")
	dps := []model.FaultInjection{{Name: "pending", State: 0}}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if err := store.PackageToZip(zw, dps, nil); err == nil {
		t.Errorf("expected error for unbuilt datapack")
	}
}
