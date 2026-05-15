package middleware

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"

	"aegis/platform/consts"
	"aegis/platform/crypto"
	"aegis/platform/dto"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

// trusted-header name constants — mirror clients/gateway.Header* without
// creating an import cycle (middleware ← gateway ← sso ← middleware).
const (
	trustedHeaderUserID       = "X-Aegis-User-Id"
	trustedHeaderUserEmail    = "X-Aegis-User-Email"
	trustedHeaderRoles        = "X-Aegis-Roles"
	trustedHeaderTokenAud     = "X-Aegis-Token-Aud"
	trustedHeaderTokenJti     = "X-Aegis-Token-Jti"
	trustedHeaderSignature    = "X-Aegis-Signature"
	trustedHeaderUsername     = "X-Aegis-Username"
	trustedHeaderIsActive     = "X-Aegis-Is-Active"
	trustedHeaderIsAdmin      = "X-Aegis-Is-Admin"
	trustedHeaderAuthType     = "X-Aegis-Auth-Type"
	trustedHeaderAPIKeyID     = "X-Aegis-Api-Key-Id"
	trustedHeaderAPIKeyScopes = "X-Aegis-Api-Key-Scopes"
)

// setUserClaims stashes JWT claims onto the Gin context using the canonical
// keys from consts/context_keys.go. Both JWTAuth and OptionalJWTAuth funnel
// successful validations through this helper so the key list is in one place.
func setUserClaims(c *gin.Context, claims *crypto.Claims) {
	c.Set(consts.CtxKeyUserID, claims.UserID)
	c.Set(consts.CtxKeyUsername, claims.Username)
	c.Set(consts.CtxKeyEmail, claims.Email)
	c.Set(consts.CtxKeyIsActive, claims.IsActive)
	c.Set(consts.CtxKeyIsAdmin, claims.IsAdmin)
	c.Set(consts.CtxKeyUserRoles, claims.Roles)
	c.Set(consts.CtxKeyAuthType, claims.AuthType)
	c.Set(consts.CtxKeyAPIKeyID, claims.APIKeyID)
	c.Set(consts.CtxKeyAPIKeyScopes, append([]string(nil), claims.APIKeyScopes...))
}

func extractTokenFromHeader(header string) (string, error) {
	return crypto.ExtractTokenFromHeader(header)
}

// JWTAuth is the JWT authentication middleware
// Supports both user tokens and service tokens (for K8s jobs)
func JWTAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Extract token from Authorization header
		authHeader := c.GetHeader("Authorization")
		token, err := extractTokenFromHeader(authHeader)
		if err != nil {
			dto.ErrorResponse(c, http.StatusUnauthorized, "Unauthorized: "+err.Error())
			c.Abort()
			return
		}

		service := serviceFromContext(c)

		// Try to validate as user token first
		claims, err := service.VerifyToken(c.Request.Context(), token)
		if err == nil {
			// Valid user token - store user information in context
			setUserClaims(c, claims)
			c.Set("token_expires_at", claims.ExpiresAt.Time)
			c.Set(consts.CtxKeyTokenType, "user")
			c.Next()
			return
		}

		// Try to validate as service token (for K8s jobs)
		serviceClaims, serviceErr := service.VerifyServiceToken(c.Request.Context(), token)
		if serviceErr == nil {
			// Valid service token - store service information in context
			c.Set(consts.CtxKeyTaskID, serviceClaims.TaskID)
			c.Set("token_expires_at", serviceClaims.ExpiresAt.Time)
			c.Set(consts.CtxKeyTokenType, "service")
			c.Set(consts.CtxKeyIsServiceToken, true)
			c.Next()
			return
		}

		// Both validations failed, return the user token error (more common case)
		dto.ErrorResponse(c, http.StatusUnauthorized, "Unauthorized: "+err.Error())
		c.Abort()
	}
}

