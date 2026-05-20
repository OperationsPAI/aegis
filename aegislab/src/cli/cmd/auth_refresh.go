package cmd

import (
	"time"

	"aegis/cli/client"
	"aegis/cli/config"

	"github.com/sirupsen/logrus"
)

// refreshClock is overridable in tests.
var refreshClock = time.Now

// refreshFunc is overridable in tests; production wires it to client.RefreshTokenTLS.
var refreshFunc = client.RefreshTokenTLS

// lastRefreshError records the most recent refresh failure so the auth-error
// rendering path can surface it alongside the server's 401 — diagnosing a
// silent refresh failure from a generic Unauthorized is otherwise hopeless.
// Single-process CLI, so no mutex; the value is read once at exit.
var lastRefreshError error

// TODO(auth-refresh): SSE/WS clients (cli/client/sse.go, ws.go) read flagToken
// at stream-open and don't refresh mid-stream. Long-running watches will 401
// after ~24h. Plumb a token-provider through those constructors when it bites.
func maybeRefreshToken() {
	if cfg == nil || flagToken == "" {
		return
	}
	ctx, ctxName, err := config.GetCurrentContext(cfg)
	if err != nil || ctx.Token == "" || ctx.Token != flagToken {
		return
	}

	expiry := ctx.TokenExpiry
	if expiry.IsZero() {
		expiry = client.ParseJWTExp(ctx.Token)
	}
	if !shouldRefreshAt(expiry, tokenRefreshThreshold, refreshClock()) {
		return
	}

	server := flagServer
	if server == "" {
		server = ctx.Server
	}
	if server == "" {
		return
	}

	newToken, newExpiry, err := refreshFunc(server, ctx.Token, resolveTLSOptions())
	if err != nil {
		lastRefreshError = err
		return
	}

	ctx.Token = newToken
	if !newExpiry.IsZero() {
		ctx.TokenExpiry = newExpiry
	} else if exp := client.ParseJWTExp(newToken); !exp.IsZero() {
		ctx.TokenExpiry = exp
	}
	cfg.Contexts[ctxName] = *ctx
	flagToken = newToken

	if err := config.SaveConfig(cfg); err != nil {
		logrus.WithError(err).Warn("refreshed token but failed to persist config; next invocation will re-refresh")
	}
}

// shouldRefreshAt is the time-injectable form of client.ShouldRefreshToken so
// the predicate is unit-testable without sleeping.
func shouldRefreshAt(expiry time.Time, threshold time.Duration, now time.Time) bool {
	if expiry.IsZero() {
		return false
	}
	return expiry.Sub(now) <= threshold
}
