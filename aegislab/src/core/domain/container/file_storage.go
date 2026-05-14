package container

import "mime/multipart"

// ContainerFileStorage is the port for persisting container artefacts
// (Helm charts and Helm values files). The default implementation
// (FilesystemHelmFileStore) writes under the configured local
// `jfs.dataset_path` root; future implementations (e.g. S3 / rustfs)
// will plug in behind the same interface.
type ContainerFileStorage interface {
	SaveChart(containerName string, file *multipart.FileHeader) (string, string, error)
	SaveValueFile(containerName string, srcFileHeader *multipart.FileHeader, srcFilePath string) (string, error)
}
