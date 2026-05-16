package cmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"aegis/cli/apiclient"
	"aegis/cli/client"
	"aegis/cli/output"

	"github.com/spf13/cobra"
)

var shareCmd = &cobra.Command{
	Use:   "share",
	Short: "Upload / list / revoke / download share links (blob/share)",
	Long: `Manage temporary file share links served by the aegis-blob microservice.

Authentication is the JWT saved by ` + "`aegisctl auth login`" + `; download
goes through the unauthenticated /s/<code> endpoint.`,
}

// --- share upload ---

var (
	shareUploadTTL      string
	shareUploadMaxViews int
	shareUploadLegacy   bool
	shareUploadNoSHA256 bool
)

var shareUploadCmd = &cobra.Command{
	Use:   "upload <file>",
	Short: "Upload a file and produce a public /s/<code> link",
	Long: `Upload a file and produce a public /s/<code> link.

By default uses the three-step presigned-PUT flow:

  1. POST /api/v2/share/init                — reserve a short code + presigned PUT URL
  2. PUT  <presigned_url>                   — stream the file body directly to object storage (no aegislab hop)
  3. POST /api/v2/share/<code>/commit       — finalise the share row server-side

This bypasses the aegislab and edge-proxy buffers, restoring full bandwidth
for users on slow / lossy international links. SHA-256 is computed on the
client during the PUT and forwarded to commit for integrity verification.

Pass --legacy to fall back to the multipart POST /api/v2/share/upload
endpoint (the SDK-generated path) for debugging / very small files.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := args[0]
		st, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("stat %q: %w", path, err)
		}
		if st.IsDir() {
			return fmt.Errorf("%q is a directory; share upload takes a single file", path)
		}

		var ttlSecs int64
		if shareUploadTTL != "" {
			secs, err := parseTTL(shareUploadTTL)
			if err != nil {
				return err
			}
			ttlSecs = secs
		}

		if shareUploadLegacy {
			return shareUploadLegacyMultipart(path, ttlSecs, shareUploadMaxViews)
		}
		return shareUploadPresigned(path, st.Size(), ttlSecs, shareUploadMaxViews, !shareUploadNoSHA256)
	},
}

// shareUploadLegacyMultipart drives the deprecated SDK path
// (multipart POST through aegislab). Kept behind --legacy for debugging.
func shareUploadLegacyMultipart(path string, ttlSecs int64, maxViews int) error {
	f, err := os.Open(path) //nolint:gosec // caller-supplied path is intentional.
	if err != nil {
		return fmt.Errorf("open %q: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	cli, ctx := newAPIClient()
	req := cli.ShareAPI.ShareUpload(ctx).File(f)
	if ttlSecs > 0 {
		req = req.TtlSeconds(int32(ttlSecs))
	}
	if maxViews > 0 {
		req = req.MaxViews(int32(maxViews))
	}
	resp, _, err := req.Execute()
	if err != nil {
		return fmt.Errorf("share upload: %w", err)
	}
	data := resp.Data
	if data == nil {
		return fmt.Errorf("share upload: empty response")
	}
	return printShareUploadResult(data.GetShortCode(), data.GetShareUrl(), int64(data.GetSize()), data.GetExpiresAt())
}

// shareInitResponse mirrors initUploadResp on the server side. Hand-rolled
// because the SDK regen lags this endpoint.
type shareInitResponse struct {
	Code            string            `json:"code"`
	PresignedPutURL string            `json:"presigned_put_url"`
	Method          string            `json:"method"`
	Headers         map[string]string `json:"headers,omitempty"`
	ExpiresAt       string            `json:"expires_at"`
	MaxSize         int64             `json:"max_size"`
	CommitURL       string            `json:"commit_url"`
	Bucket          string            `json:"bucket"`
	ObjectKey       string            `json:"object_key"`
}

// shareCommitResponse mirrors uploadResp.
type shareCommitResponse struct {
	ShortCode string `json:"short_code"`
	ShareURL  string `json:"share_url"`
	Size      int64  `json:"size"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

func shareUploadPresigned(path string, size, ttlSecs int64, maxViews int, computeSHA bool) error {
	contentType := guessContentType(path)
	// Step 1: init.
	init, err := shareCallInit(path, size, contentType, ttlSecs, maxViews)
	if err != nil {
		return err
	}

	// Step 2: PUT the body to the presigned URL, streaming with optional
	// sha256 tap. Progress goes to stderr every ~5 s.
	f, err := os.Open(path) //nolint:gosec // caller-supplied path is intentional.
	if err != nil {
		return fmt.Errorf("open %q: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	var sha string
	progress := &progressReader{src: f, total: size}
	var src io.Reader = progress
	hasher := sha256.New()
	if computeSHA {
		src = io.TeeReader(progress, hasher)
	}
	stop := startProgressTicker(progress, size)
	putContentType := contentType
	if h, ok := init.Headers["Content-Type"]; ok && h != "" {
		putContentType = h
	}
	if err := httpPutFromReader(init.PresignedPutURL, src, putContentType, size); err != nil {
		stop()
		return fmt.Errorf("share upload PUT: %w", err)
	}
	stop()
	if computeSHA {
		sha = hex.EncodeToString(hasher.Sum(nil))
	}

	// Step 3: commit.
	commit, err := shareCallCommit(init.Code, size, contentType, sha)
	if err != nil {
		return err
	}
	return printShareUploadResult(commit.ShortCode, commit.ShareURL, commit.Size, commit.ExpiresAt)
}

func shareCallInit(path string, size int64, contentType string, ttlSecs int64, maxViews int) (*shareInitResponse, error) {
	body := map[string]any{
		"filename":     filepathBase(path),
		"size":         size,
		"content_type": contentType,
		"ttl_seconds":  ttlSecs,
		"max_views":    maxViews,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("share init: encode body: %w", err)
	}
	respBody, status, err := shareDoJSON(http.MethodPost, "/api/v2/share/init", raw)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("share init: HTTP %d: %s", status, serverErrorMessage(respBody, status))
	}
	var env struct {
		Data *shareInitResponse `json:"data"`
	}
	if err := json.Unmarshal(respBody, &env); err != nil {
		return nil, fmt.Errorf("share init: decode: %w", err)
	}
	if env.Data == nil || env.Data.PresignedPutURL == "" {
		return nil, fmt.Errorf("share init: empty response")
	}
	return env.Data, nil
}

func shareCallCommit(code string, size int64, contentType, sha string) (*shareCommitResponse, error) {
	body := map[string]any{}
	if size > 0 {
		body["size"] = size
	}
	if contentType != "" {
		body["content_type"] = contentType
	}
	if sha != "" {
		body["sha256"] = sha
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("share commit: encode body: %w", err)
	}
	respBody, status, err := shareDoJSON(http.MethodPost, "/api/v2/share/"+url.PathEscape(code)+"/commit", raw)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("share commit: HTTP %d: %s", status, serverErrorMessage(respBody, status))
	}
	var env struct {
		Data *shareCommitResponse `json:"data"`
	}
	if err := json.Unmarshal(respBody, &env); err != nil {
		return nil, fmt.Errorf("share commit: decode: %w", err)
	}
	if env.Data == nil {
		return nil, fmt.Errorf("share commit: empty response")
	}
	return env.Data, nil
}

// shareDoJSON sends an authed JSON request to the aegislab base URL,
// mirroring pagesDoMultipart's transport + auth wiring.
func shareDoJSON(method, path string, body []byte) ([]byte, int, error) {
	if flagServer == "" {
		return nil, 0, missingEnvErrorf("--server or AEGIS_SERVER is required")
	}
	req, err := http.NewRequestWithContext(context.Background(),
		method,
		strings.TrimRight(flagServer, "/")+path,
		bytes.NewReader(body))
	if err != nil {
		return nil, 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if flagToken != "" {
		req.Header.Set("Authorization", "Bearer "+flagToken)
	}
	httpClient := &http.Client{Transport: client.TransportFor(resolveTLSOptions())}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read response: %w", err)
	}
	return b, resp.StatusCode, nil
}

func printShareUploadResult(code, shareURL string, size int64, expiresAt string) error {
	fullURL := joinURL(flagServer, shareURL)
	if output.OutputFormat(flagOutput) == output.FormatJSON {
		output.PrintJSON(map[string]any{
			"short_code": code,
			"share_url":  fullURL,
			"size_bytes": size,
			"expires_at": expiresAt,
		})
		return nil
	}
	fmt.Printf("Short code:  %s\n", code)
	fmt.Printf("Share URL:   %s\n", fullURL)
	fmt.Printf("Size bytes:  %d\n", size)
	if expiresAt != "" {
		fmt.Printf("Expires at:  %s\n", expiresAt)
	}
	return nil
}

// progressReader is a tiny io.Reader wrapper that tracks bytes pumped,
// for a stderr progress ticker.
type progressReader struct {
	src   io.Reader
	total int64
	read  atomic.Int64
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.src.Read(b)
	if n > 0 {
		p.read.Add(int64(n))
	}
	return n, err
}

// startProgressTicker prints "n/total bytes (pct%)" to stderr every 5
// seconds until stop() is called. Returns the stop function.
func startProgressTicker(p *progressReader, total int64) func() {
	tick := time.NewTicker(5 * time.Second)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-tick.C:
				cur := p.read.Load()
				pct := 0.0
				if total > 0 {
					pct = (float64(cur) / float64(total)) * 100
				}
				fmt.Fprintf(os.Stderr, "uploaded %d / %d bytes (%.1f%%)\n", cur, total, pct)
			case <-done:
				return
			}
		}
	}()
	return func() {
		tick.Stop()
		close(done)
	}
}

