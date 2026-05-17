package injection

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	blobclient "aegis/clients/blob"
	"aegis/platform/consts"
	"aegis/platform/model"
	"aegis/platform/utils"

	"github.com/sirupsen/logrus"
)

// S3DatapackStore is the S3/rustfs-backed implementation of DatapackStorage.
// Objects live under the key prefix `<datapackName>/<filePath>` in the
// configured bucket. Filesystem semantics (datapack-name as the top-level
// "directory") are preserved by treating the datapack name as a key
// prefix.
type S3DatapackStore struct {
	client blobclient.Client
	bucket string
}

// NewS3DatapackStore constructs the S3-backed DatapackStorage. Bucket
// comes from `jfs.s3.datapack_bucket`; the blob.Client is wired by fx
// and reads its credentials/endpoint from process env (AWS_* /
// BLOB_S3_*).
func NewS3DatapackStore(client blobclient.Client, bucket string) *S3DatapackStore {
	return &S3DatapackStore{client: client, bucket: bucket}
}

var _ DatapackStorage = (*S3DatapackStore)(nil)

// RootDir returns the prefix used to address objects belonging to this
// datapack. Callers that previously concatenated a filesystem path onto
// this value will see a logical key prefix instead — both forms still
// uniquely identify the datapack root.
func (s *S3DatapackStore) RootDir(datapackName string) string {
	return datapackName
}

func (s *S3DatapackStore) ctx() context.Context { return context.Background() }

func (s *S3DatapackStore) listPrefix(prefix, delimiter string) ([]blobclient.ObjectMeta, []string, error) {
	var objects []blobclient.ObjectMeta
	var commonPrefixes []string
	token := ""
	for {
		res, err := s.client.List(s.ctx(), s.bucket, prefix, blobclient.ListOpts{
			Delimiter:         delimiter,
			ContinuationToken: token,
		})
		if err != nil {
			return nil, nil, err
		}
		objects = append(objects, res.Objects...)
		commonPrefixes = append(commonPrefixes, res.CommonPrefixes...)
		if !res.IsTruncated || res.NextContinuationToken == "" {
			break
		}
		token = res.NextContinuationToken
	}
	return objects, commonPrefixes, nil
}

