package dataset

import (
	"archive/zip"
	"fmt"
	"io/fs"
	"path/filepath"

	blobclient "aegis/clients/blob"
	"aegis/platform/config"
	"aegis/platform/consts"
	"aegis/platform/model"
	"aegis/platform/utils"
)

// FilesystemDatapackFileStore is the local-filesystem implementation of
// DatasetFileStorage. The legacy name `DatapackFileStore` is kept as a
// type alias below so existing struct-literal call sites (notably tests)
// keep compiling without behavioural change.
type FilesystemDatapackFileStore struct {
	basePath string
}

// DatapackFileStore is a backward-compat alias for
// FilesystemDatapackFileStore.
type DatapackFileStore = FilesystemDatapackFileStore

// NewFilesystemDatapackFileStore constructs the filesystem-backed
// DatasetFileStorage implementation.
func NewFilesystemDatapackFileStore() *FilesystemDatapackFileStore {
	return &FilesystemDatapackFileStore{basePath: config.GetString("jfs.dataset_path")}
}

// NewDatapackFileStore returns the DatasetFileStorage implementation
// selected by `jfs.backend` ("filesystem" — default — or "s3"). The
// blob client is injected by fx and is unused on the filesystem path.
func NewDatapackFileStore(client blobclient.Client) DatasetFileStorage {
	switch config.GetString("jfs.backend") {
	case "s3":
		// Logical bucket name resolved by blob.Registry (see
		// NewDatapackStore in the injection package for the rationale).
		logical := config.GetString("jfs.s3.dataset_blob_bucket")
		if logical == "" {
			logical = "dataset"
		}
		return NewS3DatapackFileStore(client, logical)
	default:
		return NewFilesystemDatapackFileStore()
	}
}

var _ DatasetFileStorage = (*FilesystemDatapackFileStore)(nil)

func (s *FilesystemDatapackFileStore) PackageToZip(zipWriter *zip.Writer, datapacks []model.FaultInjection, excludeRules []utils.ExculdeRule) error {
	for i := range datapacks {
		if err := s.packageDatapackToZip(zipWriter, &datapacks[i], excludeRules); err != nil {
			return err
		}
	}
	return nil
}

func (s *FilesystemDatapackFileStore) packageDatapackToZip(zipWriter *zip.Writer, datapack *model.FaultInjection, excludeRules []utils.ExculdeRule) error {
	if datapack.State < consts.DatapackBuildSuccess {
		return fmt.Errorf("datapack %s is not in a downloadable state", datapack.Name)
	}

	workDir := filepath.Join(s.basePath, datapack.Name)
	if !utils.IsAllowedPath(workDir) {
		return fmt.Errorf("invalid path access to %s", workDir)
	}

	err := filepath.WalkDir(workDir, func(path string, dir fs.DirEntry, err error) error {
		if err != nil || dir.IsDir() {
			return err
		}

		relPath, _ := filepath.Rel(workDir, path)
		fullRelPath := filepath.Join(consts.DownloadFilename, filepath.Base(workDir), relPath)
		fileName := filepath.Base(path)

		for _, rule := range excludeRules {
			if utils.MatchFile(fileName, rule) {
				return nil
			}
		}

		fileInfo, err := dir.Info()
		if err != nil {
			return err
		}

		return utils.AddToZip(zipWriter, fileInfo, path, filepath.ToSlash(fullRelPath))
	})
	if err != nil {
		return fmt.Errorf("failed to package datapack %s: %w", datapack.Name, err)
	}

	return nil
}
