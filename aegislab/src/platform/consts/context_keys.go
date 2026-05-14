package consts

// Gin context keys set by JWT middleware (middleware/auth.go).
// Reading code uses c.Get(consts.CtxKeyUserID) etc.; a typo on either side
// silently breaks auth, so always reference these constants.
const (
	CtxKeyUserID         = "user_id"
	CtxKeyUsername       = "username"
	CtxKeyEmail          = "email"
	CtxKeyIsActive       = "is_active"
	CtxKeyIsAdmin        = "is_admin"
	CtxKeyUserRoles      = "user_roles"
	CtxKeyAuthType       = "auth_type"
	CtxKeyAPIKeyID       = "api_key_id"
	CtxKeyAPIKeyScopes   = "api_key_scopes"
	CtxKeyIsServiceToken = "is_service_token"
	CtxKeyTokenType      = "token_type"
	CtxKeyTaskID         = "task_id"
	CtxKeyGroupID        = "groupID"
)

// auth_type values stashed under CtxKeyAuthType.
const (
	AuthTypeUser    = "user"
	AuthTypeAPIKey  = "api_key"
	AuthTypeService = "service"
)

// API-key scope syntax used by RequireAPIKeyScopesAny middleware.
const (
	ScopeWildcard  = "*"
	ScopeSeparator = ":"
)
