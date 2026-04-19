package user

import (
	"context"

	"aegis/model"
)

type Reader interface {
	GetByID(context.Context, int) (*model.User, error)
	GetByUsername(context.Context, string) (*model.User, error)
}

func AsReader(s *Service) *Service { return s }

var _ Reader = (*Service)(nil)
