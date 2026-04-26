package pedestal

import (
	"context"
	"fmt"
	"strings"

	"aegis/config"
	"aegis/consts"
	"aegis/model"
	"aegis/service/initialization"
)

// Service is the pedestal module's application-layer facade. It keeps the
// handler aligned with the standard HandlerService pattern used by other
// Phase 4 modules while preserving the existing HTTP contract.
type Service struct {
	repo   *Repository
	runner Runner
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo, runner: RealRunner{}}
}

func (s *Service) GetHelmConfig(ctx context.Context, versionID int) (*model.HelmConfig, error) {
	return s.repo.GetHelmConfigByContainerVersionID(ctx, versionID)
}

func (s *Service) UpsertHelmConfig(ctx context.Context, versionID int, fields *model.HelmConfig) (*model.HelmConfig, error) {
	return s.repo.UpsertHelmConfig(ctx, versionID, fields)
}

func (s *Service) VerifyHelmConfig(ctx context.Context, versionID int) (*Result, error) {
	cfg, err := s.repo.GetHelmConfigByContainerVersionID(ctx, versionID)
	if err != nil {
		return nil, err
	}

	result := Run(s.runner, Config{
		ChartName: cfg.ChartName,
		Version:   cfg.Version,
		RepoURL:   cfg.RepoURL,
		RepoName:  cfg.RepoName,
		ValueFile: cfg.ValueFile,
	}, VerifyValueFile)
	return &result, nil
}

// ReseedHelmConfig propagates a data.yaml chart-version / values bump to the
// running DB for one container_version. This is the targeted hot-reseed
// counterpart to `aegisctl system reseed` and is keyed by container_version_id
// rather than system name. See issue #201 for motivation.
//
// The service layer here keeps two responsibilities:
//   - Resolve the seed file path against the same `initialization.data_path`
//     config key used by the first-boot loader so reseed and seed always read
//     the same file.
//   - Default Apply=false to dry-run for safety, matching the chaossystem
//     reseed contract.
func (s *Service) ReseedHelmConfig(ctx context.Context, in ReseedHelmConfigInput) (*initialization.ReseedReport, error) {
	if in.ContainerVersionID <= 0 {
		return nil, fmt.Errorf("container_version_id is required and must be > 0: %w", consts.ErrBadRequest)
	}
	basePath := strings.TrimSpace(in.DataPath)
	if basePath == "" {
		basePath = config.GetString("initialization.data_path")
	}
	seedPath, err := initialization.ResolveSeedPath(basePath, strings.TrimSpace(in.Env))
	if err != nil {
		return nil, fmt.Errorf("resolve seed path: %w: %w", err, consts.ErrBadRequest)
	}
	return initialization.ReseedHelmConfigForVersion(ctx, s.repo.db, initialization.ReseedHelmConfigForVersionRequest{
		DataPath:           seedPath,
		ContainerVersionID: in.ContainerVersionID,
		DryRun:             !in.Apply,
		Prune:              in.Prune,
	})
}
