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
// Use a single `group:"routes"` with an `Audience` tag (rather than four
// separate groups keyed by audience) because:
//   - A module often touches multiple audiences (e.g. injections has portal
//     + sdk + admin endpoints). One registrar-per-audience means the module
//     writes one small function per bucket, grouped in module/<name>/routes.go.
//     Four groups would require four different fx.ResultTags on four
//     different provide functions — more cognitive load for every new module.
//   - The aggregator (router.New) iterates once over the flat slice and
//     dispatches on `.Audience` — trivially readable.
//
// A module provides it from `fx.Provide(module.Routes, fx.ResultTags(`group:"routes"`))`.
type RouteRegistrar struct {
	// Audience chooses which sub-group of /api/v2 Register is mounted on.
	Audience Audience

	// Name is a short human-readable label used only for tracing /
	// debugging (e.g. "label", "injection.portal").
	Name string

	// Register attaches this module's routes to the given gin.RouterGroup.
	// The group passed in is the audience's /api/v2 bucket. The function
	// should NOT add /api/v2 itself — it's already there.
	Register func(group *gin.RouterGroup)
}

// EngineRegistrar is the escape hatch for modules that need to register
// routes outside the /api/v2 prefix (e.g. SSR pages at /p/* or vendor
// static assets at /static/*). Use sparingly — the API surface should
// live under /api/v2.
//
// A module provides it from
// `fx.Provide(fx.Annotate(module.EngineRoutes, fx.ResultTags(`group:"engine_routes"`)))`.
type EngineRegistrar struct {
	// Name is a short human-readable label used only for tracing /
	// debugging (e.g. "pages.ssr").
	Name string

	// Register attaches routes to the root gin.Engine.
	Register func(engine *gin.Engine)
}
