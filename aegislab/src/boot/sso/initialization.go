package sso

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"aegis/boot/seed"
	"aegis/platform/config"
	"aegis/platform/consts"
	"aegis/platform/model"
	ssomod "aegis/crud/iam/sso"

	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func registerSSOInitialization(lc fx.Lifecycle, db *gorm.DB, clients *ssomod.Service) {
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			if err := seedDefaultAdmin(db); err != nil {
				return err
			}
			if err := seedDefaultOIDCClient(ctx, db, clients); err != nil {
				return err
			}
			if err := seedConsoleOIDCClient(ctx, db); err != nil {
				return err
			}
			if err := seedCLIOIDCClient(ctx, db); err != nil {
				return err
			}
			// SSO must run this in addition to aegis-api: user/auth/rbac
			// PermissionRegistrar entries only populate the in-process
			// consts.SystemRolePermissions map of THIS binary's fx graph,
			// so without this call super_admin never gets `user:update:all`
			// et al. The reconcile is idempotent — running from both
			// processes is safe and converges.
			if err := initialization.ReconcileSystemPermissions(db); err != nil {
				return fmt.Errorf("reconcile system permissions: %w", err)
			}
			return nil
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

	password := strings.TrimSpace(os.Getenv("AEGIS_SSO_BOOTSTRAP_PASSWORD"))
	fromEnv := password != ""
	if fromEnv {
		if len(password) < 12 {
			return fmt.Errorf("AEGIS_SSO_BOOTSTRAP_PASSWORD must be at least 12 characters")
		}
	} else {
		generated, err := randomHex(16)
		if err != nil {
			return err
		}
		password = generated
	}

	admin := &model.User{
		Username: "admin",
		Email:    "admin@aegis.local",
		Password: password,
		FullName: "Aegis Admin",
		IsActive: true,
		Status:   consts.CommonEnabled,
	}
	if err := db.Omit("active_username").Create(admin).Error; err != nil {
		return err
	}

	if fromEnv {
		logrus.Info("Seeded default SSO admin user (password from AEGIS_SSO_BOOTSTRAP_PASSWORD)")
		return nil
	}

	logrus.Warnf("Seeded default SSO admin user. ONE-TIME PASSWORD: %s", password)
	dumpPath := config.GetString("sso.seed_secret_dump_path")
	if dumpPath == "" {
		dumpPath = "/var/lib/sso/.first-boot-secret"
	}
	adminDumpPath := dumpPath + ".admin"
	if _, err := os.Stat(adminDumpPath); os.IsNotExist(err) {
		if mkErr := os.MkdirAll(filepath.Dir(adminDumpPath), 0o700); mkErr != nil {
			logrus.WithError(mkErr).Warn("could not create dir for admin password dump")
			return nil
		}
		if writeErr := os.WriteFile(adminDumpPath, []byte(password+"\n"), 0o600); writeErr != nil {
			logrus.WithError(writeErr).Warn("could not write admin password dump file")
		}
	}
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
		dumpPath = "/var/lib/sso/.first-boot-secret"
	}
	if _, err := os.Stat(dumpPath); os.IsNotExist(err) {
		if mkErr := os.MkdirAll(filepath.Dir(dumpPath), 0o700); mkErr != nil {
			logrus.WithError(mkErr).Warn("could not create dir for seed-secret dump")
		} else if writeErr := os.WriteFile(dumpPath, []byte(secret+"\n"), 0o600); writeErr != nil {
			logrus.WithError(writeErr).Warn("could not write seed-secret dump file")
		}
	}

	// Mirror the bootstrap secret into a K8s Secret so downstream services
	// (aegis-api etc.) can pick it up via envFrom without needing access to
	// the SSO PVC. Best-effort — silently no-op when running outside of K8s.
	upsertBootstrapK8sSecret(ctx, secret)
	return nil
}

func upsertBootstrapK8sSecret(ctx context.Context, plaintext string) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		logrus.WithError(err).Debug("sso bootstrap: not running in cluster, skipping K8s Secret upsert")
		return
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		logrus.WithError(err).Warn("sso bootstrap: build kubernetes client failed")
		return
	}
	nsBytes, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err != nil {
		logrus.WithError(err).Warn("sso bootstrap: read pod namespace failed")
		return
	}
	namespace := strings.TrimSpace(string(nsBytes))

	secretName := config.GetString("sso.bootstrap_secret_name")
	if secretName == "" {
		secretName = "sso-bootstrap"
	}
	desired := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: namespace},
		Type:       corev1.SecretTypeOpaque,
		StringData: map[string]string{"aegis-backend-secret": plaintext},
	}
	if _, err := cs.CoreV1().Secrets(namespace).Create(ctx, desired, metav1.CreateOptions{}); err == nil {
		logrus.WithField("secret", secretName).Info("sso bootstrap: created K8s Secret with backend client_secret")
		return
	} else if !apierrors.IsAlreadyExists(err) {
		logrus.WithError(err).Warn("sso bootstrap: create K8s Secret failed")
		return
	}
	if _, err := cs.CoreV1().Secrets(namespace).Update(ctx, desired, metav1.UpdateOptions{}); err != nil {
		logrus.WithError(err).Warn("sso bootstrap: update K8s Secret failed")
		return
	}
	logrus.WithField("secret", secretName).Info("sso bootstrap: updated K8s Secret with backend client_secret")
}