func (s *S3DatapackStore) Package(zipWriter *zip.Writer, datapackName string, excludeRules []utils.ExculdeRule) error {
	prefix := datapackName + "/"
	objects, _, err := s.listPrefix(prefix, "")
	if err != nil {
		return fmt.Errorf("failed to list datapack %s: %w", datapackName, err)
	}
	for _, obj := range objects {
		relKey := strings.TrimPrefix(obj.Key, prefix)
		fileName := path.Base(relKey)
		skip := false
		for _, rule := range excludeRules {
			if utils.MatchFile(fileName, rule) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		zipPath := path.Join(consts.DownloadFilename, datapackName, relKey)
		w, err := zipWriter.Create(zipPath)
		if err != nil {
			return fmt.Errorf("failed to create zip entry %s: %w", zipPath, err)
		}
		rc, _, err := s.client.GetReader(s.ctx(), s.bucket, obj.Key)
		if err != nil {
			return fmt.Errorf("failed to get object %s: %w", obj.Key, err)
		}
		if _, err := io.Copy(w, rc); err != nil {
			_ = rc.Close()
			return fmt.Errorf("failed to copy object %s into zip: %w", obj.Key, err)
		}
		_ = rc.Close()
	}
	return nil
}

func (s *S3DatapackStore) BuildFileTree(datapackName, baseURL string, datapackID int) (*DatapackFilesResp, error) {
	_ = baseURL
	prefix := datapackName + "/"
	// Existence check — empty prefix means no datapack on disk.
	probe, err := s.client.List(s.ctx(), s.bucket, prefix, blobclient.ListOpts{MaxKeys: 1})
	if err != nil {
		return nil, fmt.Errorf("failed to probe datapack %s: %w", datapackName, err)
	}
	if len(probe.Objects) == 0 && len(probe.CommonPrefixes) == 0 {
		return nil, fmt.Errorf("%w: datapack directory not found for datapack id %d", consts.ErrNotFound, datapackID)
	}

	resp := &DatapackFilesResp{Files: []DatapackFileItem{}}
	items, err := s.buildSubtree(prefix, "", resp)
	if err != nil {
		return nil, err
	}
	resp.Files = items
	return resp, nil
}

func (s *S3DatapackStore) buildSubtree(absPrefix, relPath string, resp *DatapackFilesResp) ([]DatapackFileItem, error) {
	objects, commonPrefixes, err := s.listPrefix(absPrefix, "/")
	if err != nil {
		return nil, err
	}

	var items []DatapackFileItem

	for _, cp := range commonPrefixes {
		// cp is "<absPrefix><dirname>/"
		trimmed := strings.TrimSuffix(strings.TrimPrefix(cp, absPrefix), "/")
		if trimmed == "" {
			continue
		}
		childRel := path.Join(relPath, trimmed)
		children, err := s.buildSubtree(cp, childRel, resp)
		if err != nil {
			return nil, err
		}
		subFolderCount, fileCount := 0, 0
		for _, c := range children {
			if len(c.Children) > 0 {
				subFolderCount++
			} else {
				fileCount++
			}
		}
		items = append(items, DatapackFileItem{
			Name:     trimmed,
			Path:     filepath.ToSlash(childRel),
			Size:     fmt.Sprintf("%d subfolders, %d files", subFolderCount, fileCount),
			Children: children,
		})
		resp.DirCount++
	}

	for _, obj := range objects {
		name := strings.TrimPrefix(obj.Key, absPrefix)
		if name == "" || strings.Contains(name, "/") {
			// belongs to a subdirectory (defensive — delimiter should
			// have collapsed these into CommonPrefixes already).
			continue
		}
		modTime := obj.UpdatedAt
		items = append(items, DatapackFileItem{
			Name:    name,
			Path:    filepath.ToSlash(path.Join(relPath, name)),
			Size:    formatFileSize(obj.Size),
			ModTime: &modTime,
		})
		resp.FileCount++
	}

	return items, nil
}

// seekableTempReader downloads the object into a temp file so the
// returned reader satisfies io.ReadSeekCloser (required by gin's
// http.ServeContent path). TODO: replace with an HTTP-range-backed
// seekable reader for large objects.
type seekableTempReader struct {
	*os.File
}

func (r *seekableTempReader) Close() error {
	name := r.File.Name()
	err := r.File.Close()
	_ = os.Remove(name)
	return err
}

func (s *S3DatapackStore) OpenFile(datapackName, filePath string) (string, string, int64, io.ReadSeekCloser, error) {
	key, err := s.resolveKey(datapackName, filePath)
	if err != nil {
		return "", "", 0, nil, err
	}

	meta, err := s.client.Stat(s.ctx(), s.bucket, key)
	if err != nil {
		return "", "", 0, nil, fmt.Errorf("%w: %v", consts.ErrNotFound, err)
	}

	rc, _, err := s.client.GetReader(s.ctx(), s.bucket, key)
	if err != nil {
		return "", "", 0, nil, fmt.Errorf("failed to get object %s: %w", key, err)
	}
	defer func() { _ = rc.Close() }()

	tmp, err := os.CreateTemp("", "datapack-download-*")
	if err != nil {
		return "", "", 0, nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	if _, err := io.Copy(tmp, rc); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return "", "", 0, nil, fmt.Errorf("failed to buffer object %s: %w", key, err)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return "", "", 0, nil, fmt.Errorf("failed to rewind temp file: %w", err)
	}

	fileName := path.Base(key)
	contentType := meta.ContentType
	if contentType == "" {
		contentType = guessContentTypeByExt(fileName)
	}
	return fileName, contentType, meta.Size, &seekableTempReader{File: tmp}, nil
}

func guessContentTypeByExt(name string) string {
	switch filepath.Ext(name) {
	case ".json":
		return "application/json"
	case ".yaml", ".yml":
		return "application/x-yaml"
	case ".txt", ".log":
		return "text/plain"
	case ".csv":
		return "text/csv"
	case ".xml":
		return "application/xml"
	case ".html", ".htm":
		return "text/html"
	case ".pdf":
		return "application/pdf"
	case ".zip":
		return "application/zip"
	case ".tar", ".gz", ".tgz":
		return "application/x-tar"
	}
	return "application/octet-stream"
}

func (s *S3DatapackStore) ResolveFilePath(datapackName, filePath string) (string, error) {
	return s.resolveKey(datapackName, filePath)
}

// ParquetReaderPath returns a presigned HTTPS URL pointing at the object.
// DuckDB's httpfs extension can `read_parquet()` it directly. TTL must
// outlast the query — 10 minutes is a sensible default for one-shot
// schema-and-query usage; for connections that run multiple sequential
// queries against the same VIEW, set a longer TTL.
func (s *S3DatapackStore) ParquetReaderPath(ctx context.Context, datapackName, filePath string, ttl time.Duration) (string, error) {
	key, err := s.resolveKey(datapackName, filePath)
	if err != nil {
		return "", err
	}
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	presigned, err := s.client.PresignGet(ctx, s.bucket, key, blobclient.PresignGetReq{
		TTLSeconds: int(ttl.Seconds()),
	})
	if err != nil {
		return "", fmt.Errorf("%w: presign %s/%s: %v", consts.ErrNotFound, datapackName, filePath, err)
	}
	return presigned.URL, nil
}

func (s *S3DatapackStore) resolveKey(datapackName, filePath string) (string, error) {
	raw := filepath.ToSlash(filePath)
	for _, seg := range strings.Split(raw, "/") {
		if seg == ".." {
			return "", fmt.Errorf("invalid file path: path traversal detected")
		}
	}
	cleaned := path.Clean("/" + raw)
	cleaned = strings.TrimPrefix(cleaned, "/")
	if cleaned == "." || cleaned == "" {
		return "", fmt.Errorf("invalid file path: empty")
	}
	return datapackName + "/" + cleaned, nil
}

func (s *S3DatapackStore) CreateUploadTempFile() (*os.File, error) {
	return os.CreateTemp("", "datapack-upload-*.zip")
}

func (s *S3DatapackStore) ValidateArchive(zipPath string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("failed to open zip archive: %w", err)
	}
	defer func() { _ = r.Close() }()

	for _, f := range r.File {
		name := filepath.Base(f.Name)
		if validParquetFiles[name] {
			return nil
		}
	}
	return fmt.Errorf("archive must contain at least one parquet file from: abnormal_traces.parquet, abnormal_metrics.parquet, abnormal_logs.parquet, normal_traces.parquet, normal_metrics.parquet, normal_logs.parquet")
}

