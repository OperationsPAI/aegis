package middleware

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"

	"aegis/platform/consts"
)

// Principal describes the identity to mint trusted headers for in tests.
type Principal struct {
	UserID       int
	Username     string
	Email        string
	IsActive     bool
	IsAdmin      bool
	Roles        []string
	AuthType     string
	APIKeyID     int
	APIKeyScopes []string
	TokenAud     string
	TokenJti     string
	TaskID       string
}

// ServicePrincipal returns a Principal that looks like a gateway-injected
// service token for the given service name and task ID.
func ServicePrincipal(service, taskID string) Principal {
	return Principal{
		UserID:   0,
		Username: "service",
		IsActive: true,
		IsAdmin:  false,
		Roles:    []string{consts.ClaimSubjectServicePrefix + service},
		AuthType: consts.AuthTypeService,
		TokenJti: "test-jti",
		TaskID:   taskID,
	}
}

// MintTrustedHeadersForTest is the test-side counterpart to the gateway's
// applyAndSign — produces a fully-signed header set for the supplied
// principal. Tests that previously mounted middleware.JWTAuth() with a
// real JWT now mount middleware.TrustedHeaderAuth() and set the request
// headers via this helper.
func MintTrustedHeadersForTest(r *http.Request, signingKey []byte, p Principal) {
	isActive := "0"
	if p.IsActive {
		isActive = "1"
	}
	isAdmin := "0"
	if p.IsAdmin {
		isAdmin = "1"
	}
	rolesStr := strings.Join(p.Roles, ",")
	scopesStr := strings.Join(p.APIKeyScopes, ",")

	fields := map[string]string{
		trustedHeaderUserID:       strconv.Itoa(p.UserID),
		trustedHeaderUserEmail:    p.Email,
		trustedHeaderRoles:        rolesStr,
		trustedHeaderTokenAud:     p.TokenAud,
		trustedHeaderTokenJti:     p.TokenJti,
		trustedHeaderUsername:     p.Username,
		trustedHeaderIsActive:     isActive,
		trustedHeaderIsAdmin:      isAdmin,
		trustedHeaderAuthType:     p.AuthType,
		trustedHeaderAPIKeyID:     strconv.Itoa(p.APIKeyID),
		trustedHeaderAPIKeyScopes: scopesStr,
		trustedHeaderTaskID:       p.TaskID,
	}

	canonical := strings.Join([]string{
		fields[trustedHeaderUserID],
		fields[trustedHeaderUserEmail],
		fields[trustedHeaderRoles],
		fields[trustedHeaderTokenAud],
		fields[trustedHeaderTokenJti],
		fields[trustedHeaderUsername],
		fields[trustedHeaderIsActive],
		fields[trustedHeaderIsAdmin],
		fields[trustedHeaderAuthType],
		fields[trustedHeaderAPIKeyID],
		fields[trustedHeaderAPIKeyScopes],
		fields[trustedHeaderTaskID],
	}, "|")

	mac := hmac.New(sha256.New, signingKey)
	_, _ = mac.Write([]byte(canonical))
	sig := hex.EncodeToString(mac.Sum(nil))

	for k, v := range fields {
		r.Header.Set(k, v)
	}
	r.Header.Set(trustedHeaderSignature, sig)
}
