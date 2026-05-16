package cmd

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"aegis/cli/apiclient"
	"aegis/cli/client"
	"aegis/cli/internal/cli/blobref"
	"aegis/cli/output"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var blobCmd = &cobra.Command{
	Use:   "blob",
	Short: "Inspect / copy / move / delete objects in blob buckets",
	Long: `Operate on objects in blob buckets in a way that mirrors the mc / aws-s3
CLIs. All commands take paths in the canonical form '<bucket>:<key>' for
remote objects and a plain filesystem path for local files. Use '-' as a
path to mean stdin (for cp / mv) or stdout (for cp dst).

EXAMPLES:
  aegisctl blob ls aegis-pages:                         # bucket root
  aegisctl blob ls aegis-pages:my-site/                  # by prefix
  aegisctl blob stat aegis-pages:my-site/index.md
  aegisctl blob cat aegis-pages:my-site/index.md > out
  aegisctl blob cp ./build/index.html aegis-pages:my-site/index.html
  aegisctl blob cp aegis-pages:my-site/index.md ./tmp/
  aegisctl blob cp -r ./build/ aegis-pages:my-site/
  aegisctl blob mv aegis-pages:tmp/file aegis-pages:final/file
  aegisctl blob rm aegis-pages:my-site/index.md --yes
  aegisctl blob find aegis-pages: --name '*.md'
  aegisctl blob mirror ./build/ aegis-pages:my-site/ --delete --dry-run
  aegisctl blob presign aegis-pages:my-site/index.md --ttl 1h

See 'aegisctl bucket --help' for bucket-level operations and
'aegisctl share --help' for short-code public links.`,
}

// ============================================================================
// helpers
// ============================================================================

// requireRemote returns an error if r is not a remote ref.
func requireRemote(arg string, r blobref.Ref) error {
	if r.Local {
		return usageErrorf("expected a remote <bucket>:<key> reference, got local path %q", arg)
	}
	return nil
}

