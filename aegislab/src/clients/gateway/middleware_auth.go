package gateway

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"aegis/platform/config"
	"aegis/platform/consts"
	"aegis/clients/sso"
	"aegis/platform/crypto"

	"github.com/sirupsen/logrus"
)

// Authenticator pre-authenticates requests at the edge and injects
// trusted headers signed with a shared HMAC key. Upstreams that opt
// into trusted-header short-circuit (Phase C of the RFC) verify the
// signature instead of re-running the JWT pipeline.
type Authenticator struct {
	client *ssoclient.Client
	key    []byte
}

// NewAuthenticator wires the SSO client + the gateway-wide HMAC signing
// key. In production an empty key is fatal: the fallback exists only so
// `go run ./main.go` works without setup. Configure
// `gateway.trusted_header_key` (or env GATEWAY_TRUSTED_HEADER_KEY)
// before deploying.
func NewAuthenticator(client *ssoclient.Client, key string) (*Authenticator, error) {
	k := strings.TrimSpace(key)
	if k == "" {
		if config.IsProduction() {
			return nil, errors.New("gateway.trusted_header_key is required in production")
		}
		logrus.Warnf("gateway: trusted_header_key is empty; using dev fallback (env=%s). Set [gateway].trusted_header_key in production.", config.Env())
		k = "aegis-dev-trusted-header-key-do-not-use-in-prod"
	}
	return &Authenticator{client: client, key: []byte(k)}, nil
}

// Middleware returns an http middleware that enforces the route's
// AuthPolicy. On success it injects trusted headers + signature.
func (a *Authenticator) Middleware(route *Route, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch route.Auth {
		case AuthNone:
			next.ServeHTTP(w, r)
			return
		case AuthJWT, AuthJWTOrService, AuthServiceToken:
			if err := a.enforce(r, route); err != nil {
				logrus.WithError(err).WithField("path", r.URL.Path).Debug("gateway: auth rejected")
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		default:
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		}
	})
}

func (a *Authenticator) enforce(r *http.Request, route *Route) error {
	raw := bearer(r)
	if raw == "" {
		return errMissingToken
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	switch route.Auth {
	case AuthServiceToken:
		sc, err := a.client.VerifyServiceToken(ctx, raw)
		if err != nil {
			return err
		}
		a.injectServiceHeaders(r, sc)
	case AuthJWT:
		uc, err := a.client.VerifyToken(ctx, raw)
		if err != nil {
			return err
		}
		if err := checkAudience(uc, route.Audiences); err != nil {
			return err
		}
		a.injectUserHeaders(r, uc)
	case AuthJWTOrService:
		if uc, err := a.client.VerifyToken(ctx, raw); err == nil {
			if err := checkAudience(uc, route.Audiences); err != nil {
				return err
			}
			a.injectUserHeaders(r, uc)
			return nil
		}
		sc, err := a.client.VerifyServiceToken(ctx, raw)
		if err != nil {
			return err
		}
		a.injectServiceHeaders(r, sc)
	}
	return nil
}

func (a *Authenticator) injectUserHeaders(r *http.Request, c *crypto.Claims) {
	aud := ""
	if len(c.Audience) > 0 {
		aud = c.Audience[0]
	}
	isActive := "0"
	if c.IsActive {
		isActive = "1"
	}
	isAdmin := "0"
	if c.IsAdmin {
		isAdmin = "1"
	}
	headers := map[string]string{
		HeaderUserID:       strconv.Itoa(c.UserID),
		HeaderUserEmail:    c.Email,
		HeaderRoles:        strings.Join(c.Roles, ","),
		HeaderTokenAud:     aud,
		HeaderTokenJti:     c.ID,
		HeaderUsername:     c.Username,
		HeaderIsActive:     isActive,
		HeaderIsAdmin:      isAdmin,
		HeaderAuthType:     c.AuthType,
		HeaderAPIKeyID:     strconv.Itoa(c.APIKeyID),
		HeaderAPIKeyScopes: strings.Join(c.APIKeyScopes, ","),
	}
	a.applyAndSign(r, headers)
}

func (a *Authenticator) injectServiceHeaders(r *http.Request, c *crypto.ServiceClaims) {
	aud := ""
	if len(c.Audience) > 0 {
		aud = c.Audience[0]
	}
	headers := map[string]string{
		HeaderUserID:       "0",
		HeaderUserEmail:    "",
		HeaderRoles:        consts.ClaimSubjectServicePrefix + c.Service,
		HeaderTokenAud:     aud,
		HeaderTokenJti:     c.ID,
		HeaderUsername:     "service",
		HeaderIsActive:     "1",
		HeaderIsAdmin:      "0",
		HeaderAuthType:     consts.AuthTypeService,
		HeaderAPIKeyID:     "0",
		HeaderAPIKeyScopes: "",
	}
	a.applyAndSign(r, headers)
}

// applyAndSign writes the canonical header set and an HMAC-SHA256 over the
// v2 canonical string so an upstream can detect a caller forging headers
// from outside the gateway. Canonical order (v2):
//
//	<user_id>|<email>|<roles>|<aud>|<jti>|<username>|<is_active>|<is_admin>|<auth_type>|<api_key_id>|<api_key_scopes>
func (a *Authenticator) applyAndSign(r *http.Request, h map[string]string) {
	canonical := strings.Join([]string{
		h[HeaderUserID], h[HeaderUserEmail], h[HeaderRoles], h[HeaderTokenAud], h[HeaderTokenJti],
		h[HeaderUsername], h[HeaderIsActive], h[HeaderIsAdmin], h[HeaderAuthType],
		h[HeaderAPIKeyID], h[HeaderAPIKeyScopes],
	}, "|")
	mac := hmac.New(sha256.New, a.key)
	_, _ = mac.Write([]byte(canonical))
	sig := hex.EncodeToString(mac.Sum(nil))
	for k, v := range h {
		r.Header.Set(k, v)
	}
	r.Header.Set(HeaderSignature, sig)
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ""
	}
	if !strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return ""
	}
	return strings.TrimSpace(h[len(consts.AuthSchemeBearer):])
}

func checkAudience(c *crypto.Claims, want []string) error {
	if len(want) == 0 {
		return nil
	}
	have := c.Audience
	for _, w := range want {
		for _, h := range have {
			if h == w {
				return nil
			}
		}
	}
	return errAudienceMismatch
}

type authError string

func (e authError) Error() string { return string(e) }

const (
	errMissingToken     authError = "missing bearer token"
	errAudienceMismatch authError = "audience mismatch"
)