// TrustedHeaderAuth validates the X-Aegis-Signature HMAC over the
// gateway-injected trusted-header set, populates the same ctx keys
// JWTAuth used to populate, and rejects unsigned / forged requests
// with 401. The gateway is the sole place that parses JWTs; services
// use this middleware instead of JWTAuth.
//
// The signing key is read lazily from viper key "gateway.trusted_header_key".
// If the key is empty the service refuses to start (caller should assert
// this at boot time via AssertTrustedHeaderKeyConfigured).
func TrustedHeaderAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		key := []byte(strings.TrimSpace(viper.GetString("gateway.trusted_header_key")))
		if len(key) == 0 {
			logrus.Error("middleware: gateway.trusted_header_key is empty; rejecting all requests")
			dto.ErrorResponse(c, http.StatusUnauthorized, "Unauthorized")
			c.Abort()
			return
		}

		userID := c.GetHeader(trustedHeaderUserID)
		email := c.GetHeader(trustedHeaderUserEmail)
		roles := c.GetHeader(trustedHeaderRoles)
		aud := c.GetHeader(trustedHeaderTokenAud)
		jti := c.GetHeader(trustedHeaderTokenJti)
		username := c.GetHeader(trustedHeaderUsername)
		isActiveStr := c.GetHeader(trustedHeaderIsActive)
		isAdminStr := c.GetHeader(trustedHeaderIsAdmin)
		authType := c.GetHeader(trustedHeaderAuthType)
		apiKeyIDStr := c.GetHeader(trustedHeaderAPIKeyID)
		apiKeyScopesStr := c.GetHeader(trustedHeaderAPIKeyScopes)
		sig := c.GetHeader(trustedHeaderSignature)

		if userID == "" && sig == "" {
			dto.ErrorResponse(c, http.StatusUnauthorized, "Unauthorized")
			c.Abort()
			return
		}

		canonical := strings.Join([]string{
			userID, email, roles, aud, jti,
			username, isActiveStr, isAdminStr, authType,
			apiKeyIDStr, apiKeyScopesStr,
		}, "|")
		mac := hmac.New(sha256.New, key)
		_, _ = mac.Write([]byte(canonical))
		expected := hex.EncodeToString(mac.Sum(nil))

		if !hmac.Equal([]byte(sig), []byte(expected)) {
			dto.ErrorResponse(c, http.StatusUnauthorized, "forged trusted-header set")
			c.Abort()
			return
		}

		uid, _ := strconv.Atoi(userID)
		isActive := isActiveStr == "1"
		isAdmin := isAdminStr == "1"
		apiKeyID, _ := strconv.Atoi(apiKeyIDStr)

		var apiKeyScopes []string
		if apiKeyScopesStr != "" {
			apiKeyScopes = strings.Split(apiKeyScopesStr, ",")
		}

		var roleList []string
		if roles != "" {
			roleList = strings.Split(roles, ",")
		}

		isService := uid == 0 && strings.HasPrefix(roles, consts.ClaimSubjectServicePrefix)
		if isService {
			c.Set(consts.CtxKeyIsServiceToken, true)
			c.Set(consts.CtxKeyTokenType, "service")
		} else {
			c.Set(consts.CtxKeyTokenType, "user")
		}

		c.Set(consts.CtxKeyUserID, uid)
		c.Set(consts.CtxKeyUsername, username)
		c.Set(consts.CtxKeyEmail, email)
		c.Set(consts.CtxKeyIsActive, isActive)
		c.Set(consts.CtxKeyIsAdmin, isAdmin)
		c.Set(consts.CtxKeyUserRoles, roleList)
		c.Set(consts.CtxKeyAuthType, authType)
		c.Set(consts.CtxKeyAPIKeyID, apiKeyID)
		c.Set(consts.CtxKeyAPIKeyScopes, apiKeyScopes)

		c.Next()
	}
}

// AssertTrustedHeaderKeyConfigured logs a fatal error and exits if
// gateway.trusted_header_key is empty. Call this at service boot time
// for any binary that uses TrustedHeaderAuth.
func AssertTrustedHeaderKeyConfigured() {
	if strings.TrimSpace(viper.GetString("gateway.trusted_header_key")) == "" {
		logrus.Fatal("gateway.trusted_header_key must be set; service refuses to start without a signing key")
	}
}

// OptionalJWTAuth is an optional JWT authentication middleware
// If token is provided, it validates it and sets user/service info
// If no token is provided, it continues without authentication
// Supports both user tokens and service tokens (for K8s jobs)
func OptionalJWTAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			// No authentication provided, continue
			c.Next()
			return
		}

		token, err := extractTokenFromHeader(authHeader)
		if err != nil {
			// Invalid header format, continue without auth
			c.Next()
			return
		}

		service := serviceFromContext(c)

		// Try to validate as user token first
		claims, err := service.VerifyToken(c.Request.Context(), token)
		if err == nil {
			// Valid user token, set user information
			setUserClaims(c, claims)
			c.Set("token_expires_at", claims.ExpiresAt.Time)
			c.Set(consts.CtxKeyTokenType, "user")
			c.Next()
			return
		}

		// Try to validate as service token (for K8s jobs)
		serviceClaims, serviceErr := service.VerifyServiceToken(c.Request.Context(), token)
		if serviceErr == nil {
			// Valid service token, set service information
			c.Set(consts.CtxKeyTaskID, serviceClaims.TaskID)
			c.Set("token_expires_at", serviceClaims.ExpiresAt.Time)
			c.Set(consts.CtxKeyTokenType, "service")
			c.Set(consts.CtxKeyIsServiceToken, true)
			c.Next()
			return
		}

		// Invalid token, continue without auth
		c.Next()
	}
}

