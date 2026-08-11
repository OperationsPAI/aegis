package injection

import (
	"archive/zip"
	"context"
	"io"
	"os"
	"time"

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
	// ParquetReaderPath returns a path/URL usable inside DuckDB's
	// read_parquet(). Filesystem backends return an absolute local path;
	// S3-backed backends return a short-lived presigned HTTPS URL ready
	// for the `httpfs` extension. TTL must outlive the query.
	ParquetReaderPath(ctx context.Context, datapackName, filePath string, ttl time.Duration) (string, error)
	CreateUploadTempFile() (*os.File, error)
	ValidateArchive(zipPath string) error
	EnsureDatapackDirAvailable(datapackName string) (string, error)
	ExtractArchive(zipPath, targetDir string) error
	RemoveAll(path string) error
	Remove(path string) error
	ExtractGroundtruths(dir string) []model.Groundtruth
}
