// SDK API-key scopes used by middleware.RequireAPIKeyScopesAny in
// module/<resource>/routes.go. Adding a scope requires touching this file
// plus the route registration — keeping both halves visible together
// surfaces drift at code-review time.
package consts

const (
	ScopeSDKAll = "sdk:*"

	ScopeSDKDatasetsAll   = "sdk:datasets:*"
	ScopeSDKDatasetsRead  = "sdk:datasets:read"
	ScopeSDKDatasetsWrite = "sdk:datasets:write"

	ScopeSDKEvaluationsAll  = "sdk:evaluations:*"
	ScopeSDKEvaluationsRead = "sdk:evaluations:read"

	ScopeSDKExecutionsAll   = "sdk:executions:*"
	ScopeSDKExecutionsRead  = "sdk:executions:read"
	ScopeSDKExecutionsWrite = "sdk:executions:write"

	ScopeSDKMetricsAll  = "sdk:metrics:*"
	ScopeSDKMetricsRead = "sdk:metrics:read"
)