// GetCurrentUserID extracts current user ID from context
func GetCurrentUserID(c *gin.Context) (int, bool) {
	userID, exists := c.Get(consts.CtxKeyUserID)
	if !exists {
		return 0, false
	}

	id, ok := userID.(int)
	return id, ok
}

// GetCurrentUsername extracts current username from context
func GetCurrentUsername(c *gin.Context) (string, bool) {
	username, exists := c.Get(consts.CtxKeyUsername)
	if !exists {
		return "", false
	}

	name, ok := username.(string)
	return name, ok
}

// GetCurrentUserEmail extracts current user email from context
func GetCurrentUserEmail(c *gin.Context) (string, bool) {
	email, exists := c.Get(consts.CtxKeyEmail)
	if !exists {
		return "", false
	}

	userEmail, ok := email.(string)
	return userEmail, ok
}

// IsCurrentUserActive checks if current user is active
func IsCurrentUserActive(c *gin.Context) bool {
	isActive, exists := c.Get(consts.CtxKeyIsActive)
	if !exists {
		return false
	}

	active, ok := isActive.(bool)
	return ok && active
}

// IsServiceToken checks if the current request is authenticated with a service token
func IsServiceToken(c *gin.Context) bool {
	isService, exists := c.Get(consts.CtxKeyIsServiceToken)
	if !exists {
		return false
	}

	service, ok := isService.(bool)
	return ok && service
}

// GetTokenType returns the type of token used for authentication ("user", "service", or "")
func GetTokenType(c *gin.Context) string {
	tokenType, exists := c.Get(consts.CtxKeyTokenType)
	if !exists {
		return ""
	}

	t, ok := tokenType.(string)
	if !ok {
		return ""
	}
	return t
}

// IsCurrentUserAdmin checks if current user has system admin role (from JWT token)
func IsCurrentUserAdmin(c *gin.Context) bool {
	isAdmin, exists := c.Get(consts.CtxKeyIsAdmin)
	if !exists {
		return false
	}

	admin, ok := isAdmin.(bool)
	return ok && admin
}

// GetCurrentUserRoles returns the roles of the current user (from JWT token)
func GetCurrentUserRoles(c *gin.Context) ([]string, bool) {
	roles, exists := c.Get(consts.CtxKeyUserRoles)
	if !exists {
		return nil, false
	}

	userRoles, ok := roles.([]string)
	return userRoles, ok
}

// GetCurrentAPIKeyScopes returns API key scopes when the current bearer token
// was issued via Key ID / Key Secret exchange.
func GetCurrentAPIKeyScopes(c *gin.Context) ([]string, bool) {
	scopes, exists := c.Get(consts.CtxKeyAPIKeyScopes)
	if !exists {
		return nil, false
	}

	apiKeyScopes, ok := scopes.([]string)
	return apiKeyScopes, ok
}

// GetAuthType returns the auth_type claim of the current bearer token when present.
func GetAuthType(c *gin.Context) string {
	authType, exists := c.Get(consts.CtxKeyAuthType)
	if !exists {
		return ""
	}

	value, ok := authType.(string)
	if !ok {
		return ""
	}
	return value
}

// GetServiceTaskID extracts task ID from service token context
func GetServiceTaskID(c *gin.Context) (string, bool) {
	taskID, exists := c.Get(consts.CtxKeyTaskID)
	if !exists {
		return "", false
	}

	id, ok := taskID.(string)
	return id, ok
}

// RequireAuth is a helper that ensures user or service is authenticated
func RequireAuth(c *gin.Context) bool {
	// Check if authenticated with service token
	if IsServiceToken(c) {
		taskID, exists := GetServiceTaskID(c)
		return exists && taskID != ""
	}

	// Check if authenticated with user token
	userID, exists := GetCurrentUserID(c)
	if !exists || userID <= 0 {
		dto.ErrorResponse(c, http.StatusUnauthorized, "Authentication required")
		return false
	}

	if !IsCurrentUserActive(c) {
		dto.ErrorResponse(c, http.StatusForbidden, "User account is inactive")
		return false
	}

	return true
}

// RequireUserAuth is a helper that ensures user (not service) is authenticated
func RequireUserAuth(c *gin.Context) bool {
	if IsServiceToken(c) {
		dto.ErrorResponse(c, http.StatusForbidden, "User authentication required, service token not allowed")
		return false
	}

	return RequireAuth(c)
}

// RequireServiceTokenAuth is a helper that ensures the current request uses a service token.
func RequireServiceTokenAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !RequireAuth(c) {
			c.Abort()
			return
		}
		if !IsServiceToken(c) {
			dto.ErrorResponse(c, http.StatusForbidden, "Service token required")
			c.Abort()
			return
		}
		c.Next()
	}
}

// RequireActiveUser ensures the current user exists and is active
func RequireActiveUser() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !RequireAuth(c) {
			c.Abort()
			return
		}
		c.Next()
	}
}