func guessContentType(path string) string {
	low := strings.ToLower(path)
	switch {
	case strings.HasSuffix(low, ".apk"):
		return "application/vnd.android.package-archive"
	case strings.HasSuffix(low, ".json"):
		return "application/json"
	case strings.HasSuffix(low, ".tar.gz"), strings.HasSuffix(low, ".tgz"):
		return "application/gzip"
	case strings.HasSuffix(low, ".zip"):
		return "application/zip"
	case strings.HasSuffix(low, ".txt"):
		return "text/plain"
	}
	return "application/octet-stream"
}

func filepathBase(p string) string {
	// Drop directory parts on whichever separator the OS uses.
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}

// --- share list ---

var (
	shareListPage           int
	shareListSize           int
	shareListIncludeExpired bool
)

var shareListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the share links you own",
	RunE: func(cmd *cobra.Command, args []string) error {
		cli, ctx := newAPIClient()
		resp, _, err := cli.ShareAPI.ShareList(ctx).
			Page(int32(shareListPage)).
			Size(int32(shareListSize)).
			IncludeExpired(shareListIncludeExpired).
			Execute()
		if err != nil {
			return err
		}
		items := []apiclient.ShareLinkView{}
		if resp.Data != nil {
			items = resp.Data.GetItems()
		}

		switch output.OutputFormat(flagOutput) {
		case output.FormatJSON:
			output.PrintJSON(items)
			return nil
		case output.FormatNDJSON:
			return output.PrintNDJSON(items)
		}

		rows := make([][]string, 0, len(items))
		for _, it := range items {
			rows = append(rows, []string{
				it.GetShortCode(),
				it.GetOriginalFilename(),
				strconv.FormatInt(int64(it.GetSizeBytes()), 10),
				strconv.FormatInt(int64(it.GetViewCount()), 10),
				it.GetExpiresAt(),
			})
		}
		output.PrintTable(
			[]string{"Code", "Filename", "Bytes", "Views", "Expires"},
			rows,
		)
		return nil
	},
}

