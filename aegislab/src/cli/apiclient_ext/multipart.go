// Package apiclient_ext supplies typed wrappers around generated apiclient
// operations whose Mustache-generated shape is wrong for our wire format.
//
// The openapi-generator Go template collapses an `array of binary` request
// body to a single *os.File and hard-codes the multipart part filename to
// the OS basename of that file. The /api/v2/pages and /api/v2/share/upload
// endpoints both need multiple files keyed by a caller-supplied path. This
// package builds the multipart request directly while reusing the generated
// Configuration (server URL, HTTPClient/transport, BearerAuth context key)
// so callers do not need to hand-roll the transport.
package apiclient_ext

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"

	"aegis/cli/apiclient"
)

// FileUpload is one multipart part. Name is the form field name (e.g. "files"
// for /pages, "file" for /share). Filename is the per-part filename that the
// server will use as the site-relative path or stored object name. Content is
// the part body; callers own its lifecycle (the wrapper does not close it).
type FileUpload struct {
	Name     string
	Filename string
	Content  io.Reader
}

// PagesCreateMulti POSTs /api/v2/pages with one multipart part per file. The
// per-part filename is taken verbatim from FileUpload.Filename (site-relative
// path), not from any underlying *os.File basename.
//
// The returned *http.Response has its body fully drained and replaced with a
// buffered NopCloser, so callers can re-read it for error envelopes.
func PagesCreateMulti(
	ctx context.Context,
	cfg *apiclient.Configuration,
	slug, visibility, title string,
	files []FileUpload,
) (*apiclient.PagesPageSiteResponse, *http.Response, error) {
	fields := url.Values{}
	if slug != "" {
		fields.Set("slug", slug)
	}
	if visibility != "" {
		fields.Set("visibility", visibility)
	}
	if title != "" {
		fields.Set("title", title)
	}

	body, contentType, err := buildMultipart(fields, files)
	if err != nil {
		return nil, nil, err
	}
	resp, respBody, err := doMultipart(ctx, cfg, http.MethodPost, "/api/v2/pages", body, contentType)
	if err != nil {
		return nil, resp, err
	}
	if resp.StatusCode >= 300 {
		return nil, resp, decodeErrorEnvelope(resp.Status, respBody)
	}
	var env apiclient.DtoGenericResponsePagesPageSiteResponse
	if err := json.Unmarshal(respBody, &env); err != nil {
		return nil, resp, fmt.Errorf("decode response: %w", err)
	}
	return env.Data, resp, nil
}

// PagesReplaceMulti PUTs /api/v2/pages/{id} with the same per-part semantics
// as PagesCreateMulti. The site is replaced atomically server-side.
func PagesReplaceMulti(
	ctx context.Context,
	cfg *apiclient.Configuration,
	id int32,
	files []FileUpload,
) (*apiclient.PagesPageSiteResponse, *http.Response, error) {
	body, contentType, err := buildMultipart(nil, files)
	if err != nil {
		return nil, nil, err
	}
	path := fmt.Sprintf("/api/v2/pages/%d", id)
	resp, respBody, err := doMultipart(ctx, cfg, http.MethodPut, path, body, contentType)
	if err != nil {
		return nil, resp, err
	}
	if resp.StatusCode >= 300 {
		return nil, resp, decodeErrorEnvelope(resp.Status, respBody)
	}
	var env apiclient.DtoGenericResponsePagesPageSiteResponse
	if err := json.Unmarshal(respBody, &env); err != nil {
		return nil, resp, fmt.Errorf("decode response: %w", err)
	}
	return env.Data, resp, nil
}

