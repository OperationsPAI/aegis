package sdk

import (
	"testing"

	"aegis/framework"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSDKModuleRegistrars(t *testing.T) {
	t.Run("RoutesSDK returns registrar with AudienceSDK and name sdk", func(t *testing.T) {
		handler := &Handler{}
		reg := RoutesSDK(handler)

		require.Equal(t, framework.AudienceSDK, reg.Audience)
		require.Equal(t, "sdk", reg.Name)
		require.NotNil(t, reg.Register)
	})

	t.Run("Permissions returns registrar with module sdk and empty rules", func(t *testing.T) {
		reg := Permissions()

		require.Equal(t, "sdk", reg.Module)
		require.Empty(t, reg.Rules)
	})

	t.Run("RoleGrants returns registrar with module sdk and empty grants", func(t *testing.T) {
		reg := RoleGrants()

		require.Equal(t, "sdk", reg.Module)
		require.Empty(t, reg.Grants)
	})

	t.Run("Migrations returns registrar with module sdk and nil entities", func(t *testing.T) {
		reg := Migrations()

		require.Equal(t, "sdk", reg.Module)
		require.Nil(t, reg.Entities)
	})
}

func TestSDKHandlerServiceInterface(t *testing.T) {
	s := &Service{}
	hs := AsHandlerService(s)

	require.NotNil(t, hs)

	// Verify it satisfies the HandlerService interface at compile time.
	var _ HandlerService = hs
}

func TestSDKRoutesSDKRegistersExpectedPaths(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler := &Handler{}
	reg := RoutesSDK(handler)

	engine := gin.New()
	group := engine.Group("/api/v2")
	reg.Register(group)

	routes := engine.Routes()

	expectedPaths := []string{
		"/api/v2/sdk/evaluations",
		"/api/v2/sdk/evaluations/experiments",
		"/api/v2/sdk/evaluations/:id",
		"/api/v2/sdk/datasets",
	}

	registeredPaths := make(map[string]bool, len(routes))
	for _, r := range routes {
		registeredPaths[r.Path] = true
	}

	for _, expected := range expectedPaths {
		require.True(t, registeredPaths[expected], "expected route %s to be registered", expected)
	}
}
