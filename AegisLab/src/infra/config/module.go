package config

import (
	"aegis/config"
	"aegis/utils"

	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
)

type Params struct {
	Path string
}

var Module = fx.Module("config",
	fx.Invoke(Init),
)

func Init(params Params) {
	config.Init(params.Path)

	// Fail-fast on missing / default JWT secret. We do this after config.Init
	// so that any infra logging from viper is already set up, but before any
	// downstream module (auth, middleware) attempts a JWT operation.
	if err := utils.InitJWTSecret(); err != nil {
		logrus.Fatalf("JWT secret validation failed: %v", err)
	}
	if err := utils.ValidateJWTSecret(); err != nil {
		logrus.Fatalf("JWT secret validation failed: %v", err)
	}
}
