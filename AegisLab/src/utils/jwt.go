package utils

import (
	"crypto/rsa"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"aegis/consts"

	"github.com/golang-jwt/jwt/v5"
)

const (
	// JWTSecretEnvVar names the symmetric secret used to derive the API-key
	// envelope KEK (see access_key_crypto.go). It is NOT used for JWT signing
	// anymore — JWTs are RS256-signed via an SSO-owned RSA keypair.
	JWTSecretEnvVar = "AEGIS_JWT_SECRET"

	LegacyJWTSecretDefault = "your-secret-key-change-this-in-production"

	TokenExpiration        = 24 * time.Hour
	RefreshTokenExpiration = 7 * 24 * time.Hour
	ServiceTokenExpiration = 24 * time.Hour
)

var JWTSecret string

func InitJWTSecret() error {
	secret := os.Getenv(JWTSecretEnvVar)
	if secret == "" {
		return fmt.Errorf("%s environment variable is not set", JWTSecretEnvVar)
	}
	if secret == LegacyJWTSecretDefault {
		return fmt.Errorf("%s is set to the legacy hardcoded default; refusing to start", JWTSecretEnvVar)
	}
	JWTSecret = secret
	return nil
}

func ValidateJWTSecret() error {
	if JWTSecret == "" {
		return fmt.Errorf("API-key KEK secret is not initialized; set %s and call InitJWTSecret at startup", JWTSecretEnvVar)
	}
	if JWTSecret == LegacyJWTSecretDefault {
		return fmt.Errorf("API-key KEK secret equals the legacy hardcoded default; set %s to a unique value", JWTSecretEnvVar)
	}
	return nil
}

type Claims struct {
	UserID       int      `json:"user_id"`
	Username     string   `json:"username"`
	Email        string   `json:"email"`
	IsActive     bool     `json:"is_active"`
	IsAdmin      bool     `json:"is_admin"`
	Roles        []string `json:"roles"`
	AuthType     string   `json:"auth_type,omitempty"`
	APIKeyID     int      `json:"api_key_id,omitempty"`
	APIKeyScopes []string `json:"api_key_scopes,omitempty"`
	jwt.RegisteredClaims
}

type RefreshClaims struct {
	UserID   int    `json:"user_id"`
	Username string `json:"username"`
	jwt.RegisteredClaims
}

type ServiceClaims struct {
	TaskID    string   `json:"task_id"`
	TokenType string   `json:"token_type,omitempty"` // "service" for OIDC client_credentials
	Service   string   `json:"service,omitempty"`    // OIDC client_id
	Scopes    []string `json:"scopes,omitempty"`     // OIDC scopes
	jwt.RegisteredClaims
}

// PublicKeyResolver maps a JWT header kid to the RSA public key that should
// verify the signature. Returning an error rejects the token.
type PublicKeyResolver func(kid string) (*rsa.PublicKey, error)

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

func GenerateToken(userID int, username, email string, isActive, isAdmin bool, roles []string, priv *rsa.PrivateKey, kid string) (string, time.Time, error) {
	return generateUserToken(userID, username, email, isActive, isAdmin, roles, "user", 0, nil, priv, kid)
}

func GenerateAPIKeyToken(userID int, username, email string, isActive, isAdmin bool, roles []string, apiKeyID int, apiKeyScopes []string, priv *rsa.PrivateKey, kid string) (string, time.Time, error) {
	return generateUserToken(userID, username, email, isActive, isAdmin, roles, "api_key", apiKeyID, apiKeyScopes, priv, kid)
}

func generateUserToken(userID int, username, email string, isActive, isAdmin bool, roles []string, authType string, apiKeyID int, apiKeyScopes []string, priv *rsa.PrivateKey, kid string) (string, time.Time, error) {
	expirationTime := time.Now().Add(TokenExpiration)

	claims := &Claims{
		UserID:       userID,
		Username:     username,
		Email:        email,
		IsActive:     isActive,
		IsAdmin:      isAdmin,
		Roles:        roles,
		AuthType:     authType,
		APIKeyID:     apiKeyID,
		APIKeyScopes: append([]string(nil), apiKeyScopes...),
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        fmt.Sprintf("%s_%s_%d_%d", consts.JWTJTIPrefixUser, authType, userID, time.Now().Unix()),
			ExpiresAt: jwt.NewNumericDate(expirationTime),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			NotBefore: jwt.NewNumericDate(time.Now()),
			Issuer:    consts.JWTIssuerUser,
			Subject:   strconv.Itoa(userID),
		},
	}

	tokenString, err := signRS256(claims, priv, kid)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to generate token: %v", err)
	}
	return tokenString, expirationTime, nil
}

