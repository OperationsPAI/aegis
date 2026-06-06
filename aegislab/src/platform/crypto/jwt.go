package crypto

import (
	"crypto/rsa"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"aegis/platform/consts"

	"github.com/golang-jwt/jwt/v5"
)

const (
	// JWTSecretEnvVar names the symmetric secret used to derive the API-key
	// envelope KEK (see access_key_crypto.go). It is NOT used for JWT signing
	// anymore -- JWTs are RS256-signed via an SSO-owned RSA keypair.
	JWTSecretEnvVar = "AEGIS_JWT_SECRET"

	TokenExpiration        = 24 * time.Hour
	RefreshTokenExpiration = 7 * 24 * time.Hour
	ServiceTokenExpiration = 24 * time.Hour

	// JWTIssuerUnified is the single issuer for all tokens minted by the
	// unified token pipeline. Legacy per-type issuers are no longer emitted.
	JWTIssuerUnified = "aegis"
)

var JWTSecret string

func InitJWTSecret() error {
	secret := os.Getenv(JWTSecretEnvVar)
	if secret == "" {
		return fmt.Errorf("%s environment variable is not set", JWTSecretEnvVar)
	}
	JWTSecret = secret
	return nil
}

func ValidateJWTSecret() error {
	if JWTSecret == "" {
		return fmt.Errorf("API-key KEK secret is not initialized; set %s and call InitJWTSecret at startup", JWTSecretEnvVar)
	}
	return nil
}

// ErrInvalidToken is returned (wrapped) by Parse* when the input is "not a
// token of this kind" -- either it doesn't structure-parse as a JWT at all, or
// it parses but fails our policy checks (wrong issuer, etc.). Callers can use
// errors.Is to fall through to alternative auth schemes. Signature / expiry
// failures bubble up as raw errors from jwt.ParseWithClaims and are NOT
// wrapped with ErrInvalidToken -- those must reject the request.
var ErrInvalidToken = errors.New("invalid token")

// PublicKeyResolver maps a JWT header kid to the RSA public key that should
// verify the signature. Returning an error rejects the token.
type PublicKeyResolver func(kid string) (*rsa.PublicKey, error)

// UnifiedClaims is the single JWT claim set for all token types (human, task,
// service_account). The Typ field discriminates; fields irrelevant to a given
// type are zero-valued and omitted from JSON.
type UnifiedClaims struct {
	Typ      string   `json:"typ"`                    // "human", "task", "service_account"
	UserID   int      `json:"user_id,omitempty"`      // human tokens
	Username string   `json:"username,omitempty"`      // human tokens
	Email    string   `json:"email,omitempty"`         // human tokens
	IsActive bool     `json:"is_active,omitempty"`     // human tokens
	IsAdmin  bool     `json:"is_admin,omitempty"`      // human tokens
	Roles    []string `json:"roles,omitempty"`         // human tokens
	AuthType string   `json:"auth_type,omitempty"`     // "user", "api_key", "service_account"
	Idp      string   `json:"idp,omitempty"`           // identity provider ("local", OIDC provider name)
	TaskID   string   `json:"task_id,omitempty"`       // task tokens
	Service  string   `json:"service,omitempty"`       // service_account tokens
	Scopes   []string `json:"scopes,omitempty"`        // service_account / api_key tokens

	APIKeyID     int      `json:"api_key_id,omitempty"`     // api_key tokens
	APIKeyScopes []string `json:"api_key_scopes,omitempty"` // api_key tokens

	jwt.RegisteredClaims
}

// UnifiedTokenParams collects the inputs for GenerateUnifiedToken. Callers
// populate only the fields relevant to the token type.
type UnifiedTokenParams struct {
	Typ      string        // "human", "task", "service_account"
	UserID   int           // human
	Username string        // human
	Email    string        // human
	IsActive bool          // human
	IsAdmin  bool          // human
	Roles    []string      // human
	AuthType string        // "user", "api_key", "service_account"
	Idp      string        // identity provider
	TaskID   string        // task
	Service  string        // service_account
	Scopes   []string      // service_account / api_key
	Lifetime time.Duration // token validity

	APIKeyID     int      // api_key
	APIKeyScopes []string // api_key

	Audience []string // JWT aud claim
}