// buildMultipart writes form fields + file parts to a buffer and returns the
// buffer plus the Content-Type carrying the boundary. Files keep their caller-
// supplied Filename verbatim — no filepath.Base sanitization.
func buildMultipart(fields url.Values, files []FileUpload) (*bytes.Buffer, string, error) {
	if len(files) == 0 {
		return nil, "", errors.New("apiclient_ext: at least one file is required")
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, vs := range fields {
		for _, v := range vs {
			if err := mw.WriteField(k, v); err != nil {
				return nil, "", fmt.Errorf("write field %q: %w", k, err)
			}
		}
	}
	for i, f := range files {
		if f.Name == "" {
			return nil, "", fmt.Errorf("file[%d]: Name (form field) is required", i)
		}
		if f.Filename == "" {
			return nil, "", fmt.Errorf("file[%d]: Filename is required", i)
		}
		if f.Content == nil {
			return nil, "", fmt.Errorf("file[%d] %q: Content reader is nil", i, f.Filename)
		}
		hdr := make(textproto.MIMEHeader)
		// Quote both name and filename via %q so RFC 7578 stays well-formed
		// even when the site-relative path contains spaces or other escapable
		// runes. mime/multipart's default CreateFormFile uses filepath.Base —
		// we deliberately bypass it to preserve the caller's relative path.
		hdr.Set("Content-Disposition",
			fmt.Sprintf("form-data; name=%q; filename=%q", f.Name, f.Filename))
		hdr.Set("Content-Type", "application/octet-stream")
		part, err := mw.CreatePart(hdr)
		if err != nil {
			return nil, "", fmt.Errorf("create part %q: %w", f.Filename, err)
		}
		if _, err := io.Copy(part, f.Content); err != nil {
			return nil, "", fmt.Errorf("copy part %q: %w", f.Filename, err)
		}
	}
	if err := mw.Close(); err != nil {
		return nil, "", fmt.Errorf("close multipart writer: %w", err)
	}
	return &buf, mw.FormDataContentType(), nil
}

// doMultipart sends the multipart request using the same Configuration the
// generated client uses: server URL, HTTPClient (so the CLI's TLS / capture
// transport applies), and BearerAuth pulled from ContextAPIKeys. The
// response body is drained, returned as []byte, and re-attached as a
// NopCloser so callers may still call resp.Body.
func doMultipart(
	ctx context.Context,
	cfg *apiclient.Configuration,
	method, path string,
	body *bytes.Buffer,
	contentType string,
) (*http.Response, []byte, error) {
	if cfg == nil {
		return nil, nil, errors.New("apiclient_ext: Configuration is nil")
	}
	base, err := serverBase(cfg)
	if err != nil {
		return nil, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, body)
	if err != nil {
		return nil, nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")
	if cfg.UserAgent != "" {
		req.Header.Set("User-Agent", cfg.UserAgent)
	}
	for k, v := range cfg.DefaultHeader {
		req.Header.Set(k, v)
	}
	applyAuth(ctx, req)

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	respBody, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(respBody))
	if readErr != nil {
		return resp, respBody, fmt.Errorf("read response body: %w", readErr)
	}
	return resp, respBody, nil
}

func serverBase(cfg *apiclient.Configuration) (string, error) {
	if len(cfg.Servers) == 0 {
		return "", errors.New("apiclient_ext: Configuration has no Servers")
	}
	u, err := cfg.ServerURL(0, nil)
	if err != nil {
		return "", fmt.Errorf("resolve server URL: %w", err)
	}
	return strings.TrimRight(u, "/"), nil
}

// applyAuth replicates the BearerAuth lookup the generated *Execute methods
// perform: pull ContextAPIKeys, find "BearerAuth", attach Authorization.
func applyAuth(ctx context.Context, req *http.Request) {
	if ctx == nil {
		return
	}
	keys, ok := ctx.Value(apiclient.ContextAPIKeys).(map[string]apiclient.APIKey)
	if !ok {
		return
	}
	k, ok := keys["BearerAuth"]
	if !ok || k.Key == "" {
		return
	}
	if k.Prefix != "" {
		req.Header.Set("Authorization", k.Prefix+" "+k.Key)
	} else {
		req.Header.Set("Authorization", k.Key)
	}
}

// decodeErrorEnvelope returns a generic error carrying the server-side
// `message` field when present, falling back to HTTP status text. Callers
// that need richer error handling can inspect the *http.Response we return
// alongside (status, headers like X-Request-Id) and re-read its body.
func decodeErrorEnvelope(status string, body []byte) error {
	var env struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &env) == nil && env.Message != "" {
		return fmt.Errorf("%s: %s", status, env.Message)
	}
	if len(body) == 0 {
		return errors.New(status)
	}
	return fmt.Errorf("%s: %s", status, strings.TrimSpace(string(body)))
}