// seedConsoleOIDCClient ensures the public client used by the aegis-ui
// console exists and matches the current config on every boot. Redirect
// URIs come from `[sso] console_redirect_uris` (or `SSO_CONSOLE_REDIRECT_URIS`
// env override, comma-separated); grants/scopes/IsConfidential are
// reconciled too so an operator can rotate the whitelist without manual
// DB surgery. No client_secret — the browser proves possession via PKCE.
func seedConsoleOIDCClient(_ context.Context, db *gorm.DB) error {
	raw := config.GetString("sso.console_redirect_uris")
	if raw == "" {
		raw = "http://localhost:3100/auth/callback"
	}
	var uris []string
	for _, u := range strings.Split(raw, ",") {
		if t := strings.TrimSpace(u); t != "" {
			uris = append(uris, t)
		}
	}

	desired := &model.OIDCClient{
		ClientID:         "aegis-console",
		ClientSecretHash: "",
		Name:             "Aegis Console",
		Service:          "aegis-console",
		RedirectURIs:     uris,
		Grants:           []string{"authorization_code", "refresh_token"},
		Scopes:           []string{"openid", "profile", "email"},
		IsConfidential:   false,
		Status:           consts.CommonEnabled,
	}

	var existing model.OIDCClient
	err := db.Where("client_id = ?", "aegis-console").First(&existing).Error
	switch {
	case err == gorm.ErrRecordNotFound:
		if err := db.Create(desired).Error; err != nil {
			return err
		}
	case err != nil:
		return err
	default:
		// `Updates(map)` would emit `('a','b')` for the JSON-serialized
		// slice columns and MySQL trips on "Operand should contain 1
		// column". A struct-form update + Select on the slice columns
		// preserves the registered gorm json serializer.
		desired.ID = existing.ID
		desired.CreatedAt = existing.CreatedAt
		if err := db.Model(&existing).
			Select("Name", "Service", "RedirectURIs", "Grants", "Scopes",
				"ClientSecretHash", "Status").
			Updates(desired).Error; err != nil {
			return err
		}
	}
	// GORM's INSERT skips bool(false) zero-values and lets the column
	// fall back to `default:true`. Force it false in a follow-up update
	// so reconciliation can't accidentally promote it to confidential.
	if err := db.Model(&model.OIDCClient{}).
		Where("client_id = ?", "aegis-console").
		Update("is_confidential", false).Error; err != nil {
		return err
	}
	logrus.WithFields(logrus.Fields{"client_id": "aegis-console", "redirect_uris": uris}).
		Info("Reconciled public OIDC client for aegis-ui console")
	return nil
}

// seedCLIOIDCClient ensures a public `aegis-cli` OIDC client exists so
// the aegisctl binary can do `grant_type=password` (and refresh_token)
// against /token without shipping a client_secret. Public client
// (IsConfidential=false) → VerifySecret short-circuits the bcrypt check
// in crud/iam/sso/service.go.
func seedCLIOIDCClient(_ context.Context, db *gorm.DB) error {
	desired := &model.OIDCClient{
		ClientID:         "aegis-cli",
		ClientSecretHash: "",
		Name:             "Aegis CLI",
		Service:          "aegis-cli",
		RedirectURIs:     []string{},
		Grants:           []string{"password", "refresh_token"},
		Scopes:           []string{"openid", "profile", "email"},
		IsConfidential:   false,
		Status:           consts.CommonEnabled,
	}

	var existing model.OIDCClient
	err := db.Where("client_id = ?", "aegis-cli").First(&existing).Error
	switch {
	case err == gorm.ErrRecordNotFound:
		if err := db.Create(desired).Error; err != nil {
			return err
		}
	case err != nil:
		return err
	default:
		desired.ID = existing.ID
		desired.CreatedAt = existing.CreatedAt
		if err := db.Model(&existing).
			Select("Name", "Service", "RedirectURIs", "Grants", "Scopes",
				"ClientSecretHash", "Status").
			Updates(desired).Error; err != nil {
			return err
		}
	}
	// Same gotcha as aegis-console: GORM skips bool(false) zero-values.
	if err := db.Model(&model.OIDCClient{}).
		Where("client_id = ?", "aegis-cli").
		Update("is_confidential", false).Error; err != nil {
		return err
	}
	logrus.WithField("client_id", "aegis-cli").Info("Reconciled public OIDC client for aegisctl")
	return nil
}

func randomHex(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
