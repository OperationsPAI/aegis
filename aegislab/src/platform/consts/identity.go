package consts

// Project identity. The legacy "rcabench" name persists in OTel tracer
// namespaces and Loki app labels because changing those values would break
// existing log queries. Use these constants instead of inline literals so
// the eventual rename is a one-line change.
const (
	ProjectName       = "aegislab"
	LegacyProjectName = "rcabench"
)

// JTI (JWT ID claim) prefixes used as the first segment of the `jti` claim.
// Format strings are kept in code, but the prefix tokens live here.
const (
	JWTJTIPrefixUser           = "jwt"
	JWTJTIPrefixService        = "svc"
	JWTJTIPrefixServiceAccount = "sa"
	JWTJTIPrefixUnified        = "u"
)

// OpenTelemetry tracer instrumentation namespaces. Currently use the legacy
// project name as a prefix; aegislab logs/traces still appear under "rcabench/*".
const (
	OTelTracerGroup = "rcabench/group"
	OTelTracerTask  = "rcabench/task"
	OTelTracerTrace = "rcabench/trace"
)

// LokiAppLabel is the Loki app label used in stream selectors. Logs are tagged `app="rcabench"`.
const LokiAppLabel = "rcabench"