func signRS256(claims jwt.Claims, priv *rsa.PrivateKey, kid string) (string, error) {
	if priv == nil {
		return "", errors.New("rsa private key is nil")
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	if kid != "" {
		token.Header["kid"] = kid
	}
	return token.SignedString(priv)
}

// AudienceForHuman returns the JWT audiences a human token carries. Everyone
// gets "portal"; admins additionally get "admin" so the same console can reach
// admin-scoped routes (the gateway matches a route's required audience against
// any of the token's audiences).
func AudienceForHuman(isAdmin bool) []string {
	if isAdmin {
		return []string{"portal", "admin"}
	}
	return []string{"portal"}
}

func GenerateUnifiedToken(p UnifiedTokenParams, priv *rsa.PrivateKey, kid string) (string, time.Time, error) {
	now := time.Now()
	lifetime := p.Lifetime
	if lifetime <= 0 {
		lifetime = TokenExpiration
	}
	exp := now.Add(lifetime)

	var sub string
	var jtiPrefix string
	switch p.Typ {
	case "human":
		sub = strconv.Itoa(p.UserID)
		jtiPrefix = consts.JWTJTIPrefixUser
	case "task":
		sub = p.TaskID
		jtiPrefix = consts.JWTJTIPrefixService
	case "service_account":
		if p.Service == "" {
			return "", time.Time{}, errors.New("service account name is required")
		}
		sub = p.Service
		jtiPrefix = consts.JWTJTIPrefixServiceAccount
	default:
		return "", time.Time{}, fmt.Errorf("unknown token type: %q", p.Typ)
	}

	jti := fmt.Sprintf("%s_%s_%d", jtiPrefix, sub, now.Unix())

	var aud jwt.ClaimStrings
	if len(p.Audience) > 0 {
		aud = jwt.ClaimStrings(p.Audience)
	}

	claims := &UnifiedClaims{
		Typ:          p.Typ,
		UserID:       p.UserID,
		Username:     p.Username,
		Email:        p.Email,
		IsActive:     p.IsActive,
		IsAdmin:      p.IsAdmin,
		Roles:        p.Roles,
		AuthType:     p.AuthType,
		Idp:          p.Idp,
		TaskID:       p.TaskID,
		Service:      p.Service,
		Scopes:       append([]string(nil), p.Scopes...),
		APIKeyID:     p.APIKeyID,
		APIKeyScopes: append([]string(nil), p.APIKeyScopes...),
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        jti,
			ExpiresAt: jwt.NewNumericDate(exp),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			Issuer:    JWTIssuerUnified,
			Subject:   sub,
			Audience:  aud,
		},
	}

	tokenString, err := signRS256(claims, priv, kid)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to generate token: %v", err)
	}
	return tokenString, exp, nil
}

func keyFunc(resolve PublicKeyResolver) jwt.Keyfunc {
	return func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		kid, _ := token.Header["kid"].(string)
		return resolve(kid)
	}
}

// ParseUnifiedToken validates and returns unified claims. It accepts tokens
// with the unified issuer ("aegis") as well as all legacy issuers so that
// tokens minted before the migration still work during rollout.
func ParseUnifiedToken(tokenString string, resolve PublicKeyResolver) (*UnifiedClaims, error) {
	if tokenString == "" {
		return nil, fmt.Errorf("%w: empty token", ErrInvalidToken)
	}
	if resolve == nil {
		return nil, errors.New("public key resolver is nil")
	}

	token, err := jwt.ParseWithClaims(tokenString, &UnifiedClaims{}, keyFunc(resolve))
	if err != nil {
		if errors.Is(err, jwt.ErrTokenMalformed) {
			return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
		}
		return nil, fmt.Errorf("failed to parse token: %v", err)
	}

	claims, ok := token.Claims.(*UnifiedClaims)
	if !ok || !token.Valid {
		return nil, ErrInvalidToken
	}

	switch claims.Issuer {
	case JWTIssuerUnified:
		// New unified issuer -- accept as-is.
	case consts.JWTIssuerUser, consts.JWTIssuerService, consts.JWTIssuerServiceAccount:
		// Legacy issuers accepted during migration rollout.
	default:
		return nil, fmt.Errorf("%w: unrecognized issuer %q", ErrInvalidToken, claims.Issuer)
	}

	// Policy: human tokens for inactive users are rejected.
	if claims.Typ == "human" && !claims.IsActive {
		return nil, errors.New("user account is inactive")
	}

	return claims, nil
}

// ParseTokenWithoutValidation parses a token without verifying signature or
// expiration. Used to extract a user_id from an expired token for logging.
func ParseTokenWithoutValidation(tokenString string) (*UnifiedClaims, error) {
	token, _, err := new(jwt.Parser).ParseUnverified(tokenString, &UnifiedClaims{})
	if err != nil {
		return nil, fmt.Errorf("failed to parse token: %v", err)
	}

	claims, ok := token.Claims.(*UnifiedClaims)
	if !ok {
		return nil, errors.New("invalid token claims")
	}

	return claims, nil
}

func ExtractTokenFromHeader(header string) (string, error) {
	if header == "" {
		return "", errors.New("authorization header is empty")
	}

	parts := strings.Split(header, " ")
	if len(parts) != 2 || parts[0] != consts.TokenTypeBearer {
		return "", errors.New("invalid authorization header format")
	}

	return parts[1], nil
}

