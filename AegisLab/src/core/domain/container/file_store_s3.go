package container

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path"
	"path/filepath"
	"time"

	blobclient "aegis/clients/blob"

	"github.com/sirupsen/logrus"
)

// S3HelmFileStore is the S3/rustfs-backed implementation of
// ContainerFileStorage. Object keys live under `helm-charts/...` and
// `helm-values/...` inside the configured bucket — same logical layout
// as the filesystem impl, just without the `<basePath>` prefix.
type S3HelmFileStore struct {
	client blobclient.Client
	bucket string
}

// NewS3HelmFileStore constructs the S3-backed ContainerFileStorage.
// Bucket comes from `jfs.s3.helm_chart_bucket`.
func NewS3HelmFileStore(client blobclient.Client, bucket string) *S3HelmFileStore {
	return &S3HelmFileStore{client: client, bucket: bucket}
}

var _ ContainerFileStorage = (*S3HelmFileStore)(nil)

func (s *S3HelmFileStore) ctx() context.Context { return context.Background() }

func (s *S3HelmFileStore) SaveChart(containerName string, file *multipart.FileHeader) (string, string, error) {
	if s.bucket == "" {
		return "", "", fmt.Errorf("s3 helm bucket not configured")
	}
	key := path.Join("helm-charts", fmt.Sprintf("%s_chart_%d%s", containerName, time.Now().Unix(), filepath.Ext(file.Filename)))

	src, err := file.Open()
	if err != nil {
		return "", "", fmt.Errorf("failed to open uploaded chart file: %w", err)
	}
	defer func() { _ = src.Close() }()

	buf, err := io.ReadAll(src)
	if err != nil {
		return "", "", fmt.Errorf("failed to read uploaded chart file: %w", err)
	}
	if len(buf) == 0 {
		return "", "", fmt.Errorf("refusing to save empty helm chart for %s", containerName)
	}

	sum := sha256.Sum256(buf)
	checksum := hex.EncodeToString(sum[:])

	if _, err := s.client.PutBytes(s.ctx(), s.bucket, buf, blobclient.PresignPutReq{
		Key:           key,
		ContentType:   "application/gzip",
		ContentLength: int64(len(buf)),
	}); err != nil {
		return "", "", fmt.Errorf("failed to upload chart file: %w", err)
	}

	logrus.WithFields(logrus.Fields{
		"bucket":   s.bucket,
		"key":      key,
		"checksum": checksum,
	}).Info("Helm chart package uploaded to S3 successfully")
	return key, checksum, nil
}

func (s *S3HelmFileStore) SaveValueFile(containerName string, srcFileHeader *multipart.FileHeader, srcFilePath string) (string, error) {
	if s.bucket == "" {
		return "", fmt.Errorf("s3 helm bucket not configured")
	}

	timestamp := time.Now().Unix()
	var (
		buf []byte
		ext string
		err error
	)
	switch {
	case srcFileHeader != nil:
		if srcFileHeader.Size == 0 {
			return "", fmt.Errorf("refusing to save empty helm values file for %s (uploaded source is 0 bytes)", containerName)
		}
		src, openErr := srcFileHeader.Open()
		if openErr != nil {
			return "", fmt.Errorf("failed to open uploaded values file: %w", openErr)
		}
		buf, err = io.ReadAll(src)
		_ = src.Close()
		if err != nil {
			return "", fmt.Errorf("failed to read uploaded values file: %w", err)
		}
		ext = filepath.Ext(srcFileHeader.Filename)
	case srcFilePath != "":
		info, statErr := os.Stat(srcFilePath)
		if statErr != nil {
			return "", fmt.Errorf("failed to stat source values file %s: %w", srcFilePath, statErr)
		}
		if info.Size() == 0 {
			return "", fmt.Errorf("refusing to save empty helm values file for %s (source path %s is 0 bytes)", containerName, srcFilePath)
		}
		buf, err = os.ReadFile(srcFilePath)
		if err != nil {
			return "", fmt.Errorf("failed to read source values file %s: %w", srcFilePath, err)
		}
		ext = filepath.Ext(srcFilePath)
	default:
		return "", fmt.Errorf("either source file header or source file path is required")
	}

	if len(buf) == 0 {
		return "", fmt.Errorf("refusing to save empty helm values file for %s", containerName)
	}

	key := path.Join("helm-values", fmt.Sprintf("%s_values_%d%s", containerName, timestamp, ext))
	if _, err := s.client.PutBytes(s.ctx(), s.bucket, buf, blobclient.PresignPutReq{
		Key:           key,
		ContentType:   "application/x-yaml",
		ContentLength: int64(len(buf)),
	}); err != nil {
		return "", fmt.Errorf("failed to upload helm values file: %w", err)
	}

	logrus.WithFields(logrus.Fields{
		"bucket": s.bucket,
		"key":    key,
	}).Info("Helm values file uploaded to S3 successfully")
	return key, nil
}
