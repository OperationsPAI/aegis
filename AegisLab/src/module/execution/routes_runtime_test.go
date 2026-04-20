package execution

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"aegis/dto"
	"aegis/consts"
	"aegis/middleware"
	"aegis/utils"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type runtimeRouteVerifier struct{}

func (runtimeRouteVerifier) VerifyToken(context.Context, string) (*utils.Claims, error) {
	return nil, context.Canceled
}

func (runtimeRouteVerifier) VerifyServiceToken(_ context.Context, token string) (*utils.ServiceClaims, error) {
	return utils.ValidateServiceToken(token)
}

type runtimeRouteService struct{ middleware.Service }

func (runtimeRouteService) CheckUserPermission(context.Context, *dto.CheckPermissionParams) (bool, error) {
	return false, nil
}
func (runtimeRouteService) IsUserTeamAdmin(context.Context, int, int) (bool, error) { return false, nil }
func (runtimeRouteService) IsUserInTeam(context.Context, int, int) (bool, error)    { return false, nil }
func (runtimeRouteService) IsTeamPublic(context.Context, int) (bool, error)          { return false, nil }
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
	t.Setenv(utils.JWTSecretEnvVar, "test-jwt-secret-please-ignore-not-for-prod")
	require.NoError(t, utils.InitJWTSecret())

	token, _, err := utils.GenerateServiceToken("task-123")
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.InjectService(runtimeRouteService{Service: middleware.NewService(nil, runtimeRouteVerifier{})}))

	v2 := router.Group("/api/v2")
	runtime := v2.Group("/executions", middleware.JWTAuth(), middleware.RequireServiceTokenAuth())
	runtime.POST("/:execution_id/detector_results", func(c *gin.Context) {
		taskID, ok := middleware.GetServiceTaskID(c)
		require.True(t, ok)
		require.Equal(t, "task-123", taskID)
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v2/executions/12/detector_results", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp := httptest.NewRecorder()

	router.ServeHTTP(resp, req)

	require.Equal(t, http.StatusNoContent, resp.Code, resp.Body.String())
}

func TestMain(m *testing.M) {
	_ = os.Setenv(utils.JWTSecretEnvVar, "test-jwt-secret-please-ignore-not-for-prod")
	if err := utils.InitJWTSecret(); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}
