package gateway

import (
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// clientIP prefers the left-most X-Forwarded-For hop, falling back to the
// peer address. The gateway is the trust boundary, so an upstream proxy
// (if any) is expected to set XFF; absent that we use RemoteAddr.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// isMutating reports whether the method changes upstream state and thus
// warrants an audit-log entry (read-only verbs only hit the access log).
func isMutating(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}

// redactSensitivePathSegments masks signed-token segments that appear in URL
// paths. /raw/<HMAC-token> is the only case today — the token is the auth
// for that endpoint, so it leaking to an OTel collector / log aggregator is
// equivalent to leaking a 10-minute bearer credential.
func redactSensitivePathSegments(path string) string {
	const marker = "/api/v2/blob/raw/"
	if i := strings.Index(path, marker); i >= 0 {
		return path[:i+len(marker)] + "<redacted>"
	}
	return path
}

// statusRecorder captures the response status code so the access log
// can include it without buffering the body.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// LoggingMiddleware emits one access-log line per request, mirrors a
// structured record to the on-disk access/audit sink, and propagates
// `traceparent` to the upstream via OTel propagators. It is the outermost
// middleware in the gateway chain, so it observes the final status —
// including auth rejections — which makes it the gateway's audit point.
func LoggingMiddleware(route *Route, audit *AuditSink, next http.Handler) http.Handler {
	prop := otel.GetTextMapPropagator()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		reqID := r.Header.Get(HeaderRequestID)
		if reqID == "" {
			reqID = uuid.New().String()
			r.Header.Set(HeaderRequestID, reqID)
		}
		w.Header().Set(HeaderRequestID, reqID)

		// OTel context extraction so the upstream proxy carries
		// `traceparent` forward. The downstream ReverseProxy preserves
		// req.Header so this propagator hand-off is enough.
		ctx := prop.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		prop.Inject(ctx, propagation.HeaderCarrier(r.Header))
		r = r.WithContext(ctx)

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		path := redactSensitivePathSegments(r.URL.Path)
		latency := time.Since(start).Milliseconds()

		logrus.WithFields(logrus.Fields{
			"route":      route.Prefix,
			"upstream":   route.Upstream,
			"method":     r.Method,
			"path":       path,
			"status":     rec.status,
			"latency_ms": latency,
			"user_id":    r.Header.Get(HeaderUserID),
			"request_id": reqID,
		}).Info("gateway: access")

		// After auth runs, the identity headers reflect the verified
		// caller; on rejection they are absent and status is 401/403.
		decision := "allow"
		if rec.status == http.StatusUnauthorized || rec.status == http.StatusForbidden {
			decision = "deny"
		}
		audit.Record(AuditEvent{
			Time:         start.UTC().Format(time.RFC3339Nano),
			RequestID:    reqID,
			Route:        route.Prefix,
			Upstream:     route.Upstream,
			Method:       r.Method,
			Path:         path,
			Status:       rec.status,
			LatencyMS:    latency,
			ClientIP:     clientIP(r),
			UserID:       r.Header.Get(HeaderUserID),
			Username:     r.Header.Get(HeaderUsername),
			Roles:        r.Header.Get(HeaderRoles),
			IsAdmin:      r.Header.Get(HeaderIsAdmin),
			AuthType:     r.Header.Get(HeaderAuthType),
			AuthDecision: decision,
		}, isMutating(r.Method))
	})
}
