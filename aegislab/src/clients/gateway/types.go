// Package gateway implements the aegis L7 application gateway.
//
// Responsibilities:
//   - Route → upstream mapping (config-driven, configmap [[gateway.routes]]).
//   - JWT pre-auth via clients/sso + trusted-header injection.
//   - Per-route + global rate limit.
//   - CORS, access logging, trace propagation.
//
// This module has NO database and NO business logic. It is the
// transport-layer policy point.
package gateway

import "time"

// AuthPolicy controls how the gateway authenticates a request before
// proxying it to the upstream.
type AuthPolicy string

const (
	// AuthNone disables auth on the route — request is proxied as-is.
	AuthNone AuthPolicy = "none"
	// AuthJWT requires a valid user JWT (Bearer token).
	AuthJWT AuthPolicy = "jwt"
	// AuthServiceToken requires a valid service token (client_credentials).
	AuthServiceToken AuthPolicy = "service-token"
	// AuthJWTOrService accepts either a user JWT or a service token.
	AuthJWTOrService AuthPolicy = "jwt-or-service"
)

// RateLimitPolicy is a per-route override of the global limiter.
type RateLimitPolicy struct {
	// RPS is the steady-state requests-per-second.
	RPS float64 `mapstructure:"rps"`
	// Burst is the bucket size.
	Burst int `mapstructure:"burst"`
}

// RetryPolicy describes how the proxy retries upstream failures.
type RetryPolicy struct {
	// Attempts is the number of additional attempts after the first.
	Attempts int `mapstructure:"attempts"`
	// OnStatus is the set of HTTP status codes that trigger a retry.
	OnStatus []int `mapstructure:"on_status"`
}

// Route is one entry in the gateway's route table. Matching is by
// longest-prefix, first-match against the request path.
type Route struct {
	Prefix         string          `mapstructure:"prefix"`
	Upstream       string          `mapstructure:"upstream"`
	Auth           AuthPolicy      `mapstructure:"auth"`
	Audiences      []string        `mapstructure:"audiences"`
	RateLimit      RateLimitPolicy `mapstructure:"rate_limit"`
	StripPrefix    bool            `mapstructure:"strip_prefix"`
	TimeoutSeconds int             `mapstructure:"timeout_seconds"`
	Retry          RetryPolicy     `mapstructure:"retry"`

	// UpstreamAuthHeader, when set, is written onto the request just
	// before it is proxied (after edge auth has already passed) — e.g.
	// "Authorization". Use it to present a fixed service credential to an
	// upstream that runs its own authentication, so aegis stays the single
	// RBAC + audit choke point without the upstream needing to trust the
	// X-Aegis-* headers.
	UpstreamAuthHeader string `mapstructure:"upstream_auth_header"`
	// UpstreamAuthValue is the literal value for UpstreamAuthHeader. Prefer
	// UpstreamAuthValueEnv so the secret never lands in config.
	UpstreamAuthValue string `mapstructure:"upstream_auth_value"`
	// UpstreamAuthValueEnv names an env var holding the value; it takes
	// precedence over UpstreamAuthValue and is resolved once at load time
	// (NewRouteTable), so the secret can be sourced from a K8s Secret.
	UpstreamAuthValueEnv string `mapstructure:"upstream_auth_value_env"`

	// resolvedUpstreamAuth is the post-resolution value (set by
	// NewRouteTable); never populated from config directly.
	resolvedUpstreamAuth string
}

// UpstreamAuth returns the header name and resolved value to inject
// toward the upstream, or empty strings when no credential is configured.
func (r Route) UpstreamAuth() (string, string) {
	if r.UpstreamAuthHeader == "" || r.resolvedUpstreamAuth == "" {
		return "", ""
	}
	return r.UpstreamAuthHeader, r.resolvedUpstreamAuth
}

// Timeout returns the configured per-route upstream timeout, falling
// back to a 30s default.
func (r Route) Timeout() time.Duration {
	if r.TimeoutSeconds <= 0 {
		return 30 * time.Second
	}
	return time.Duration(r.TimeoutSeconds) * time.Second
}

// CORSConfig is the gateway-wide CORS policy.
type CORSConfig struct {
	AllowedOrigins   []string `mapstructure:"allowed_origins"`
	AllowedMethods   []string `mapstructure:"allowed_methods"`
	AllowedHeaders   []string `mapstructure:"allowed_headers"`
	AllowCredentials bool     `mapstructure:"allow_credentials"`
	MaxAgeSeconds    int      `mapstructure:"max_age_seconds"`
}

// Config is the [gateway] section of config.<env>.toml.
type Config struct {
	Routes           []Route         `mapstructure:"routes"`
	CORS             CORSConfig      `mapstructure:"cors"`
	RateLimit        RateLimitPolicy `mapstructure:"rate_limit"`
	TrustedHeaderKey string          `mapstructure:"trusted_header_key"`
	Audit            AuditConfig     `mapstructure:"audit"`
}

// Trusted-header names injected by the gateway after JWT pre-auth.
// Upstreams that opt in to trusted-header auth can trust these instead of
// re-verifying the JWT. Always sent together with X-Aegis-Signature
// (HMAC of the canonical header set keyed by gateway.trusted_header_key).
//
// Canonical string (v2) — fields joined by "|" in this exact order:
//
//	<user_id>|<email>|<roles>|<aud>|<jti>|<username>|<is_active>|<is_admin>|<auth_type>|<api_key_id>|<api_key_scopes>|<task_id>
//
// is_active and is_admin are "1" or "0". api_key_scopes is comma-separated.
// task_id is empty for user tokens; set for service tokens.
const (
	HeaderUserID       = "X-Aegis-User-Id"
	HeaderUserEmail    = "X-Aegis-User-Email"
	HeaderRoles        = "X-Aegis-Roles"
	HeaderTokenAud     = "X-Aegis-Token-Aud"
	HeaderTokenJti     = "X-Aegis-Token-Jti"
	HeaderSignature    = "X-Aegis-Signature"
	HeaderRequestID    = "X-Aegis-Request-Id"
	HeaderUsername     = "X-Aegis-Username"
	HeaderIsActive     = "X-Aegis-Is-Active"
	HeaderIsAdmin      = "X-Aegis-Is-Admin"
	HeaderAuthType     = "X-Aegis-Auth-Type"
	HeaderAPIKeyID     = "X-Aegis-Api-Key-Id"
	HeaderAPIKeyScopes = "X-Aegis-Api-Key-Scopes"
	HeaderTaskID       = "X-Aegis-Task-Id"
)
