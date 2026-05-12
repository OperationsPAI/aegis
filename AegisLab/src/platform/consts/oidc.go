package consts

// OIDC / OAuth 2.0 grant types per RFC 6749 + extensions.
const (
	OIDCGrantAuthorizationCode = "authorization_code"
	OIDCGrantRefreshToken      = "refresh_token"
	OIDCGrantClientCredentials = "client_credentials"
	OIDCGrantPassword          = "password"
)

// OIDCGrantsSupported is the canonical list returned by the discovery endpoint
// and validated against in token-exchange handlers.
var OIDCGrantsSupported = []string{
	OIDCGrantAuthorizationCode,
	OIDCGrantRefreshToken,
	OIDCGrantClientCredentials,
	OIDCGrantPassword,
}

// OIDC standard scopes.
const (
	OIDCScopeOpenID  = "openid"
	OIDCScopeProfile = "profile"
	OIDCScopeEmail   = "email"
)

// OIDCScopesSupported is the canonical list of scopes advertised by discovery.
var OIDCScopesSupported = []string{
	OIDCScopeOpenID,
	OIDCScopeProfile,
	OIDCScopeEmail,
}

// OIDC standard claim names emitted in the userinfo / id_token payload.
const (
	OIDCClaimSubject           = "sub"
	OIDCClaimPreferredUsername = "preferred_username"
	OIDCClaimEmail             = "email"
	OIDCClaimEmailVerified     = "email_verified"
	OIDCClaimName              = "name"
	OIDCClaimPicture           = "picture"
	OIDCClaimAudience          = "aud"
	OIDCClaimTokenType         = "token_type"
)

// PKCE code-challenge methods (RFC 7636).
const (
	PKCEMethodPlain = "plain"
	PKCEMethodS256  = "S256"
)

// OAuth 2.0 token types used as JSON token_type field and Authorization scheme.
const (
	TokenTypeBearer  = "Bearer"
	TokenTypeService = "service"
)

// AuthSchemeBearer is the scheme prefix used in HTTP Authorization headers
// (`Authorization: Bearer <token>`); note the trailing space.
const AuthSchemeBearer = "Bearer "

// Audiences used in JWT `aud` claims for cross-service calls.
const (
	AudienceSSOInternal = "sso"
)

// ClaimSubjectServicePrefix is the subject prefix used in service-to-service JWTs
// minted via client_credentials. Example: subject "service:aegis-backend" identifies
// the calling OIDC client.
const ClaimSubjectServicePrefix = "service:"

// OIDC error codes (RFC 6749 §5.2 and OIDC core §3.1.2.6).
const (
	OIDCErrorInvalidGrant         = "invalid_grant"
	OIDCErrorInvalidClient        = "invalid_client"
	OIDCErrorInvalidRequest       = "invalid_request"
	OIDCErrorUnsupportedGrantType = "unsupported_grant_type"
	OIDCErrorUnauthorizedClient   = "unauthorized_client"
	OIDCErrorServerError          = "server_error"
)