func (s *S3DatapackStore) EnsureDatapackDirAvailable(datapackName string) (string, error) {
	if s.bucket == "" {
		return "", fmt.Errorf("s3 datapack bucket not configured")
	}
	prefix := datapackName + "/"
	res, err := s.client.List(s.ctx(), s.bucket, prefix, blobclient.ListOpts{MaxKeys: 1})
	if err != nil {
		return "", fmt.Errorf("failed to probe datapack prefix %s: %w", prefix, err)
	}
	if len(res.Objects) > 0 || len(res.CommonPrefixes) > 0 {
		return "", fmt.Errorf("%w: directory %s already exists", consts.ErrAlreadyExists, datapackName)
	}
	return datapackName, nil
}

// ExtractArchive uploads each zip entry as an object under
// targetDir/<entry-name>. targetDir here is a key prefix (no leading
// slash). TODO: switch to streaming PutObject when blob.Client gains
// a Put(io.Reader) variant — for now we buffer each entry in memory.
func (s *S3DatapackStore) ExtractArchive(zipPath, targetDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("failed to open zip archive: %w", err)
	}
	defer func() { _ = r.Close() }()

	targetPrefix := strings.TrimSuffix(targetDir, "/")
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		entryName := strings.TrimLeft(filepath.ToSlash(f.Name), "/")
		if strings.Contains(entryName, "..") {
			return fmt.Errorf("illegal file path in archive: %s", f.Name)
		}
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("failed to open file in archive %s: %w", f.Name, err)
		}
		buf, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			return fmt.Errorf("failed to read archive entry %s: %w", f.Name, err)
		}
		key := targetPrefix + "/" + entryName
		ct := guessContentTypeByExt(entryName)
		if _, err := s.client.PutBytes(s.ctx(), s.bucket, buf, blobclient.PresignPutReq{
			Key:           key,
			ContentType:   ct,
			ContentLength: int64(len(buf)),
		}); err != nil {
			return fmt.Errorf("failed to upload archive entry %s: %w", f.Name, err)
		}
	}
	return nil
}

func (s *S3DatapackStore) RemoveAll(p string) error {
	prefix := strings.TrimSuffix(p, "/") + "/"
	objects, _, err := s.listPrefix(prefix, "")
	if err != nil {
		return fmt.Errorf("failed to list %s: %w", prefix, err)
	}
	for _, obj := range objects {
		if err := s.client.Delete(s.ctx(), s.bucket, obj.Key); err != nil {
			return fmt.Errorf("failed to delete %s: %w", obj.Key, err)
		}
	}
	return nil
}

func (s *S3DatapackStore) Remove(p string) error {
	return s.client.Delete(s.ctx(), s.bucket, p)
}

func (s *S3DatapackStore) ExtractGroundtruths(dir string) []model.Groundtruth {
	prefix := strings.TrimSuffix(dir, "/")
	key := prefix + "/injection.json"
	data, _, err := s.client.GetBytes(s.ctx(), s.bucket, key)
	if err != nil {
		if errors.Is(err, consts.ErrNotFound) {
			logrus.Debugf("No injection.json found at %s", key)
		} else {
			logrus.Debugf("Failed to read injection.json at %s: %v", key, err)
		}
		return nil
	}

	var parsed injectionJSONFile
	if err := json.Unmarshal(bytes.TrimSpace(data), &parsed); err != nil {
		logrus.Warnf("Failed to parse injection.json at %s: %v", key, err)
		return nil
	}

	rawGTs := parsed.Groundtruths
	if len(rawGTs) == 0 {
		rawGTs = parsed.GroundTruth
	}
	if len(rawGTs) == 0 {
		return nil
	}

	result := make([]model.Groundtruth, 0, len(rawGTs))
	for _, gt := range rawGTs {
		result = append(result, model.Groundtruth{
			Service:   gt.Service,
			Pod:       gt.Pod,
			Container: gt.Container,
			Metric:    gt.Metric,
			Function:  gt.Function,
			Span:      gt.Span,
		})
	}
	return result
}
