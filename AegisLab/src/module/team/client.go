package team

import (
	"context"
	"errors"

	"aegis/consts"
	"aegis/dto"
	"aegis/model"

	"gorm.io/gorm"
)

type Reader interface {
	GetTeam(context.Context, int) (*model.Team, error)
	ListProjects(context.Context, *TeamProjectListReq, int) (*dto.ListResp[TeamProjectItem], error)
}

func AsReader(service *Service) *Service {
	return service
}

func (s *Service) GetTeam(_ context.Context, teamID int) (*model.Team, error) {
	team, err := s.repo.loadTeam(teamID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, consts.ErrNotFound
		}
		return nil, err
	}
	return team, nil
}

func (s *Service) ListProjects(ctx context.Context, req *TeamProjectListReq, teamID int) (*dto.ListResp[TeamProjectItem], error) {
	return s.ListTeamProjects(ctx, req, teamID)
}
