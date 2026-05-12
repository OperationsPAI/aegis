package consts

// Project identity. The legacy "rcabench" name persists in JWT issuers,
// OTel tracer namespaces, and Loki app labels because changing those values
// would invalidate live tokens and break existing log queries. Use these
// constants instead of inline literals so the eventual rename is a one-line change.
const (
	ProjectName       = "AegisLab"
	LegacyProjectName = "rcabench"
)

// JWT issuers. Values are the strings currently emitted in the `iss` claim;
// changing a value breaks all live tokens minted with the old issuer.
const (
	JWTIssuerUser    = "rcabench"         // user access tokens (utils/jwt.go GenerateToken)
	JWTIssuerRefresh = "rcabench-refresh" // refresh tokens
	JWTIssuerService = "rcabench-service" // legacy service tokens (utils/jwt.go GenerateServiceToken)
)

// JTI (JWT ID claim) prefixes used as the first segment of the `jti` claim.
// Format strings are kept in code, but the prefix tokens live here.
const (
	JWTJTIPrefixUser    = "jwt"
	JWTJTIPrefixService = "svc"
)

// OpenTelemetry tracer instrumentation namespaces. Currently use the legacy
// project name as a prefix; AegisLab logs/traces still appear under "rcabench/*".
const (
	OTelTracerGroup = "rcabench/group"
	OTelTracerTask  = "rcabench/task"
	OTelTracerTrace = "rcabench/trace"
)

// LokiAppLabel is the Loki app label used in stream selectors. Logs are tagged `app="rcabench"`.
const LokiAppLabel = "rcabench"
