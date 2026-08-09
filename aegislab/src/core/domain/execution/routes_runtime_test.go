package execution

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"aegis/platform/consts"
	"aegis/platform/dto"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

var testSigningKey = []byte("test-trusted-header-key-for-unit-tests")

type runtimeRouteService struct{ middleware.Service }

func (runtimeRouteService) CheckUserPermission(context.Context, *dto.CheckPermissionParams) (bool, error) {
	return false, nil
}
func (runtimeRouteService) IsUserTeamAdmin(context.Context, int, int) (bool, error) { return false, nil }
func (runtimeRouteService) IsUserInTeam(context.Context, int, int) (bool, error)    { return false, nil }
func (runtimeRouteService) IsTeamPublic(context.Context, int) (bool, error)         { return false, nil }
func (runtimeRouteService) IsUserProjectAdmin(context.Context, int, int) (bool, error) {
	return false, nil
}
func (runtimeRouteService) IsUserInProject(context.Context, int, int) (bool, error) { return false, nil }
func (runtimeRouteService) LogFailedAction(string, string, string, string, int, int, consts.ResourceName) error {
	return nil
}
func (runtimeRouteService) LogUserAction(string, string, string, string, int, int, consts.ResourceName) error {
	return nil
}

func TestRuntimeExecutionRoutesAcceptServiceToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.InjectService(runtimeRouteService{Service: middleware.NewService(nil, nil, nil)}))

	v2 := router.Group("/api/v2")
	runtime := v2.Group("/executions", middleware.TrustedHeaderAuth(), middleware.RequireServiceTokenAuth())
	runtime.POST("/:execution_id/detector_results", func(c *gin.Context) {
		taskID, ok := middleware.GetServiceTaskID(c)
		require.True(t, ok)
		require.Equal(t, "task-123", taskID)
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v2/executions/12/detector_results", nil)
	middleware.MintTrustedHeadersForTest(req, testSigningKey, middleware.ServicePrincipal("aegis-backend", "task-123"))
	resp := httptest.NewRecorder()

	router.ServeHTTP(resp, req)

	require.Equal(t, http.StatusNoContent, resp.Code, resp.Body.String())
}

func TestMain(m *testing.M) {
	viper.Set("gateway.trusted_header_key", string(testSigningKey))
	os.Exit(m.Run())
}
