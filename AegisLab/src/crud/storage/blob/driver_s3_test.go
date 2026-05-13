package blob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// mockS3 is a minimal path-style S3 backend sufficient to exercise the
// driver's PutObject / GetObject / StatObject / RemoveObject paths plus
// the NoSuchKey → ErrObjectNotFound mapping. ListObjects/Presign are
// covered by separate URL-construction tests.
type mockS3 struct {
	mu      sync.Mutex
	objects map[string][]byte
	cts     map[string]string
}

// decodeAWSChunked strips the aws-chunked framing (size;chunk-signature=…\r\n<data>\r\n…0\r\n).
func decodeAWSChunked(b []byte) []byte {
	var out []byte
	for len(b) > 0 {
		nl := indexCRLF(b)
		if nl < 0 {
			break
		}
		header := string(b[:nl])
		b = b[nl+2:]
		// strip ";chunk-signature=…" if present
		semi := strings.IndexByte(header, ';')
		if semi >= 0 {
			header = header[:semi]
		}
		var size int
		_, err := fmt.Sscanf(header, "%x", &size)
		if err != nil || size == 0 {
			break
		}
		if size > len(b) {
			break
		}
		out = append(out, b[:size]...)
		b = b[size:]
		if len(b) >= 2 && b[0] == '\r' && b[1] == '\n' {
			b = b[2:]
		}
	}
	return out
}

func indexCRLF(b []byte) int {
	for i := 0; i+1 < len(b); i++ {
		if b[i] == '\r' && b[i+1] == '\n' {
			return i
		}
	}
	return -1
}

func newMockS3() *mockS3 {
	return &mockS3{objects: map[string][]byte{}, cts: map[string]string{}}
}

func (m *mockS3) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		parts := strings.SplitN(path, "/", 2)
		if len(parts) == 0 || parts[0] == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		// /<bucket>            → bucket op
		// /<bucket>/<key...>   → object op
		if len(parts) == 1 {
			switch r.Method {
			case http.MethodHead, http.MethodGet:
				w.WriteHeader(http.StatusOK)
			case http.MethodPut:
				w.WriteHeader(http.StatusOK)
			default:
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
			return
		}
		key := parts[1]
		m.mu.Lock()
		defer m.mu.Unlock()
		switch r.Method {
		case http.MethodPut:
			body, _ := io.ReadAll(r.Body)
			if decoded := r.Header.Get("X-Amz-Decoded-Content-Length"); decoded != "" {
				body = decodeAWSChunked(body)
			}
			m.objects[key] = body
			m.cts[key] = r.Header.Get("Content-Type")
			w.Header().Set("ETag", `"deadbeef"`)
			w.WriteHeader(http.StatusOK)
		case http.MethodHead:
			b, ok := m.objects[key]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(b)))
			w.Header().Set("Content-Type", m.cts[key])
			w.Header().Set("ETag", `"deadbeef"`)
			w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			b, ok := m.objects[key]
			if !ok {
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`<?xml version="1.0"?><Error><Code>NoSuchKey</Code><Message>x</Message></Error>`))
				return
			}
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(b)))
			w.Header().Set("Content-Type", m.cts[key])
			w.Header().Set("ETag", `"deadbeef"`)
			w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(b)
		case http.MethodDelete:
			delete(m.objects, key)
			delete(m.cts, key)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func newTestDriver(t *testing.T) (*S3Driver, *mockS3, func()) {
	t.Helper()
	mock := newMockS3()
	srv := httptest.NewServer(mock.handler(t))
	cfg := BucketConfig{
		Name:      "test",
		Driver:    "s3",
		Endpoint:  srv.URL,
		Region:    "us-east-1",
		AccessKey: "minioadmin",
		SecretKey: "minioadmin",
		Bucket:    "test",
		PathStyle: true,
	}
	d, err := NewS3Driver(cfg)
	if err != nil {
		srv.Close()
		t.Fatalf("NewS3Driver: %v", err)
	}
	return d, mock, srv.Close
}

