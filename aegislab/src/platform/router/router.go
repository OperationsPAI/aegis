package router

import (
	_ "aegis/docs/openapi2"
	"aegis/platform/framework"
	"aegis/platform/middleware"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"go.uber.org/fx"
)

// Params is the fx-group collection input for router.New.
type Params struct {
	fx.In

	Handlers   *Handlers
	Middleware middleware.Service
	Registrars []framework.RouteRegistrar `group:"routes"`
}

// New assembles the gin.Engine. It iterates every module-provided
// `framework.RouteRegistrar` via fx-group, dispatching each to its
// declared audience bucket (or to the engine root when BasePath is set).
func New(params Params) *gin.Engine {
	router := gin.Default()

	// CORS configuration
	config := cors.DefaultConfig()
	config.AllowAllOrigins = true
	config.AllowHeaders = []string{"Origin", "Content-Type", "Accept", "Authorization", "X-Requested-With", "Cache-Control", "X-Request-Id"}
	config.AllowMethods = []string{"GET", "POST", "PUT", "DELETE", "OPTIONS", "PATCH", "HEAD"}
	config.AllowCredentials = true
	config.ExposeHeaders = []string{"Content-Length", "Content-Type", "X-Request-Id"}

	// Middleware setup
	router.Use(
		middleware.InjectService(params.Middleware),
		middleware.RequestID(),
		middleware.GroupID(),
		middleware.SSEPath(),
		cors.New(config),
		middleware.TracerMiddleware(),
	)

	v2 := router.Group("/api/v2")

	// Framework-registered routes. Audience-mounted registrars attach to
	// the matching /api/v2 sub-group; BasePath-mounted registrars attach
	// at the engine root under that prefix (e.g. SSR /p/*).
	for _, r := range params.Registrars {
		if r.BasePath != "" {
			r.Register(router.Group(r.BasePath))
			continue
		}
		r.Register(v2)
	}

	// Swagger documentation
	router.GET("/docs/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	// Well-known endpoints (RFC 8615) — no auth, no api/v2 prefix.
	registerWellKnownRoutes(router)

	return router
}

// NewForTest constructs a gin.Engine directly without fx and allows tests
// to inject optional self-registered routes via framework.RouteRegistrar.
func NewForTest(handlers *Handlers, middlewareService middleware.Service, registrars ...framework.RouteRegistrar) *gin.Engine {
	return New(Params{
		Handlers:   handlers,
		Middleware: middlewareService,
		Registrars: registrars,
	})
}
