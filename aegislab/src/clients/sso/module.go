package ssoclient

import (
	"context"
	"os"
	"strings"
	"time"

	"aegis/platform/config"
	"aegis/platform/framework"
	"aegis/platform/middleware"

	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
)

// firstBootSecretFile is the conventional path used by helm/sso to drop
// the bootstrap client_secret on first install.
const firstBootSecretFile = "/var/lib/sso/.first-boot-secret"

func newConfig() Config {
	secret := config.GetString("sso.client_secret")
	if secret == "" {
		if data, err := os.ReadFile(firstBootSecretFile); err == nil {
			secret = strings.TrimSpace(string(data))
		}
	}
	// Optional hot-reload path. When set (typically to a mounted K8s Secret
	// projected by SSO at bootstrap), refreshServiceToken re-reads this file
	// on each /token call so a rotated secret takes effect without a pod
	// restart.
	secretFile := config.GetString("sso.client_secret_file")
	return Config{
		BaseURL:          config.GetString("sso.base_url"),
		ClientID:         config.GetString("sso.client_id"),
		ClientSecret:     secret,
		ClientSecretFile: secretFile,
		JWKSURL:          config.GetString("sso.jwks_url"),
	}
}

// asTokenVerifier exposes *Client through the middleware.TokenVerifier
// interface so Task #8 can wire the producer middleware swap by depending on
// the interface (without ssoclient importing middleware reverse-dependencies).
func asTokenVerifier(c *Client) middleware.TokenVerifier { return c }

// permissionCheckerAdapter bridges *Client's struct-based Check API to the
// narrow positional-arg interface middleware consumes.
type permissionCheckerAdapter struct{ c *Client }

func (a permissionCheckerAdapter) Check(ctx context.Context, userID int, permission, scopeType, scopeID string) (bool, error) {
	return a.c.Check(ctx, CheckParams{
		UserID:     userID,
		Permission: permission,
		ScopeType:  scopeType,
		ScopeID:    scopeID,
	})
}

func asPermissionChecker(c *Client) middleware.PermissionChecker {
	return permissionCheckerAdapter{c: c}
}

type bootstrapDeps struct {
	fx.In

	Client      *Client
	Lifecycle   fx.Lifecycle
	Registrars  []framework.PermissionRegistrar `group:"permissions"`
}

func registerBootstrap(d bootstrapDeps) {
	d.Lifecycle.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			tokCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			if _, err := d.Client.refreshServiceToken(tokCtx); err != nil {
				logrus.WithError(err).Warn("ssoclient: initial service-token fetch failed; will retry on demand")
			} else {
				d.Client.startTokenRefresher(context.Background())
			}

			specs := flattenSpecs(d.Registrars)
			if len(specs) == 0 {
				return nil
			}
			regCtx, regCancel := context.WithTimeout(ctx, 15*time.Second)
			defer regCancel()
			if err := d.Client.RegisterPermissions(regCtx, specs); err != nil {
				// Per task: don't crash aegislab over a transient SSO outage.
				logrus.WithError(err).WithField("count", len(specs)).Warn("ssoclient: permission registration failed at startup")
			}
			return nil
		},
	})
}

func flattenSpecs(rs []framework.PermissionRegistrar) []PermissionSpec {
	seen := make(map[string]struct{})
	out := make([]PermissionSpec, 0)
	for _, r := range rs {
		for _, rule := range r.Rules {
			name := rule.String()
			if _, dup := seen[name]; dup {
				continue
			}
			seen[name] = struct{}{}
			out = append(out, PermissionSpec{
				Name:        name,
				DisplayName: name,
				ScopeType:   string(rule.Scope),
			})
		}
	}
	return out
}

// Module wires the SSO HTTP client into the consuming process.
//
// Provides:
//   - *Client (concrete) for callers that need grant/check by struct
//   - middleware.TokenVerifier (interface) so Task #8 can swap producer auth
//
// Depends on `*jwtkeys.Verifier` being provided by the caller. Pick one:
//
//   - sso / monolith / runtime-worker bring `app.WithSigner()` —
//     the signer's pubkey doubles as the verifier (same key both ways).
//   - verify-only binaries (aegis-notify, aegis-blob, aegis-gateway,
//     aegis-configcenter) bring `app.WithRemoteVerifier()` — verifier
//     refreshes from `sso.jwks_url`.
//
// Previously this module pulled `jwtkeys.VerifierModule` in transitively,
// which collided with `app.WithSigner()`'s self-verifier in any process
// that did both (the monolith — fx blew up at boot with "*jwtkeys.Verifier
// already provided"). Picking the verifier at the binary level avoids
// that gotcha and matches the comments on the helpers in app/app.go.
var Module = fx.Module("ssoclient",
	fx.Provide(newConfig),
	fx.Provide(NewClient),
	fx.Provide(asTokenVerifier),
	fx.Provide(asPermissionChecker),
	fx.Invoke(registerBootstrap),
)
