package injection

import (
	"archive/zip"
	"io"
	"os"

	"aegis/platform/model"
	"aegis/platform/utils"
)

// DatapackStorage is the port for datapack file persistence. The default
// implementation (FilesystemDatapackStore) stores datapacks on a local /
// network filesystem rooted at the configured `jfs.dataset_path`; future
// implementations (e.g. S3 / rustfs) will plug in behind the same interface.
type DatapackStorage interface {
	RootDir(datapackName string) string
	Package(zipWriter *zip.Writer, datapackName string, excludeRules []utils.ExculdeRule) error
	BuildFileTree(datapackName, baseURL string, datapackID int) (*DatapackFilesResp, error)
	OpenFile(datapackName, filePath string) (string, string, int64, io.ReadSeekCloser, error)
	ResolveFilePath(datapackName, filePath string) (string, error)
	CreateUploadTempFile() (*os.File, error)
	ValidateArchive(zipPath string) error
	EnsureDatapackDirAvailable(datapackName string) (string, error)
	ExtractArchive(zipPath, targetDir string) error
	RemoveAll(path string) error
	Remove(path string) error
	ExtractGroundtruths(dir string) []model.Groundtruth
}
