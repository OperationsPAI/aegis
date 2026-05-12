package project

import (
	"context"
	"fmt"

	"aegis/platform/dto"
)

type projectStatisticsSource interface {
	ListProjectStatistics(context.Context, []int) (map[int]*dto.ProjectStatistics, error)
}

type projectStatisticsAdapter struct {
	repository *Repository
}

func newProjectStatisticsSource(repository *Repository) projectStatisticsSource {
	return projectStatisticsAdapter{
		repository: repository,
	}
}

func (a projectStatisticsAdapter) ListProjectStatistics(_ context.Context, projectIDs []int) (map[int]*dto.ProjectStatistics, error) {
	if a.repository == nil {
		return nil, fmt.Errorf("project statistics source is not configured")
	}
	return a.repository.ListProjectStatistics(projectIDs)
}
