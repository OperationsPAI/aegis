package router

import (
	_ "aegis/docs/openapi2"
	"aegis/framework"
	"aegis/middleware"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"go.uber.org/fx"
)

// Params is the fx-group collection input for router.New.
//
// Central registrations (public/sdk/portal/admin Setup* funcs below) and
// module-provided `framework.RouteRegistrar` contributions coexist
// during Phase 3/4. Each Phase 4 PR migrates a module's entries from the
// central Setup* files into its own module/<name>/routes.go.
type Params struct {
	fx.In

	Handlers   *Handlers
	Middleware middleware.Service
	Registrars []framework.RouteRegistrar `group:"routes"`
}

// New assembles the gin.Engine. It runs the centralized Setup*V2Routes
// functions (today's modules) AND iterates every module-provided
// `framework.RouteRegistrar` via fx-group, dispatching each to its
// declared audience bucket.
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
	SetupPublicV2Routes(v2, params.Handlers)
	SetupSDKV2Routes(v2, params.Handlers)
	SetupAdminV2Routes(v2, params.Handlers)
	SetupPortalV2Routes(v2, params.Handlers)

	// Framework-registered routes (Phase 3+). Each registrar declares an
	// Audience and is mounted onto the matching /api/v2 sub-group.
	for _, r := range params.Registrars {
		r.Register(v2)
	}

	// Swagger documentation
	router.GET("/docs/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

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
