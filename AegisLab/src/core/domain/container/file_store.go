package container

import (
	"fmt"
	"mime/multipart"
	"os"
	"path/filepath"
	"time"

	blobclient "aegis/clients/blob"
	"aegis/platform/config"
	"aegis/platform/utils"

	"github.com/sirupsen/logrus"
)

// FilesystemHelmFileStore is the local-filesystem implementation of
// ContainerFileStorage. The legacy name `HelmFileStore` is kept as a type
// alias below so existing struct-literal call sites (notably tests) keep
// compiling without behavioural change.
type FilesystemHelmFileStore struct {
	basePath string
}

// HelmFileStore is a backward-compat alias for FilesystemHelmFileStore.
type HelmFileStore = FilesystemHelmFileStore

// NewFilesystemHelmFileStore constructs the filesystem-backed
// ContainerFileStorage implementation.
func NewFilesystemHelmFileStore() *FilesystemHelmFileStore {
	return &FilesystemHelmFileStore{basePath: config.GetString("jfs.dataset_path")}
}

// NewHelmFileStore returns the ContainerFileStorage implementation
// selected by `jfs.backend` ("filesystem" — default — or "s3"). The
// blob client is injected by fx and is unused on the filesystem path.
func NewHelmFileStore(client blobclient.Client) ContainerFileStorage {
	switch config.GetString("jfs.backend") {
	case "s3":
		return NewS3HelmFileStore(client, config.GetString("jfs.s3.helm_chart_bucket"))
	default:
		return NewFilesystemHelmFileStore()
	}
}

var _ ContainerFileStorage = (*FilesystemHelmFileStore)(nil)

func (s *FilesystemHelmFileStore) SaveChart(containerName string, file *multipart.FileHeader) (string, string, error) {
	if s.basePath == "" {
		return "", "", fmt.Errorf("jfs.dataset_path is not configured")
	}

	targetDir := filepath.Join(s.basePath, "helm-charts")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return "", "", fmt.Errorf("failed to create directory: %w", err)
	}

	targetPath := filepath.Join(
		targetDir,
		fmt.Sprintf("%s_chart_%d%s", containerName, time.Now().Unix(), filepath.Ext(file.Filename)),
	)
	if err := utils.CopyFileFromFileHeader(file, targetPath); err != nil {
		return "", "", fmt.Errorf("failed to save chart file: %w", err)
	}

	checksum, err := utils.CalculateFileSHA256(targetPath)
	if err != nil {
		logrus.WithField("file_path", targetPath).Warnf("failed to calculate checksum: %v", err)
		checksum = ""
	}

	logrus.WithFields(logrus.Fields{
		"file_path": targetPath,
		"checksum":  checksum,
	}).Info("Helm chart package uploaded successfully")

	return targetPath, checksum, nil
}

func (s *FilesystemHelmFileStore) SaveValueFile(containerName string, srcFileHeader *multipart.FileHeader, srcFilePath string) (string, error) {
	if s.basePath == "" {
		return "", fmt.Errorf("jfs.dataset_path is not configured")
	}

	targetDir := filepath.Join(s.basePath, "helm-values")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create directory: %w", err)
	}

	timestamp := time.Now().Unix()
	var targetPath string

	switch {
	case srcFileHeader != nil:
		if srcFileHeader.Size == 0 {
			return "", fmt.Errorf("refusing to save empty helm values file for %s (uploaded source is 0 bytes)", containerName)
		}
		targetPath = filepath.Join(targetDir, fmt.Sprintf("%s_values_%d%s", containerName, timestamp, filepath.Ext(srcFileHeader.Filename)))
		if err := utils.CopyFileFromFileHeader(srcFileHeader, targetPath); err != nil {
			return "", fmt.Errorf("failed to save file: %w", err)
		}
	case srcFilePath != "":
		info, statErr := os.Stat(srcFilePath)
		if statErr != nil {
			return "", fmt.Errorf("failed to stat source values file %s: %w", srcFilePath, statErr)
		}
		if info.Size() == 0 {
			return "", fmt.Errorf("refusing to save empty helm values file for %s (source path %s is 0 bytes)", containerName, srcFilePath)
		}
		targetPath = filepath.Join(targetDir, fmt.Sprintf("%s_values_%d%s", containerName, timestamp, filepath.Ext(srcFilePath)))
		if err := utils.CopyFile(srcFilePath, targetPath); err != nil {
			return "", fmt.Errorf("failed to save file: %w", err)
		}
	default:
		return "", fmt.Errorf("either source file header or source file path is required")
	}

	if info, err := os.Stat(targetPath); err != nil {
		return "", fmt.Errorf("failed to stat saved helm values file %s: %w", targetPath, err)
	} else if info.Size() == 0 {
		_ = os.Remove(targetPath)
		return "", fmt.Errorf("saved helm values file %s ended up 0 bytes; removed to prevent downstream use of an empty or invalid values file", targetPath)
	}

	logrus.WithField("file_path", targetPath).Info("Helm values file uploaded successfully")
	return targetPath, nil
}
