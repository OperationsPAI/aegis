package label

import (
	"aegis/platform/consts"
	"aegis/platform/dto"
	"aegis/platform/model"

	"gorm.io/gorm"
)

// Writer is the cross-module label mutation surface. It keeps label
// creation/upsert logic owned by the label module even when callers are
// inside another module's transaction.
type Writer interface {
	CreateLabelCore(*gorm.DB, *model.Label) (*model.Label, error)
	CreateOrUpdateLabelsFromItems(*gorm.DB, []dto.LabelItem, consts.LabelCategory) ([]model.Label, error)
}

func AsWriter(repo *Repository) Writer {
	return repo
}

var _ Writer = (*Repository)(nil)
