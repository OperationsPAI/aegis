package apiclient_ext

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aegis/cli/apiclient"
)

func TestPagesCreateMultiSendsPerPartFilenames(t *testing.T) {
	var capturedFilenames []string
	var capturedFields = map[string]string{}
	var capturedAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil {
			t.Fatalf("parse media type: %v", err)
		}
		if mediaType != "multipart/form-data" {
			t.Fatalf("media type = %q, want multipart/form-data", mediaType)
		}
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("next part: %v", err)
			}
			// We deliberately read the raw Content-Disposition filename rather
			// than calling p.FileName() — the latter passes it through
			// filepath.Base(), which would mask the bug this test is for.
			_, dispParams, err := mime.ParseMediaType(p.Header.Get("Content-Disposition"))
			if err != nil {
				t.Fatalf("parse content-disposition: %v", err)
			}
			if fn := dispParams["filename"]; fn != "" {
				capturedFilenames = append(capturedFilenames, fn)
				_, _ = io.Copy(io.Discard, p)
			} else {
				b, _ := io.ReadAll(p)
				capturedFields[p.FormName()] = string(b)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"id":   42,
				"slug": "demo",
			},
		})
	}))
	defer srv.Close()

	cfg := apiclient.NewConfiguration()
	cfg.Servers = apiclient.ServerConfigurations{{URL: srv.URL}}
	ctx := context.WithValue(context.Background(), apiclient.ContextAPIKeys,
		map[string]apiclient.APIKey{"BearerAuth": {Key: "tok", Prefix: "Bearer"}})

	resp, httpResp, err := PagesCreateMulti(ctx, cfg, "demo", "public_listed", "Demo", []FileUpload{
		{Name: "files", Filename: "index.md", Content: strings.NewReader("# hi\n")},
		{Name: "files", Filename: "assets/logo.svg", Content: strings.NewReader("<svg/>")},
	})
	if err != nil {
		t.Fatalf("PagesCreateMulti: %v", err)
	}
	if httpResp == nil || httpResp.StatusCode != http.StatusOK {
		t.Fatalf("status = %v, want 200", httpResp)
	}
	if resp == nil || resp.GetSlug() != "demo" {
		t.Fatalf("response data = %+v, want slug=demo", resp)
	}
	if got, want := capturedFilenames, []string{"index.md", "assets/logo.svg"}; !equal(got, want) {
		t.Fatalf("filenames = %v, want %v", got, want)
	}
	if capturedFields["slug"] != "demo" || capturedFields["visibility"] != "public_listed" || capturedFields["title"] != "Demo" {
		t.Fatalf("form fields = %+v", capturedFields)
	}
	if capturedAuth != "Bearer tok" {
		t.Fatalf("Authorization = %q, want %q", capturedAuth, "Bearer tok")
	}
}

func TestPagesCreateMultiRejectsEmptyFiles(t *testing.T) {
	cfg := apiclient.NewConfiguration()
	cfg.Servers = apiclient.ServerConfigurations{{URL: "http://127.0.0.1:0"}}
	_, _, err := PagesCreateMulti(context.Background(), cfg, "", "", "", nil)
	if err == nil {
		t.Fatal("expected error for empty files, got nil")
	}
}

func TestPagesCreateMultiSurfacesServerErrorMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-Id", "req-123")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":    409,
			"message": "slug taken",
		})
	}))
	defer srv.Close()

	cfg := apiclient.NewConfiguration()
	cfg.Servers = apiclient.ServerConfigurations{{URL: srv.URL}}

	_, httpResp, err := PagesCreateMulti(context.Background(), cfg, "demo", "", "", []FileUpload{
		{Name: "files", Filename: "index.md", Content: strings.NewReader("body")},
	})
	if err == nil {
		t.Fatal("expected error from 409 response")
	}
	if !strings.Contains(err.Error(), "slug taken") {
		t.Fatalf("err = %q, want it to contain server message", err.Error())
	}
	if httpResp == nil || httpResp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %+v, want 409", httpResp)
	}
	if got := httpResp.Header.Get("X-Request-Id"); got != "req-123" {
		t.Fatalf("X-Request-Id = %q, want req-123", got)
	}
	// Body re-attached as NopCloser — callers can still read it.
	body, _ := io.ReadAll(httpResp.Body)
	if !strings.Contains(string(body), "slug taken") {
		t.Fatalf("re-read body = %q, want it to contain server message", string(body))
	}
}

func TestPagesReplaceMultiTargetsIDPath(t *testing.T) {
	var gotPath string
	var gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"id": 7, "slug": "x"}})
	}))
	defer srv.Close()

	cfg := apiclient.NewConfiguration()
	cfg.Servers = apiclient.ServerConfigurations{{URL: srv.URL}}

	_, _, err := PagesReplaceMulti(context.Background(), cfg, 7, []FileUpload{
		{Name: "files", Filename: "index.md", Content: strings.NewReader("body")},
	})
	if err != nil {
		t.Fatalf("PagesReplaceMulti: %v", err)
	}
	if gotMethod != http.MethodPut {
		t.Fatalf("method = %q, want PUT", gotMethod)
	}
	if gotPath != "/api/v2/pages/7" {
		t.Fatalf("path = %q, want /api/v2/pages/7", gotPath)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
