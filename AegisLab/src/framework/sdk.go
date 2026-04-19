package framework

import "github.com/gin-gonic/gin"

// SDKRoutesHandler captures the HTTP endpoints contributed by module/sdk
// so router wiring does not need to import the module package directly.
type SDKRoutesHandler interface {
	GetEvaluation(*gin.Context)
	ListDatasetSamples(*gin.Context)
	ListEvaluations(*gin.Context)
	ListExperiments(*gin.Context)
}
