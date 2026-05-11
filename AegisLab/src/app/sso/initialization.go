package sso

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"

	"aegis/config"
	"aegis/consts"
	"aegis/model"
	ssomod "aegis/module/sso"

	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

func registerSSOInitialization(lc fx.Lifecycle, db *gorm.DB, clients *ssomod.Service) {
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			if err := seedDefaultAdmin(db); err != nil {
				return err
			}
			return seedDefaultOIDCClient(ctx, db, clients)
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
		Password: "admin123",
		FullName: "Aegis Admin",
		IsActive: true,
		Status:   consts.CommonEnabled,
	}
	if err := db.Omit("active_username").Create(admin).Error; err != nil {
		return err
	}
	logrus.Info("Seeded default SSO admin user")
	return nil
}

// seedDefaultOIDCClient creates the canonical `aegis-backend` client on
// first boot. The plaintext secret is logged once and (when configured)
// written to `[sso] seed_secret_dump_path` so operators can recover it
// without DB access. On subsequent boots the seed is a no-op.
func seedDefaultOIDCClient(ctx context.Context, db *gorm.DB, _ *ssomod.Service) error {
	var existing model.OIDCClient
	err := db.Where("client_id = ?", "aegis-backend").First(&existing).Error
	if err == nil {
		return nil
	}
	if err != gorm.ErrRecordNotFound {
		return err
	}

	secret, err := randomHex(32)
	if err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	redirectURI := config.GetString("sso.backend_redirect_uri")
	if redirectURI == "" {
		redirectURI = "http://aegis-backend:8082/auth/oidc/callback"
	}

	client := &model.OIDCClient{
		ClientID:         "aegis-backend",
		ClientSecretHash: string(hash),
		Name:             "Aegis Backend",
		Service:          "aegis-backend",
		RedirectURIs:     []string{redirectURI},
		Grants:           []string{"authorization_code", "refresh_token", "client_credentials", "password"},
		Scopes:           []string{"openid", "profile", "email"},
		IsConfidential:   true,
		Status:           consts.CommonEnabled,
	}
	if err := db.Create(client).Error; err != nil {
		return err
	}

	logrus.WithFields(logrus.Fields{"client_id": "aegis-backend"}).
		Warnf("Seeded default OIDC client. ONE-TIME SECRET: %s", secret)

	dumpPath := config.GetString("sso.seed_secret_dump_path")
	if dumpPath == "" {
		dumpPath = "/var/lib/aegis-sso/.first-boot-secret"
	}
	if _, err := os.Stat(dumpPath); os.IsNotExist(err) {
		if mkErr := os.MkdirAll(filepath.Dir(dumpPath), 0o700); mkErr != nil {
			logrus.WithError(mkErr).Warn("could not create dir for seed-secret dump")
			return nil
		}
		if writeErr := os.WriteFile(dumpPath, []byte(secret+"\n"), 0o600); writeErr != nil {
			logrus.WithError(writeErr).Warn("could not write seed-secret dump file")
		}
	}
	return nil
}

func randomHex(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
