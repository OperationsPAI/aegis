package widget

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

var testSigningKey = []byte("test-trusted-header-key-for-widget")

type widgetRouteService struct{ middleware.Service }

func (widgetRouteService) CheckUserPermission(context.Context, *dto.CheckPermissionParams) (bool, error) {
	return false, nil
}
func (widgetRouteService) IsUserTeamAdmin(context.Context, int, int) (bool, error) { return false, nil }
func (widgetRouteService) IsUserInTeam(context.Context, int, int) (bool, error)    { return false, nil }
func (widgetRouteService) IsTeamPublic(context.Context, int) (bool, error)         { return false, nil }
func (widgetRouteService) IsUserProjectAdmin(context.Context, int, int) (bool, error) {
	return false, nil
}
func (widgetRouteService) IsUserInProject(context.Context, int, int) (bool, error) { return false, nil }
func (widgetRouteService) LogFailedAction(string, string, string, string, int, int, consts.ResourceName) error {
	return nil
}
func (widgetRouteService) LogUserAction(string, string, string, string, int, int, consts.ResourceName) error {
	return nil
}

type stubService struct{ pingResp *PingResp }

func (s stubService) Ping(context.Context) (*PingResp, error) { return s.pingResp, nil }

func mountWidget(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(middleware.InjectService(widgetRouteService{Service: middleware.NewService(nil, nil, nil)}))

	v2 := engine.Group("/api/v2")
	v2.Use(middleware.TrustedHeaderAuth())
	reg := Routes(NewHandler(stubService{pingResp: &PingResp{Module: "widget", RouteRegistered: true}}))
	reg.Register(v2)
	return engine
}

func TestWidgetAdminPingRequiresSystemAdmin(t *testing.T) {
	engine := mountWidget(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/widgets/ping", nil)
	middleware.MintTrustedHeadersForTest(req, testSigningKey, middleware.Principal{
		UserID:   42,
		Username: "alice",
		IsActive: true,
		IsAdmin:  false,
		AuthType: consts.AuthTypeUser,
		TokenJti: "non-admin-jti",
	})
	resp := httptest.NewRecorder()

	engine.ServeHTTP(resp, req)

	require.Equal(t, http.StatusForbidden, resp.Code, resp.Body.String())
}

func TestWidgetAdminPingAcceptsSystemAdmin(t *testing.T) {
	engine := mountWidget(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/widgets/ping", nil)
	middleware.MintTrustedHeadersForTest(req, testSigningKey, middleware.Principal{
		UserID:   1,
		Username: "admin",
		IsActive: true,
		IsAdmin:  true,
		AuthType: consts.AuthTypeUser,
		TokenJti: "admin-jti",
	})
	resp := httptest.NewRecorder()

	engine.ServeHTTP(resp, req)

	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
}

func TestMain(m *testing.M) {
	viper.Set("gateway.trusted_header_key", string(testSigningKey))
	os.Exit(m.Run())
}