func GenerateRefreshToken(userID int, username string, priv *rsa.PrivateKey, kid string) (string, time.Time, error) {
	expirationTime := time.Now().Add(RefreshTokenExpiration)

	claims := &RefreshClaims{
		UserID:   userID,
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expirationTime),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			NotBefore: jwt.NewNumericDate(time.Now()),
			Issuer:    consts.JWTIssuerRefresh,
			Subject:   strconv.Itoa(userID),
		},
	}

	tokenString, err := signRS256(claims, priv, kid)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to generate refresh token: %v", err)
	}
	return tokenString, expirationTime, nil
}

func GenerateServiceToken(taskID string, priv *rsa.PrivateKey, kid string) (string, time.Time, error) {
	expirationTime := time.Now().Add(ServiceTokenExpiration)

	claims := &ServiceClaims{
		TaskID: taskID,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        fmt.Sprintf("%s_%s_%d", consts.JWTJTIPrefixService, taskID, time.Now().Unix()),
			ExpiresAt: jwt.NewNumericDate(expirationTime),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			NotBefore: jwt.NewNumericDate(time.Now()),
			Issuer:    consts.JWTIssuerService,
			Subject:   taskID,
		},
	}

	tokenString, err := signRS256(claims, priv, kid)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to generate service token: %v", err)
	}
	return tokenString, expirationTime, nil
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

func ParseToken(tokenString string, resolve PublicKeyResolver) (*Claims, error) {
	if tokenString == "" {
		return nil, errors.New("token is required")
	}
	if resolve == nil {
		return nil, errors.New("public key resolver is nil")
	}

	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, keyFunc(resolve))
	if err != nil {
		return nil, fmt.Errorf("failed to parse token: %v", err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token")
	}

	if !claims.IsActive {
		return nil, errors.New("user account is inactive")
	}

	return claims, nil
}

func ParseTokenWithCustomClaims(tokenString string, resolve PublicKeyResolver, validateFunc func(*Claims) error) (*Claims, error) {
	claims, err := ParseToken(tokenString, resolve)
	if err != nil {
		return nil, err
	}
	if validateFunc != nil {
		if err := validateFunc(claims); err != nil {
			return nil, fmt.Errorf("custom validation failed: %v", err)
		}
	}
	return claims, nil
}

func ParseServiceToken(tokenString string, resolve PublicKeyResolver) (*ServiceClaims, error) {
	if tokenString == "" {
		return nil, errors.New("service token is required")
	}
	if resolve == nil {
		return nil, errors.New("public key resolver is nil")
	}

	token, err := jwt.ParseWithClaims(tokenString, &ServiceClaims{}, keyFunc(resolve))
	if err != nil {
		return nil, fmt.Errorf("failed to parse service token: %v", err)
	}

	claims, ok := token.Claims.(*ServiceClaims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid service token")
	}

	// Accept legacy consts.JWTIssuerService issuer + any token tagged
	// token_type=consts.TokenTypeService (OIDC client_credentials grants from sso use the issuer URL).
	if claims.Issuer != consts.JWTIssuerService && claims.TokenType != consts.TokenTypeService {
		return nil, errors.New("not a valid service token")
	}

	return claims, nil
}

// ParseTokenWithoutValidation parses a token without verifying signature or
// expiration. Used to extract a user_id from an expired token for logging.
func ParseTokenWithoutValidation(tokenString string) (*Claims, error) {
	token, _, err := new(jwt.Parser).ParseUnverified(tokenString, &Claims{})
	if err != nil {
		return nil, fmt.Errorf("failed to parse token: %v", err)
	}

	claims, ok := token.Claims.(*Claims)
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
