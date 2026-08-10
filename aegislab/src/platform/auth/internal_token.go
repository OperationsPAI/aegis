package auth

import (
	"crypto/rsa"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"aegis/platform/consts"
	"aegis/platform/crypto"

	"github.com/golang-jwt/jwt/v5"
)

const (
	InternalTokenIssuer = "aegis-internal"
	InternalTokenTTL    = 30 * time.Second

	HeaderInternalToken = "X-Aegis-Internal-Token"

	jtiPrefixInternal = "int"
)

var ErrNotInternalToken = errors.New("not an internal token")

// InternalClaims carries the authenticated principal's identity in a
// gateway-to-upstream internal assertion JWT. Fields mirror crypto.Claims
// so the upstream can populate the same Gin context keys.
type InternalClaims struct {
	UserID       int      `json:"uid,omitempty"`
	Username     string   `json:"usr,omitempty"`
	Email        string   `json:"email,omitempty"`
	IsActive     bool     `json:"act,omitempty"`
	IsAdmin      bool     `json:"adm,omitempty"`
	Roles        []string `json:"roles,omitempty"`
	AuthType     string   `json:"aty,omitempty"`
	APIKeyID     int      `json:"kid,omitempty"`
	APIKeyScopes []string `json:"aks,omitempty"`
	TaskID       string   `json:"tid,omitempty"`
	jwt.RegisteredClaims
}

// MintInternalToken creates a short-lived JWT carrying the caller's
// identity for gateway-to-upstream propagation. The gateway calls this
// after verifying the external JWT/HMAC; upstreams verify it with
// ParseInternalToken.
func MintInternalToken(c *InternalClaims, priv *rsa.PrivateKey, kid string) (string, error) {
	if priv == nil {
		return "", errors.New("rsa private key is nil")
	}
	now := time.Now()
	c.RegisteredClaims = jwt.RegisteredClaims{
		ID:        fmt.Sprintf("%s_%d_%d", jtiPrefixInternal, c.UserID, now.Unix()),
		Issuer:    InternalTokenIssuer,
		Subject:   strconv.Itoa(c.UserID),
		IssuedAt:  jwt.NewNumericDate(now),
		NotBefore: jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(InternalTokenTTL)),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, c)
	if kid != "" {
		token.Header["kid"] = kid
	}
	return token.SignedString(priv)
}

// MintInternalTokenFromUnifiedClaims converts verified UnifiedClaims into an
// internal assertion.
func MintInternalTokenFromUnifiedClaims(src *crypto.UnifiedClaims, priv *rsa.PrivateKey, kid string) (string, error) {
	ic := &InternalClaims{
		UserID:       src.UserID,
		Username:     src.Username,
		Email:        src.Email,
		IsActive:     src.IsActive,
		IsAdmin:      src.IsAdmin,
		Roles:        append([]string(nil), src.Roles...),
		AuthType:     src.AuthType,
		APIKeyID:     src.APIKeyID,
		APIKeyScopes: append([]string(nil), src.APIKeyScopes...),
		TaskID:       src.TaskID,
	}
	return MintInternalToken(ic, priv, kid)
}

// ParseInternalToken verifies an internal assertion JWT and returns the
// claims. It rejects tokens whose issuer is not InternalTokenIssuer.
func ParseInternalToken(tokenString string, resolve crypto.PublicKeyResolver) (*InternalClaims, error) {
	if tokenString == "" {
		return nil, errors.New("internal token is required")
	}
	if resolve == nil {
		return nil, errors.New("public key resolver is nil")
	}
	kf := func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		kid, _ := token.Header["kid"].(string)
		return resolve(kid)
	}
	token, err := jwt.ParseWithClaims(tokenString, &InternalClaims{}, kf)
	if err != nil {
		if errors.Is(err, jwt.ErrTokenMalformed) {
			return nil, fmt.Errorf("%w: %v", ErrNotInternalToken, err)
		}
		return nil, fmt.Errorf("failed to parse internal token: %v", err)
	}
	claims, ok := token.Claims.(*InternalClaims)
	if !ok || !token.Valid {
		return nil, ErrNotInternalToken
	}
	if claims.Issuer != InternalTokenIssuer {
		return nil, ErrNotInternalToken
	}
	return claims, nil
}

// SetGinContext populates the canonical Gin context keys from an internal
// assertion's claims, matching the shape TrustedHeaderAuth and JWTAuth set.
func (c *InternalClaims) SetGinContext(gc interface{ Set(any, any) }) {
	gc.Set(consts.CtxKeyUserID, c.UserID)
	gc.Set(consts.CtxKeyUsername, c.Username)
	gc.Set(consts.CtxKeyEmail, c.Email)
	gc.Set(consts.CtxKeyIsActive, c.IsActive)
	gc.Set(consts.CtxKeyIsAdmin, c.IsAdmin)
	gc.Set(consts.CtxKeyUserRoles, append([]string(nil), c.Roles...))
	gc.Set(consts.CtxKeyAuthType, c.AuthType)
	gc.Set(consts.CtxKeyAPIKeyID, c.APIKeyID)
	gc.Set(consts.CtxKeyAPIKeyScopes, append([]string(nil), c.APIKeyScopes...))

	isService := c.UserID == 0 && len(c.Roles) > 0 && strings.HasPrefix(c.Roles[0], consts.ClaimSubjectServicePrefix)
	if isService {
		gc.Set(consts.CtxKeyIsServiceToken, true)
		gc.Set(consts.CtxKeyTokenType, "service")
		if c.TaskID != "" {
			gc.Set(consts.CtxKeyTaskID, c.TaskID)
		}
	} else {
		gc.Set(consts.CtxKeyTokenType, "user")
	}
}
