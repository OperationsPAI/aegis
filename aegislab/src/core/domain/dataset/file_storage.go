package dataset

import (
	"archive/zip"

	"aegis/platform/model"
	"aegis/platform/utils"
)

// DatasetFileStorage is the port for packaging dataset (multi-datapack)
// content out to clients. The default implementation
// (FilesystemDatapackFileStore) reads source files from the configured
// local `jfs.dataset_path` root; future implementations (e.g. S3 /
// rustfs) will plug in behind the same interface.
type DatasetFileStorage interface {
	PackageToZip(zipWriter *zip.Writer, datapacks []model.FaultInjection, excludeRules []utils.ExculdeRule) error
}
