package consumer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"aegis/platform/crypto"
	"aegis/platform/jwtkeys"
	"aegis/platform/model"

	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
	"gorm.io/gorm"
)

const (
	chaosClientSAName    = "chaos-client"
	chaosSATokenLifetime = 24 * time.Hour
	chaosSATokenRefresh  = 12 * time.Hour
)

// chaosSATokenRef holds the most recently minted backend→chaos SA token.
// Dispatcher / catalog preflight read this atomically. Nil while boot mint
// hasn't completed (or in test binaries that don't load the module) — both
// callers fall back to the legacy CHAOS_OUTBOUND_BEARER env in that case.
var chaosSATokenRef atomic.Pointer[string]

// ChaosSATokenModule mints a backend→chaos SA token at fx OnStart and
// refreshes it every chaosSATokenRefresh on a background goroutine. The
// token is signed with the local *jwtkeys.Signer (no SSO HTTP roundtrip)
// against the seeded chaos-client ServiceAccount row.
var ChaosSATokenModule = fx.Module("consumer.chaos_sa_token",
	fx.Invoke(registerChaosSATokenLifecycle),
)

func registerChaosSATokenLifecycle(lc fx.Lifecycle, db *gorm.DB, signer *jwtkeys.Signer) {
	logger := logrus.StandardLogger().WithField("component", "chaos_sa_token")
	ctx, cancel := context.WithCancel(context.Background())
	lc.Append(fx.Hook{
		OnStart: func(_ context.Context) error {
			if err := refreshChaosSAToken(ctx, db, signer, logger); err != nil {
				logger.WithError(err).Error("initial backend→chaos SA token mint failed; dispatcher will fall back to CHAOS_OUTBOUND_BEARER")
				// Soft-fail: don't block boot. Dispatcher's env fallback covers
				// us until the SA seed lands.
			}
			go runChaosSATokenRefresh(ctx, db, signer, logger)
			return nil
		},
		OnStop: func(_ context.Context) error {
			cancel()
			return nil
		},
	})
}

func runChaosSATokenRefresh(ctx context.Context, db *gorm.DB, signer *jwtkeys.Signer, logger *logrus.Entry) {
	ticker := time.NewTicker(chaosSATokenRefresh)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := refreshChaosSAToken(ctx, db, signer, logger); err != nil {
				logger.WithError(err).Error("backend→chaos SA token refresh failed; previous token still served until expiry")
			}
		}
	}
}

func refreshChaosSAToken(ctx context.Context, db *gorm.DB, signer *jwtkeys.Signer, logger *logrus.Entry) error {
	tok, exp, err := mintBackendChaosSAToken(ctx, db, signer, chaosSATokenLifetime)
	if err != nil {
		return err
	}
	chaosSATokenRef.Store(&tok)
	logger.WithField("expires_at", exp.Format(time.RFC3339)).Info("backend→chaos SA token minted")
	return nil
}

// mintBackendChaosSAToken looks up the chaos-client ServiceAccount row and
// signs a JWT against the local key. Returns an error when the row is missing
// or revoked — both are operator-correctable conditions, not silent fallbacks.
func mintBackendChaosSAToken(ctx context.Context, db *gorm.DB, signer *jwtkeys.Signer, lifetime time.Duration) (string, time.Time, error) {
	if signer == nil || signer.PrivateKey == nil {
		return "", time.Time{}, errors.New("jwt signer not initialized")
	}
	var sa model.ServiceAccount
	if err := db.WithContext(ctx).Where("name = ?", chaosClientSAName).First(&sa).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", time.Time{}, fmt.Errorf("service account %q not seeded; apply initial-data and reseed", chaosClientSAName)
		}
		return "", time.Time{}, fmt.Errorf("lookup service account %q: %w", chaosClientSAName, err)
	}
	if sa.RevokedAt != nil {
		return "", time.Time{}, fmt.Errorf("service account %q is revoked at %s", chaosClientSAName, sa.RevokedAt.Format(time.RFC3339))
	}
	return crypto.GenerateServiceAccountToken(sa.Name, parseChaosSAScopes(sa.Scopes), lifetime, signer.PrivateKey, signer.Kid)
}

func parseChaosSAScopes(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// currentChaosSAToken returns the minted SA token, or "" if mint hasn't
// completed. Exposed for the dispatcher / catalog preflight token-source
// helpers; tests overwrite chaosSATokenRef directly.
func currentChaosSAToken() string {
	if p := chaosSATokenRef.Load(); p != nil {
		return *p
	}
	return ""
}
