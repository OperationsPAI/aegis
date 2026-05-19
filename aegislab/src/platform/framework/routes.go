package framework

import "github.com/gin-gonic/gin"

// Audience identifies which top-level API audience bucket a route belongs to.
// The four buckets map one-to-one to today's router/{public,sdk,portal,admin}.go
// files. Preserved so that framework-registered and centrally-registered
// routes can be mounted onto the same gin sub-group.
type Audience string

const (
	AudiencePublic Audience = "public" // /api/v2 — unauthenticated + auth/*
	AudienceSDK    Audience = "sdk"    // /api/v2/sdk/* + SDK-consumable endpoints
	AudiencePortal Audience = "portal" // /api/v2 — human-UI portal endpoints
	AudienceAdmin  Audience = "admin"  // /api/v2 — administrative endpoints
)

// RouteRegistrar is what a module contributes for route self-registration.
//
// A module provides it from `fx.Provide(module.Routes, fx.ResultTags(`group:"routes"`))`.
// Each domain provides exactly one Routes function — collisions between
// audience-keyed registrars used to surface as gin panics at startup, so
// we consolidate per domain and let `@x-api-type` annotations on each
// handler drive swagger SDK bucketing instead.
type RouteRegistrar struct {
	// Audience selects the canonical auth/middleware chain the router
	// prepends to this registrar's group at boot. AudiencePublic gets no
	// chain; AudiencePortal / AudienceSDK / AudienceAdmin currently all
	// prepend TrustedHeaderAuth. The chain table lives in
	// platform/router/audience_chain.go. Ignored when BasePath is
	// non-empty or when SkipDefaultChain is true.
	Audience Audience

	// SkipDefaultChain opts this registrar out of the audience-driven
	// default middleware chain. Set true for registrars that deliberately
	// mix authenticated and unauthenticated sub-routes (e.g. a /blob/raw
	// HMAC-token escape under an otherwise TrustedHeaderAuth group) and
	// take full responsibility for attaching their own auth.
	SkipDefaultChain bool

	// Name is a short human-readable label used only for tracing /
	// debugging (e.g. "label", "injection.portal").
	Name string

	// BasePath is the escape hatch for modules that need to mount outside
	// /api/v2 (e.g. SSR pages at /p/* or vendor static assets at /static/*).
	// When non-empty, router.New mounts Register on engine.Group(BasePath)
	// and Audience is ignored. Use sparingly — most API surface should
	// live under /api/v2 via an Audience-mounted registrar.
	BasePath string

	// Register attaches this module's routes to the given gin.RouterGroup.
	// For audience-mounted registrars the group is the audience's /api/v2
	// bucket — do NOT re-add /api/v2. For BasePath-mounted registrars the
	// group is engine.Group(BasePath).
	Register func(group *gin.RouterGroup)
}