func TestS3Driver_PutGetStatDelete(t *testing.T) {
	d, _, done := newTestDriver(t)
	defer done()
	ctx := context.Background()

	payload := []byte("hello-rustfs")
	meta, err := d.Put(ctx, "a/b.parquet", strings.NewReader(string(payload)), PutOpts{
		ContentType:   "application/parquet",
		ContentLength: int64(len(payload)),
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if meta.Size != int64(len(payload)) {
		t.Fatalf("Put size: got %d want %d", meta.Size, len(payload))
	}

	stat, err := d.Stat(ctx, "a/b.parquet")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if stat.Size != int64(len(payload)) {
		t.Fatalf("Stat size: got %d want %d", stat.Size, len(payload))
	}

	rc, _, err := d.Get(ctx, "a/b.parquet")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	got, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(got) != string(payload) {
		t.Fatalf("Get body: got %q want %q", got, payload)
	}

	if err := d.Delete(ctx, "a/b.parquet"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	// Delete is idempotent.
	if err := d.Delete(ctx, "a/b.parquet"); err != nil {
		t.Fatalf("Delete idempotent: %v", err)
	}
}

func TestS3Driver_StatNotFound(t *testing.T) {
	d, _, done := newTestDriver(t)
	defer done()
	_, err := d.Stat(context.Background(), "missing")
	if !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("expected ErrObjectNotFound, got %v", err)
	}
}

func TestS3Driver_NewRequiresEndpoint(t *testing.T) {
	_, err := NewS3Driver(BucketConfig{Name: "x", AccessKey: "a", SecretKey: "b"})
	if err == nil || !strings.Contains(err.Error(), "endpoint") {
		t.Fatalf("expected endpoint error, got %v", err)
	}
}

func TestS3Driver_NewRequiresCredentials(t *testing.T) {
	_, err := NewS3Driver(BucketConfig{Name: "x", Endpoint: "http://127.0.0.1:1"})
	if err == nil || !strings.Contains(err.Error(), "access_key") {
		t.Fatalf("expected credentials error, got %v", err)
	}
}

func TestS3Driver_CredentialsFromEnv(t *testing.T) {
	t.Setenv("TEST_BLOB_AK", "ak")
	t.Setenv("TEST_BLOB_SK", "sk")
	ak, sk, err := resolveS3Credentials(BucketConfig{
		AccessKeyEnv: "TEST_BLOB_AK",
		SecretKeyEnv: "TEST_BLOB_SK",
	})
	if err != nil || ak != "ak" || sk != "sk" {
		t.Fatalf("env creds: %v %q %q", err, ak, sk)
	}
}

func TestNormalizeS3Endpoint(t *testing.T) {
	cases := []struct {
		in       string
		inSSL    bool
		wantHost string
		wantSSL  bool
	}{
		{"http://rustfs:9000", false, "rustfs:9000", false},
		{"https://s3.amazonaws.com", false, "s3.amazonaws.com", true},
		{"rustfs:9000", true, "rustfs:9000", true},
		{"rustfs:9000", false, "rustfs:9000", false},
	}
	for _, tc := range cases {
		gotHost, gotSSL := normalizeS3Endpoint(tc.in, tc.inSSL)
		if gotHost != tc.wantHost || gotSSL != tc.wantSSL {
			t.Errorf("normalize(%q,%v) = %q,%v want %q,%v",
				tc.in, tc.inSSL, gotHost, gotSSL, tc.wantHost, tc.wantSSL)
		}
	}
}

func TestPresignTTL(t *testing.T) {
	if got := presignTTL(0); got != 15*time.Minute {
		t.Errorf("default ttl: got %v", got)
	}
	if got := presignTTL(2 * time.Minute); got != 2*time.Minute {
		t.Errorf("passthrough ttl: got %v", got)
	}
	if got := presignTTL(30 * 24 * time.Hour); got != 7*24*time.Hour {
		t.Errorf("clamp ttl: got %v", got)
	}
}

func TestS3Driver_PresignGetReturnsURL(t *testing.T) {
	d, _, done := newTestDriver(t)
	defer done()
	req, err := d.PresignGet(context.Background(), "a/b", GetOpts{TTL: time.Minute})
	if err != nil {
		t.Fatalf("PresignGet: %v", err)
	}
	if req.Method != "GET" {
		t.Fatalf("method: %q", req.Method)
	}
	u, err := url.Parse(req.URL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	if !strings.Contains(u.Path, "/test/a/b") {
		t.Fatalf("path-style url expected, got %q", u.Path)
	}
	if u.Query().Get("X-Amz-Signature") == "" {
		t.Fatalf("missing signature in %q", req.URL)
	}
}
