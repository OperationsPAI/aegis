package label

import (
	"aegis/platform/framework"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
)

// Routes is the fx.Provide function that contributes this module's HTTP
// routes to the framework's `group:"routes"` value-group.
//
// The label module's routes all live under the /labels portal bucket,
// so a single RouteRegistrar is sufficient. Phase 4 modules with
// routes spanning multiple audiences (public, sdk, portal, admin) will
// provide one RouteRegistrar per audience.
//
// Handler reference: /api/v2/labels — see module/label/handler.go for
// each endpoint's Swagger annotations. These were previously
// centrally registered in router/portal.go ("labels" block);
// that block is being removed in the same commit.
func Routes(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudiencePortal,
		Name:     "label",
		Register: func(v2 *gin.RouterGroup) {
			labels := v2.Group("/labels", middleware.JWTAuth())
			{
				labelRead := labels.Group("", middleware.RequireLabelRead)
				{
					labelRead.GET("/:label_id", handler.GetLabelDetail)
					labelRead.GET("", handler.ListLabels)
				}

				labels.POST("", middleware.RequireLabelCreate, handler.CreateLabel)
				labels.PATCH("/:label_id", middleware.RequireLabelUpdate, handler.UpdateLabel)
				labels.DELETE("/:label_id", middleware.RequireLabelDelete, handler.DeleteLabel)
				labels.POST("/batch-delete", middleware.RequireLabelDelete, handler.BatchDeleteLabels)
			}
		},
	}
}
