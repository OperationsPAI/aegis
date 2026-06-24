package gateway

import "net/http"

// upstreamAuthMiddleware presents a fixed service credential to the
// upstream after edge auth has already passed. It sits between the
// authenticator and the proxy, so an unauthenticated caller is rejected
// before the credential is ever attached. When the route configures no
// upstream credential it is a transparent pass-through.
//
// This is the "static service credential" trust model: aegis performs
// JWT + RBAC + audit at the edge, then swaps the caller's bearer for a
// fixed credential the upstream's own auth recognises — keeping the
// upstream fail-closed without it having to trust X-Aegis-* headers.
func upstreamAuthMiddleware(route *Route, next http.Handler) http.Handler {
	header, value := route.UpstreamAuth()
	if header == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set(header, value)
		next.ServeHTTP(w, r)
	})
}
