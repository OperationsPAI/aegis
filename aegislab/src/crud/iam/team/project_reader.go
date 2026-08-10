package team

import (
	"context"
	"fmt"

	"aegis/platform/consts"
	"aegis/platform/dto"
	"aegis/platform/model"
	project "aegis/crud/iam/project"
)

type projectReader interface {
	CountProjects(context.Context, int) (int, error)
	ListProjects(context.Context, *TeamProjectListReq, int) (*dto.ListResp[TeamProjectItem], error)
}

type projectReaderAdapter struct {
	repo  *Repository
	stats project.Reader
}

func newProjectReader(repo *Repository, stats project.Reader) projectReader {
	return projectReaderAdapter{
		repo:  repo,
		stats: stats,
	}
}

func (r projectReaderAdapter) CountProjects(_ context.Context, teamID int) (int, error) {
	var projectCount int64
	if err := r.repo.db.Model(&model.Project{}).
		Where("team_id = ? AND status != ?", teamID, consts.CommonDeleted).
		Count(&projectCount).Error; err != nil {
		return 0, fmt.Errorf("failed to get team project count: %w", err)
	}
	return int(projectCount), nil
}

func (r projectReaderAdapter) ListProjects(ctx context.Context, req *TeamProjectListReq, teamID int) (*dto.ListResp[TeamProjectItem], error) {
	if req == nil {
		req = &TeamProjectListReq{}
	}
	limit, offset := req.ToGormParams()
	projects, total, err := r.repo.listTeamProjectViews(teamID, limit, offset, req.IsPublic, req.Status)
	if err != nil {
		return nil, err
	}

	projectIDs := make([]int, 0, len(projects))
	for _, projectItem := range projects {
		projectIDs = append(projectIDs, projectItem.ID)
	}

	statsMap, err := r.stats.ListProjectStatistics(ctx, projectIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to list project statistics: %w", err)
	}

	items := make([]TeamProjectItem, 0, len(projects))
	for i := range projects {
		items = append(items, *project.NewProjectResp(&projects[i], statsMap[projects[i].ID]))
	}

	return &dto.ListResp[TeamProjectItem]{
		Items:      items,
		Pagination: req.ConvertToPaginationInfo(total),
	}, nil
}

var _ projectReader = (*projectReaderAdapter)(nil)
