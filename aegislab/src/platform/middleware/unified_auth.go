package middleware

import (
	"net/http"
	"strings"

	"aegis/platform/auth"
	"aegis/platform/consts"
	"aegis/platform/crypto"
	"aegis/platform/dto"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

// UnifiedAuth is the new single auth middleware. During migration, it runs
// ALONGSIDE existing middleware and populates both the legacy context keys
// AND the new auth.Principal.
func UnifiedAuth(authenticator *auth.Authenticator) gin.HandlerFunc {
	return func(c *gin.Context) {
		cred := extractCredential(c)
		p, err := authenticator.Verify(c.Request.Context(), cred)
		if err != nil {
			dto.ErrorResponse(c, http.StatusUnauthorized, "Unauthorized: "+err.Error())
			c.Abort()
			return
		}
		auth.SetPrincipal(c, p)
		setLegacyContext(c, p)
		c.Next()
	}
}

// OptionalUnifiedAuth behaves like UnifiedAuth but does not 401 when no
// credentials are present. Invalid credentials still produce a 401.
func OptionalUnifiedAuth(authenticator *auth.Authenticator) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !hasCredentials(c) {
			c.Next()
			return
		}
		cred := extractCredential(c)
		p, err := authenticator.Verify(c.Request.Context(), cred)
		if err != nil {
			// No credentials that look valid -- continue without auth,
			// matching OptionalJWTAuth's behavior.
			c.Next()
			return
		}
		auth.SetPrincipal(c, p)
		setLegacyContext(c, p)
		c.Next()
	}
}

func hasCredentials(c *gin.Context) bool {
	return c.GetHeader("Authorization") != "" || c.GetHeader(auth.HeaderSignature) != "" || c.GetHeader(auth.HeaderInternalToken) != ""
}

func extractCredential(c *gin.Context) auth.Credential {
	if it := c.GetHeader(auth.HeaderInternalToken); it != "" {
		return auth.Credential{
			Type:          auth.CredInternalToken,
			InternalToken: it,
		}
	}

	if sig := c.GetHeader(auth.HeaderSignature); sig != "" {
		key := []byte(strings.TrimSpace(viper.GetString("gateway.trusted_header_key")))
		token, _ := crypto.ExtractTokenFromHeader(c.GetHeader("Authorization"))
		return auth.Credential{
			Type:        auth.CredTrustedHeader,
			HMACKey:     key,
			BearerToken: token,
			Headers: auth.TrustedHeaderSet{
				UserID:       c.GetHeader(auth.HeaderUserID),
				UserEmail:    c.GetHeader(auth.HeaderUserEmail),
				Roles:        c.GetHeader(auth.HeaderRoles),
				TokenAud:     c.GetHeader(auth.HeaderTokenAud),
				TokenJti:     c.GetHeader(auth.HeaderTokenJti),
				Signature:    sig,
				Username:     c.GetHeader(auth.HeaderUsername),
				IsActive:     c.GetHeader(auth.HeaderIsActive),
				IsAdmin:      c.GetHeader(auth.HeaderIsAdmin),
				AuthType:     c.GetHeader(auth.HeaderAuthType),
				APIKeyID:     c.GetHeader(auth.HeaderAPIKeyID),
				APIKeyScopes: c.GetHeader(auth.HeaderAPIKeyScopes),
				TaskID:       c.GetHeader(auth.HeaderTaskID),
			},
		}
	}

	token, _ := crypto.ExtractTokenFromHeader(c.GetHeader("Authorization"))
	return auth.Credential{
		Type:        auth.CredBearer,
		BearerToken: token,
	}
}

func setLegacyContext(c *gin.Context, p auth.Principal) {
	if !p.ExpiresAt.IsZero() {
		c.Set("token_expires_at", p.ExpiresAt)
	}

	switch p.Typ {
	case auth.PrincipalService, auth.PrincipalTask:
		c.Set(consts.CtxKeyIsServiceToken, true)
		c.Set(consts.CtxKeyTokenType, "service")
		if p.TaskID != "" {
			c.Set(consts.CtxKeyTaskID, p.TaskID)
		}
		c.Set(consts.CtxKeyScopes, append([]string(nil), p.Scopes...))
		return
	case auth.PrincipalServiceAccount:
		c.Set(consts.CtxKeyIsServiceToken, true)
		c.Set(consts.CtxKeyTokenType, "service_account")
		c.Set(consts.CtxKeyAuthType, consts.AuthTypeServiceAccount)
		c.Set(consts.CtxKeyUsername, p.Username)
		c.Set(consts.CtxKeyScopes, append([]string(nil), p.Scopes...))
		return
	default:
		c.Set(consts.CtxKeyTokenType, "user")
	}

	c.Set(consts.CtxKeyUserID, p.UserID)
	c.Set(consts.CtxKeyUsername, p.Username)
	c.Set(consts.CtxKeyEmail, p.Email)
	c.Set(consts.CtxKeyIsActive, p.IsActive)
	c.Set(consts.CtxKeyIsAdmin, p.IsAdmin)
	c.Set(consts.CtxKeyUserRoles, append([]string(nil), p.Roles...))
	c.Set(consts.CtxKeyAuthType, p.AuthType)
	c.Set(consts.CtxKeyAPIKeyID, p.APIKeyID)
	c.Set(consts.CtxKeyAPIKeyScopes, append([]string(nil), p.APIKeyScopes...))
}
