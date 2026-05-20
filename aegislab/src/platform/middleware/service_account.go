package middleware

import (
	"errors"
	"net/http"

	"aegis/platform/consts"
	"aegis/platform/crypto"
	"aegis/platform/dto"
	"aegis/platform/model"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// RequireServiceAccount authenticates the request as a non-revoked service
// account whose name appears in allowedNames (or any SA when allowedNames is
// empty). It is intended to sit at the head of an auth chain: on a missing
// or non-SA bearer token it short-circuits to c.Next() so downstream
// middleware (TrustedHeaderAuth / JWTAuth) can run.
//
// DB lookup per request is intentional — revocation enforcement, closes the
// issue/revoke race per C3 review. We cannot trust the token's lifetime
// alone because RevokedAt can flip after the token was minted.
func RequireServiceAccount(db *gorm.DB, resolve crypto.PublicKeyResolver, allowedNames ...string) gin.HandlerFunc {
	allowed := make(map[string]struct{}, len(allowedNames))
	for _, n := range allowedNames {
		allowed[n] = struct{}{}
	}

	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.Next()
			return
		}
		token, err := crypto.ExtractTokenFromHeader(authHeader)
		if err != nil {
			c.Next()
			return
		}

		claims, err := crypto.ParseServiceAccountToken(token, resolve)
		if err != nil {
			// Distinguish "not an SA token" (wrong issuer → ErrInvalidToken)
			// from "an SA token that failed signature/expiry checks". The
			// former should fall through to other auth middleware; the
			// latter must be rejected so a tampered/expired SA token isn't
			// silently accepted by a downstream user-JWT path.
			if errors.Is(err, crypto.ErrInvalidToken) {
				c.Next()
				return
			}
			dto.ErrorResponse(c, http.StatusUnauthorized, "Unauthorized: "+err.Error())
			c.Abort()
			return
		}

		name := claims.Subject
		var sa model.ServiceAccount
		if err := db.WithContext(c.Request.Context()).
			Where("name = ?", name).First(&sa).Error; err != nil {
			dto.ErrorResponse(c, http.StatusUnauthorized, "Unauthorized: unknown service account")
			c.Abort()
			return
		}
		if sa.RevokedAt != nil {
			dto.ErrorResponse(c, http.StatusUnauthorized, "Unauthorized: service account revoked")
			c.Abort()
			return
		}

		if len(allowed) > 0 {
			if _, ok := allowed[name]; !ok {
				dto.ErrorResponse(c, http.StatusForbidden, "service account not permitted on this route")
				c.Abort()
				return
			}
		}

		c.Set(consts.CtxKeyIsServiceToken, true)
		c.Set(consts.CtxKeyTokenType, "service_account")
		c.Set(consts.CtxKeyAuthType, consts.AuthTypeServiceAccount)
		c.Set(consts.CtxKeyUsername, name)
		c.Set(consts.CtxKeyScopes, append([]string(nil), claims.Scopes...))
		c.Next()
	}
}
