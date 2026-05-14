package config

import (
	"aegis/platform/config"
	"aegis/platform/crypto"

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

	// Fail-fast on missing API-key KEK secret (AEGIS_JWT_SECRET; no longer used
	// for JWT signing — that moved to RS256 — but still seeds the api-key
	// envelope KEK in utils/access_key_crypto.go).
	if err := crypto.InitJWTSecret(); err != nil {
		logrus.Fatalf("API-key KEK secret validation failed: %v", err)
	}
	if err := crypto.ValidateJWTSecret(); err != nil {
		logrus.Fatalf("API-key KEK secret validation failed: %v", err)
	}
}
