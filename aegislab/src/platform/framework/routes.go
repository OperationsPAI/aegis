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
	// Audience is retained for backwards compatibility with module tests
	// that pin a registrar's "primary" audience and for human readers
	// skimming routes.go. The router does NOT dispatch on it — every
	// registrar mounts on the same /api/v2 sub-group at runtime — and
	// swagger SDK filtering is driven by per-handler @x-api-type
	// annotations, not this field. Set it to the audience that best
	// describes the registrar's center of mass; nothing branches on it.
	// Ignored when BasePath is non-empty.
	Audience Audience

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
