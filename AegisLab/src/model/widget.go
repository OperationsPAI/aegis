package model

import (
	"time"

	"aegis/consts"
)

type Widget struct {
	ID        int               `gorm:"primaryKey;autoIncrement"`
	Name      string            `gorm:"not null;size:128;uniqueIndex"`
	Status    consts.StatusType `gorm:"not null;default:1"`
	CreatedAt time.Time         `gorm:"autoCreateTime"`
	UpdatedAt time.Time         `gorm:"autoUpdateTime"`
}
