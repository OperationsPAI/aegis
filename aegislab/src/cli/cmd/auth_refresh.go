package cmd

import (
	"time"

	"aegis/cli/client"
	"aegis/cli/config"
)

// refreshClock is overridable in tests.
var refreshClock = time.Now

// refreshFunc is overridable in tests; production wires it to client.RefreshTokenTLS.
var refreshFunc = client.RefreshTokenTLS

// maybeRefreshToken proactively swaps the cached JWT for a fresh one when it
// is within tokenRefreshThreshold of expiry. It is a no-op when:
//   - no config-resident context is in use (token came from --token / env);
//   - the active context's token differs from flagToken (caller overrode it);
//   - the token has no decodable expiry and none was persisted at login.
//
// On any refresh error the cached token is kept untouched — the server will
// reject with 401 and the caller sees the real failure rather than a swallowed
// one.
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

	_ = config.SaveConfig(cfg)
}

// shouldRefreshAt is the time-injectable form of client.ShouldRefreshToken so
// the predicate is unit-testable without sleeping.
func shouldRefreshAt(expiry time.Time, threshold time.Duration, now time.Time) bool {
	if expiry.IsZero() {
		return false
	}
	return expiry.Sub(now) <= threshold
}
