package sso

import (
	"context"

	"aegis/consts"
	"aegis/model"

	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
	"gorm.io/gorm"
)

func registerSSOInitialization(lc fx.Lifecycle, db *gorm.DB) {
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			return seedDefaultAdmin(db)
		},
	})
}

func seedDefaultAdmin(db *gorm.DB) error {
	var count int64
	if err := db.Model(&model.User{}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	admin := &model.User{
		Username: "admin",
		Email:    "admin@aegis.local",
		Password: "admin",
		FullName: "Aegis Admin",
		IsActive: true,
		Status:   consts.CommonEnabled,
	}
	if err := db.Create(admin).Error; err != nil {
		return err
	}
	logrus.Info("Seeded default SSO admin user")
	return nil
}
