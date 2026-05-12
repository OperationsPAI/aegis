package gateway

import (
	"net/http"
	"strconv"
	"strings"
)

// CORSMiddleware applies the gateway-wide CORS policy to every response
// and short-circuits OPTIONS preflight requests with 204.
func CORSMiddleware(cfg CORSConfig, next http.Handler) http.Handler {
	allowedOrigins := index(cfg.AllowedOrigins)
	allowAll := allowedOrigins["*"]
	methods := joinNonEmpty(cfg.AllowedMethods, ",")
	headers := joinNonEmpty(cfg.AllowedHeaders, ",")
	maxAge := strconv.Itoa(cfg.MaxAgeSeconds)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && (allowAll || allowedOrigins[origin]) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			if cfg.AllowCredentials {
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			}
			if methods != "" {
				w.Header().Set("Access-Control-Allow-Methods", methods)
			}
			if headers != "" {
				w.Header().Set("Access-Control-Allow-Headers", headers)
			}
			if cfg.MaxAgeSeconds > 0 {
				w.Header().Set("Access-Control-Max-Age", maxAge)
			}
			w.Header().Add("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func index(ss []string) map[string]bool {
	out := make(map[string]bool, len(ss))
	for _, s := range ss {
		out[s] = true
	}
	return out
}

func joinNonEmpty(ss []string, sep string) string {
	clean := ss[:0:0]
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			clean = append(clean, s)
		}
	}
	return strings.Join(clean, sep)
}
