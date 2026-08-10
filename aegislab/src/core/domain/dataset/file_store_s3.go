package dataset

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"strings"

	blobclient "aegis/clients/blob"
	"aegis/platform/consts"
	"aegis/platform/model"
	"aegis/platform/utils"
)

// S3DatapackFileStore is the S3/rustfs-backed implementation of
// DatasetFileStorage. Source datapack bytes are read from object keys
// under `<datapackName>/...` in the configured bucket, mirroring the
// filesystem layout used by S3DatapackStore.
type S3DatapackFileStore struct {
	client blobclient.Client
	bucket string
}

// NewS3DatapackFileStore constructs the S3-backed DatasetFileStorage.
// Bucket comes from `jfs.s3.dataset_bucket`.
func NewS3DatapackFileStore(client blobclient.Client, bucket string) *S3DatapackFileStore {
	return &S3DatapackFileStore{client: client, bucket: bucket}
}

var _ DatasetFileStorage = (*S3DatapackFileStore)(nil)

func (s *S3DatapackFileStore) ctx() context.Context { return context.Background() }

func (s *S3DatapackFileStore) PackageToZip(zipWriter *zip.Writer, datapacks []model.FaultInjection, excludeRules []utils.ExculdeRule) error {
	for i := range datapacks {
		if err := s.packageDatapackToZip(zipWriter, &datapacks[i], excludeRules); err != nil {
			return err
		}
	}
	return nil
}

func (s *S3DatapackFileStore) packageDatapackToZip(zipWriter *zip.Writer, datapack *model.FaultInjection, excludeRules []utils.ExculdeRule) error {
	if datapack.State < consts.DatapackBuildSuccess {
		return fmt.Errorf("datapack %s is not in a downloadable state", datapack.Name)
	}

	prefix := datapack.Name + "/"

	token := ""
	for {
		res, err := s.client.List(s.ctx(), s.bucket, prefix, blobclient.ListOpts{ContinuationToken: token})
		if err != nil {
			return fmt.Errorf("failed to list datapack %s: %w", datapack.Name, err)
		}
		for _, obj := range res.Objects {
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

			zipPath := path.Join(consts.DownloadFilename, datapack.Name, relKey)
			w, err := zipWriter.Create(filepath.ToSlash(zipPath))
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
		if !res.IsTruncated || res.NextContinuationToken == "" {
			break
		}
		token = res.NextContinuationToken
	}
	return nil
}