// --- share revoke ---

var shareRevokeCmd = &cobra.Command{
	Use:   "revoke <code>",
	Short: "Revoke (delete) a share link",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		code := args[0]
		cli, ctx := newAPIClient()
		if _, _, err := cli.ShareAPI.ShareRevoke(ctx, code).Execute(); err != nil {
			return err
		}
		output.PrintInfo(fmt.Sprintf("share link %q revoked", code))
		return nil
	},
}

// --- share download ---

var shareDownloadOut string

var shareDownloadCmd = &cobra.Command{
	Use:   "download <code>",
	Short: "Download a share link's bytes via /s/<code>",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		code := args[0]
		out := shareDownloadOut
		if out == "" {
			out = code + ".bin"
		}

		// /s/:code is auth=none and streams bytes; the generated client's
		// ShareRedirect treats 302 as an error and never returns the body
		// stream, so keep the manual streaming path here.
		c := newClient()
		f, err := os.Create(out)
		if err != nil {
			return fmt.Errorf("create %q: %w", out, err)
		}
		defer func() { _ = f.Close() }()
		n, err := c.GetRaw("/s/"+url.PathEscape(code), f)
		if err != nil {
			return fmt.Errorf("download: %w", err)
		}
		output.PrintInfo(fmt.Sprintf("downloaded %d bytes to %s", n, out))
		return nil
	},
}

