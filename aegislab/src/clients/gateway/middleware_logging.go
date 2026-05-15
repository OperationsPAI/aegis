package gateway

import (
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

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

// LoggingMiddleware emits one access-log line per request and
// propagates `traceparent` to the upstream via OTel propagators. It is
// the outermost middleware in the gateway chain.
func LoggingMiddleware(route *Route, next http.Handler) http.Handler {
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

		logrus.WithFields(logrus.Fields{
			"route":      route.Prefix,
			"upstream":   route.Upstream,
			"method":     r.Method,
			"path":       redactSensitivePathSegments(r.URL.Path),
			"status":     rec.status,
			"latency_ms": time.Since(start).Milliseconds(),
			"user_id":    r.Header.Get(HeaderUserID),
			"request_id": reqID,
		}).Info("gateway: access")
	})
}
