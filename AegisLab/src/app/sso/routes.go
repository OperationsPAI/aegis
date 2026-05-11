package sso

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func decorateEngineWithHealthz(engine *gin.Engine) *gin.Engine {
	engine.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	return engine
}
