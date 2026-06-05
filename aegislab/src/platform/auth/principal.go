package auth

import (
	"strconv"
	"strings"
	"time"

	"aegis/platform/consts"
	"aegis/platform/crypto"
)

type PrincipalType string

const (
	PrincipalHuman          PrincipalType = "human"
	PrincipalService        PrincipalType = "service"
	PrincipalTask           PrincipalType = "task"
	PrincipalServiceAccount PrincipalType = "service_account"
)

// Principal is the unified identity assertion. Every authenticated request
// resolves to exactly one Principal, regardless of how the caller authenticated.
type Principal struct {
	Sub    string
	Typ    PrincipalType
	Scopes []string
	JTI    string
	Idp    string

	UserID       int
	Username     string
	Email        string
	IsActive     bool
	IsAdmin      bool
	Roles        []string
	AuthType     string
	APIKeyID     int
	APIKeyScopes []string
	TaskID       string
	ExpiresAt    time.Time
}

func PrincipalFromClaims(c *crypto.Claims) Principal {
	var exp time.Time
	if c.ExpiresAt != nil {
		exp = c.ExpiresAt.Time
	}
	return Principal{
		Sub:          strconv.Itoa(c.UserID),
		Typ:          PrincipalHuman,
		JTI:          c.ID,
		Idp:          "local",
		UserID:       c.UserID,
		Username:     c.Username,
		Email:        c.Email,
		IsActive:     c.IsActive,
		IsAdmin:      c.IsAdmin,
		Roles:        append([]string(nil), c.Roles...),
		AuthType:     c.AuthType,
		APIKeyID:     c.APIKeyID,
		APIKeyScopes: append([]string(nil), c.APIKeyScopes...),
		ExpiresAt:    exp,
	}
}

func PrincipalFromServiceClaims(c *crypto.ServiceClaims) Principal {
	typ := PrincipalService
	if c.TaskID != "" {
		typ = PrincipalTask
	}
	var exp time.Time
	if c.ExpiresAt != nil {
		exp = c.ExpiresAt.Time
	}
	return Principal{
		Sub:       c.Subject,
		Typ:       typ,
		Scopes:    append([]string(nil), c.Scopes...),
		JTI:       c.ID,
		Idp:       "local",
		TaskID:    c.TaskID,
		ExpiresAt: exp,
	}
}

func PrincipalFromServiceAccountClaims(c *crypto.ServiceAccountClaims, name string) Principal {
	var exp time.Time
	if c.ExpiresAt != nil {
		exp = c.ExpiresAt.Time
	}
	return Principal{
		Sub:       name,
		Typ:       PrincipalServiceAccount,
		Scopes:    append([]string(nil), c.Scopes...),
		JTI:       c.ID,
		Idp:       "local",
		AuthType:  consts.AuthTypeServiceAccount,
		Username:  name,
		ExpiresAt: exp,
	}
}

func PrincipalFromTrustedHeaders(h TrustedHeaderSet) Principal {
	uid, _ := strconv.Atoi(h.UserID)
	apiKeyID, _ := strconv.Atoi(h.APIKeyID)
	isActive := h.IsActive == "1"
	isAdmin := h.IsAdmin == "1"

	var roles []string
	if h.Roles != "" {
		roles = strings.Split(h.Roles, ",")
	}
	var apiKeyScopes []string
	if h.APIKeyScopes != "" {
		apiKeyScopes = strings.Split(h.APIKeyScopes, ",")
	}

	isService := uid == 0 && strings.HasPrefix(h.Roles, consts.ClaimSubjectServicePrefix)

	typ := PrincipalHuman
	if isService {
		if h.TaskID != "" {
			typ = PrincipalTask
		} else {
			typ = PrincipalService
		}
	}

	return Principal{
		Sub:          h.UserID,
		Typ:          typ,
		JTI:          h.TokenJti,
		Idp:          "gateway",
		UserID:       uid,
		Username:     h.Username,
		Email:        h.UserEmail,
		IsActive:     isActive,
		IsAdmin:      isAdmin,
		Roles:        roles,
		AuthType:     h.AuthType,
		APIKeyID:     apiKeyID,
		APIKeyScopes: apiKeyScopes,
		TaskID:       h.TaskID,
	}
}

func PrincipalFromUnifiedClaims(c *crypto.UnifiedClaims) Principal {
	var exp time.Time
	if c.ExpiresAt != nil {
		exp = c.ExpiresAt.Time
	}
	p := Principal{
		Sub:          c.Subject,
		JTI:          c.ID,
		Idp:          c.Idp,
		UserID:       c.UserID,
		Username:     c.Username,
		Email:        c.Email,
		IsActive:     c.IsActive,
		IsAdmin:      c.IsAdmin,
		Roles:        append([]string(nil), c.Roles...),
		AuthType:     c.AuthType,
		APIKeyID:     c.APIKeyID,
		APIKeyScopes: append([]string(nil), c.APIKeyScopes...),
		TaskID:       c.TaskID,
		Scopes:       append([]string(nil), c.Scopes...),
		ExpiresAt:    exp,
	}
	switch c.Typ {
	case "human":
		p.Typ = PrincipalHuman
	case "service":
		p.Typ = PrincipalService
	case "task":
		p.Typ = PrincipalTask
	case "service_account":
		p.Typ = PrincipalServiceAccount
	case "refresh":
		p.Typ = PrincipalHuman
	default:
		p.Typ = PrincipalHuman
	}
	return p
}

// TrustedHeaderSet holds the header values the gateway injects after JWT
// validation. Field names match the gateway header names minus the "X-Aegis-"
// prefix. This struct decouples Principal construction from gin/http so the
// converter is testable without an HTTP request.
type TrustedHeaderSet struct {
	UserID       string
	UserEmail    string
	Roles        string
	TokenAud     string
	TokenJti     string
	Signature    string
	Username     string
	IsActive     string
	IsAdmin      string
	AuthType     string
	APIKeyID     string
	APIKeyScopes string
	TaskID       string
}

// Header name constants mirrored from clients/gateway and middleware to avoid
// import cycles. Values MUST stay identical to both sources.
const (
	HeaderUserID       = "X-Aegis-User-Id"
	HeaderUserEmail    = "X-Aegis-User-Email"
	HeaderRoles        = "X-Aegis-Roles"
	HeaderTokenAud     = "X-Aegis-Token-Aud"
	HeaderTokenJti     = "X-Aegis-Token-Jti"
	HeaderSignature    = "X-Aegis-Signature"
	HeaderUsername      = "X-Aegis-Username"
	HeaderIsActive     = "X-Aegis-Is-Active"
	HeaderIsAdmin      = "X-Aegis-Is-Admin"
	HeaderAuthType     = "X-Aegis-Auth-Type"
	HeaderAPIKeyID     = "X-Aegis-Api-Key-Id"
	HeaderAPIKeyScopes = "X-Aegis-Api-Key-Scopes"
	HeaderTaskID       = "X-Aegis-Task-Id"
)
