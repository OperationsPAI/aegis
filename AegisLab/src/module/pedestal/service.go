package pedestal

import (
	"context"

	"aegis/model"
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
