package team

import (
	"context"
	"fmt"

	"aegis/consts"
	"aegis/dto"
	"aegis/model"
	project "aegis/module/project"
)

type projectReader interface {
	CountProjects(context.Context, int) (int, error)
	ListProjects(context.Context, *TeamProjectListReq, int) (*dto.ListResp[TeamProjectItem], error)
}

type projectReaderAdapter struct {
	repo *Repository
}

func newProjectReader(repo *Repository) projectReader {
	return projectReaderAdapter{
		repo: repo,
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

func (r projectReaderAdapter) ListProjects(_ context.Context, req *TeamProjectListReq, teamID int) (*dto.ListResp[TeamProjectItem], error) {
	if req == nil {
		req = &TeamProjectListReq{}
	}
	limit, offset := req.ToGormParams()
	projects, statsMap, total, err := r.repo.listTeamProjectViews(teamID, limit, offset, req.IsPublic, req.Status)
	if err != nil {
		return nil, err
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
