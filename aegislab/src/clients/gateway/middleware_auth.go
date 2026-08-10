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

	"aegis/platform/auth"
	"aegis/platform/config"
	"aegis/platform/consts"
	"aegis/clients/sso"
	"aegis/platform/crypto"
	"aegis/platform/jwtkeys"

	"github.com/sirupsen/logrus"
)

// Authenticator pre-authenticates requests at the edge and injects
// trusted headers signed with a shared HMAC key. Upstreams that opt
// into trusted-header short-circuit (Phase C of the RFC) verify the
// signature instead of re-running the JWT pipeline.
type Authenticator struct {
	client *ssoclient.Client
	key    []byte
	signer *jwtkeys.Signer
}

func NewAuthenticator(client *ssoclient.Client, key string, signer *jwtkeys.Signer) (*Authenticator, error) {
	k := strings.TrimSpace(key)
	if k == "" && signer == nil {
		if config.IsProduction() {
			return nil, errors.New("gateway: either trusted_header_key or signer must be configured in production")
		}
		logrus.Warnf("gateway: trusted_header_key is empty; using dev fallback (env=%s).", config.Env())
		k = "aegis-dev-trusted-header-key-do-not-use-in-prod"
	}
	if signer != nil {
		logrus.Info("gateway: signer available; will mint internal assertion tokens")
	}
	return &Authenticator{client: client, key: []byte(k), signer: signer}, nil
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

	claims, err := a.client.VerifyToken(ctx, raw)
	if err != nil {
		return err
	}

	switch route.Auth {
	case AuthServiceToken:
		if claims.Typ != "task" && claims.Typ != "service_account" {
			return errMissingToken
		}
	case AuthJWT:
		if err := checkAudience(claims, route.Audiences); err != nil {
			return err
		}
	case AuthJWTOrService:
		if claims.Typ == "human" {
			if err := checkAudience(claims, route.Audiences); err != nil {
				return err
			}
		}
	}

	a.injectHeaders(r, claims)
	return nil
}

func (a *Authenticator) injectHeaders(r *http.Request, c *crypto.UnifiedClaims) {
	if a.signer != nil {
		it, err := auth.MintInternalTokenFromUnifiedClaims(c, a.signer.PrivateKey, a.signer.Kid)
		if err == nil {
			r.Header.Set(auth.HeaderInternalToken, it)
			return
		}
		logrus.WithError(err).Warn("gateway: failed to mint internal token, falling back to HMAC")
	}

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
		HeaderTaskID:       c.TaskID,
	}

	if c.Typ == "task" || c.Typ == "service_account" {
		headers[HeaderUserID] = "0"
		headers[HeaderRoles] = consts.ClaimSubjectServicePrefix + c.Service
		headers[HeaderUsername] = "service"
		headers[HeaderIsActive] = "1"
		headers[HeaderIsAdmin] = "0"
		headers[HeaderAuthType] = c.AuthType
		if c.AuthType == "" {
			headers[HeaderAuthType] = consts.AuthTypeService
		}
	}

	a.applyAndSign(r, headers)
}

// applyAndSign writes the canonical header set and an HMAC-SHA256 over the
// v2 canonical string so an upstream can detect a caller forging headers
// from outside the gateway. Canonical order (v2):
//
//	<user_id>|<email>|<roles>|<aud>|<jti>|<username>|<is_active>|<is_admin>|<auth_type>|<api_key_id>|<api_key_scopes>|<task_id>
func (a *Authenticator) applyAndSign(r *http.Request, h map[string]string) {
	canonical := strings.Join([]string{
		h[HeaderUserID], h[HeaderUserEmail], h[HeaderRoles], h[HeaderTokenAud], h[HeaderTokenJti],
		h[HeaderUsername], h[HeaderIsActive], h[HeaderIsAdmin], h[HeaderAuthType],
		h[HeaderAPIKeyID], h[HeaderAPIKeyScopes], h[HeaderTaskID],
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

func checkAudience(c *crypto.UnifiedClaims, want []string) error {
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