// --- helpers ---

// parseTTL accepts either a bare integer (seconds) or a Go-style
// duration like "30m"/"24h"/"7d". Days are expanded manually because
// time.ParseDuration doesn't know about days.
func parseTTL(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty ttl")
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n, nil
	}
	if strings.HasSuffix(s, "d") {
		days, err := strconv.ParseInt(strings.TrimSuffix(s, "d"), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid ttl %q: %w", s, err)
		}
		return days * 24 * 60 * 60, nil
	}
	mult := int64(1)
	num := s
	switch {
	case strings.HasSuffix(s, "h"):
		mult, num = 60*60, strings.TrimSuffix(s, "h")
	case strings.HasSuffix(s, "m"):
		mult, num = 60, strings.TrimSuffix(s, "m")
	case strings.HasSuffix(s, "s"):
		mult, num = 1, strings.TrimSuffix(s, "s")
	default:
		return 0, fmt.Errorf("invalid ttl %q (use seconds, or 30m/24h/7d)", s)
	}
	v, err := strconv.ParseInt(num, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid ttl %q: %w", s, err)
	}
	return v * mult, nil
}

// joinURL forms an absolute URL by combining `base` (e.g. http://host:8082)
// with a server-side path. Absolute `rel` is returned unchanged.
func joinURL(base, rel string) string {
	if rel == "" {
		return base
	}
	if strings.HasPrefix(rel, "http://") || strings.HasPrefix(rel, "https://") {
		return rel
	}
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(rel, "/")
}

func init() {
	shareUploadCmd.Flags().StringVar(&shareUploadTTL, "ttl", "", "Link lifetime: seconds, e.g. 3600, 30m, 24h, 7d (server default if unset)")
	shareUploadCmd.Flags().IntVar(&shareUploadMaxViews, "max-views", 0, "Cap on download count (server default if 0)")
	shareUploadCmd.Flags().BoolVar(&shareUploadLegacy, "legacy", false, "Fall back to the deprecated multipart POST /api/v2/share/upload (no presigned PUT)")
	shareUploadCmd.Flags().BoolVar(&shareUploadNoSHA256, "no-sha256", false, "Skip streaming sha256 computation during PUT (faster, no integrity hint)")

	shareListCmd.Flags().IntVar(&shareListPage, "page", 1, "Page number")
	shareListCmd.Flags().IntVar(&shareListSize, "size", 50, "Page size")
	shareListCmd.Flags().BoolVar(&shareListIncludeExpired, "include-expired", false, "Include expired/revoked links")

	shareDownloadCmd.Flags().StringVarP(&shareDownloadOut, "output", "o", "", "Output file path (default: <code>.bin)")

	shareCmd.AddCommand(shareUploadCmd)
	shareCmd.AddCommand(shareListCmd)
	shareCmd.AddCommand(shareRevokeCmd)
	shareCmd.AddCommand(shareDownloadCmd)
}