// httpPutFromReader streams body to url with the configured token; used to
// drive the presigned-PUT half of an upload when the SDK lacks a direct
// PutObject method.
func httpPutFromReader(rawURL string, body io.Reader, contentType string, contentLength int64) error {
	req, err := http.NewRequest(http.MethodPut, rawURL, body)
	if err != nil {
		return fmt.Errorf("build PUT request: %w", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if contentLength > 0 {
		req.ContentLength = contentLength
	}
	// Presigned URLs MUST NOT carry Authorization (the signature is the auth).
	httpClient := &http.Client{Transport: client.DefaultTransport()}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("PUT %s: %w", rawURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("PUT %s returned %d: %s", rawURL, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

// streamRemoteToWriter downloads a single object to w via the inline-GET
// route (which supports wildcard keys).
func streamRemoteToWriter(bucket, key string, w io.Writer) (int64, error) {
	c := newClient()
	urlPath := "/api/v2/blob/buckets/" + url.PathEscape(bucket) + "/objects/" + escapeKey(key)
	n, err := c.GetRaw(urlPath, w)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// escapeKey escapes a remote key for URL inclusion while preserving slashes
// (which act as path separators in object keys).
func escapeKey(key string) string {
	parts := strings.Split(key, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

// listAllObjects exhausts the paginated driver-list endpoint and returns
// every BlobObjectMeta in the bucket below prefix. The caller is in charge
// of capping if needed.
func listAllObjects(bucket, prefix string, maxItems int) ([]apiclient.BlobObjectMeta, error) {
	cli, ctx := newAPIClient()
	var (
		out    []apiclient.BlobObjectMeta
		cursor string
	)
	for {
		req := cli.BlobAPI.BlobListObjects(ctx, bucket).MaxKeys(1000)
		if prefix != "" {
			req = req.Prefix(prefix)
		}
		if cursor != "" {
			req = req.ContinuationToken(cursor)
		}
		resp, _, err := req.Execute()
		if err != nil {
			return out, err
		}
		if resp.Data != nil {
			out = append(out, resp.Data.GetItems()...)
			if maxItems > 0 && len(out) >= maxItems {
				return out[:maxItems], nil
			}
			cursor = resp.Data.GetNextContinuationToken()
			if cursor == "" {
				break
			}
		} else {
			break
		}
	}
	return out, nil
}

// confirmPrompt is a generic [y/N] prompt; mirrors confirmDeletion's gating
// for non-delete confirmations (e.g. mirror --delete).
func confirmPrompt(stmt string, yes, force bool) error {
	if yes || force {
		return nil
	}
	if flagNonInteractive {
		return usageErrorf("refusing to proceed without --yes in non-interactive mode")
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return usageErrorf("refusing to proceed without --yes when stdin is not a TTY")
	}
	fmt.Fprintf(os.Stderr, "%s [y/N] ", stmt)
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	if line != "y" && line != "yes" {
		return usageErrorf("aborted by user")
	}
	return nil
}

// isBinaryMime returns true for content types that should not be dumped to
// a TTY without --force. Mirrors mc's behavior.
func isBinaryMime(mime string) bool {
	if mime == "" {
		return false
	}
	if strings.HasPrefix(mime, "text/") || strings.HasPrefix(mime, "application/json") ||
		strings.HasPrefix(mime, "application/xml") || strings.HasPrefix(mime, "application/yaml") {
		return false
	}
	if strings.HasPrefix(mime, "image/") || strings.HasPrefix(mime, "audio/") ||
		strings.HasPrefix(mime, "video/") || strings.HasPrefix(mime, "application/octet-stream") ||
		strings.Contains(mime, "zip") || strings.Contains(mime, "tar") || strings.Contains(mime, "gzip") {
		return true
	}
	return false
}

// ============================================================================
// blob ls
// ============================================================================

var (
	blobLsMax    int
	blobLsPageSz int
)

var blobLsCmd = &cobra.Command{
	Use:     "ls <bucket>[:<prefix>]",
	Aliases: []string{"list"},
	Short:   "List objects in a bucket, optionally under a prefix",
	Long: `List objects in <bucket> via the driver-level list endpoint (the
storage-side source of truth, matches S3 list-objects-v2 conventions).

EXAMPLES:
  aegisctl blob ls aegis-pages:
  aegisctl blob ls aegis-pages:my-site/
  aegisctl blob ls aegis-pages:my-site/ --output ndjson | jq -r .key

See also: aegisctl bucket ls, aegisctl blob find.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ref, err := blobref.Parse(args[0])
		if err != nil {
			return usageErrorf("%v", err)
		}
		if err := requireRemote(args[0], ref); err != nil {
			return err
		}
		if err := requireAPIContext(true); err != nil {
			return err
		}
		items, err := listAllObjects(ref.Bucket, ref.Key, blobLsMax)
		if err != nil {
			return err
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
				it.GetKey(),
				strconv.FormatInt(int64(it.GetSizeBytes()), 10),
				it.GetUpdatedAt(),
				it.GetContentType(),
			})
		}
		output.PrintTable([]string{"KEY", "SIZE", "UPDATED_AT", "CONTENT_TYPE"}, rows)
		return nil
	},
}

// ============================================================================
// blob stat
// ============================================================================

var blobStatCmd = &cobra.Command{
	Use:   "stat <bucket>:<key>",
	Short: "Print metadata for a single object without streaming the body",
	Long: `Return object metadata: size, content-type, etag, last-modified, and any
attached user metadata. Exits 7 (not found) if the object is absent.

EXAMPLES:
  aegisctl blob stat aegis-pages:my-site/index.md
  aegisctl blob stat aegis-pages:my-site/index.md --output json | jq .size_bytes
`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ref, err := blobref.Parse(args[0])
		if err != nil {
			return usageErrorf("%v", err)
		}
		if err := requireRemote(args[0], ref); err != nil {
			return err
		}
		if ref.Key == "" {
			return usageErrorf("stat requires a non-empty key")
		}
		if err := requireAPIContext(true); err != nil {
			return err
		}
		cli, ctx := newAPIClient()
		resp, _, err := cli.BlobAPI.BlobStat(ctx, ref.Bucket, ref.Key).Execute()
		if err != nil {
			return err
		}
		if resp.Data == nil {
			return notFoundErrorf("object %q not found", args[0])
		}
		meta := resp.Data
		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(meta)
			return nil
		}
		rows := [][]string{
			{"key", meta.GetKey()},
			{"size_bytes", strconv.FormatInt(int64(meta.GetSizeBytes()), 10)},
			{"content_type", meta.GetContentType()},
			{"etag", meta.GetEtag()},
			{"updated_at", meta.GetUpdatedAt()},
		}
		for k, v := range meta.GetMetadata() {
			rows = append(rows, []string{"meta." + k, v})
		}
		output.PrintTable([]string{"FIELD", "VALUE"}, rows)
		return nil
	},
}

// ============================================================================
// blob cat
// ============================================================================

var blobCatForce bool

var blobCatCmd = &cobra.Command{
	Use:   "cat <bucket>:<key>",
	Short: "Stream the raw bytes of an object to stdout",
	Long: `Stream the object body to stdout. Refuses to write a binary content-type
to a terminal unless --force is given (mirrors mc's behavior).

EXAMPLES:
  aegisctl blob cat aegis-pages:my-site/index.md
  aegisctl blob cat aegis-pages:dist/app.tar.gz --force > app.tar.gz
`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ref, err := blobref.Parse(args[0])
		if err != nil {
			return usageErrorf("%v", err)
		}
		if err := requireRemote(args[0], ref); err != nil {
			return err
		}
		if ref.Key == "" {
			return usageErrorf("cat requires a non-empty key")
		}
		if err := requireAPIContext(true); err != nil {
			return err
		}

		// Guard binary -> TTY before downloading anything.
		if !blobCatForce && term.IsTerminal(int(os.Stdout.Fd())) {
			cli, ctx := newAPIClient()
			if meta, _, err := cli.BlobAPI.BlobStat(ctx, ref.Bucket, ref.Key).Execute(); err == nil &&
				meta.Data != nil && isBinaryMime(meta.Data.GetContentType()) {
				return &exitError{Code: ExitCodeTimeout, Message: fmt.Sprintf(
					"object content-type %q looks binary; refusing to write to terminal — use --force to override or redirect stdout",
					meta.Data.GetContentType(),
				)}
			}
		}

		_, err = streamRemoteToWriter(ref.Bucket, ref.Key, os.Stdout)
		return err
	},
}

// ============================================================================
// blob cp
// ============================================================================

var (
	blobCpRecursive    bool
	blobCpIfNotExists  bool
	blobCpContentType  string
)

var blobCpCmd = &cobra.Command{
	Use:   "cp <src> <dst>",
	Short: "Copy bytes between local and remote (or remote and remote)",
	Long: `Copy a file or object. At most one side may be a local path; Local->Local
is refused (use real cp).

  Local -> Remote   Uploads via a presigned PUT.
  Remote -> Local   Streams via the inline-GET route. Use '-' as dst for stdout.
  Remote -> Remote  Uses server-side copy when within the same bucket; falls
                    back to download+upload across buckets.

EXAMPLES:
  aegisctl blob cp ./index.html aegis-pages:my-site/index.html
  aegisctl blob cp aegis-pages:my-site/index.html ./tmp/
  aegisctl blob cp -r ./build/ aegis-pages:my-site/
  aegisctl blob cp aegis-pages:tmp/a aegis-pages:final/a
  aegisctl blob cp ./big.tar.gz aegis-pages:dist/big.tar.gz --content-type application/gzip

See also: aegisctl blob mv, aegisctl blob mirror.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		src, err := blobref.Parse(args[0])
		if err != nil {
			return usageErrorf("src: %v", err)
		}
		dst, err := blobref.Parse(args[1])
		if err != nil {
			return usageErrorf("dst: %v", err)
		}
		if src.Local && dst.Local {
			return usageErrorf("blob cp does not handle Local->Local; use plain cp")
		}
		if err := requireAPIContext(true); err != nil {
			return err
		}
		return doCopy(src, dst, blobCpRecursive, blobCpIfNotExists, blobCpContentType, false)
	},
}

func doCopy(src, dst blobref.Ref, recursive, ifNotExists bool, contentType string, deleteSrc bool) error {
	// Plan first so --dry-run reports something useful.
	switch {
	case src.Local && !dst.Local:
		return copyLocalToRemote(src, dst, recursive, ifNotExists, contentType, deleteSrc)
	case !src.Local && dst.Local:
		return copyRemoteToLocal(src, dst, recursive, ifNotExists, deleteSrc)
	case !src.Local && !dst.Local:
		return copyRemoteToRemote(src, dst, recursive, ifNotExists, deleteSrc)
	}
	return usageErrorf("unsupported copy direction")
}

// presignPut requests a signed PUT URL from the server.
func presignPut(bucket, key, contentType string) (string, error) {
	cli, ctx := newAPIClient()
	req := apiclient.NewBlobPresignPutReq()
	req.SetKey(key)
	if contentType != "" {
		req.SetContentType(contentType)
	}
	resp, _, err := cli.BlobAPI.BlobPresignPut(ctx, bucket).BlobPresignPutReq(*req).Execute()
	if err != nil {
		return "", err
	}
	if resp.Data == nil {
		return "", fmt.Errorf("presign-put: empty response")
	}
	return resp.Data.GetUrl(), nil
}

// objectExists returns whether the remote object is present.
func objectExists(bucket, key string) (bool, error) {
	cli, ctx := newAPIClient()
	_, httpResp, err := cli.BlobAPI.BlobStat(ctx, bucket, key).Execute()
	if err == nil {
		return true, nil
	}
	if httpResp != nil && httpResp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	// Some servers return generic 404 wrapped as GenericOpenAPIError.
	var apiErr *apiclient.GenericOpenAPIError
	if errors.As(err, &apiErr) && strings.HasPrefix(apiErr.Error(), "404 ") {
		return false, nil
	}
	return false, err
}

func copyLocalToRemote(src, dst blobref.Ref, recursive, ifNotExists bool, contentType string, deleteSrc bool) error {
	if src.Stdin {
		return uploadOne(os.Stdin, -1, dst.Bucket, dst.Key, contentType, ifNotExists)
	}
	st, err := os.Stat(src.Key)
	if err != nil {
		return fmt.Errorf("stat %q: %w", src.Key, err)
	}
	if st.IsDir() {
		if !recursive {
			return usageErrorf("%q is a directory; pass --recursive/-r to upload its contents", src.Key)
		}
		return walkAndUpload(src.Key, dst, ifNotExists, contentType, deleteSrc)
	}

	// Single-file upload.
	key := dst.Key
	if key == "" || strings.HasSuffix(key, "/") {
		key = strings.TrimRight(key, "/") + "/" + filepath.Base(src.Key)
		key = strings.TrimPrefix(key, "/")
	}
	if flagDryRun {
		emitDryRunCopy(src.Key, dst.Bucket+":"+key)
		return nil
	}
	f, err := os.Open(src.Key)
	if err != nil {
		return fmt.Errorf("open %q: %w", src.Key, err)
	}
	defer func() { _ = f.Close() }()
	if err := uploadOne(f, st.Size(), dst.Bucket, key, contentType, ifNotExists); err != nil {
		return err
	}
	if deleteSrc {
		if err := os.Remove(src.Key); err != nil {
			return fmt.Errorf("source delete failed after upload: %w", err)
		}
	}
	return nil
}

func walkAndUpload(localRoot string, dst blobref.Ref, ifNotExists bool, contentType string, deleteSrc bool) error {
	var planned []string
	err := filepath.WalkDir(localRoot, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(localRoot, p)
		if err != nil {
			return err
		}
		remoteKey := strings.TrimRight(dst.Key, "/")
		if remoteKey != "" {
			remoteKey += "/"
		}
		remoteKey += filepath.ToSlash(rel)
		planned = append(planned, p+" -> "+dst.Bucket+":"+remoteKey)
		if flagDryRun {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return fmt.Errorf("open %q: %w", p, err)
		}
		defer func() { _ = f.Close() }()
		st, _ := f.Stat()
		if err := uploadOne(f, st.Size(), dst.Bucket, remoteKey, contentType, ifNotExists); err != nil {
			return err
		}
		output.PrintInfo("uploaded " + p + " -> " + dst.Bucket + ":" + remoteKey)
		return nil
	})
	if err != nil {
		return err
	}
	if flagDryRun {
		emitDryRunPlan(planned, nil, nil)
	}
	if deleteSrc && !flagDryRun {
		return os.RemoveAll(localRoot)
	}
	return nil
}

func uploadOne(r io.Reader, size int64, bucket, key, contentType string, ifNotExists bool) error {
	if ifNotExists {
		exists, err := objectExists(bucket, key)
		if err != nil {
			return err
		}
		if exists {
			output.PrintInfo(fmt.Sprintf("skip %s:%s (already exists)", bucket, key))
			return nil
		}
	}
	signed, err := presignPut(bucket, key, contentType)
	if err != nil {
		return fmt.Errorf("presign-put %s:%s: %w", bucket, key, err)
	}
	return httpPutFromReader(signed, r, contentType, size)
}

func copyRemoteToLocal(src, dst blobref.Ref, recursive, ifNotExists, deleteSrc bool) error {
	if src.Key == "" || strings.HasSuffix(src.Key, "/") || recursive {
		// Prefix download.
		if !recursive {
			return usageErrorf("source looks like a prefix; pass --recursive/-r to download it")
		}
		items, err := listAllObjects(src.Bucket, src.Key, 0)
		if err != nil {
			return err
		}
		var planned []string
		for _, it := range items {
			rel := strings.TrimPrefix(it.GetKey(), src.Key)
			rel = strings.TrimPrefix(rel, "/")
			localPath := filepath.Join(dst.Key, filepath.FromSlash(rel))
			planned = append(planned, src.Bucket+":"+it.GetKey()+" -> "+localPath)
			if flagDryRun {
				continue
			}
			if ifNotExists {
				if _, statErr := os.Stat(localPath); statErr == nil {
					output.PrintInfo("skip " + localPath + " (already exists)")
					continue
				}
			}
			if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
				return err
			}
			f, err := os.Create(localPath)
			if err != nil {
				return err
			}
			if _, err := streamRemoteToWriter(src.Bucket, it.GetKey(), f); err != nil {
				_ = f.Close()
				return err
			}
			_ = f.Close()
			output.PrintInfo("downloaded " + src.Bucket + ":" + it.GetKey() + " -> " + localPath)
			if deleteSrc {
				if err := deleteObject(src.Bucket, it.GetKey()); err != nil {
					return err
				}
			}
		}
		if flagDryRun {
			emitDryRunPlan(nil, planned, nil)
		}
		return nil
	}

	// Single-object download.
	target := dst.Key
	if target == "-" || dst.Stdin {
		if flagDryRun {
			emitDryRunCopy(src.Bucket+":"+src.Key, "-")
			return nil
		}
		_, err := streamRemoteToWriter(src.Bucket, src.Key, os.Stdout)
		return err
	}
	st, err := os.Stat(target)
	if err == nil && st.IsDir() {
		target = filepath.Join(target, path.Base(src.Key))
	}
	if flagDryRun {
		emitDryRunCopy(src.Bucket+":"+src.Key, target)
		return nil
	}
	if ifNotExists {
		if _, statErr := os.Stat(target); statErr == nil {
			output.PrintInfo("skip " + target + " (already exists)")
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	f, err := os.Create(target)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err := streamRemoteToWriter(src.Bucket, src.Key, f); err != nil {
		return err
	}
	if deleteSrc {
		return deleteObject(src.Bucket, src.Key)
	}
	return nil
}

func copyRemoteToRemote(src, dst blobref.Ref, recursive, ifNotExists, deleteSrc bool) error {
	if recursive {
		items, err := listAllObjects(src.Bucket, src.Key, 0)
		if err != nil {
			return err
		}
		var planned []string
		for _, it := range items {
			rel := strings.TrimPrefix(it.GetKey(), src.Key)
			rel = strings.TrimPrefix(rel, "/")
			dstKey := strings.TrimRight(dst.Key, "/")
			if dstKey != "" {
				dstKey += "/"
			}
			dstKey += rel
			planned = append(planned, src.Bucket+":"+it.GetKey()+" -> "+dst.Bucket+":"+dstKey)
			if flagDryRun {
				continue
			}
			if err := copyOneRemote(src.Bucket, it.GetKey(), dst.Bucket, dstKey, ifNotExists, deleteSrc); err != nil {
				return err
			}
		}
		if flagDryRun {
			emitDryRunPlan(planned, nil, nil)
		}
		return nil
	}
	if flagDryRun {
		emitDryRunCopy(src.Bucket+":"+src.Key, dst.Bucket+":"+dst.Key)
		return nil
	}
	return copyOneRemote(src.Bucket, src.Key, dst.Bucket, dst.Key, ifNotExists, deleteSrc)
}

func copyOneRemote(srcBucket, srcKey, dstBucket, dstKey string, ifNotExists, deleteSrc bool) error {
	if ifNotExists {
		if exists, err := objectExists(dstBucket, dstKey); err != nil {
			return err
		} else if exists {
			output.PrintInfo(fmt.Sprintf("skip %s:%s (already exists)", dstBucket, dstKey))
			return nil
		}
	}
	if srcBucket == dstBucket {
		// Server-side copy within a bucket (the only shape the backend
		// supports today).
		cli, ctx := newAPIClient()
		req := apiclient.NewBlobCopyReq(dstKey, srcKey)
		if deleteSrc {
			req.SetDeleteSrc(true)
		}
		if _, _, err := cli.BlobAPI.BlobCopy(ctx, srcBucket).BlobCopyReq(*req).Execute(); err != nil {
			return fmt.Errorf("server-side copy: %w", err)
		}
		return nil
	}
	// Cross-bucket fallback: download into a pipe, upload to dst.
	pr, pw := io.Pipe()
	errCh := make(chan error, 1)
	go func() {
		_, copyErr := streamRemoteToWriter(srcBucket, srcKey, pw)
		_ = pw.CloseWithError(copyErr)
		errCh <- copyErr
	}()
	if err := uploadOne(pr, -1, dstBucket, dstKey, "", false); err != nil {
		return err
	}
	if err := <-errCh; err != nil {
		return err
	}
	if deleteSrc {
		return deleteObject(srcBucket, srcKey)
	}
	return nil
}

func emitDryRunCopy(src, dst string) {
	if output.OutputFormat(flagOutput) == output.FormatJSON {
		output.PrintJSON(map[string]any{"dry_run": true, "would_copy": []map[string]string{{"src": src, "dst": dst}}})
		return
	}
	fmt.Fprintf(os.Stderr, "would copy %s -> %s\n", src, dst)
}

func emitDryRunPlan(uploads, downloads, deletes []string) {
	if output.OutputFormat(flagOutput) == output.FormatJSON {
		output.PrintJSON(map[string]any{
			"dry_run":         true,
			"would_upload":   uploads,
			"would_download": downloads,
			"would_delete":   deletes,
		})
		return
	}
	for _, u := range uploads {
		fmt.Fprintln(os.Stderr, "would upload:   "+u)
	}
	for _, d := range downloads {
		fmt.Fprintln(os.Stderr, "would download: "+d)
	}
	for _, d := range deletes {
		fmt.Fprintln(os.Stderr, "would delete:   "+d)
	}
}

// ============================================================================
// blob mv
// ============================================================================

var blobMvCmd = &cobra.Command{
	Use:   "mv <src> <dst>",
	Short: "Move bytes (cp + delete src). Not atomic across local/remote boundaries.",
	Long: `Move = copy + delete the source. Within a single remote bucket the backend
performs this server-side and returns a multi-status response if the
post-copy source delete fails (the caller is told via exit code 8/conflict).
Across local/remote or across remote buckets, blob mv is cp + rm and is NOT
atomic — interrupt at the wrong moment and you may end up with both copies.

EXAMPLES:
  aegisctl blob mv aegis-pages:tmp/a aegis-pages:final/a
  aegisctl blob mv ./local.md aegis-pages:my-site/index.md

See also: aegisctl blob cp, aegisctl blob rm.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		src, err := blobref.Parse(args[0])
		if err != nil {
			return usageErrorf("src: %v", err)
		}
		dst, err := blobref.Parse(args[1])
		if err != nil {
			return usageErrorf("dst: %v", err)
		}
		if src.Local && dst.Local {
			return usageErrorf("blob mv does not handle Local->Local; use plain mv")
		}
		if err := requireAPIContext(true); err != nil {
			return err
		}
		return doCopy(src, dst, false, false, "", true)
	},
}

// ============================================================================
// blob rm
// ============================================================================

var (
	blobRmRecursive bool
	blobRmYes       bool
)

var blobRmCmd = &cobra.Command{
	Use:   "rm <bucket>:<key>",
	Short: "Delete a single object or a prefix (with --recursive)",
	Long: `Delete an object by key, or every object under a prefix with --recursive.
Refuses non-recursive delete when the path is empty or ends with '/'.

EXAMPLES:
  aegisctl blob rm aegis-pages:my-site/old.md --yes
  aegisctl blob rm -r aegis-pages:tmp/ --yes
  aegisctl blob rm -r aegis-pages:tmp/ --dry-run

See also: aegisctl blob mv.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ref, err := blobref.Parse(args[0])
		if err != nil {
			return usageErrorf("%v", err)
		}
		if err := requireRemote(args[0], ref); err != nil {
			return err
		}
		if err := requireAPIContext(true); err != nil {
			return err
		}
		isPrefix := ref.Key == "" || strings.HasSuffix(ref.Key, "/")
		if isPrefix && !blobRmRecursive {
			return usageErrorf("refusing to delete prefix %q without --recursive/-r", args[0])
		}

		if !blobRmRecursive {
			if flagDryRun {
				emitDryRunPlan(nil, nil, []string{ref.Bucket + ":" + ref.Key})
				return nil
			}
			if err := confirmDeletion("object", ref.Bucket+":"+ref.Key, 0, blobRmYes); err != nil {
				return err
			}
			return deleteObject(ref.Bucket, ref.Key)
		}

		// Recursive: enumerate first, then batch-delete.
		items, err := listAllObjects(ref.Bucket, ref.Key, 0)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return nil
		}
		var planned []string
		keys := make([]string, 0, len(items))
		for _, it := range items {
			planned = append(planned, ref.Bucket+":"+it.GetKey())
			keys = append(keys, it.GetKey())
		}
		if flagDryRun {
			emitDryRunPlan(nil, nil, planned)
			return nil
		}
		if err := confirmDeletion("prefix", fmt.Sprintf("%s:%s (%d objects)", ref.Bucket, ref.Key, len(keys)), 0, blobRmYes); err != nil {
			return err
		}
		cli, ctx := newAPIClient()
		body := apiclient.NewBlobBatchDeleteReq(keys)
		resp, _, err := cli.BlobAPI.BlobBatchDelete(ctx, ref.Bucket).BlobBatchDeleteReq(*body).Execute()
		if err != nil {
			return err
		}
		var deleted []string
		var failed []apiclient.BlobBatchFailItem
		if resp.Data != nil {
			deleted = resp.Data.GetDeleted()
			failed = resp.Data.GetFailed()
		}
		if len(failed) > 0 {
			fmt.Fprintf(os.Stderr, "batch delete: %d failures:\n", len(failed))
			for _, f := range failed {
				fmt.Fprintf(os.Stderr, "  %s: %s\n", f.GetKey(), f.GetError())
			}
			return workflowFailureErrorf("batch delete completed with failures")
		}
		output.PrintInfo(fmt.Sprintf("deleted %d objects under %s:%s", len(deleted), ref.Bucket, ref.Key))
		return nil
	},
}

func deleteObject(bucket, key string) error {
	cli, ctx := newAPIClient()
	_, _, err := cli.BlobAPI.BlobDelete(ctx, bucket, key).Execute()
	return err
}

// ============================================================================
// blob find
// ============================================================================

var (
	blobFindName     string
	blobFindMaxDepth int
)

var blobFindCmd = &cobra.Command{
	Use:   "find <bucket>[:<prefix>]",
	Short: "Walk a bucket and filter objects client-side",
	Long: `Walk a bucket or prefix and emit each object that matches the filter.
Defaults to ndjson output so the stream can be piped into jq / xargs.

--name accepts a path.Match glob applied to the basename of each key.
--max-depth caps how deep relative to the search prefix the walk descends.

EXAMPLES:
  aegisctl blob find aegis-pages: --name '*.md'
  aegisctl blob find aegis-pages:my-site/ --name 'index.*' --max-depth 1
  aegisctl blob find aegis-pages: --output table | head -20
`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ref, err := blobref.Parse(args[0])
		if err != nil {
			return usageErrorf("%v", err)
		}
		if err := requireRemote(args[0], ref); err != nil {
			return err
		}
		if err := requireAPIContext(true); err != nil {
			return err
		}
		items, err := listAllObjects(ref.Bucket, ref.Key, 0)
		if err != nil {
			return err
		}
		var matched []apiclient.BlobObjectMeta
		for _, it := range items {
			key := it.GetKey()
			base := path.Base(key)
			if blobFindName != "" {
				ok, mErr := path.Match(blobFindName, base)
				if mErr != nil {
					return usageErrorf("invalid --name glob %q: %v", blobFindName, mErr)
				}
				if !ok {
					continue
				}
			}
			if blobFindMaxDepth > 0 {
				rel := strings.TrimPrefix(key, ref.Key)
				rel = strings.TrimPrefix(rel, "/")
				if strings.Count(rel, "/") >= blobFindMaxDepth {
					continue
				}
			}
			matched = append(matched, it)
		}
		switch output.OutputFormat(flagOutput) {
		case output.FormatJSON:
			output.PrintJSON(matched)
			return nil
		case output.FormatTable:
			rows := make([][]string, 0, len(matched))
			for _, it := range matched {
				rows = append(rows, []string{it.GetKey(), strconv.FormatInt(int64(it.GetSizeBytes()), 10), it.GetUpdatedAt()})
			}
			output.PrintTable([]string{"KEY", "SIZE", "UPDATED_AT"}, rows)
			return nil
		default:
			return output.PrintNDJSON(matched)
		}
	},
}

// ============================================================================
// blob mirror
// ============================================================================

var (
	blobMirrorDelete bool
	blobMirrorYes    bool
)

var blobMirrorCmd = &cobra.Command{
	Use:   "mirror <src> <dst>",
	Short: "One-way sync from src to dst (with optional --delete)",
	Long: `Mirror copies missing / changed files from src to dst. With --delete it also
removes files from dst that are not present in src. Local->Local is refused
(use rsync). Always confirms when --delete is set, unless --yes / --force /
--non-interactive.

EXAMPLES:
  aegisctl blob mirror ./build/ aegis-pages:my-site/ --dry-run
  aegisctl blob mirror ./build/ aegis-pages:my-site/ --delete --yes
  aegisctl blob mirror aegis-pages:my-site/ ./snapshot/ -r

See also: aegisctl blob cp -r.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		src, err := blobref.Parse(args[0])
		if err != nil {
			return usageErrorf("src: %v", err)
		}
		dst, err := blobref.Parse(args[1])
		if err != nil {
			return usageErrorf("dst: %v", err)
		}
		if src.Local && dst.Local {
			return usageErrorf("blob mirror does not handle Local->Local; use rsync")
		}
		if err := requireAPIContext(true); err != nil {
			return err
		}
		plan, err := buildMirrorPlan(src, dst)
		if err != nil {
			return err
		}
		if !blobMirrorDelete {
			plan.Deletes = nil
		}
		if flagDryRun {
			emitDryRunPlan(plan.Uploads, plan.Downloads, plan.Deletes)
			return nil
		}
		if blobMirrorDelete && len(plan.Deletes) > 0 {
			if err := confirmPrompt(fmt.Sprintf("Mirror will delete %d objects from %s.", len(plan.Deletes), dst.String()), blobMirrorYes, false); err != nil {
				return err
			}
		}
		// Execute.
		for _, item := range plan.execItems {
			if err := item.run(); err != nil {
				return err
			}
		}
		return nil
	},
}

type mirrorItem struct {
	run func() error
}

type mirrorPlan struct {
	Uploads   []string
	Downloads []string
	Deletes   []string
	execItems []mirrorItem
}

func buildMirrorPlan(src, dst blobref.Ref) (mirrorPlan, error) {
	var plan mirrorPlan
	switch {
	case src.Local && !dst.Local:
		// Local dir -> remote prefix.
		localFiles, err := walkLocalDir(src.Key)
		if err != nil {
			return plan, err
		}
		remote, err := listAllObjects(dst.Bucket, dst.Key, 0)
		if err != nil {
			return plan, err
		}
		remoteSet := make(map[string]struct{}, len(remote))
		for _, it := range remote {
			remoteSet[strings.TrimPrefix(it.GetKey(), dst.Key)] = struct{}{}
		}
		seen := map[string]struct{}{}
		for _, rel := range localFiles {
			remoteKey := strings.TrimRight(dst.Key, "/")
			if remoteKey != "" {
				remoteKey += "/"
			}
			remoteKey += rel
			seen[strings.TrimPrefix(remoteKey, dst.Key)] = struct{}{}
			plan.Uploads = append(plan.Uploads, filepath.Join(src.Key, filepath.FromSlash(rel))+" -> "+dst.Bucket+":"+remoteKey)
			lp := filepath.Join(src.Key, filepath.FromSlash(rel))
			rk := remoteKey
			plan.execItems = append(plan.execItems, mirrorItem{run: func() error {
				f, err := os.Open(lp)
				if err != nil {
					return err
				}
				defer func() { _ = f.Close() }()
				st, _ := f.Stat()
				return uploadOne(f, st.Size(), dst.Bucket, rk, "", false)
			}})
		}
		for k := range remoteSet {
			if _, hit := seen[k]; hit {
				continue
			}
			fullKey := strings.TrimRight(dst.Key, "/") + k
			plan.Deletes = append(plan.Deletes, dst.Bucket+":"+fullKey)
			b, kk := dst.Bucket, fullKey
			plan.execItems = append(plan.execItems, mirrorItem{run: func() error { return deleteObject(b, kk) }})
		}
	case !src.Local && dst.Local:
		// Remote prefix -> local dir.
		remote, err := listAllObjects(src.Bucket, src.Key, 0)
		if err != nil {
			return plan, err
		}
		localFiles, err := walkLocalDir(dst.Key)
		if err != nil && !os.IsNotExist(err) {
			return plan, err
		}
		localSet := make(map[string]struct{}, len(localFiles))
		for _, rel := range localFiles {
			localSet[rel] = struct{}{}
		}
		seen := map[string]struct{}{}
		for _, it := range remote {
			rel := strings.TrimPrefix(it.GetKey(), src.Key)
			rel = strings.TrimPrefix(rel, "/")
			lp := filepath.Join(dst.Key, filepath.FromSlash(rel))
			seen[rel] = struct{}{}
			plan.Downloads = append(plan.Downloads, src.Bucket+":"+it.GetKey()+" -> "+lp)
			bb, kk, target := src.Bucket, it.GetKey(), lp
			plan.execItems = append(plan.execItems, mirrorItem{run: func() error {
				if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
					return err
				}
				f, err := os.Create(target)
				if err != nil {
					return err
				}
				defer func() { _ = f.Close() }()
				_, err = streamRemoteToWriter(bb, kk, f)
				return err
			}})
		}
		for _, rel := range localFiles {
			if _, hit := seen[rel]; hit {
				continue
			}
			lp := filepath.Join(dst.Key, filepath.FromSlash(rel))
			plan.Deletes = append(plan.Deletes, lp)
			target := lp
			plan.execItems = append(plan.execItems, mirrorItem{run: func() error { return os.Remove(target) }})
		}
	default:
		// Remote -> Remote
		srcItems, err := listAllObjects(src.Bucket, src.Key, 0)
		if err != nil {
			return plan, err
		}
		dstItems, err := listAllObjects(dst.Bucket, dst.Key, 0)
		if err != nil {
			return plan, err
		}
		dstSet := make(map[string]struct{}, len(dstItems))
		for _, it := range dstItems {
			dstSet[strings.TrimPrefix(it.GetKey(), dst.Key)] = struct{}{}
		}
		seen := map[string]struct{}{}
		for _, it := range srcItems {
			rel := strings.TrimPrefix(it.GetKey(), src.Key)
			rel = strings.TrimPrefix(rel, "/")
			dstKey := strings.TrimRight(dst.Key, "/") + "/" + rel
			dstKey = strings.TrimPrefix(dstKey, "/")
			seen[strings.TrimPrefix(dstKey, dst.Key)] = struct{}{}
			plan.Uploads = append(plan.Uploads, src.Bucket+":"+it.GetKey()+" -> "+dst.Bucket+":"+dstKey)
			sb, sk, db, dk := src.Bucket, it.GetKey(), dst.Bucket, dstKey
			plan.execItems = append(plan.execItems, mirrorItem{run: func() error {
				return copyOneRemote(sb, sk, db, dk, false, false)
			}})
		}
		for k := range dstSet {
			if _, hit := seen[k]; hit {
				continue
			}
			fullKey := strings.TrimRight(dst.Key, "/") + k
			plan.Deletes = append(plan.Deletes, dst.Bucket+":"+fullKey)
			b, kk := dst.Bucket, fullKey
			plan.execItems = append(plan.execItems, mirrorItem{run: func() error { return deleteObject(b, kk) }})
		}
	}
	sort.Strings(plan.Uploads)
	sort.Strings(plan.Downloads)
	sort.Strings(plan.Deletes)
	return plan, nil
}

func walkLocalDir(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	return out, err
}

// ============================================================================
// blob presign
// ============================================================================

var (
	blobPresignTTL    string
	blobPresignMethod string
)

var blobPresignCmd = &cobra.Command{
	Use:   "presign <bucket>:<key>",
	Short: "Issue a presigned GET or PUT URL",
	Long: `Issue a short-lived presigned URL the caller can hand to curl or a browser
to fetch (GET) or upload (PUT) an object without aegislab credentials.

The URL is written to stdout so it's pipeable; metadata (expires_at, method)
is written to stderr when stdout is a TTY, or folded into the JSON object on
--output json.

EXAMPLES:
  aegisctl blob presign aegis-pages:my-site/index.md --ttl 30m
  aegisctl blob presign aegis-pages:dist/big.tar.gz --method put --ttl 1h
  URL=$(aegisctl blob presign aegis-pages:my-site/index.md --ttl 5m); curl -fsSL "$URL" | head

See also: aegisctl share upload for short public links.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ref, err := blobref.Parse(args[0])
		if err != nil {
			return usageErrorf("%v", err)
		}
		if err := requireRemote(args[0], ref); err != nil {
			return err
		}
		if ref.Key == "" {
			return usageErrorf("presign requires a non-empty key")
		}
		method := strings.ToLower(blobPresignMethod)
		if method == "" {
			method = "get"
		}
		if method != "get" && method != "put" {
			return usageErrorf("--method must be 'get' or 'put'")
		}
		if err := requireAPIContext(true); err != nil {
			return err
		}
		var ttl int64
		if blobPresignTTL != "" {
			n, err := parseTTL(blobPresignTTL)
			if err != nil {
				return usageErrorf("%v", err)
			}
			ttl = n
		}

		cli, ctx := newAPIClient()
		var urlOut, expires, methodOut string
		if method == "get" {
			req := apiclient.NewBlobPresignGetReq(ref.Key)
			if ttl > 0 {
				req.SetTtlSeconds(int32(ttl))
			}
			resp, _, err := cli.BlobAPI.BlobPresignGet(ctx, ref.Bucket).BlobPresignGetReq(*req).Execute()
			if err != nil {
				return err
			}
			if resp.Data == nil {
				return fmt.Errorf("presign-get: empty response")
			}
			urlOut = resp.Data.GetUrl()
			expires = resp.Data.GetExpiresAt()
			methodOut = resp.Data.GetMethod()
		} else {
			req := apiclient.NewBlobPresignPutReq()
			req.SetKey(ref.Key)
			if ttl > 0 {
				req.SetTtlSeconds(int32(ttl))
			}
			resp, _, err := cli.BlobAPI.BlobPresignPut(ctx, ref.Bucket).BlobPresignPutReq(*req).Execute()
			if err != nil {
				return err
			}
			if resp.Data == nil {
				return fmt.Errorf("presign-put: empty response")
			}
			urlOut = resp.Data.GetUrl()
			expires = resp.Data.GetExpiresAt()
			methodOut = resp.Data.GetMethod()
		}
		if methodOut == "" {
			methodOut = strings.ToUpper(method)
		}

		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(map[string]any{
				"url":        urlOut,
				"method":     methodOut,
				"expires_at": expires,
			})
			return nil
		}
		fmt.Println(urlOut)
		if expires != "" {
			fmt.Fprintf(os.Stderr, "method: %s, expires_at: %s\n", methodOut, expires)
		}
		return nil
	},
}

// ============================================================================
// json helpers (unused but reserved)
// ============================================================================

var _ = bytes.NewReader
var _ = json.Marshal

func init() {
	blobLsCmd.Flags().IntVar(&blobLsMax, "max", 0, "Cap total objects returned (0 = no cap)")
	blobLsCmd.Flags().IntVar(&blobLsPageSz, "page-size", 1000, "Server-side page size hint")

	blobCatCmd.Flags().BoolVar(&blobCatForce, "force", false, "Allow streaming binary content to a TTY")

	blobCpCmd.Flags().BoolVarP(&blobCpRecursive, "recursive", "r", false, "Walk directories / prefixes")
	blobCpCmd.Flags().BoolVar(&blobCpIfNotExists, "if-not-exists", false, "Skip files that already exist at the destination")
	blobCpCmd.Flags().StringVar(&blobCpContentType, "content-type", "", "Content-Type for the uploaded object (default: server-detected)")

	blobRmCmd.Flags().BoolVarP(&blobRmRecursive, "recursive", "r", false, "Delete every object under the prefix")
	blobRmCmd.Flags().BoolVar(&blobRmYes, "yes", false, "Skip confirmation prompt")
	blobRmCmd.Flags().BoolVar(&blobRmYes, "force", false, "Alias for --yes")

	blobFindCmd.Flags().StringVar(&blobFindName, "name", "", "Glob applied to the object basename (e.g. '*.md')")
	blobFindCmd.Flags().IntVar(&blobFindMaxDepth, "max-depth", 0, "Cap path depth relative to the prefix (0 = no cap)")

	blobMirrorCmd.Flags().BoolVar(&blobMirrorDelete, "delete", false, "Delete from dst what's missing in src")
	blobMirrorCmd.Flags().BoolVar(&blobMirrorYes, "yes", false, "Skip the --delete confirmation prompt")

	blobPresignCmd.Flags().StringVar(&blobPresignTTL, "ttl", "", "Lifetime: seconds, or 30m/24h/7d (server default if unset)")
	blobPresignCmd.Flags().StringVar(&blobPresignMethod, "method", "get", "HTTP method: get | put")

	blobCmd.AddCommand(blobLsCmd)
	blobCmd.AddCommand(blobStatCmd)
	blobCmd.AddCommand(blobCatCmd)
	blobCmd.AddCommand(blobCpCmd)
	blobCmd.AddCommand(blobMvCmd)
	blobCmd.AddCommand(blobRmCmd)
	blobCmd.AddCommand(blobFindCmd)
	blobCmd.AddCommand(blobMirrorCmd)
	blobCmd.AddCommand(blobPresignCmd)

	cobra.OnInitialize(func() {
		markDryRunSupported(blobCpCmd)
		markDryRunSupported(blobMvCmd)
		markDryRunSupported(blobRmCmd)
		markDryRunSupported(blobMirrorCmd)
	})
}
